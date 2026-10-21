// 文件名: coverage_extra_test.go
// 用途: 覆盖缓存关闭态、默认值、租约后端错误与 singleflight 失败分支。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package cache

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

// failingLeases 让全部租约操作返回错误，驱动降级分支。
type failingLeases struct{}

func (failingLeases) acquireLease(context.Context, string, string, int64) (bool, error) {
	return false, errors.New("acquire failed")
}

func (failingLeases) preemptLease(context.Context, string, string, int64, int64) (bool, error) {
	return false, errors.New("preempt failed")
}

func (failingLeases) readLease(context.Context, string) (leaseRow, bool, error) {
	return leaseRow{}, false, errors.New("read failed")
}

func (failingLeases) markLeaseError(context.Context, string, string, int) error {
	return errors.New("mark failed")
}

func (failingLeases) deleteLease(context.Context, string, string) error {
	return errors.New("delete failed")
}

func (failingLeases) releaseAllLeases(context.Context, string) (int64, error) {
	return 0, errors.New("release all failed")
}

// failingCache 让全部缓存操作返回错误，驱动 commit 失败分支。
type failingCache struct{}

func (failingCache) Get(context.Context, string) (val []byte, hit bool, err error) {
	return nil, false, errors.New("get failed")
}

func (failingCache) Put(context.Context, string, []byte, time.Duration) error {
	return errors.New("put failed")
}

func (failingCache) Stats() (entries int, bytes int64, degraded bool) { return 0, 0, true }

func (failingCache) Prune(context.Context) (int, error) { return 0, errors.New("prune failed") }

func (failingCache) Close() error { return nil }

// hitCache 只保证 Get 命中，用于 runAcquired 的删租约失败分支。
type hitCache struct{}

func (hitCache) Get(context.Context, string) (val []byte, hit bool, err error) {
	return []byte("cached"), true, nil
}

func (hitCache) Put(context.Context, string, []byte, time.Duration) error { return nil }

func (hitCache) Stats() (entries int, bytes int64, degraded bool) { return 0, 0, false }

func (hitCache) Prune(context.Context) (int, error) { return 0, nil }

func (hitCache) Close() error { return nil }

func TestClosedCacheGuards(t *testing.T) {
	t.Parallel()

	raw, err := NewSqliteCache(resilience.Config{CacheDbPath: ":memory:"})
	if err != nil {
		t.Fatalf("NewSqliteCache() = %v", err)
	}
	c, ok := raw.(*sqliteCache)
	if !ok {
		t.Fatalf("NewSqliteCache() 返回 %T, want *sqliteCache", raw)
	}
	if closeErr := c.Close(); closeErr != nil {
		t.Fatalf("Close() = %v", closeErr)
	}

	if _, _, getErr := c.Get(context.Background(), "k"); !errors.Is(getErr, errCacheClosed) {
		t.Errorf("Get() err = %v, want errCacheClosed", getErr)
	}
	if putErr := c.Put(context.Background(), "k", nil, time.Minute); !errors.Is(putErr, errCacheClosed) {
		t.Errorf("Put() err = %v, want errCacheClosed", putErr)
	}
	if _, _, degraded := c.Stats(); !degraded {
		t.Error("Stats() degraded = false, want true")
	}
	if _, pruneErr := c.Prune(context.Background()); !errors.Is(pruneErr, errCacheClosed) {
		t.Errorf("Prune() err = %v, want errCacheClosed", pruneErr)
	}
	ctx := context.Background()
	if _, acquireErr := c.acquireLease(ctx, "k", "o", 1); !errors.Is(acquireErr, errCacheClosed) {
		t.Errorf("acquireLease() err = %v, want errCacheClosed", acquireErr)
	}
	if _, preemptErr := c.preemptLease(ctx, "k", "o", 2, 1); !errors.Is(preemptErr, errCacheClosed) {
		t.Errorf("preemptLease() err = %v, want errCacheClosed", preemptErr)
	}
	if _, _, readErr := c.readLease(ctx, "k"); !errors.Is(readErr, errCacheClosed) {
		t.Errorf("readLease() err = %v, want errCacheClosed", readErr)
	}
	if markErr := c.markLeaseError(ctx, "k", "o", 1); !errors.Is(markErr, errCacheClosed) {
		t.Errorf("markLeaseError() err = %v, want errCacheClosed", markErr)
	}
	if delErr := c.deleteLease(ctx, "k", "o"); !errors.Is(delErr, errCacheClosed) {
		t.Errorf("deleteLease() err = %v, want errCacheClosed", delErr)
	}
	if _, releaseErr := c.releaseAllLeases(ctx, "o"); !errors.Is(releaseErr, errCacheClosed) {
		t.Errorf("releaseAllLeases() err = %v, want errCacheClosed", releaseErr)
	}
}

func TestWithCacheDefaults(t *testing.T) {
	t.Parallel()

	cfg := resilience.Config{}
	withCacheDefaults(&cfg)
	if cfg.CacheDbPath != "Data/Cache.db" || cfg.CacheMaxEntries != 500 ||
		cfg.CacheMaxBytesMB != 256 || cfg.CacheMaxEntryKB != 2048 ||
		cfg.CachePruneIntervalS != 300 || cfg.TotalTimeoutSearchMs != 67000 ||
		cfg.TotalTimeoutFetchMs != 110000 {
		t.Fatalf("默认值 = %+v", cfg)
	}
}

func TestLeaseSecondsFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
		want int64
	}{
		{name: "非法值回落 fetch 默认", in: 0, want: 220},
		{name: "短超时抬到 60 秒", in: 1000, want: 60},
		{name: "正常两倍", in: 67000, want: 134},
		{name: "恰好 60 秒", in: 30000, want: 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := leaseSecondsFor(tt.in); got != tt.want {
				t.Fatalf("leaseSecondsFor(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetachedContext(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithTimeout(context.Background(), time.Minute)
	defer cancelParent()
	detached, cancel := detachedContext(parent)
	if _, ok := detached.Deadline(); !ok {
		t.Error("有截止时间的 parent 应保留 Deadline")
	}
	cancel()

	plain, cancelPlain := detachedContext(context.Background())
	if _, ok := plain.Deadline(); ok {
		t.Error("无截止时间的 parent 不应凭空产生 Deadline")
	}
	cancelPlain()
}

func TestCacheKindWithoutColon(t *testing.T) {
	t.Parallel()

	if got := cacheKind("v1:abc"); got != "unknown" {
		t.Fatalf("cacheKind(%q) = %q, want unknown", "v1:abc", got)
	}
	if got := cacheKind("v1:search:abc"); got != "search" {
		t.Fatalf("cacheKind(%q) = %q, want search", "v1:search:abc", got)
	}
}

func TestWrapCheckErr(t *testing.T) {
	t.Parallel()

	if err := wrapCheckErr("noop", nil); err != nil {
		t.Fatalf("nil error = %v, want nil", err)
	}
	if err := wrapCheckErr("remove", errors.New("boom")); err == nil || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("error = %v, want wrapped", err)
	}
}

func TestResetApprox(t *testing.T) {
	t.Parallel()

	c := &sqliteCache{}
	c.resetApprox([]evictCandidate{{key: "a", nbytes: 3}, {key: "b", nbytes: 4}}, 7)
	if c.approxEntries.Load() != 2 || c.approxBytes.Load() != 7 {
		t.Fatalf("approx = (%d, %d)", c.approxEntries.Load(), c.approxBytes.Load())
	}
}

func TestStartPruneZeroInterval(t *testing.T) {
	t.Parallel()

	c := &sqliteCache{cfg: resilience.Config{CachePruneIntervalS: 0}, done: make(chan struct{})}
	c.startPrune()
	select {
	case <-c.done:
	default:
		t.Fatal("零间隔时 startPrune 应直接关闭 done")
	}
}

func TestSingleflightLogHelpers(t *testing.T) {
	t.Parallel()

	s := &singleflight{}
	s.logWarn(evtCacheDegraded) // nil logger 不得 panic
	logger := newRecordingLogger()
	s.logger = logger
	s.logLeaseWarning("lease_read", errors.New("boom"))
	if !logger.has(evtCacheDegraded) {
		t.Fatalf("未记录 %s", evtCacheDegraded)
	}
}

func TestJoinClose(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open = %v", err)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close = %v", closeErr)
	}
	if joined := joinClose(errors.New("init failed"), db); joined == nil ||
		!strings.Contains(joined.Error(), "init failed") {
		t.Fatalf("joinClose() = %v", joined)
	}
}

func TestFailedDBOperations(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open = %v", err)
	}
	db.SetMaxOpenConns(1)
	if initErr := initCacheDB(context.Background(), db); initErr != nil {
		t.Fatalf("initCacheDB() = %v", initErr)
	}
	c := &sqliteCache{
		db:   db,
		cfg:  resilience.Config{CacheDbPath: ":memory:"},
		clk:  newFakeClock(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)),
		done: make(chan struct{}),
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close = %v", closeErr)
	}
	if _, _, getErr := c.Get(context.Background(), "k"); getErr == nil {
		t.Error("Get() 在 DB 关闭后应报错")
	}
	if putErr := c.Put(context.Background(), "k", []byte("v"), time.Minute); putErr == nil {
		t.Error("Put() 在 DB 关闭后应报错")
	}
	if _, _, degraded := c.Stats(); !degraded {
		t.Error("Stats() 在 DB 关闭后应标记 degraded")
	}
	if _, pruneErr := c.Prune(context.Background()); pruneErr == nil {
		t.Error("Prune() 在 DB 关闭后应报错")
	}
}

func TestFailLeaseReportsErrors(t *testing.T) {
	t.Parallel()

	logger := newRecordingLogger()
	s := &singleflight{leases: failingLeases{}, owner: "owner", logger: logger}
	s.failLease(context.Background(), "v1:search:abc", errors.New("upstream failed"))
	if !logger.has(evtCacheDegraded) {
		t.Fatalf("failLease 未记录 %s", evtCacheDegraded)
	}
}

func TestReadLeaseStateAndTryPreemptErrors(t *testing.T) {
	t.Parallel()

	logger := newRecordingLogger()
	s := &singleflight{leases: failingLeases{}, clk: newFakeClock(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)), owner: "o", logger: logger}
	finish := func() {}
	fn := func(context.Context) ([]byte, error) { return []byte("v"), nil }

	current := &call{done: make(chan struct{})}
	if _, found, done := s.readLeaseState(context.Background(), "v1:search:a", "a", current, fn, nil, finish); !done || found {
		t.Fatalf("readLeaseState() = found %v done %v", found, done)
	}
	if current.val == nil {
		t.Fatal("读租约失败后应降级为 owner 执行")
	}

	current = &call{done: make(chan struct{})}
	lease := leaseRow{ExpiresAt: 0}
	if !s.tryPreempt(context.Background(), "v1:search:b", "b", current, lease, fn, nil, finish) {
		t.Fatal("抢占失败后应降级为 owner 执行")
	}
}

func TestWaitCallBranches(t *testing.T) {
	t.Parallel()

	s := &singleflight{}
	current := &call{done: make(chan struct{})}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.waitCall(canceled, current, "k", 10); err == nil {
		t.Error("ctx 取消后应报错")
	}
	if _, _, err := s.waitCall(context.Background(), current, "k", 0); domain.CodeOf(err) != 3075 {
		t.Errorf("等待超时 code = %d, want 3075", domain.CodeOf(err))
	}
}

func TestWaitNextPollBranches(t *testing.T) {
	t.Parallel()

	s := &singleflight{pollInterval: time.Millisecond}
	current := &call{}
	if !s.waitNextPoll(context.Background(), time.Now().Add(-time.Second), 10, "k", current) {
		t.Fatal("超过截止时间应结束")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	current = &call{}
	if !s.waitNextPoll(canceled, time.Now().Add(time.Second), 10, "k", current) {
		t.Fatal("ctx 取消应结束")
	}
}

func TestCommitValuePutFailure(t *testing.T) {
	t.Parallel()

	logger := newRecordingLogger()
	s := &singleflight{cache: failingCache{}, maxEntrySize: 4, logger: logger}
	s.commitValue(context.Background(), "v1:search:a", []byte("12345"), nil)
	if !logger.has(evtCacheCommitFailed) {
		t.Fatalf("超限写入失败未记录 %s", evtCacheCommitFailed)
	}
}

func TestRunAcquiredLeaseDeleteFailure(t *testing.T) {
	t.Parallel()

	logger := newRecordingLogger()
	s := &singleflight{cache: hitCache{}, leases: failingLeases{}, owner: "o", logger: logger}
	current := &call{}
	s.runAcquired(context.Background(), "v1:search:a", "a", current,
		func(context.Context) ([]byte, error) { return nil, nil }, nil, func() {})
	if string(current.val) != "cached" {
		t.Fatalf("runAcquired() val = %q", current.val)
	}
	if !logger.has(evtCacheDegraded) {
		t.Fatalf("删租约失败未记录 %s", evtCacheDegraded)
	}
}
