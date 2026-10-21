// 文件名: retry.go
// 用途: 定义重试决策接口与「为什么不再重试」的原因枚举；实现见子包 retry。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import "time"

// Reason 说明「为什么不再重试」。
type Reason int

const (
	// ReasonOK 表示应当重试。
	ReasonOK Reason = iota
	// ReasonDisabled 表示 retry.enabled = false。
	ReasonDisabled
	// ReasonAttemptsExhausted 表示已达到 max_attempts。
	ReasonAttemptsExhausted
	// ReasonNotRetryable 表示错误码本身不可重试。
	ReasonNotRetryable
	// ReasonBudgetExhausted 表示重试预算耗尽，调用方 MUST 转成 3010。
	ReasonBudgetExhausted
)

// Retry 决定一次失败是否重试，并计算退避时长。
type Retry interface {
	// Should 返回是否应再试一次与原因；attempt 从 1 开始。
	Should(err error, attempt int) (bool, Reason)
	// Delay 返回第 attempt 次失败后的等待时长（含抖动）。
	Delay(attempt int) time.Duration
}
