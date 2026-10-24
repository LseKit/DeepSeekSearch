// 文件名: singleflight_test.go
// 用途: 验证进程内单飞、错误共享、panic 清理、ctx 接管与租约基础操作。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

const sfWaitMs = 2000

func newSF(t *testing.T, cache resilience.Cache, clk resilience.Clock, owner string) *singleflight {
	t.Helper()
	sf, ok := NewSingleflight(cache, clk).(*singleflight)
	if !ok {
		t.Fatalf("NewSingleflight() 返回 %T, want *singleflight", sf)
	}
	if owner != "" {
		sf.owner = owner
	}
	sf.pollInterval = time.Millisecond
	sf.pollAfter = time.After
	return sf
}

func commitTo(c resilience.Cache, key string) func(context.Context, []byte) error {
	return func(ctx context.Context, val []byte) error {
		return c.Put(ctx, key, val, time.Hour)
	}
}

type sfResult struct {
	val    []byte
	shared bool
	err    error
}

func receive(t *testing.T, ch <-chan sfResult) sfResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(3 * time.Second):
		t.Fatal("3s 内未收到 singleflight 结果")
		return sfResult{}
	}
}

func TestSingleflight(t *testing.T) {
	t.Parallel()

	t.Run("同key只执行一次", func(t *testing.T) {
		t.Parallel()
		sf := newSF(t, nil, nil, "")
		var calls atomic.Int64
		release := make(chan struct{})
		results := make(chan sfResult, 5)
		for range 5 {
			go func() {
				val, shared, err := sf.Do(context.Background(), "fetch:same", sfWaitMs,
					func(context.Context) ([]byte, error) {
						calls.Add(1)
						<-release
						return []byte("body"), nil
					}, nil)
				results <- sfResult{val: val, shared: shared, err: err}
			}()
		}
		<-time.After(10 * time.Millisecond)
		close(release)
		sharedCount := 0
		for range 5 {
			res := receive(t, results)
			if res.err != nil || string(res.val) != "body" {
				t.Fatalf("Do() = (%q, %v)", res.val, res.err)
			}
			if res.shared {
				sharedCount++
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 次数 = %d, want 1", calls.Load())
		}
		if sharedCount != 4 {
			t.Fatalf("shared 数量 = %d, want 4", sharedCount)
		}
	})

	t.Run("不同key各执行一次", func(t *testing.T) {
		t.Parallel()
		sf := newSF(t, nil, nil, "")
		var calls atomic.Int64
		var wg sync.WaitGroup
		for i := range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				key := fmt.Sprintf("fetch:k%d", i)
				if _, _, err := sf.Do(context.Background(), key, sfWaitMs,
					func(context.Context) ([]byte, error) {
						calls.Add(1)
						return []byte(key), nil
					}, nil); err != nil {
					t.Errorf("Do(%s) = %v", key, err)
				}
			}()
		}
		wg.Wait()
		if calls.Load() != 5 {
			t.Fatalf("fn 次数 = %d, want 5", calls.Load())
		}
	})

	t.Run("owner错误进程内共享同一对象", func(t *testing.T) {
		t.Parallel()
		sf := newSF(t, nil, nil, "")
		want := errors.New("upstream boom")
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int64
		ownerErr := make(chan error, 1)
		waiterErr := make(chan error, 1)
		go func() {
			_, _, err := sf.Do(context.Background(), "fetch:err", sfWaitMs,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					close(started)
					<-release
					return nil, want
				}, nil)
			ownerErr <- err
		}()
		<-started
		go func() {
			_, _, err := sf.Do(context.Background(), "fetch:err", sfWaitMs,
				func(context.Context) ([]byte, error) { return nil, want }, nil)
			waiterErr <- err
		}()
		<-time.After(10 * time.Millisecond)
		close(release)
		if got := <-ownerErr; !errors.Is(got, want) {
			t.Fatalf("owner error = %v", got)
		}
		if got := <-waiterErr; !errors.Is(got, want) {
			t.Fatalf("waiter error = %v", got)
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 次数 = %d, want 1", calls.Load())
		}
	})

	t.Run("fn panic清理调用与租约", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		sf := newSF(t, c, clk, "owner-a")
		key := "fetch:panic"
		_, _, err := sf.Do(context.Background(), key, sfWaitMs,
			func(context.Context) ([]byte, error) { panic("boom") },
			commitTo(c, key))
		if domain.CodeOf(err) != domain.CodeLocInternal {
			t.Fatalf("panic 错误码 = %d, want 3099 (err=%v)", domain.CodeOf(err), err)
		}
		sf.mu.Lock()
		_, stuck := sf.calls[cacheKey(key)]
		sf.mu.Unlock()
		if stuck {
			t.Fatal("panic 后 map 条目未清除")
		}
		var count int
		if err := c.db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM inflight`).Scan(&count); err != nil {
			t.Fatalf("查 inflight = %v", err)
		}
		if count != 0 {
			t.Fatalf("panic 后租约残留 %d 条", count)
		}
		got, _, err := sf.Do(context.Background(), key, sfWaitMs,
			func(context.Context) ([]byte, error) { return []byte("ok"), nil },
			commitTo(c, key))
		if err != nil || string(got) != "ok" {
			t.Fatalf("panic 后再次 Do() = (%q, %v)", got, err)
		}
	})

	t.Run("owner取消后等待者仍拿到值", func(t *testing.T) {
		t.Parallel()
		sf := newSF(t, nil, nil, "")
		ownerCtx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int64
		ownerDone := make(chan sfResult, 1)
		go func() {
			val, shared, err := sf.Do(ownerCtx, "fetch:cancel", sfWaitMs,
				func(ctx context.Context) ([]byte, error) {
					calls.Add(1)
					close(started)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
						return []byte("body"), nil
					}
				}, nil)
			ownerDone <- sfResult{val: val, shared: shared, err: err}
		}()
		<-started
		waiterDone := make(chan sfResult, 1)
		go func() {
			val, shared, err := sf.Do(context.Background(), "fetch:cancel", sfWaitMs,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					return nil, errors.New("不应执行")
				}, nil)
			waiterDone <- sfResult{val: val, shared: shared, err: err}
		}()
		<-time.After(10 * time.Millisecond)
		cancel()
		close(release)
		waiter := receive(t, waiterDone)
		owner := receive(t, ownerDone)
		if waiter.err != nil || string(waiter.val) != "body" {
			t.Fatalf("等待者 = (%q, %v)", waiter.val, waiter.err)
		}
		if owner.err != nil {
			t.Fatalf("owner = %v", owner.err)
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 次数 = %d, want 1", calls.Load())
		}
	})

	t.Run("等待者共享只读val", func(t *testing.T) {
		t.Parallel()
		sf := newSF(t, nil, nil, "")
		release := make(chan struct{})
		results := make(chan sfResult, 3)
		for range 3 {
			go func() {
				val, shared, err := sf.Do(context.Background(), "fetch:readonly", sfWaitMs,
					func(context.Context) ([]byte, error) {
						<-release
						return bytes.Repeat([]byte("v"), 8), nil
					}, nil)
				results <- sfResult{val: val, shared: shared, err: err}
			}()
		}
		<-time.After(10 * time.Millisecond)
		close(release)
		first := receive(t, results)
		second := receive(t, results)
		third := receive(t, results)
		for _, res := range []sfResult{first, second, third} {
			if res.err != nil || len(res.val) != 8 {
				t.Fatalf("结果 = (%v, %v)", res.val, res.err)
			}
		}
		// 三个结果共享同一底层数组；MUST NOT 原地修改（约定见 Do 方法注释）。
		if &first.val[0] != &second.val[0] || &first.val[0] != &third.val[0] {
			t.Fatal("等待者未共享同一底层数组")
		}
	})

	t.Run("等待超时返回3075", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		sf := newSF(t, c, clk, "")
		key := "fetch:timeout"
		ok, err := c.acquireLease(context.Background(), cacheKey(key), "ghost", clk.Now().Unix()+220)
		if err != nil || !ok {
			t.Fatalf("插入租约 = (%v, %v)", ok, err)
		}
		var calls atomic.Int64
		result := make(chan error, 1)
		go func() {
			_, _, doErr := sf.Do(context.Background(), key, 100,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					return nil, nil
				}, nil)
			result <- doErr
		}()
		// 等待方的截止时间取自注入的 FakeClock，必须推进时钟才会触发 3075。
		timeout := time.After(2 * time.Second)
		for {
			select {
			case err = <-result:
			case <-timeout:
				t.Fatal("FakeClock 推进后仍未返回 3075")
			default:
				clk.Advance(time.Millisecond)
				time.Sleep(100 * time.Microsecond)
				continue
			}
			break
		}
		if domain.CodeOf(err) != domain.CodeLocSingleflightTimeout {
			t.Fatalf("错误码 = %d, want 3075", domain.CodeOf(err))
		}
		payload := domain.ToPayload(err, "ds_fetch")
		if payload.Details["key"] != key || payload.Details["waited_ms"] != 100 {
			t.Fatalf("details = %v", payload.Details)
		}
		if calls.Load() != 0 {
			t.Fatalf("超时不应执行 fn，实际 %d 次", calls.Load())
		}
	})

	t.Run("过期租约可抢占", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		sf := newSF(t, c, clk, "new-owner")
		key := "fetch:expired"
		ok, err := c.acquireLease(context.Background(), cacheKey(key), "ghost", clk.Now().Unix()-1)
		if err != nil || !ok {
			t.Fatalf("插入过期租约 = (%v, %v)", ok, err)
		}
		var calls atomic.Int64
		got, _, err := sf.Do(context.Background(), key, sfWaitMs,
			func(context.Context) ([]byte, error) {
				calls.Add(1)
				return []byte("body"), nil
			}, commitTo(c, key))
		if err != nil || string(got) != "body" {
			t.Fatalf("Do() = (%q, %v)", got, err)
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 次数 = %d, want 1", calls.Load())
		}
	})

	t.Run("两个抢占者只有一个成功", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		key := cacheKey("fetch:preempt-race")
		now := clk.Now().Unix()
		if ok, err := c.acquireLease(context.Background(), key, "ghost", now-1); err != nil || !ok {
			t.Fatalf("插入过期租约 = (%v, %v)", ok, err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var successes atomic.Int64
		for _, owner := range []string{"owner-a", "owner-b"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ok, err := c.preemptLease(context.Background(), key, owner, now+220, now)
				if err != nil {
					t.Errorf("preemptLease() = %v", err)
					return
				}
				if ok {
					successes.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if successes.Load() != 1 {
			t.Fatalf("抢占成功数 = %d, want 1", successes.Load())
		}
	})

	t.Run("ReleaseAll只删自己的租约", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		now := clk.Now().Unix()
		for key, owner := range map[string]string{
			"fetch:a": "owner-a",
			"fetch:b": "owner-b",
		} {
			if ok, err := c.acquireLease(context.Background(), cacheKey(key), owner, now+220); err != nil || !ok {
				t.Fatalf("插入 %s 租约 = (%v, %v)", key, ok, err)
			}
		}
		sf := newSF(t, c, clk, "owner-a")
		released, err := sf.ReleaseAll(context.Background(), "owner-a")
		if err != nil || released != 1 {
			t.Fatalf("ReleaseAll() = (%d, %v), want (1, nil)", released, err)
		}
		var count int
		if err := c.db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM inflight`).Scan(&count); err != nil {
			t.Fatalf("查 inflight = %v", err)
		}
		if count != 1 {
			t.Fatalf("inflight 行数 = %d, want 1", count)
		}
		lease, found, err := c.readLease(context.Background(), cacheKey("fetch:b"))
		if err != nil || !found || lease.Owner != "owner-b" {
			t.Fatalf("fetch:b 租约 = (%+v, %v, %v)", lease, found, err)
		}
	})

	t.Run("cache为nil仍能单飞", func(t *testing.T) {
		t.Parallel()
		sf := newSF(t, nil, nil, "")
		var calls atomic.Int64
		release := make(chan struct{})
		results := make(chan sfResult, 5)
		for range 5 {
			go func() {
				val, shared, err := sf.Do(context.Background(), "fetch:nil-cache", sfWaitMs,
					func(context.Context) ([]byte, error) {
						calls.Add(1)
						<-release
						return []byte("body"), nil
					}, nil)
				results <- sfResult{val: val, shared: shared, err: err}
			}()
		}
		<-time.After(10 * time.Millisecond)
		close(release)
		for range 5 {
			res := receive(t, results)
			if res.err != nil || string(res.val) != "body" {
				t.Fatalf("Do() = (%q, %v)", res.val, res.err)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 次数 = %d, want 1", calls.Load())
		}
	})
}
