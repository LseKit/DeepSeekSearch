// 文件名: middleware.go
// 用途: HTTP 中间件链：recover、访问日志、安全头、healthz 分流、CORS、Host/鉴权、请求体上限。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/logging"
)

// statusWriter 必须实现 Flush 与 Unwrap。
// 少了 Flush，整个流式响应会静默卡死 —— 实测 curl 3 秒收到 0 字节、
// SDK 客户端 hang 满 3 分钟，且没有任何错误日志。这是本项目最贵的坑。
type statusWriter struct {
	http.ResponseWriter
	code int
	n    int64
}

// WriteHeader 记录状态码并透传。
func (s *statusWriter) WriteHeader(c int) {
	s.code = c
	s.ResponseWriter.WriteHeader(c)
}

// Write 透传响应体并累计字节数。
func (s *statusWriter) Write(p []byte) (int, error) {
	n, err := s.ResponseWriter.Write(p)
	s.n += int64(n)
	if err != nil {
		return n, fmt.Errorf("写 HTTP 响应体: %w", err)
	}
	return n, nil
}

// Flush 转发给底层 writer，保证 SSE 首包立即下发。
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 供 http.ResponseController 逐层解包。
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// middleware 给每层中间件带上名字，供 TestMiddlewareOrder 全等比较实际装配顺序。
type middleware struct {
	name  string
	next  http.Handler
	serve http.Handler
}

// ServeHTTP 执行本层逻辑；crossOrigin 的 serve 与 next 不同（保护层包住 next）。
func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.serve != nil {
		m.serve.ServeHTTP(w, r)
		return
	}
	m.next.ServeHTTP(w, r)
}

// middlewareNames 是 §六之二 固定链的请求经过顺序；mcp 是链末端。
var middlewareNames = []string{
	"recover", "accessLog", "securityHeaders", "healthz",
	"crossOrigin", "hostGuard", "authGuard", "bodyLimit", "mcp",
}

// chain 按 Contract 15 §六之二的字面顺序装配。顺序写死在这一处，
// 因为错一位的两种后果（浏览器客户端全挂 / SSE 静默卡死）都不报错。
// 写法：从最内层往外包，所以代码顺序与请求经过顺序相反。
func chain(cfg *config.Config, build domain.BuildInfo, log *slog.Logger, startedAt time.Time,
	cache CacheStats, policy *originPolicy, mcpHandler http.Handler) http.Handler {
	h := mcpHandler
	h = bodyLimit(cfg, h)
	h = authGuard(cfg, log, h)
	h = hostGuard(cfg, log, h)
	h = policy.handler(h)
	h = healthzHandler(cfg, build, startedAt, cache, log, h)
	h = securityHeaders(h)
	h = accessLog(log, cfg, h)
	return recoverMW(log, h)
}

func recoverMW(log *slog.Logger, next http.Handler) http.Handler {
	return &middleware{name: "recover", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error(logging.EvErr, "panic", fmt.Sprint(rec), "stack", string(debug.Stack()),
					"method", r.Method, "path", r.URL.Path)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})}
}

func accessLog(log *slog.Logger, cfg *config.Config, next http.Handler) http.Handler {
	return &middleware{name: "accessLog", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		code := sw.code
		if code == 0 {
			code = http.StatusOK
		}
		ms := time.Since(start).Milliseconds()
		// 传输层访问日志，不属于 Task-04 的 28 个业务事件。
		log.Info("http.access", "method", r.Method, "path", r.URL.Path,
			"status", code, "bytes", sw.n, "ms", ms)
		if cfg.Log.SlowMs > 0 && ms > int64(cfg.Log.SlowMs) {
			log.Warn(logging.EvWarnSlow, "method", r.Method, "path", r.URL.Path,
				"ms", ms, "slow_ms", cfg.Log.SlowMs)
		}
	})}
}

func securityHeaders(next http.Handler) http.Handler {
	return &middleware{name: "securityHeaders", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})}
}

// originPolicy 包住标准库的 CrossOriginProtection，并补上它不做的两件事：
//  1. 收发 CORS 头（标准库只拒绝，不发 Access-Control-Allow-*）；
//  2. 应答 preflight（标准库把 OPTIONS 视为安全方法直接放行，preflight 会一路走到
//     authGuard 吃 401 —— 这正是 §六之二 要消灭的浏览器端故障）。
type originPolicy struct {
	enabled    bool
	protection *http.CrossOriginProtection
	trusted    map[string]bool
}

func newOriginPolicy(cfg *config.Config) (*originPolicy, error) {
	p := &originPolicy{
		enabled: cfg.Server.Http.EnableCrossOriginProtection,
		trusted: make(map[string]bool, len(cfg.Server.Http.AllowedOrigins)),
	}
	if !p.enabled {
		return p, nil
	}
	p.protection = http.NewCrossOriginProtection()
	for _, origin := range cfg.Server.Http.AllowedOrigins {
		if err := p.protection.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("server.http.allowed_origins 含非法来源 %q: %w", origin, err)
		}
		p.trusted[origin] = true
	}
	return p, nil
}

func (p *originPolicy) handler(next http.Handler) http.Handler {
	if !p.enabled {
		return &middleware{name: "crossOrigin", next: next}
	}
	protected := p.protection.Handler(next)
	return &middleware{name: "crossOrigin", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && p.trusted[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions && origin != "" && r.Header.Get("Access-Control-Request-Method") != "" {
			if !p.trusted[origin] {
				http.Error(w, "cross-origin request denied", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			if headers := r.Header.Get("Access-Control-Request-Headers"); headers != "" {
				w.Header().Set("Access-Control-Allow-Headers", headers)
			}
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		protected.ServeHTTP(w, r)
	})}
}

func hostGuard(cfg *config.Config, log *slog.Logger, next http.Handler) http.Handler {
	if cfg.Server.Http.AllowNonLocalHost {
		return &middleware{name: "hostGuard", next: next}
	}
	return &middleware{name: "hostGuard", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoopbackHost(r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, log, http.StatusForbidden, domain.CodeLocHTTPHostRejected, "非回环 Host")
	})}
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func authGuard(cfg *config.Config, log *slog.Logger, next http.Handler) http.Handler {
	if !cfg.Server.Http.AuthEnabled {
		return &middleware{name: "authGuard", next: next}
	}
	want := []byte(cfg.HttpToken)
	return &middleware{name: "authGuard", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		// 常量时间比较，避免时序侧信道泄漏 token 前缀。
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeError(w, log, http.StatusUnauthorized, domain.CodeLocHTTPUnauthorized, "Bearer token 缺失或不匹配")
			return
		}
		next.ServeHTTP(w, r)
	})}
}

func bodyLimit(cfg *config.Config, next http.Handler) http.Handler {
	return &middleware{name: "bodyLimit", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Server.Http.MaxRequestBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.Server.Http.MaxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})}
}

func writeError(w http.ResponseWriter, log *slog.Logger, status, code int, detail string) {
	payload := domain.ToPayload(domain.NewLocalError(code, detail, nil), "")
	raw, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(raw); err != nil {
		log.Warn(logging.EvErr, "stage", "write_error_response", "error", err)
	}
}
