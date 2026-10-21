// 文件名: logger.go
// 用途: 构造结构化日志器，并计算配置指纹供排查使用。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// New 构造日志器。返回值里的 closer MUST 在进程退出前调用，
// 否则后台调度 goroutine 不会被回收（A-01 要求 13）。
//
// 绝不写 stdout：stdio 传输下那是协议通道，混一个字节客户端就断开。
func New(cfg Config) (*slog.Logger, io.Closer, error) { //nolint:gocritic // 签名由 Task-04 A-02 固定
	if cfg.TsFormat == "" {
		cfg.TsFormat = time.RFC3339
	}
	w, err := NewRotatingWriter(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: %w", err)
	}

	var out io.Writer = w
	if cfg.AlsoStderr {
		// stderr 分支与文件分支共享配额状态：配额达标后限频采样，
		// 否则 500MB 的闸只保护磁盘，stderr 管道对端会被灌爆（A-01 要求 5②）。
		out = io.MultiWriter(w, &samplingWriter{dst: os.Stderr, quota: w.quotaExceeded, every: 100})
	}

	opts := &slog.HandlerOptions{
		Level: toSlogLevel(cfg.Level),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
				// 用注入 Clock 而不是 slog 内部的真实时钟：否则行内时间无法确定性测试（H8）。
				a.Value = slog.StringValue(cfg.Clock.Now().In(cfg.TZ).Format(cfg.TsFormat))
			}
			return a
		},
	}
	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}

	// config_sha256 是进程级属性，一次性全局注入，保证每个事件必带。
	// 不写死「N/N」：事件数的真源是 Task-04 A-02，写死会随事件增减漂移。
	return slog.New(handler).With("config_sha256", cfg.ConfigSha), w, nil
}

// ConfigSha256 返回配置指纹：对 Redacted() 后的 JSON 求 sha256，取前 12 位。
// 只改 ApiKey / HttpToken 不改变指纹，二者既不带 json tag 也被 Redacted 覆盖。
func ConfigSha256(cfg config.Config) string { //nolint:gocritic // 签名由 Task-04 A-02 固定
	data, err := json.Marshal(cfg.Redacted())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

// Slow 在耗时超过 slowMs 时补一条 warn.slow；attrs 透传 call_id 等调用上下文。
func Slow(logger *slog.Logger, slowMs int, tool string, ms int64, attrs ...any) {
	if logger == nil || ms <= int64(slowMs) {
		return
	}
	args := make([]any, 0, 4+len(attrs))
	args = append(args, "tool", tool, "ms", ms)
	args = append(args, attrs...)
	logger.Warn(domain.EvWarnSlow, args...)
}

func toSlogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
