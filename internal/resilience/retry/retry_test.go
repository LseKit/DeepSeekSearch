// 文件名: retry_test.go
// 用途: 验证 Should 的 Reason 分流、三种 jitter、越界 clamp 与冷启动重试预算。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package retry

import (
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

type fixedRand struct{ v float64 }

func (r fixedRand) Float64() float64 { return r.v }

func TestRetry(t *testing.T) {
	t.Parallel()

	retryable := domain.NewUpstreamError(429, "", "", nil)
	notRetryable := domain.NewUpstreamError(404, "", "", nil)
	balance := domain.NewUpstreamError(402, "", "", nil)
	clk := newFakeClock(limiterNow)

	t.Run("Should分流", func(t *testing.T) {
		t.Parallel()
		r := testRetrier(clk, fixedRand{0.5}, nil)
		tests := []struct {
			name    string
			err     error
			attempt int
			want    bool
			reason  resilience.Reason
		}{
			{name: "429可重试", err: retryable, attempt: 1, want: true, reason: resilience.ReasonOK},
			{name: "404不可重试", err: notRetryable, attempt: 1, want: false, reason: resilience.ReasonNotRetryable},
			{name: "402余额不足不可重试", err: balance, attempt: 1, want: false, reason: resilience.ReasonNotRetryable},
			{name: "次数耗尽", err: retryable, attempt: 4, want: false, reason: resilience.ReasonAttemptsExhausted},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, reason := r.Should(tt.err, tt.attempt)
				if got != tt.want || reason != tt.reason {
					t.Fatalf("Should() = (%v, %v), want (%v, %v)", got, reason, tt.want, tt.reason)
				}
			})
		}
	})

	t.Run("disabled不重试", func(t *testing.T) {
		t.Parallel()
		r := testRetrier(clk, fixedRand{0.5}, func(c *resilience.Config) { c.RetryEnabled = false })
		if got, reason := r.Should(retryable, 1); got || reason != resilience.ReasonDisabled {
			t.Fatalf("Should() = (%v, %v), want (false, disabled)", got, reason)
		}
	})

	t.Run("fixed_plus_random", func(t *testing.T) {
		t.Parallel()
		r := testRetrier(clk, fixedRand{0.5}, nil)
		if got := r.Delay(1); got != 1500*time.Millisecond {
			t.Fatalf("Delay(1) = %v, want 1.5s", got)
		}
		if got := r.Delay(3); got != 10000*time.Millisecond {
			t.Fatalf("Delay(3) = %v, want 10s", got)
		}
		sum := r.Delay(1) + r.Delay(2) + r.Delay(3)
		if sum < 12000*time.Millisecond || sum >= 19000*time.Millisecond {
			t.Fatalf("累计退避 = %v, want [12s,19s)", sum)
		}
	})

	t.Run("full_jitter", func(t *testing.T) {
		t.Parallel()
		r := testRetrier(clk, fixedRand{0.5}, func(c *resilience.Config) { c.JitterMode = "full" })
		if got := r.Delay(1); got != 1000*time.Millisecond {
			t.Fatalf("Delay(1) = %v, want 1s", got)
		}
		if got := r.Delay(2); got != 6000*time.Millisecond {
			t.Fatalf("Delay(2) = %v, want 6s", got)
		}
	})

	t.Run("none_jitter", func(t *testing.T) {
		t.Parallel()
		r := testRetrier(clk, fixedRand{0.5}, func(c *resilience.Config) { c.JitterMode = "none" })
		if got := r.Delay(2); got != 3000*time.Millisecond {
			t.Fatalf("Delay(2) = %v, want 3s", got)
		}
	})

	t.Run("attempt越界clamp", func(t *testing.T) {
		t.Parallel()
		r := testRetrier(clk, fixedRand{0.5}, func(c *resilience.Config) { c.JitterMode = "none" })
		if got := r.Delay(6); got != 8000*time.Millisecond {
			t.Fatalf("Delay(6) = %v, want 最后一档 8s", got)
		}
	})

	t.Run("spread长度不足取0", func(t *testing.T) {
		t.Parallel()
		r := testRetrier(clk, fixedRand{0.5}, func(c *resilience.Config) {
			c.FixedMs = []int{1000, 3000}
			c.SpreadMs = []int{1000}
		})
		if got := r.Delay(2); got != 3000*time.Millisecond {
			t.Fatalf("Delay(2) = %v, want 3s（spread 越界取 0）", got)
		}
	})
}

func TestRetryBudget(t *testing.T) {
	t.Parallel()

	retryable := domain.NewUpstreamError(429, "", "", nil)
	notRetryable := domain.NewUpstreamError(404, "", "", nil)
	clk := newFakeClock(limiterNow)
	cfg := resilience.Config{
		RetryEnabled:       true,
		MaxAttempts:        6,
		JitterMode:         "none",
		FixedMs:            []int{1},
		SpreadMs:           []int{0},
		RetryBudgetEnabled: true,
		RetryBudgetRatio:   0.1,
		RetryBudgetWindowS: 60,
	}

	t.Run("冷启动放行", func(t *testing.T) {
		t.Parallel()
		r := NewRetry(cfg, clk, fixedRand{0})
		if ok, reason := r.Should(retryable, 1); !ok || reason != resilience.ReasonOK {
			t.Fatalf("冷启动 Should() = (%v, %v)", ok, reason)
		}
	})

	t.Run("样本足够后预算耗尽", func(t *testing.T) {
		t.Parallel()
		r := NewRetry(cfg, clk, fixedRand{0})
		for i := 0; i < 100; i++ {
			r.Should(notRetryable, 1)
		}
		allowed := 0
		for {
			ok, reason := r.Should(retryable, 2)
			if !ok {
				if reason != resilience.ReasonBudgetExhausted {
					t.Fatalf("reason = %v, want budget_exhausted", reason)
				}
				break
			}
			allowed++
			if allowed > 100 {
				t.Fatal("预算从未耗尽")
			}
		}
		if allowed < 1 || allowed > 20 {
			t.Fatalf("允许的重试数 = %d, want 1~20", allowed)
		}
	})

	t.Run("实例间预算隔离", func(t *testing.T) {
		t.Parallel()
		r1 := NewRetry(cfg, clk, fixedRand{0})
		r2 := NewRetry(cfg, clk, fixedRand{0})
		for i := 0; i < 100; i++ {
			r1.Should(notRetryable, 1)
		}
		for {
			if ok, _ := r1.Should(retryable, 2); !ok {
				break
			}
		}
		if ok, reason := r2.Should(retryable, 1); !ok || reason != resilience.ReasonOK {
			t.Fatalf("另一个实例应独立预算: (%v, %v)", ok, reason)
		}
	})

	t.Run("窗口滑过后重置", func(t *testing.T) {
		t.Parallel()
		local := newFakeClock(limiterNow)
		r := NewRetry(cfg, local, fixedRand{0})
		for i := 0; i < 100; i++ {
			r.Should(notRetryable, 1)
		}
		for {
			if ok, _ := r.Should(retryable, 2); !ok {
				break
			}
		}
		local.Advance(61 * time.Second)
		if ok, reason := r.Should(retryable, 2); !ok || reason != resilience.ReasonOK {
			t.Fatalf("窗口滑过后应放行: (%v, %v)", ok, reason)
		}
	})
}

func testRetrier(clk resilience.Clock, rnd RandSource, mutate func(*resilience.Config)) resilience.Retry {
	cfg := resilience.Config{
		RetryEnabled: true,
		MaxAttempts:  4,
		JitterMode:   "fixed_plus_random",
		FixedMs:      []int{1000, 3000, 8000},
		SpreadMs:     []int{1000, 2000, 4000},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewRetry(cfg, clk, rnd)
}
