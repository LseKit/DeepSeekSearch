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

// CacheStats 是 /healthz 的缓存统计来源。
// 接口定义在 transport 而不是直接接收 resilience.Cache：10 号依赖矩阵只允许
// transport 依赖 domain/config/logging，由装配层把具体实现注进来即可。
type CacheStats interface {
	Stats() (entries int, bytes int64, degraded bool)
}

// Build 按名字构造 Handler。名字未知时返回错误，MUST NOT panic。
func Build(name string, cfg config.Config, build domain.BuildInfo, logger *slog.Logger) (Handler, error) { //nolint:gocritic // 签名由 11 号 §六 固定
	return BuildWithCache(name, cfg, build, logger, nil)
}

// BuildWithCache 与 Build 相同，并把缓存统计注入 /healthz；cache 为 nil 时字段保持零值。
// 另开函数而不是改 Build：Build 的签名是 11 号 §六 的冻结契约，新增函数不破坏既有调用方。
func BuildWithCache(name string, cfg config.Config, build domain.BuildInfo, logger *slog.Logger, //nolint:gocritic // 与 Build 同口径，cfg 按值传入
	cache CacheStats) (Handler, error) {
	switch name {
	case "stdio":
		return &stdioHandler{cfg: cfg, build: build, log: logger}, nil
	case "streamable-http":
		// transport 契约没有 Clock 注入点；uptime 只用于 /healthz 展示，
		// 业务时间（TTL/跨日）不经过这里。进程启动时刻在此取一次。
		return &httpHandler{cfg: cfg, build: build, log: logger, startedAt: time.Now(), cache: cache}, nil
	default:
		return nil, fmt.Errorf("未知传输 %q（可用：%s）", name, strings.Join(Available(), ", "))
	}
}

// Available 返回已编译进二进制的传输名列表，按字典序排列。
func Available() []string {
	return []string{"stdio", "streamable-http"}
}
