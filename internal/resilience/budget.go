// 文件名: budget.go
// 用途: 三层硬顶中的调用次数窗口/日累计与单次响应字符截断。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"fmt"
	"sync"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

const dateLayout = "2006-01-02"

// Budget 是三层硬顶：单次响应字符数 / 时间窗内调用次数 / 日累计调用次数。
type Budget interface {
	// CheckWindow 在发起上游请求前调用；超限返回 3070。
	CheckWindow(key string) error
	// CheckDaily 在发起上游请求前调用；超限返回 3070。
	CheckDaily(key string) error
	// Record 在请求成功后调用一次，累加窗口与日计数。
	Record(key string, upstreamCalls int)
	// TrimChars 截断一段文本到剩余字符预算，返回截断后文本与是否被截断。
	TrimChars(s string) (string, bool)
	// Reset 清零全部计数（测试用）。
	Reset()
}

type budget struct {
	mu     sync.Mutex
	clk    Clock
	cfg    Config
	tz     *time.Location
	window time.Duration
	day    string
	daily  map[string]int
	recent map[string][]budgetEntry
}

type budgetEntry struct {
	at time.Time
	n  int
}

// NewBudget 构造预算控制器。
func NewBudget(cfg Config, clk Clock) Budget { //nolint:gocritic // 签名由 11 号 §四 固定
	if clk == nil {
		clk = SystemClock()
	}
	tz := cfg.TZ
	if tz == nil {
		tz = time.UTC
	}
	windowS := cfg.WindowS
	if windowS <= 0 {
		windowS = 3600
	}
	now := clk.Now().In(tz)
	return &budget{
		clk:    clk,
		cfg:    cfg,
		tz:     tz,
		window: time.Duration(windowS) * time.Second,
		day:    now.Format(dateLayout),
		daily:  make(map[string]int),
		recent: make(map[string][]budgetEntry),
	}
}

func (b *budget) CheckWindow(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clk.Now()
	b.pruneLocked(key, now)
	used := b.windowSumLocked(key)
	if b.cfg.MaxCallsPerWindow > 0 && used >= b.cfg.MaxCallsPerWindow {
		return budgetExceeded("window", key, used, b.cfg.MaxCallsPerWindow)
	}
	return nil
}

func (b *budget) CheckDaily(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clk.Now()
	b.rollDayLocked(now)
	used := b.daily[key]
	if b.cfg.MaxCallsPerDay > 0 && used >= b.cfg.MaxCallsPerDay {
		return budgetExceeded("daily", key, used, b.cfg.MaxCallsPerDay)
	}
	return nil
}

func (b *budget) Record(key string, upstreamCalls int) {
	if upstreamCalls <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clk.Now()
	b.rollDayLocked(now)
	b.pruneLocked(key, now)
	b.recent[key] = append(b.recent[key], budgetEntry{at: now, n: upstreamCalls})
	b.daily[key] += upstreamCalls
}

func (b *budget) TrimChars(s string) (string, bool) {
	if b.cfg.MaxTotalChars <= 0 {
		return s, false
	}
	return domain.SliceByRune(s, 0, b.cfg.MaxTotalChars)
}

func (b *budget) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.daily = make(map[string]int)
	b.recent = make(map[string][]budgetEntry)
	b.day = b.clk.Now().In(b.tz).Format(dateLayout)
}

func (b *budget) pruneLocked(key string, now time.Time) {
	cutoff := now.Add(-b.window)
	entries := b.recent[key]
	index := 0
	for index < len(entries) && !entries[index].at.After(cutoff) {
		index++
	}
	if index > 0 {
		b.recent[key] = entries[index:]
	}
}

func (b *budget) windowSumLocked(key string) int {
	total := 0
	for _, entry := range b.recent[key] {
		total += entry.n
	}
	return total
}

func (b *budget) rollDayLocked(now time.Time) {
	today := now.In(b.tz).Format(dateLayout)
	if today == b.day {
		return
	}
	b.day = today
	b.daily = make(map[string]int)
}

func budgetExceeded(kind, key string, used, limit int) error {
	return fmt.Errorf("resilience: %w", domain.NewLocalError(domain.CodeLocBudgetExceeded, "调用次数超限", map[string]any{
		"kind":  kind,
		"key":   key,
		"used":  used,
		"limit": limit,
	}))
}
