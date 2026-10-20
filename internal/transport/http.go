// 文件名: http.go
// 用途: 无状态 Streamable HTTP 传输：监听、装配中间件链、优雅关闭。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/logging"
)

const (
	shutdownTimeout   = 15 * time.Second
	readHeaderTimeout = 10 * time.Second
)

// httpHandler 用无状态 Streamable HTTP 提供 MCP。
// 恒为无状态（ADR-003）：2026-07-28 只在无状态形态下可用，
// legacy 2024-11-05 ~ 2025-11-25 同样走这条路径，单档即超集。
type httpHandler struct {
	cfg       config.Config
	build     domain.BuildInfo
	log       *slog.Logger
	startedAt time.Time
	cache     CacheStats
}

// Name 返回传输名。
func (h *httpHandler) Name() string { return "streamable-http" }

// Serve 监听配置地址直到 ctx 取消，并用 15s 超时优雅关闭。
func (h *httpHandler) Serve(ctx context.Context, srv *mcp.Server) error {
	// 配置层已校验过；这里再断言一次对外暴露必须伴随鉴权（14 号 3092 的语义）。
	if h.cfg.Server.Http.AllowNonLocalHost && !h.cfg.Server.Http.AuthEnabled {
		return fmt.Errorf("allow_non_local_host=true 时 MUST 同时启用 auth_enabled")
	}

	handler, err := newHTTPHandler(&h.cfg, h.build, h.log, h.startedAt, h.cache, srv)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(h.cfg.Server.Http.Host, strconv.Itoa(h.cfg.Server.Http.Port))
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}

	httpSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// config_sha256 由装配层经 logger.With 注入，这条日志自动带上它。
	h.log.Info(logging.EvAppReady, "transport", "streamable-http",
		"addr", ln.Addr().String(), "path", h.cfg.Server.Http.Path)

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // 优雅关闭必须用独立的 15s 窗口，父 ctx 已取消
			return fmt.Errorf("HTTP 优雅关闭失败: %w", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
		return nil
	}
}

func newHTTPHandler(cfg *config.Config, build domain.BuildInfo, log *slog.Logger,
	startedAt time.Time, cache CacheStats, srv *mcp.Server) (http.Handler, error) {
	policy, err := newOriginPolicy(cfg)
	if err != nil {
		return nil, err
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: cfg.Server.Http.JsonResponse},
	)
	return chain(cfg, build, log, startedAt, cache, policy, mcpHandler), nil
}
