// 文件名: budget_test.go
// 用途: 验证窗口/日累计硬顶、跨日清零、按 key 隔离与 rune 截断。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package budget

import (
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

func TestBudget(t *testing.T) {
	t.Parallel()

	t.Run("窗口硬顶", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		b := newTestBudget(clk)
		if err := b.CheckWindow("search"); err != nil {
			t.Fatalf("初始 CheckWindow() = %v", err)
		}
		b.Record("search", 2)
		err := b.CheckWindow("search")
		if got := domain.CodeOf(err); got != domain.CodeLocBudgetExceeded {
			t.Fatalf("Code = %d, want %d", got, domain.CodeLocBudgetExceeded)
		}
		details := domain.ToPayload(err, "").Details
		if details["kind"] != "window" || details["key"] != "search" {
			t.Fatalf("details = %v", details)
		}
		if err := b.CheckWindow("fetch"); err != nil {
			t.Fatalf("fetch 不应受 search 影响: %v", err)
		}
	})

	t.Run("日累计硬顶与跨日清零", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		b := newTestBudget(clk)
		b.Record("search", 2)
		if err := b.CheckDaily("search"); domain.CodeOf(err) != domain.CodeLocBudgetExceeded {
			t.Fatalf("日累计应超限，err=%v", err)
		}
		clk.Advance(24 * time.Hour)
		if err := b.CheckDaily("search"); err != nil {
			t.Fatalf("跨日后应清零: %v", err)
		}
	})

	t.Run("窗口滑动", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		b := newTestBudget(clk)
		b.Record("search", 2)
		clk.Advance(61 * time.Second)
		if err := b.CheckWindow("search"); err != nil {
			t.Fatalf("窗口滑过后应放行: %v", err)
		}
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		b := newTestBudget(clk)
		b.Record("search", 2)
		b.Reset()
		if err := b.CheckWindow("search"); err != nil {
			t.Fatalf("Reset 后 CheckWindow() = %v", err)
		}
		if err := b.CheckDaily("search"); err != nil {
			t.Fatalf("Reset 后 CheckDaily() = %v", err)
		}
	})

	t.Run("TrimChars", func(t *testing.T) {
		t.Parallel()
		b := newTestBudget(newFakeClock(limiterNow))
		if got, truncated := b.TrimChars("abcdef"); got != "abcde" || !truncated {
			t.Fatalf("TrimChars(abcdef) = (%q, %v)", got, truncated)
		}
		if got, truncated := b.TrimChars("abc"); got != "abc" || truncated {
			t.Fatalf("TrimChars(abc) = (%q, %v)", got, truncated)
		}
		if got, truncated := b.TrimChars("中文abcd"); got != "中文abc" || !truncated {
			t.Fatalf("TrimChars(中文abcd) = (%q, %v)", got, truncated)
		}
	})

	t.Run("Record非正数忽略", func(t *testing.T) {
		t.Parallel()
		b := newTestBudget(newFakeClock(limiterNow))
		b.Record("search", 0)
		b.Record("search", -1)
		if err := b.CheckWindow("search"); err != nil {
			t.Fatalf("非正数不应计数: %v", err)
		}
	})
}

func newTestBudget(clk resilience.Clock) resilience.Budget {
	return NewBudget(resilience.Config{
		MaxCallsPerWindow: 2,
		WindowS:           60,
		MaxCallsPerDay:    2,
		MaxTotalChars:     5,
		TZ:                time.UTC,
	}, clk)
}
