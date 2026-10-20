// 文件名: cache_test.go
// 用途: 验证 SQLite 缓存的 TTL、超限、淘汰、命中统计、降级复位、前缀与并发安全。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type logRecord struct {
	event string
	attrs map[string]any
}

type recordingLogger struct {
	mu     sync.Mutex
	events []logRecord
	ch     chan string
}

func newRecordingLogger() *recordingLogger {
	return &recordingLogger{ch: make(chan string, 64)}
}

func (l *recordingLogger) Warn(msg string, args ...any) { l.add(msg, args) }
func (l *recordingLogger) Info(msg string, args ...any) { l.add(msg, args) }

func (l *recordingLogger) add(event string, args []any) {
	attrs := make(map[string]any, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		attrs[key] = args[i+1]
	}
	l.mu.Lock()
	l.events = append(l.events, logRecord{event: event, attrs: attrs})
	l.mu.Unlock()
	select {
	case l.ch <- event:
	default:
	}
}

func (l *recordingLogger) has(event string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, rec := range l.events {
		if rec.event == event {
			return true
		}
	}
	return false
}

func (l *recordingLogger) find(event string) (logRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, rec := range l.events {
		if rec.event == event {
			return rec, true
		}
	}
	return logRecord{}, false
}

func (l *recordingLogger) wait(t *testing.T, event string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-l.ch:
			if got == event {
				return
			}
		case <-deadline:
			t.Fatalf("2s 内未等到日志事件 %s", event)
		}
	}
}

func newTestCache(t *testing.T, configure func(*Config)) (*sqliteCache, *fakeClock, *recordingLogger) {
	t.Helper()
	clk := newFakeClock(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC))
	logger := newRecordingLogger()
	cfg := Config{
		CacheDbPath:         ":memory:",
		CacheMaxEntries:     100,
		CacheMaxBytesMB:     1,
		CacheMaxEntryKB:     1024,
		CachePruneIntervalS: 300,
		Clock:               clk,
		Logger:              logger,
	}
	if configure != nil {
		configure(&cfg)
	}
	cache, err := NewSqliteCache(cfg)
	if err != nil {
		t.Fatalf("NewSqliteCache() = %v", err)
	}
	c, ok := cache.(*sqliteCache)
	if !ok {
		t.Fatalf("NewSqliteCache() 返回 %T, want *sqliteCache", cache)
	}
	t.Cleanup(func() {
		if closeErr := c.Close(); closeErr != nil {
			t.Errorf("Close() = %v", closeErr)
		}
	})
	return c, clk, logger
}

func TestNewSqliteCache(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Data", "Cache.db")
	cache, err := NewSqliteCache(Config{
		CacheDbPath:         path,
		CacheMaxEntries:     10,
		CacheMaxBytesMB:     1,
		CacheMaxEntryKB:     16,
		CachePruneIntervalS: 300,
	})
	if err != nil {
		t.Fatalf("NewSqliteCache() = %v", err)
	}
	ctx := context.Background()
	if err := cache.Put(ctx, "fetch:persist", []byte("body"), time.Hour); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("缓存文件未创建: %v", statErr)
	}

	reopened, err := NewSqliteCache(Config{
		CacheDbPath:         path,
		CacheMaxEntries:     10,
		CacheMaxBytesMB:     1,
		CacheMaxEntryKB:     16,
		CachePruneIntervalS: 300,
	})
	if err != nil {
		t.Fatalf("重开 NewSqliteCache() = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Errorf("Close() = %v", closeErr)
		}
	})
	got, hit, err := reopened.Get(ctx, "fetch:persist")
	if err != nil || !hit || string(got) != "body" {
		t.Fatalf("重开后 Get() = (%q, %v, %v), want (body, true, nil)", got, hit, err)
	}
	reopenedCache, ok := reopened.(*sqliteCache)
	if !ok {
		t.Fatalf("重开的缓存类型 = %T, want *sqliteCache", reopened)
	}
	var mode string
	if err := reopenedCache.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("读 journal_mode = %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("再次 Close() = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("重复 Close() = %v", err)
	}
}

func TestSqliteCache(t *testing.T) {
	t.Parallel()

	t.Run("Put后Get命中", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newTestCache(t, nil)
		ctx := context.Background()
		if err := c.Put(ctx, "fetch:a", []byte("hello"), time.Hour); err != nil {
			t.Fatalf("Put() = %v", err)
		}
		got, hit, err := c.Get(ctx, "fetch:a")
		if err != nil || !hit || !bytes.Equal(got, []byte("hello")) {
			t.Fatalf("Get() = (%q, %v, %v)", got, hit, err)
		}
	})

	t.Run("TTL到期视为未命中", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		ctx := context.Background()
		if err := c.Put(ctx, "fetch:ttl", []byte("v"), time.Second); err != nil {
			t.Fatalf("Put() = %v", err)
		}
		clk.Advance(2 * time.Second)
		if _, hit, err := c.Get(ctx, "fetch:ttl"); err != nil || hit {
			t.Fatalf("过期 Get() hit=%v err=%v, want 未命中", hit, err)
		}
		var count int
		if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cache").Scan(&count); err != nil {
			t.Fatalf("COUNT(*) = %v", err)
		}
		if count != 0 {
			t.Fatalf("过期行未被删除，count = %d", count)
		}
	})

	t.Run("TTL非正立即未命中", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newTestCache(t, nil)
		ctx := context.Background()
		if err := c.Put(ctx, "fetch:zero", []byte("v"), 0); err != nil {
			t.Fatalf("Put(ttl=0) = %v", err)
		}
		if _, hit, err := c.Get(ctx, "fetch:zero"); err != nil || hit {
			t.Fatalf("ttl=0 Get() hit=%v err=%v, want 未命中", hit, err)
		}
	})

	t.Run("超单条上限不写入", func(t *testing.T) {
		t.Parallel()
		c, _, logger := newTestCache(t, func(cfg *Config) { cfg.CacheMaxEntryKB = 1 })
		ctx := context.Background()
		val := bytes.Repeat([]byte("x"), 1025)
		if err := c.Put(ctx, "fetch:big", val, time.Hour); err != nil {
			t.Fatalf("超限 Put() = %v, want nil", err)
		}
		if _, hit, err := c.Get(ctx, "fetch:big"); err != nil || hit {
			t.Fatalf("超限后 Get() hit=%v err=%v, want 未命中", hit, err)
		}
		rec, ok := logger.find(evtCacheOversize)
		if !ok {
			t.Fatal("缺少 warn.cache_oversize 日志")
		}
		if rec.attrs["bytes"] != len(val) || rec.attrs["limit_kb"] != 1 {
			t.Fatalf("oversize 字段 = %v", rec.attrs)
		}
		if _, ok := rec.attrs["key_hash"]; !ok {
			t.Fatalf("oversize 缺少 key_hash: %v", rec.attrs)
		}
	})

	t.Run("超maxEntries淘汰最久未访问", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, func(cfg *Config) { cfg.CacheMaxEntries = 2 })
		ctx := context.Background()
		mustPut(t, c, "fetch:a", "a")
		clk.Advance(time.Second)
		mustPut(t, c, "fetch:b", "b")
		clk.Advance(time.Second)
		if _, hit, err := c.Get(ctx, "fetch:a"); err != nil || !hit {
			t.Fatalf("Get(a) hit=%v err=%v", hit, err)
		}
		clk.Advance(time.Second)
		mustPut(t, c, "fetch:c", "c")
		if _, err := c.Prune(ctx); err != nil {
			t.Fatalf("Prune() = %v", err)
		}
		if _, hit, err := c.Get(ctx, "fetch:b"); err != nil || hit {
			t.Fatalf("Get(b) hit=%v err=%v, want 未命中", hit, err)
		}
		for _, key := range []string{"fetch:a", "fetch:c"} {
			if _, hit, err := c.Get(ctx, key); err != nil || !hit {
				t.Fatalf("Get(%s) hit=%v err=%v, want 命中", key, hit, err)
			}
		}
	})

	t.Run("命中统计与last_hit_at", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newTestCache(t, nil)
		ctx := context.Background()
		mustPut(t, c, "fetch:hits", "v")
		for range 2 {
			if _, hit, err := c.Get(ctx, "fetch:hits"); err != nil || !hit {
				t.Fatalf("Get() hit=%v err=%v", hit, err)
			}
		}
		if _, err := c.Prune(ctx); err != nil {
			t.Fatalf("Prune() = %v", err)
		}
		var hits int
		var lastHit int64
		if err := c.db.QueryRowContext(ctx,
			`SELECT hits, last_hit_at FROM cache WHERE capture_id = ?`, "v1:fetch:hits").
			Scan(&hits, &lastHit); err != nil {
			t.Fatalf("读命中统计 = %v", err)
		}
		if hits != 2 {
			t.Fatalf("hits = %d, want 2", hits)
		}
		if lastHit == 0 {
			t.Fatal("last_hit_at 未更新")
		}
	})

	t.Run("Prune删除过期行并返回条数", func(t *testing.T) {
		t.Parallel()
		c, clk, _ := newTestCache(t, nil)
		ctx := context.Background()
		if err := c.Put(ctx, "fetch:old", []byte("v"), time.Second); err != nil {
			t.Fatalf("Put(old) = %v", err)
		}
		if err := c.Put(ctx, "fetch:live", []byte("v"), time.Hour); err != nil {
			t.Fatalf("Put(live) = %v", err)
		}
		clk.Advance(2 * time.Second)
		removed, err := c.Prune(ctx)
		if err != nil {
			t.Fatalf("Prune() = %v", err)
		}
		if removed != 1 {
			t.Fatalf("removed = %d, want 1", removed)
		}
		if _, hit, err := c.Get(ctx, "fetch:live"); err != nil || !hit {
			t.Fatalf("Get(live) hit=%v err=%v, want 命中", hit, err)
		}
	})

	t.Run("Stats准确且故障降级", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newTestCache(t, nil)
		mustPut(t, c, "fetch:s1", "alpha")
		mustPut(t, c, "fetch:s2", "bravo!")
		entries, nbytes, degraded := c.Stats()
		if entries != 2 || nbytes != 11 || degraded {
			t.Fatalf("Stats() = (%d, %d, %v), want (2, 11, false)", entries, nbytes, degraded)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("Close() = %v", err)
		}
		entries, nbytes, degraded = c.Stats()
		if entries != 0 || nbytes != 0 || !degraded {
			t.Fatalf("关闭后 Stats() = (%d, %d, %v), want (0, 0, true)", entries, nbytes, degraded)
		}
	})

	t.Run("key带schema版本前缀", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newTestCache(t, nil)
		ctx := context.Background()
		mustPut(t, c, "fetch:raw", "v")
		mustPut(t, c, "v1:fetch:already", "v")
		for want, key := range map[string]string{
			"v1:fetch:raw":     "fetch:raw",
			"v1:fetch:already": "v1:fetch:already",
		} {
			if _, hit, err := c.Get(ctx, key); err != nil || !hit {
				t.Fatalf("Get(%s) hit=%v err=%v", key, hit, err)
			}
			var count int
			if err := c.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM cache WHERE capture_id = ?`, want).Scan(&count); err != nil {
				t.Fatalf("查 capture_id = %v", err)
			}
			if count != 1 {
				t.Fatalf("capture_id %q 行数 = %d, want 1", want, count)
			}
		}
	})
}

func TestCacheConcurrentAccess(t *testing.T) {
	t.Parallel()

	c, _, _ := newTestCache(t, nil)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := "fetch:k" + string(rune('a'+i%10))
			if err := c.Put(ctx, key, []byte("v"), time.Minute); err != nil {
				errs <- err
				return
			}
			if _, _, err := c.Get(ctx, key); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发 Get/Put = %v", err)
	}
}

func TestCacheDegradedRecovery(t *testing.T) {
	t.Parallel()

	c, _, logger := newTestCache(t, nil)
	for range 2 {
		c.recordFailure(errors.New("boom"))
	}
	if c.degraded.Load() {
		t.Fatal("连续 2 次失败不应置位 degraded")
	}
	c.recordFailure(errors.New("boom"))
	if !c.degraded.Load() {
		t.Fatal("连续 3 次失败应置位 degraded")
	}
	if !logger.has(evtCacheDegraded) {
		t.Fatal("缺少 warn.cache_degraded")
	}
	c.recordSuccess()
	if c.degraded.Load() {
		t.Fatal("成功操作后 degraded 应复位")
	}
	if !logger.has(evtCacheRecovered) {
		t.Fatal("缺少 info.cache_recovered")
	}
}

func TestCachePruneScheduler(t *testing.T) {
	t.Parallel()

	c, clk, logger := newTestCache(t, func(cfg *Config) { cfg.CachePruneIntervalS = 1 })
	ctx := context.Background()
	if err := c.Put(ctx, "fetch:expire", []byte("v"), time.Second); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	clk.WaitTimer(t)
	clk.Advance(2 * time.Second)
	logger.wait(t, evtCachePrune)
	if _, hit, err := c.Get(ctx, "fetch:expire"); err != nil || hit {
		t.Fatalf("调度器未清理过期行: hit=%v err=%v", hit, err)
	}
}

func mustPut(t *testing.T, c *sqliteCache, key, val string) {
	t.Helper()
	if err := c.Put(context.Background(), key, []byte(val), time.Hour); err != nil {
		t.Fatalf("Put(%s) = %v", key, err)
	}
}
