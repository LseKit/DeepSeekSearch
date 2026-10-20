// 文件名: config.go
// 用途: 定义 resilience 层的窄配置，由 Task-12 从全局 config.Config 映射。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import "time"

// Config 是限流之外三个保护器的依赖参数，不包含业务语义。
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
}
