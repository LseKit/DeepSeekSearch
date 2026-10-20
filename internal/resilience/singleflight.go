// 文件名: singleflight.go
// 用途: 实现进程内与跨进程单飞（SQLite inflight 租约），并保证 commit 先于租约释放。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

const (
	cachePollInterval    = 100 * time.Millisecond
	oversizeMarkerTTL    = 60 * time.Second
	oversizeMarkerKind   = "oversize"
	defaultSearchTimeout = 67000
	defaultFetchTimeout  = 110000
)

// Singleflight 保证「同一 key 在同一时刻只有一次上游请求」，跨 goroutine 与跨进程同时生效。
// 内存层用 map+mutex，跨进程层用 SQLite inflight 表。
type Singleflight interface {
	// Do 执行 fn，或等待他人执行完共享结果。
	// 返回 shared=true 表示本次没有真正执行 fn（结果来自他人）。
	// 等待超过 waitMs 返回 *domain.LocalError（3075）。
	//
	// 🔴 commit MUST 是 Do 的参数，不能由调用方在 Do 返回后再自己 Put：
	//    owner 的顺序必须是 fn 成功 → commit(val) 成功 → 才 DELETE 租约。
	//    否则「租约消失」与「值可读」之间存在窗口，跨进程等待者在这个窗口里
	//    既读不到值也看不到租约，只能重新发起上游请求 ⇒ 同一次调用被计费两次。
	//
	// 🔴 返回的 val 是共享的底层数组，所有等待者只读，MUST NOT 原地修改。
	Do(ctx context.Context, key string, waitMs int,
		fn func(context.Context) ([]byte, error),
		commit func(context.Context, []byte) error) (val []byte, shared bool, err error)

	// ReleaseAll 清理属于 ownerID 的全部租约。MUST 在 cache.Close 之前调用。
	// ownerID 传空串表示本实例：owner 由 NewSingleflight 内部生成，不暴露给装配层。
	ReleaseAll(ctx context.Context, ownerID string) (released int, err error)
}

type call struct {
	done     chan struct{}
	once     sync.Once
	val      []byte
	err      error
	executed bool
}

type singleflight struct {
	mu           sync.Mutex
	calls        map[string]*call
	cache        Cache
	leases       leaseBackend
	clk          Clock
	owner        string
	pollInterval time.Duration
	searchLeaseS int64
	fetchLeaseS  int64
	maxEntrySize int64
	logger       cacheLogger
}

type leaseRow struct {
	Owner         string
	ExpiresAt     int64
	LastErrorCode int
}

type leaseBackend interface {
	acquireLease(ctx context.Context, key, owner string, expiresAt int64) (bool, error)
	preemptLease(ctx context.Context, key, owner string, expiresAt, now int64) (bool, error)
	readLease(ctx context.Context, key string) (leaseRow, bool, error)
	markLeaseError(ctx context.Context, key, owner string, code int) error
	deleteLease(ctx context.Context, key, owner string) error
	releaseAllLeases(ctx context.Context, owner string) (int64, error)
}

type leaseTimeoutProvider interface {
	searchTotalTimeoutMs() int
	fetchTotalTimeoutMs() int
}

type cacheLimitProvider interface {
	cacheMaxEntryBytes() int64
}

type cacheLoggerProvider interface {
	singleflightLogger() cacheLogger
}

// NewSingleflight 构造跨进程 singleflight。cache 为 nil 时退化为进程内单飞。
func NewSingleflight(cache Cache, clk Clock) Singleflight {
	if clk == nil {
		clk = SystemClock()
	}
	s := &singleflight{
		calls:        make(map[string]*call),
		cache:        cache,
		clk:          clk,
		owner:        ownerID(clk),
		pollInterval: cachePollInterval,
		searchLeaseS: leaseSecondsFor(defaultSearchTimeout),
		fetchLeaseS:  leaseSecondsFor(defaultFetchTimeout),
	}
	if cache == nil {
		return s
	}
	if leases, ok := cache.(leaseBackend); ok {
		s.leases = leases
	}
	if p, ok := cache.(leaseTimeoutProvider); ok {
		s.searchLeaseS = leaseSecondsFor(p.searchTotalTimeoutMs())
		s.fetchLeaseS = leaseSecondsFor(p.fetchTotalTimeoutMs())
	}
	if p, ok := cache.(cacheLimitProvider); ok {
		s.maxEntrySize = p.cacheMaxEntryBytes()
	}
	if p, ok := cache.(cacheLoggerProvider); ok {
		s.logger = p.singleflightLogger()
	}
	return s
}

// ownerID 生成 hostname:pid:startupUnixNano。
// MUST NOT 只用 PID：容器里 PID 常常恒为 1，共享 Cache.db 的两个容器会互删租约。
func ownerID(clk Clock) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s:%d:%d", host, os.Getpid(), clk.Now().UnixNano())
}

// leaseSecondsFor 返回租约秒数：max(60, ceil(2 × total_timeout_ms / 1000))。
// 2 倍是因为 owner 最坏情况包含排队与全部重试，租约短于实际耗时会引发重复计费。
func leaseSecondsFor(totalTimeoutMs int) int64 {
	if totalTimeoutMs < 1 {
		totalTimeoutMs = defaultFetchTimeout
	}
	seconds := int64((totalTimeoutMs + 999) / 1000)
	if seconds*2 < 60 {
		return 60
	}
	return seconds * 2
}

func (s *singleflight) Do(
	ctx context.Context,
	key string,
	waitMs int,
	fn func(context.Context) ([]byte, error),
	commit func(context.Context, []byte) error,
) (val []byte, shared bool, err error) {
	storageKey := cacheKey(key)
	if cached, hit, cacheErr := s.cached(ctx, storageKey); cacheErr == nil && hit {
		if bytes, oversize := decodeOversize(cached); oversize {
			return nil, true, oversizeError(key, bytes)
		}
		return cached, true, nil
	}

	s.mu.Lock()
	if existing, ok := s.calls[storageKey]; ok {
		s.mu.Unlock()
		return s.waitCall(ctx, existing, key, waitMs)
	}
	current := &call{done: make(chan struct{})}
	s.calls[storageKey] = current
	s.mu.Unlock()

	s.lead(ctx, storageKey, key, waitMs, current, fn, commit)
	return current.val, !current.executed, current.err
}

func (s *singleflight) cached(ctx context.Context, storageKey string) (val []byte, hit bool, err error) {
	if s.cache == nil {
		return nil, false, nil
	}
	val, hit, err = s.cache.Get(ctx, storageKey)
	if err != nil {
		return nil, false, fmt.Errorf("read cache for singleflight: %w", err)
	}
	return val, hit, nil
}

func (s *singleflight) waitCall(ctx context.Context, current *call, userKey string, waitMs int) (val []byte, shared bool, err error) {
	timer := time.NewTimer(time.Duration(waitMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-current.done:
		return current.val, true, current.err
	case <-ctx.Done():
		return nil, true, fmt.Errorf("singleflight wait canceled: %w", ctx.Err())
	case <-timer.C:
		return nil, true, waitTimeoutError(userKey, waitMs)
	}
}

func (s *singleflight) lead(
	ctx context.Context,
	storageKey, userKey string,
	waitMs int,
	current *call,
	fn func(context.Context) ([]byte, error),
	commit func(context.Context, []byte) error,
) {
	finished := false
	finish := func() {
		if finished {
			return
		}
		finished = true
		s.finishCall(storageKey, current)
	}
	defer finish()

	deadline := time.Now().Add(time.Duration(waitMs) * time.Millisecond)
	for {
		if cached, hit, cacheErr := s.cached(ctx, storageKey); cacheErr == nil && hit {
			if bytes, oversize := decodeOversize(cached); oversize {
				current.err = oversizeError(userKey, bytes)
				return
			}
			current.val = cached
			return
		}
		if s.leases == nil {
			s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
			return
		}
		acquired, acquireErr := s.leases.acquireLease(
			ctx, storageKey, s.owner, s.clk.Now().Unix()+s.leaseSeconds(storageKey))
		if acquireErr != nil {
			s.logLeaseWarning("lease_acquire", acquireErr)
			s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
			return
		}
		if acquired {
			s.runAcquired(ctx, storageKey, userKey, current, fn, commit, finish)
			return
		}
		lease, found, readErr := s.leases.readLease(ctx, storageKey)
		if readErr != nil {
			s.logLeaseWarning("lease_read", readErr)
			s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
			return
		}
		if found && lease.LastErrorCode != 0 {
			current.err = ownerFailedError(userKey, lease.LastErrorCode)
			return
		}
		if !found {
			// owner 已删除租约：下一轮先读缓存；缓存仍空则立即重抢租约（A-02 4.1）。
			continue
		}
		now := s.clk.Now().Unix()
		if lease.ExpiresAt < now {
			preempted, preemptErr := s.leases.preemptLease(
				ctx, storageKey, s.owner, now+s.leaseSeconds(storageKey), now)
			if preemptErr != nil {
				s.logLeaseWarning("lease_preempt", preemptErr)
				s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
				return
			}
			if preempted {
				s.runAcquired(ctx, storageKey, userKey, current, fn, commit, finish)
				return
			}
		}
		if time.Now().After(deadline) {
			current.err = waitTimeoutError(userKey, waitMs)
			return
		}
		select {
		case <-ctx.Done():
			current.err = fmt.Errorf("singleflight wait canceled: %w", ctx.Err())
			return
		case <-time.After(s.pollInterval):
		}
	}
}

// runAcquired 在拿到租约后先重读缓存，堵住「读缓存 miss 与抢租约之间旧 owner 已完成 commit」的窗口。
// 少了这一步，等待者会误判「租约消失且缓存无值」而再打一次上游，造成双重计费。
func (s *singleflight) runAcquired(
	ctx context.Context,
	storageKey, userKey string,
	current *call,
	fn func(context.Context) ([]byte, error),
	commit func(context.Context, []byte) error,
	finish func(),
) {
	if cached, hit, cacheErr := s.cached(ctx, storageKey); cacheErr == nil && hit {
		if deleteErr := s.leases.deleteLease(ctx, storageKey, s.owner); deleteErr != nil {
			s.logLeaseWarning("lease_delete_after_hit", deleteErr)
		}
		if bytes, oversize := decodeOversize(cached); oversize {
			current.err = oversizeError(userKey, bytes)
			return
		}
		current.val = cached
		return
	}
	s.runOwner(ctx, storageKey, userKey, current, fn, commit, true, finish)
}

func (s *singleflight) runOwner(
	ctx context.Context,
	storageKey, userKey string,
	current *call,
	fn func(context.Context) ([]byte, error),
	commit func(context.Context, []byte) error,
	leaseHeld bool,
	finish func(),
) {
	current.executed = true
	cleanupCtx := context.WithoutCancel(ctx)
	defer func() {
		if recovered := recover(); recovered != nil {
			current.val = nil
			current.err = domain.NewLocalError(domain.CodeLocInternal, "singleflight fn panic", map[string]any{
				"key":   userKey,
				"panic": fmt.Sprint(recovered),
			})
			// 先清进程内条目再删租约；否则该 key 会永久等待一个不会 fulfill 的 call。
			finish()
			if leaseHeld {
				s.failLease(cleanupCtx, storageKey, current.err)
			}
		}
	}()

	fnCtx, cancel := detachedContext(ctx)
	defer cancel()
	val, err := fn(fnCtx)
	if err != nil {
		current.val = nil
		current.err = err
		if leaseHeld {
			s.failLease(cleanupCtx, storageKey, err)
		}
		return
	}
	current.val = val
	current.err = nil
	s.commitValue(cleanupCtx, storageKey, val, commit)
	if !leaseHeld {
		return
	}
	if deleteErr := s.leases.deleteLease(cleanupCtx, storageKey, s.owner); deleteErr != nil {
		s.logLeaseWarning("lease_delete", deleteErr)
	}
}

func (s *singleflight) commitValue(
	ctx context.Context,
	storageKey string,
	val []byte,
	commit func(context.Context, []byte) error,
) {
	if s.cache != nil && s.maxEntrySize > 0 && int64(len(val)) > s.maxEntrySize {
		marker, err := json.Marshal(oversizeMarker{
			Kind:     oversizeMarkerKind,
			Oversize: true,
			Bytes:    len(val),
		})
		if err != nil {
			s.logWarn(evtCacheCommitFailed, "key_hash", cacheKeyHash(storageKey), "err", err.Error())
			return
		}
		if putErr := s.cache.Put(ctx, storageKey, marker, oversizeMarkerTTL); putErr != nil {
			s.logWarn(evtCacheCommitFailed, "key_hash", cacheKeyHash(storageKey), "err", putErr.Error())
		}
		return
	}
	if commit == nil {
		return
	}
	if commitErr := commit(ctx, val); commitErr != nil {
		s.logWarn(evtCacheCommitFailed, "key_hash", cacheKeyHash(storageKey), "err", commitErr.Error())
	}
}

func (s *singleflight) failLease(ctx context.Context, storageKey string, cause error) {
	code := domain.CodeOf(cause)
	if markErr := s.leases.markLeaseError(ctx, storageKey, s.owner, code); markErr != nil {
		s.logLeaseWarning("lease_mark_error", markErr)
	}
	if deleteErr := s.leases.deleteLease(ctx, storageKey, s.owner); deleteErr != nil {
		s.logLeaseWarning("lease_delete", deleteErr)
	}
}

func (s *singleflight) finishCall(storageKey string, current *call) {
	s.mu.Lock()
	if s.calls[storageKey] == current {
		delete(s.calls, storageKey)
	}
	s.mu.Unlock()
	current.once.Do(func() { close(current.done) })
}

func (s *singleflight) ReleaseAll(ctx context.Context, ownerID string) (int, error) {
	if s.leases == nil {
		return 0, nil
	}
	if ownerID == "" {
		ownerID = s.owner
	}
	released, err := s.leases.releaseAllLeases(ctx, ownerID)
	if err != nil {
		return 0, fmt.Errorf("release singleflight leases: %w", err)
	}
	return int(released), nil
}

func (s *singleflight) leaseSeconds(storageKey string) int64 {
	if cacheKind(storageKey) == "fetch" {
		return s.fetchLeaseS
	}
	return s.searchLeaseS
}

func waitTimeoutError(key string, waitMs int) error {
	return fmt.Errorf("singleflight wait: %w",
		domain.NewLocalError(domain.CodeLocSingleflightTimeout, "等待他人填充缓存超时", map[string]any{
			"key":       key,
			"waited_ms": waitMs,
		}))
}

func ownerFailedError(key string, code int) error {
	return fmt.Errorf("singleflight owner failed: %w",
		domain.NewLocalError(domain.CodeLocSingleflightTimeout, "等待的上游请求刚刚失败", map[string]any{
			"key":              key,
			"owner_error_code": code,
		}))
}

func oversizeError(key string, bytes int) error {
	return fmt.Errorf("singleflight oversize: %w",
		domain.NewLocalError(domain.CodeLocSingleflightTimeout, "超限内容无法通过缓存共享", map[string]any{
			"key":      key,
			"oversize": true,
			"bytes":    bytes,
		}))
}

func (s *singleflight) logLeaseWarning(stage string, err error) {
	s.logWarn(evtCacheDegraded, "stage", stage, "err", err.Error())
}

func (s *singleflight) logWarn(event string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(event, args...)
	}
}

func (c *sqliteCache) cacheMaxEntryBytes() int64 {
	return int64(c.cfg.CacheMaxEntryKB) * 1024
}

func (c *sqliteCache) searchTotalTimeoutMs() int { return c.cfg.TotalTimeoutSearchMs }

func (c *sqliteCache) fetchTotalTimeoutMs() int { return c.cfg.TotalTimeoutFetchMs }

func (c *sqliteCache) singleflightLogger() cacheLogger { return c.logger }

func (c *sqliteCache) acquireLease(ctx context.Context, key, owner string, expiresAt int64) (bool, error) {
	if c.closed.Load() {
		return false, errCacheClosed
	}
	res, err := c.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO inflight (k, owner, expires_at, last_error_code) VALUES (?, ?, ?, 0)`,
		key, owner, expiresAt)
	if err != nil {
		c.recordFailure(err)
		return false, fmt.Errorf("acquire lease: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		c.recordFailure(err)
		return false, fmt.Errorf("acquire lease rows affected: %w", err)
	}
	c.recordSuccess()
	return affected == 1, nil
}

func (c *sqliteCache) preemptLease(ctx context.Context, key, owner string, expiresAt, now int64) (bool, error) {
	if c.closed.Load() {
		return false, errCacheClosed
	}
	res, err := c.db.ExecContext(ctx,
		`UPDATE inflight SET owner = ?, expires_at = ?, last_error_code = 0
         WHERE k = ? AND expires_at < ?`,
		owner, expiresAt, key, now)
	if err != nil {
		c.recordFailure(err)
		return false, fmt.Errorf("preempt lease: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		c.recordFailure(err)
		return false, fmt.Errorf("preempt lease rows affected: %w", err)
	}
	c.recordSuccess()
	return affected == 1, nil
}

func (c *sqliteCache) readLease(ctx context.Context, key string) (leaseRow, bool, error) {
	if c.closed.Load() {
		return leaseRow{}, false, errCacheClosed
	}
	var lease leaseRow
	err := c.db.QueryRowContext(ctx,
		`SELECT owner, expires_at, last_error_code FROM inflight WHERE k = ?`, key).
		Scan(&lease.Owner, &lease.ExpiresAt, &lease.LastErrorCode)
	if errors.Is(err, sql.ErrNoRows) {
		c.recordSuccess()
		return leaseRow{}, false, nil
	}
	if err != nil {
		c.recordFailure(err)
		return leaseRow{}, false, fmt.Errorf("read lease: %w", err)
	}
	c.recordSuccess()
	return lease, true, nil
}

func (c *sqliteCache) markLeaseError(ctx context.Context, key, owner string, code int) error {
	if c.closed.Load() {
		return errCacheClosed
	}
	if _, err := c.db.ExecContext(ctx,
		`UPDATE inflight SET last_error_code = ? WHERE k = ? AND owner = ?`,
		code, key, owner); err != nil {
		c.recordFailure(err)
		return fmt.Errorf("mark lease error: %w", err)
	}
	c.recordSuccess()
	return nil
}

func (c *sqliteCache) deleteLease(ctx context.Context, key, owner string) error {
	if c.closed.Load() {
		return errCacheClosed
	}
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM inflight WHERE k = ? AND owner = ?`, key, owner); err != nil {
		c.recordFailure(err)
		return fmt.Errorf("delete lease: %w", err)
	}
	c.recordSuccess()
	return nil
}

func (c *sqliteCache) releaseAllLeases(ctx context.Context, owner string) (int64, error) {
	if c.closed.Load() {
		return 0, errCacheClosed
	}
	res, err := c.db.ExecContext(ctx, `DELETE FROM inflight WHERE owner = ?`, owner)
	if err != nil {
		c.recordFailure(err)
		return 0, fmt.Errorf("release all leases: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		c.recordFailure(err)
		return 0, fmt.Errorf("release all leases rows affected: %w", err)
	}
	c.recordSuccess()
	return affected, nil
}
