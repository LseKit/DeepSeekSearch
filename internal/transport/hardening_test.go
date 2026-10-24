// 文件名: hardening_test.go
// 用途: 验证传输层的四处加固：healthz 不泄露构建指纹、no-store、panic 也有访问日志、Bearer 大小写不敏感。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/config"
)

// hardenedChain 用真实中间件链包住一个可替换的内层 handler。
// 不构造 MCP server：这几条断言只关心中间件行为，内层是什么无关紧要。
func hardenedChain(t *testing.T, cfg *config.Config, log *slog.Logger, inner http.Handler) http.Handler {
	t.Helper()
	policy, err := newOriginPolicy(cfg)
	if err != nil {
		t.Fatalf("newOriginPolicy: %v", err)
	}
	return chain(cfg, testBuild, log, time.Now(), nil, policy, inner)
}

func TestHealthzOmitsBuildFingerprint(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	h := hardenedChain(t, &cfg, discardLogger(), http.NotFoundHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// /healthz 排在鉴权之前，任何人都能读：带上这些就等于送出精确的版本指纹。
	for _, leak := range []string{"go_version", `"commit"`, "build_time", "base_url", "go1."} {
		if strings.Contains(body, leak) {
			t.Fatalf("/healthz 泄露了 %q: %s", leak, body)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	for _, want := range []string{"status", "name", "version", "protocol", "transport", "uptime_s", "cache", "upstream"} {
		if _, ok := payload[want]; !ok {
			t.Fatalf("运维必需字段 %q 缺失: %s", want, body)
		}
	}
}

func TestSecurityHeadersSetNoStore(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	h := hardenedChain(t, &cfg, discardLogger(), http.NotFoundHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", http.NoBody))

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store（响应含抓取内容，不能被中间层留存）", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestAccessLogRecordsPanickedRequest(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.Server.Http.AllowNonLocalHost = true // httptest 的 Host 是 example.com，否则先吃 hostGuard 的 403
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := hardenedChain(t, &cfg, log, inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", http.NoBody))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "http.access") {
		t.Fatalf("panic 请求没有访问日志（最该留痕的 500 反而无记录）: %s", logged)
	}
	if !strings.Contains(logged, `"status":500`) {
		t.Fatalf("访问日志的状态码不是 500: %s", logged)
	}
	if !strings.Contains(logged, "boom") {
		t.Fatalf("recover 层没有记下原始 panic 值: %s", logged)
	}
}

func TestAuthGuardAcceptsAnyBearerCase(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.Server.Http.AuthEnabled = true
	cfg.Server.Http.AllowNonLocalHost = true
	cfg.HttpToken = "0123456789abcdef"
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := hardenedChain(t, &cfg, discardLogger(), inner)

	for _, header := range []string{"Bearer 0123456789abcdef", "bearer 0123456789abcdef", "BeArEr 0123456789abcdef"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", header)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot {
			t.Fatalf("Authorization=%q 被拒（status=%d）：RFC 7235 的 scheme 大小写不敏感", header, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", http.NoBody)
	req.Header.Set("Authorization", "bearer not-the-right-token")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错 token status = %d, want 401", rec.Code)
	}
}
