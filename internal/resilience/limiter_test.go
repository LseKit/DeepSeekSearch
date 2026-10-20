// 文件名: limiter_test.go
// 用途: 验证令牌桶、per-key 在飞配额、双段等待、超时 details 与并发安全。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

var limiterNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func TestLimiter(t *testing.T) {
	t.Run("未知key报错", func(t *testing.T) {
		l := mustLimiter(t, newFakeClock(limiterNow), defaultSpec(), 8, 0.5, 5*time.Second)
		if err := l.Wait(context.Background(), "other"); err == nil {
			t.Fatal("未知 key 应返回 error")
		}
		if got := l.Depth("other"); got != 0 {
			t.Fatalf("未知 key Depth = %d, want 0", got)
		}
		l.Release("other")
	})

	t.Run("令牌等待", func(t *testing.T) {
		clk := newFakeClock(limiterNow)
		l := mustLimiter(t, clk, defaultSpec(), 8, 0.5, 5*time.Second)
		ctx := context.Background()

		if err := l.Wait(ctx, "search"); err != nil {
			t.Fatalf("第一次 Wait() = %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- l.Wait(ctx, "search") }()
		clk.WaitTimer(t)
		clk.Advance(time.Second)

		if err := <-done; err != nil {
			t.Fatalf("推进 1s 后 Wait() = %v", err)
		}
		l.Release("search")
		l.Release("search")
	})

	t.Run("maxWait超时3000", func(t *testing.T) {
		clk := newFakeClock(limiterNow)
		l := mustLimiter(t, clk, defaultSpec(), 8, 0.5, 100*time.Millisecond)
		ctx := context.Background()

		if err := l.Wait(ctx, "search"); err != nil {
			t.Fatalf("第一次 Wait() = %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- l.Wait(ctx, "search") }()
		clk.WaitTimer(t)
		clk.Advance(100 * time.Millisecond)

		err := <-done
		if got := domain.CodeOf(err); got != domain.CodeLocQueueTimeout {
			t.Fatalf("Code = %d, want %d（err=%v）", got, domain.CodeLocQueueTimeout, err)
		}
		details := domain.ToPayload(err, "").Details
		if details["stage"] != "token" {
			t.Fatalf("stage = %v, want token", details["stage"])
		}
		if qd, ok := details["queue_depth"].(int); !ok || qd < 1 {
			t.Fatalf("queue_depth = %v", details["queue_depth"])
		}
		if waited, ok := details["waited_ms"].(int64); !ok || waited != 100 {
			t.Fatalf("waited_ms = %v, want 100", details["waited_ms"])
		}
		l.Release("search")
	})

	t.Run("ctx取消", func(t *testing.T) {
		clk := newFakeClock(limiterNow)
		l := mustLimiter(t, clk, defaultSpec(), 8, 0.5, 5*time.Second)
		if err := l.Wait(context.Background(), "search"); err != nil {
			t.Fatalf("第一次 Wait() = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- l.Wait(ctx, "search") }()
		clk.WaitTimer(t)
		cancel()

		err := <-done
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		l.Release("search")
	})

	t.Run("search与fetch独立", func(t *testing.T) {
		l := mustLimiter(t, newFakeClock(limiterNow), defaultSpec(), 8, 0.5, 5*time.Second)
		ctx := context.Background()
		if err := l.Wait(ctx, "search"); err != nil {
			t.Fatalf("search Wait() = %v", err)
		}
		if err := l.Wait(ctx, "fetch"); err != nil {
			t.Fatalf("fetch 不应被 search 阻塞: %v", err)
		}
		l.Release("fetch")
		l.Release("search")
	})

	t.Run("在飞配额释放后通过", func(t *testing.T) {
		spec := map[string]BucketSpec{
			"search": {Rps: 1000, Burst: 10},
			"fetch":  {Rps: 1000, Burst: 10},
		}
		l := mustLimiter(t, newFakeClock(limiterNow), spec, 3, 0.1, 5*time.Second)
		ctx := context.Background()
		release := make(chan struct{})
		results := make(chan error, 3)
		for i := 0; i < 3; i++ {
			go func() {
				err := l.Wait(ctx, "fetch")
				results <- err
				if err == nil {
					<-release
					l.Release("fetch")
				}
			}()
		}

		for i := 0; i < 2; i++ {
			if err := <-results; err != nil {
				t.Fatalf("前两个 Wait() = %v", err)
			}
		}
		select {
		case err := <-results:
			t.Fatalf("第三个不应在释放前返回: %v", err)
		default:
		}
		close(release)
		if err := <-results; err != nil {
			t.Fatalf("释放名额后第三个 Wait() = %v", err)
		}
	})

	t.Run("100并发", func(t *testing.T) {
		spec := map[string]BucketSpec{
			"search": {Rps: 10000, Burst: 200},
			"fetch":  {Rps: 10000, Burst: 200},
		}
		l := mustLimiter(t, newFakeClock(limiterNow), spec, 64, 0.5, 10*time.Second)
		ctx := context.Background()

		var wg sync.WaitGroup
		errs := make(chan error, 100)
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := l.Wait(ctx, "search"); err != nil {
					errs <- err
					return
				}
				l.Release("search")
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("并发 Wait() = %v", err)
		}
	})
}

func TestNewLimiter(t *testing.T) {
	t.Parallel()

	valid := defaultSpec()
	tests := []struct {
		name        string
		spec        map[string]BucketSpec
		maxInflight int
		share       float64
		maxWait     time.Duration
		clk         Clock
	}{
		{name: "nil clock", spec: valid, maxInflight: 8, share: 0.5, maxWait: time.Second, clk: nil},
		{name: "缺少fetch", spec: map[string]BucketSpec{"search": {Rps: 1, Burst: 1}}, maxInflight: 8, share: 0.5, maxWait: time.Second, clk: newFakeClock(limiterNow)},
		{name: "rps为0", spec: map[string]BucketSpec{"search": {Rps: 0, Burst: 1}, "fetch": {Rps: 1, Burst: 1}}, maxInflight: 8, share: 0.5, maxWait: time.Second, clk: newFakeClock(limiterNow)},
		{name: "burst为0", spec: map[string]BucketSpec{"search": {Rps: 1, Burst: 0}, "fetch": {Rps: 1, Burst: 1}}, maxInflight: 8, share: 0.5, maxWait: time.Second, clk: newFakeClock(limiterNow)},
		{name: "maxInflight过小", spec: valid, maxInflight: 2, share: 0.5, maxWait: time.Second, clk: newFakeClock(limiterNow)},
		{name: "maxInflight过大", spec: valid, maxInflight: 65, share: 0.5, maxWait: time.Second, clk: newFakeClock(limiterNow)},
		{name: "share为0", spec: valid, maxInflight: 8, share: 0, maxWait: time.Second, clk: newFakeClock(limiterNow)},
		{name: "share为1", spec: valid, maxInflight: 8, share: 1, maxWait: time.Second, clk: newFakeClock(limiterNow)},
		{name: "maxWait为0", spec: valid, maxInflight: 8, share: 0.5, maxWait: 0, clk: newFakeClock(limiterNow)},
		{name: "配额之和超总额度", spec: valid, maxInflight: 8, share: 0.9, maxWait: time.Second, clk: newFakeClock(limiterNow)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewLimiter(tt.spec, tt.maxInflight, tt.share, tt.maxWait, tt.clk); err == nil {
				t.Fatal("期望 NewLimiter 返回 error")
			} else if got := domain.CodeOf(err); got != domain.CodeLocConfigInvalid {
				t.Fatalf("Code = %d, want %d", got, domain.CodeLocConfigInvalid)
			}
		})
	}
}

func TestClock(t *testing.T) {
	clk := SystemClock()
	if clk.Now().IsZero() {
		t.Fatal("SystemClock.Now() 为零值")
	}
	select {
	case <-clk.After(0):
	case <-time.After(time.Second):
		t.Fatal("SystemClock.After(0) 未立即触发")
	}
	clk.Sleep(0)
}

func defaultSpec() map[string]BucketSpec {
	return map[string]BucketSpec{
		"search": {Rps: 1, Burst: 1},
		"fetch":  {Rps: 1, Burst: 1},
	}
}

func mustLimiter(t *testing.T, clk Clock, spec map[string]BucketSpec, maxInflight int, share float64, maxWait time.Duration) Limiter {
	t.Helper()
	l, err := NewLimiter(spec, maxInflight, share, maxWait, clk)
	if err != nil {
		t.Fatalf("NewLimiter() = %v", err)
	}
	return l
}

func TestLimiterDiagnostics(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(limiterNow)
	l := mustLimiter(t, clk, map[string]BucketSpec{
		"search": {Rps: 1000, Burst: 8},
		"fetch":  {Rps: 1000, Burst: 8},
	}, 8, 0.5, 5*time.Second)
	ctx := context.Background()
	for range 4 {
		if err := l.Wait(ctx, "search"); err != nil {
			t.Fatalf("Wait() = %v", err)
		}
	}
	lim, ok := l.(*limiter)
	if !ok {
		t.Fatalf("限流器类型 = %T", l)
	}
	diag := lim.Diagnostics("search")
	if diag.Key != "search" || diag.Inflight != 4 || diag.Quota != 4 {
		t.Fatalf("Diagnostics = %+v", diag)
	}
	if diag.Waiters != 0 {
		t.Fatalf("Waiters = %d, want 0", diag.Waiters)
	}
	if diag.Stage != "inflight" {
		t.Fatalf("Stage = %q, want inflight", diag.Stage)
	}
	if unknown := lim.Diagnostics("other"); unknown.Stage != "unknown" {
		t.Fatalf("未知 key Stage = %q", unknown.Stage)
	}
}
