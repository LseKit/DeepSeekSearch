// 文件名: config.go
// 用途: 定义 resilience 层的窄配置，由 Task-12 从全局 config.Config 映射。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import "time"

// Config 是限流之外各保护器的依赖参数，不包含业务语义。
type Config struct {
	RetryEnabled       bool
	MaxAttempts        int
	JitterMode         string
	FixedMs            []int
	SpreadMs           []int
	RetryBudgetEnabled bool
	RetryBudgetRatio   float64
	RetryBudgetWindowS int

	MaxCallsPerWindow int
	WindowS           int
	MaxCallsPerDay    int
	MaxTotalChars     int
	TZ                *time.Location

	CircuitFailThreshold  int
	CircuitOpenMs         int
	CircuitHalfOpenProbes int

	CacheDbPath         string
	CacheMaxEntries     int
	CacheMaxBytesMB     int
	CacheMaxEntryKB     int
	CachePruneIntervalS int
	Clock               Clock
	Logger              cacheLogger

	TotalTimeoutSearchMs int
	TotalTimeoutFetchMs  int
}

// cacheLogger 是注入给缓存层的窄日志接口。
//
// resilience 不允许 import internal/logging（10 号 §2.5），因此用接口接收
// *slog.Logger；事件名仍以 Task-04 A-02 为唯一真源。
type cacheLogger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}
