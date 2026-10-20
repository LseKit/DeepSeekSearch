// 文件名: stdio.go
// 用途: 通过标准输入输出运行 MCP 协议，并对 fd 1 做防污染加固。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// stdioHandler 通过标准输入输出跑 MCP 协议。
// 致命约束：fd 1 只能出协议帧；日志必须走文件或 stderr。
type stdioHandler struct {
	cfg   config.Config
	build domain.BuildInfo
	log   *slog.Logger
}

// Name 返回传输名。
func (h *stdioHandler) Name() string { return "stdio" }

// Serve 在 stdin/stdout 上阻塞运行 MCP 会话。
//
// stdout 加固三步（顺序固定）：
//  1. 保存原始 fd 1 的 *os.File；
//  2. 用它显式构造 IOTransport（捕获的就是这个文件）；
//  3. 才把 os.Stdout 改指向 os.Stderr。
//
// MUST NOT 用 SDK 的 &mcp.StdioTransport{}：v1.8.0 的 Connect 在**连接时**才读
// 全局 os.Stdout，第 3 步之后协议帧会跟着日志一起进 stderr，客户端读到 EOF。
// 显式注入保存的文件既保住协议通道，又让此后任何库写 os.Stdout 落到 stderr。
// CGO_ENABLED=0 保证没有 C 代码绕过 os.Stdout 直接 write(1, …)。
func (h *stdioHandler) Serve(ctx context.Context, srv *mcp.Server) error {
	// 配置层已用 3090 拒绝过；这里再断言一次 —— 第 3 步的重定向会把
	// 「配错了但没被发现」变成静默行为，transport 是最后能显形的地方。
	if isStdoutDest(h.cfg.Log.Dir) {
		return fmt.Errorf("stdio 传输下 log.dir 不能指向 stdout（当前 %q）", h.cfg.Log.Dir)
	}

	h.log.Info("app.transport", "transport", "stdio",
		"version", h.build.Version, "protocol", "2026-07-28")

	proto := os.Stdout
	tr := &mcp.IOTransport{Reader: os.Stdin, Writer: nopCloseWriter{proto}}
	os.Stdout = os.Stderr

	err := srv.Run(ctx, tr)
	// SIGTERM 经 signal.NotifyContext 变成 ctx 取消；对 stdio 而言这是正常退出。
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stdio 会话异常结束: %w", err)
	}
	return nil
}

// nopCloseWriter 让 SDK 关闭会话时不要关掉 fd 1。
type nopCloseWriter struct{ io.Writer }

// Close 不关闭底层文件。
func (nopCloseWriter) Close() error { return nil }

// isStdoutDest 与 internal/config 的启动期检查同口径：
// 配置没有独立的 dest 键，log.dir 是唯一目的地，"-" 与 /dev/stdout 是常见别名。
func isStdoutDest(dir string) bool {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "stdout", "-", "/dev/stdout":
		return true
	default:
		return false
	}
}
