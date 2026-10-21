// 文件名: fake_clock_test.go
// 用途: 可推进的假 Clock，驱动限流等待与重试/熔断时间分支。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package retry

import (
	"sync"
	"testing"
	"time"
)

var limiterNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu         sync.Mutex
	now        time.Time
	timers     []*fakeTimer
	timerAdded chan struct{}
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, timerAdded: make(chan struct{}, 1)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	if d <= 0 {
		ch <- c.now
		c.mu.Unlock()
		return ch
	}
	c.timers = append(c.timers, &fakeTimer{at: c.now.Add(d), ch: ch})
	c.mu.Unlock()

	select {
	case c.timerAdded <- struct{}{}:
	default:
	}
	return ch
}

func (c *fakeClock) Sleep(d time.Duration) {
	<-c.After(d)
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	due := make([]*fakeTimer, 0, len(c.timers))
	remaining := c.timers[:0]
	for _, timer := range c.timers {
		if !timer.at.After(now) {
			due = append(due, timer)
			continue
		}
		remaining = append(remaining, timer)
	}
	c.timers = remaining
	c.mu.Unlock()

	for _, timer := range due {
		timer.ch <- now
	}
}

func (c *fakeClock) WaitTimer(t *testing.T) {
	t.Helper()
	select {
	case <-c.timerAdded:
	case <-time.After(2 * time.Second):
		t.Fatal("等待方未在 2s 内注册定时器")
	}
}
