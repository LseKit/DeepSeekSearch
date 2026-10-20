// 文件名: transport.go
// 用途: 定义传输 Handler 接口、按名字构造的 Build 与已编译传输列表 Available。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// Handler 是所有传输实现的唯一契约。
// 新增传输只需新增一个实现文件，并让 Build 识别新名字。
type Handler interface {
	// Name 返回 config.toml 中 [server] transport 的取值。
	Name() string
	// Serve 阻塞运行直到 ctx 取消或发生致命错误，且 MUST 在返回前完成资源释放。
	Serve(ctx context.Context, srv *mcp.Server) error
}

// Build 按名字构造 Handler。名字未知时返回错误，MUST NOT panic。
func Build(name string, cfg config.Config, build domain.BuildInfo, logger *slog.Logger) (Handler, error) { //nolint:gocritic // 签名由 11 号 §六 固定
	switch name {
	case "stdio":
		return &stdioHandler{cfg: cfg, build: build, log: logger}, nil
	case "streamable-http":
		// transport 契约没有 Clock 注入点；uptime 只用于 /healthz 展示，
		// 业务时间（TTL/跨日）不经过这里。进程启动时刻在此取一次。
		return &httpHandler{cfg: cfg, build: build, log: logger, startedAt: time.Now()}, nil
	default:
		return nil, fmt.Errorf("未知传输 %q（可用：%s）", name, strings.Join(Available(), ", "))
	}
}

// Available 返回已编译进二进制的传输名列表，按字典序排列。
func Available() []string {
	return []string{"stdio", "streamable-http"}
}
