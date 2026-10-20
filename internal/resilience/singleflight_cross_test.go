// 文件名: singleflight_cross_test.go
// 用途: 验证跨进程单飞的租约共享、错误传播、commit 时序与 oversize 标记。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func newFileCaches(t *testing.T, maxEntryKB int) (first, second *sqliteCache) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.db")
	open := func(offset time.Duration) *sqliteCache {
		clk := newFakeClock(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC).Add(offset))
		cache, err := NewSqliteCache(Config{
			CacheDbPath:         path,
			CacheMaxEntries:     100,
			CacheMaxBytesMB:     10,
			CacheMaxEntryKB:     maxEntryKB,
			CachePruneIntervalS: 300,
			Clock:               clk,
			Logger:              newRecordingLogger(),
		})
		if err != nil {
			t.Fatalf("NewSqliteCache() = %v", err)
		}
		c, ok := cache.(*sqliteCache)
		if !ok {
			t.Fatalf("缓存类型 = %T", cache)
		}
		t.Cleanup(func() {
			if closeErr := c.Close(); closeErr != nil {
				t.Errorf("Close() = %v", closeErr)
			}
		})
		return c
	}
	return open(0), open(time.Nanosecond)
}

type eventRecorder struct {
	*sqliteCache
	mu            sync.Mutex
	events        []string
	blockDelete   chan struct{}
	deleteStarted chan struct{}
	once          sync.Once
}

func (r *eventRecorder) record(event string) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *eventRecorder) Put(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	r.record("put")
	return r.sqliteCache.Put(ctx, key, val, ttl)
}

func (r *eventRecorder) deleteLease(ctx context.Context, key, owner string) error {
	r.record("deleteLease")
	if r.deleteStarted != nil {
		r.once.Do(func() { close(r.deleteStarted) })
	}
	if r.blockDelete != nil {
		<-r.blockDelete
	}
	return r.sqliteCache.deleteLease(ctx, key, owner)
}

func (r *eventRecorder) eventIndex(want string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, event := range r.events {
		if event == want {
			return i
		}
	}
	return -1
}

func TestSingleflightCrossProcess(t *testing.T) {
	t.Parallel()

	t.Run("跨进程共享结果", func(t *testing.T) {
		t.Parallel()
		c1, c2 := newFileCaches(t, 1024)
		sf1 := newSF(t, c1, c1.clk, "owner-a")
		sf2 := newSF(t, c2, c2.clk, "owner-b")
		key := "fetch:shared"
		var calls atomic.Int64
		started := make(chan struct{})
		release := make(chan struct{})
		ownerDone := make(chan sfResult, 1)
		go func() {
			val, shared, err := sf1.Do(context.Background(), key, sfWaitMs,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					close(started)
					<-release
					return []byte("body"), nil
				}, commitTo(c1, key))
			ownerDone <- sfResult{val: val, shared: shared, err: err}
		}()
		<-started
		waiterDone := make(chan sfResult, 1)
		go func() {
			val, shared, err := sf2.Do(context.Background(), key, sfWaitMs,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("duplicate"), nil
				}, commitTo(c2, key))
			waiterDone <- sfResult{val: val, shared: shared, err: err}
		}()
		<-time.After(20 * time.Millisecond)
		close(release)
		waiter := receive(t, waiterDone)
		owner := receive(t, ownerDone)
		if waiter.err != nil || string(waiter.val) != "body" || !waiter.shared {
			t.Fatalf("等待者 = (%q, shared=%v, %v)", waiter.val, waiter.shared, waiter.err)
		}
		if owner.err != nil || owner.shared {
			t.Fatalf("owner = (shared=%v, %v)", owner.shared, owner.err)
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 次数 = %d, want 1", calls.Load())
		}
	})

	t.Run("跨进程等待者拿到owner错误码", func(t *testing.T) {
		t.Parallel()
		c1, c2 := newFileCaches(t, 1024)
		rec := &eventRecorder{
			sqliteCache:   c1,
			blockDelete:   make(chan struct{}),
			deleteStarted: make(chan struct{}),
		}
		sfOwner := newSF(t, rec, c1.clk, "owner-a")
		sfWaiter := newSF(t, c2, c2.clk, "owner-b")
		key := "fetch:owner-error"
		ownerErr := domain.NewUpstreamError(429, "req-1", "rate limited", nil)
		ownerDone := make(chan error, 1)
		go func() {
			_, _, err := sfOwner.Do(context.Background(), key, sfWaitMs,
				func(context.Context) ([]byte, error) { return nil, fmt.Errorf("owner: %w", ownerErr) },
				commitTo(rec, key))
			ownerDone <- err
		}()
		<-rec.deleteStarted
		start := time.Now()
		_, _, err := sfWaiter.Do(context.Background(), key, sfWaitMs,
			func(context.Context) ([]byte, error) {
				t.Error("等待者不应执行 fn")
				return nil, nil
			}, commitTo(c2, key))
		elapsed := time.Since(start)
		if domain.CodeOf(err) != domain.CodeLocSingleflightTimeout {
			t.Fatalf("等待者错误码 = %d, want 3075 (err=%v)", domain.CodeOf(err), err)
		}
		payload := domain.ToPayload(err, "ds_fetch")
		if payload.Details["owner_error_code"] != domain.CodeUpRateLimited {
			t.Fatalf("owner_error_code = %v", payload.Details["owner_error_code"])
		}
		if elapsed > time.Second {
			t.Fatalf("等待者耗时 %v，说明没有立即返回", elapsed)
		}
		close(rec.blockDelete)
		if got := <-ownerDone; domain.CodeOf(got) != domain.CodeUpRateLimited {
			t.Fatalf("owner 错误 = %v", got)
		}
	})

	t.Run("commit先于删除租约", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		rec := &eventRecorder{sqliteCache: c}
		sf := newSF(t, rec, clk, "owner-a")
		key := "fetch:order"
		var calls atomic.Int64
		_, _, err := sf.Do(context.Background(), key, sfWaitMs,
			func(context.Context) ([]byte, error) {
				calls.Add(1)
				return []byte("body"), nil
			}, commitTo(rec, key))
		if err != nil {
			t.Fatalf("Do() = %v", err)
		}
		putAt := rec.eventIndex("put")
		deleteAt := rec.eventIndex("deleteLease")
		if putAt < 0 || deleteAt < 0 || putAt > deleteAt {
			t.Fatalf("事件顺序 = %v，want put 早于 deleteLease", rec.events)
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 次数 = %d", calls.Load())
		}
	})

	t.Run("commit窗口内的等待者不重复计费", func(t *testing.T) {
		t.Parallel()
		c1, c2 := newFileCaches(t, 1024)
		sf1 := newSF(t, c1, c1.clk, "owner-a")
		sf2 := newSF(t, c2, c2.clk, "owner-b")
		key := "fetch:window"
		var calls atomic.Int64
		commitStarted := make(chan struct{})
		releaseCommit := make(chan struct{})
		ownerDone := make(chan sfResult, 1)
		go func() {
			val, shared, err := sf1.Do(context.Background(), key, sfWaitMs,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("body"), nil
				}, func(ctx context.Context, val []byte) error {
					close(commitStarted)
					<-releaseCommit
					return c1.Put(ctx, key, val, time.Hour)
				})
			ownerDone <- sfResult{val: val, shared: shared, err: err}
		}()
		<-commitStarted
		waiterDone := make(chan sfResult, 1)
		go func() {
			val, shared, err := sf2.Do(context.Background(), key, sfWaitMs,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("duplicate"), nil
				}, commitTo(c2, key))
			waiterDone <- sfResult{val: val, shared: shared, err: err}
		}()
		<-time.After(20 * time.Millisecond)
		if calls.Load() != 1 {
			t.Fatalf("commit 完成前 fn 次数 = %d, want 1", calls.Load())
		}
		close(releaseCommit)
		waiter := receive(t, waiterDone)
		owner := receive(t, ownerDone)
		if waiter.err != nil || string(waiter.val) != "body" {
			t.Fatalf("等待者 = (%q, %v)", waiter.val, waiter.err)
		}
		if owner.err != nil {
			t.Fatalf("owner = %v", owner.err)
		}
		if calls.Load() != 1 {
			t.Fatalf("fn 总次数 = %d, want 1", calls.Load())
		}
	})

	t.Run("超限值写oversize标记", func(t *testing.T) {
		t.Parallel()
		c1, c2 := newFileCaches(t, 1)
		sf1 := newSF(t, c1, c1.clk, "owner-a")
		sf2 := newSF(t, c2, c2.clk, "owner-b")
		key := "fetch:oversize"
		val := bytes.Repeat([]byte("x"), 2048)
		var commitCalls atomic.Int64
		got, _, err := sf1.Do(context.Background(), key, sfWaitMs,
			func(context.Context) ([]byte, error) { return val, nil },
			func(context.Context, []byte) error {
				commitCalls.Add(1)
				return nil
			})
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("owner Do() = (%d bytes, %v)", len(got), err)
		}
		if commitCalls.Load() != 0 {
			t.Fatal("超限值不应调用 commit")
		}
		marker, hit, err := c1.Get(context.Background(), key)
		if err != nil || !hit {
			t.Fatalf("oversize 标记 Get() = (%v, %v)", hit, err)
		}
		var decoded map[string]any
		if json.Unmarshal(marker, &decoded) != nil || decoded["oversize"] != true {
			t.Fatalf("oversize 标记 = %s", marker)
		}
		start := time.Now()
		_, _, err = sf2.Do(context.Background(), key, sfWaitMs,
			func(context.Context) ([]byte, error) {
				t.Error("等待者不应执行 fn")
				return nil, nil
			}, commitTo(c2, key))
		if domain.CodeOf(err) != domain.CodeLocSingleflightTimeout {
			t.Fatalf("等待者错误码 = %d, want 3075", domain.CodeOf(err))
		}
		payload := domain.ToPayload(err, "ds_fetch")
		if payload.Details["oversize"] != true {
			t.Fatalf("details = %v", payload.Details)
		}
		if time.Since(start) > time.Second {
			t.Fatal("等待者没有立即返回")
		}
	})

	t.Run("租约消失后重新抢租约", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		sf := newSF(t, c, clk, "new-owner")
		key := "fetch:gone"
		ok, err := c.acquireLease(context.Background(), cacheKey(key), "ghost", clk.Now().Unix()+220)
		if err != nil || !ok {
			t.Fatalf("插入租约 = (%v, %v)", ok, err)
		}
		var calls atomic.Int64
		calls.Store(1)
		done := make(chan sfResult, 1)
		go func() {
			val, shared, err := sf.Do(context.Background(), key, sfWaitMs,
				func(context.Context) ([]byte, error) {
					calls.Add(1)
					return []byte("body"), nil
				}, commitTo(c, key))
			done <- sfResult{val: val, shared: shared, err: err}
		}()
		<-time.After(20 * time.Millisecond)
		if err := c.deleteLease(context.Background(), cacheKey(key), "ghost"); err != nil {
			t.Fatalf("删除 ghost 租约 = %v", err)
		}
		res := receive(t, done)
		if res.err != nil || string(res.val) != "body" {
			t.Fatalf("Do() = (%q, %v)", res.val, res.err)
		}
		if calls.Load() != 2 {
			t.Fatalf("fn 次数 = %d, want 2", calls.Load())
		}
	})
}
