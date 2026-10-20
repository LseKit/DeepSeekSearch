// 文件名: retry.go
// 用途: 按错误码判定是否重试、计算三种抖动退避，并维护 per-instance 重试预算。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

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

// RandSource 是 Delay 的随机源抽象；实现 MUST 并发安全。
type RandSource interface {
	Float64() float64
}

type retrier struct {
	cfg    Config
	clk    Clock
	rnd    RandSource
	budget *retryBudget
}

// NewRetry 构造重试策略。重试预算计数属于实例内部，
// 调用方 MUST 为 search/fetch 各建一个实例以隔离预算。
func NewRetry(cfg Config, clk Clock, rnd RandSource) Retry { //nolint:gocritic // 签名由 11 号 §四 固定
	if clk == nil {
		clk = SystemClock()
	}
	if rnd == nil {
		rnd = globalRand{}
	}
	return &retrier{
		cfg:    cfg,
		clk:    clk,
		rnd:    rnd,
		budget: newRetryBudget(time.Duration(cfg.RetryBudgetWindowS) * time.Second),
	}
}

func (r *retrier) Should(err error, attempt int) (bool, Reason) {
	if !r.cfg.RetryEnabled {
		return false, ReasonDisabled
	}
	if attempt >= r.cfg.MaxAttempts {
		return false, ReasonAttemptsExhausted
	}

	now := r.clk.Now()
	if r.cfg.RetryBudgetEnabled && attempt <= 1 {
		r.budget.recordCall(now)
	}

	code := domain.CodeOf(err)
	info, ok := domain.CodeRegistry[code]
	if !ok || !info.Retryable {
		return false, ReasonNotRetryable
	}
	if r.cfg.RetryBudgetEnabled && !r.budget.allowRetry(now, r.cfg.RetryBudgetRatio) {
		return false, ReasonBudgetExhausted
	}
	return true, ReasonOK
}

func (r *retrier) Delay(attempt int) time.Duration {
	if len(r.cfg.FixedMs) == 0 {
		return 0
	}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(r.cfg.FixedMs) {
		index = len(r.cfg.FixedMs) - 1
	}
	base := r.cfg.FixedMs[index]
	if base < 0 {
		base = 0
	}

	switch r.cfg.JitterMode {
	case "none":
		return time.Duration(base) * time.Millisecond
	case "full":
		factor := 1 << min(max(attempt, 1), 10)
		return time.Duration(int(float64(base*factor)*r.rnd.Float64())) * time.Millisecond
	default:
		spread := 0
		if index < len(r.cfg.SpreadMs) && r.cfg.SpreadMs[index] > 0 {
			spread = r.cfg.SpreadMs[index]
		}
		return time.Duration(base+int(float64(spread)*r.rnd.Float64())) * time.Millisecond
	}
}

type globalRand struct{}

func (globalRand) Float64() float64 { return rand.Float64() } //nolint:gosec // 退避抖动不是安全敏感随机

const retryBudgetMinSamples = 20

type retryBudget struct {
	mu      sync.Mutex
	window  time.Duration
	calls   []time.Time
	retries []time.Time
}

func newRetryBudget(window time.Duration) *retryBudget {
	if window <= 0 {
		window = 60 * time.Second
	}
	return &retryBudget{window: window}
}

func (b *retryBudget) recordCall(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	b.calls = append(b.calls, now)
}

// allowRetry 在冷启动（样本 <20）恒放行；否则按 retries/(calls+retries) ≤ ratio 判定。
func (b *retryBudget) allowRetry(now time.Time, ratio float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)

	total := len(b.calls) + len(b.retries)
	if total < retryBudgetMinSamples {
		b.retries = append(b.retries, now)
		return true
	}
	if float64(len(b.retries)+1)/float64(total+1) > ratio {
		return false
	}
	b.retries = append(b.retries, now)
	return true
}

func (b *retryBudget) pruneLocked(now time.Time) {
	cutoff := now.Add(-b.window)
	b.calls = pruneTimes(b.calls, cutoff)
	b.retries = pruneTimes(b.retries, cutoff)
}

func pruneTimes(times []time.Time, cutoff time.Time) []time.Time {
	index := 0
	for index < len(times) && !times[index].After(cutoff) {
		index++
	}
	return times[index:]
}
