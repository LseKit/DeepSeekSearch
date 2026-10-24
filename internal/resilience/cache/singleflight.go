// 文件名: singleflight.go
// 用途: 实现进程内与跨进程单飞（SQLite inflight 租约），并保证 commit 先于租约释放。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

const (
	cachePollInterval    = 100 * time.Millisecond
	oversizeMarkerTTL    = 60 * time.Second
	oversizeMarkerKind   = "oversize"
	defaultSearchTimeout = 67000
	defaultFetchTimeout  = 110000
)

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
	cache        resilience.Cache
	leases       leaseBackend
	clk          resilience.Clock
	owner        string
	pollInterval time.Duration
	pollAfter    func(time.Duration) <-chan time.Time
	searchLeaseS int64
	fetchLeaseS  int64
	maxEntrySize int64
	logger       logger
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
	singleflightLogger() logger
}

// NewSingleflight 构造跨进程 singleflight。cache 为 nil 时退化为进程内单飞。
func NewSingleflight(cache resilience.Cache, clk resilience.Clock) resilience.Singleflight {
	if clk == nil {
		clk = resilience.SystemClock()
	}
	s := &singleflight{
		calls:        make(map[string]*call),
		cache:        cache,
		clk:          clk,
		owner:        ownerID(clk),
		pollInterval: cachePollInterval,
		pollAfter:    clk.After,
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
func ownerID(clk resilience.Clock) string {
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
	select {
	case <-current.done:
		return current.val, true, current.err
	case <-ctx.Done():
		return nil, true, fmt.Errorf("singleflight wait canceled: %w", ctx.Err())
	case <-s.clk.After(time.Duration(waitMs) * time.Millisecond):
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

	deadline := s.clk.Now().Add(time.Duration(waitMs) * time.Millisecond)
	for {
		if s.fillFromCache(ctx, storageKey, userKey, current) {
			return
		}
		if s.acquireOwnership(ctx, storageKey, userKey, current, fn, commit, finish) {
			return
		}
		lease, found, done := s.readLeaseState(ctx, storageKey, userKey, current, fn, commit, finish)
		if done {
			return
		}
		if !found {
			continue
		}
		if s.tryPreempt(ctx, storageKey, userKey, current, lease, fn, commit, finish) {
			return
		}
		if s.waitNextPoll(ctx, deadline, waitMs, userKey, current) {
			return
		}
	}
}

// fillFromCache 命中缓存时填充 current 并返回 true（含超大条目错误）。
func (s *singleflight) fillFromCache(ctx context.Context, storageKey, userKey string, current *call) bool {
	cached, hit, cacheErr := s.cached(ctx, storageKey)
	if cacheErr != nil || !hit {
		return false
	}
	if bytes, oversize := decodeOversize(cached); oversize {
		current.err = oversizeError(userKey, bytes)
		return true
	}
	current.val = cached
	return true
}

// acquireOwnership 处理「无租约表 / 抢租约」两条路径；返回 true 表示 lead 已结束。
func (s *singleflight) acquireOwnership(
	ctx context.Context, storageKey, userKey string, current *call,
	fn func(context.Context) ([]byte, error), commit func(context.Context, []byte) error, finish func(),
) bool {
	if s.leases == nil {
		s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
		return true
	}
	acquired, acquireErr := s.leases.acquireLease(
		ctx, storageKey, s.owner, s.clk.Now().Unix()+s.leaseSeconds(storageKey))
	if acquireErr != nil {
		s.logLeaseWarning("lease_acquire", acquireErr)
		s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
		return true
	}
	if acquired {
		s.runAcquired(ctx, storageKey, userKey, current, fn, commit, finish)
		return true
	}
	return false
}

// readLeaseState 读租约；done=true 表示已结束（读失败或 owner 失败已写入 current）。
func (s *singleflight) readLeaseState(
	ctx context.Context, storageKey, userKey string, current *call,
	fn func(context.Context) ([]byte, error), commit func(context.Context, []byte) error, finish func(),
) (leaseRow, bool, bool) {
	lease, found, readErr := s.leases.readLease(ctx, storageKey)
	if readErr != nil {
		s.logLeaseWarning("lease_read", readErr)
		s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
		return leaseRow{}, false, true
	}
	// 错误行同样 MUST 判过期：租约的「标记失败」与「删除」不是原子的，进程被 SIGKILL
	// 或删除失败（只记日志）就会留下一行 last_error_code≠0 的记录。不判过期的话这个
	// key 会被永久毒化 —— 所有进程对它恒返回 3075，重启也不恢复（Prune 只清 cache 表）。
	// 过期后交回正常路径，由 tryPreempt 抢占重跑。
	if found && lease.LastErrorCode != 0 && lease.ExpiresAt >= s.clk.Now().Unix() {
		current.err = ownerFailedError(userKey, lease.LastErrorCode)
		return leaseRow{}, false, true
	}
	return lease, found, false
}

// waitNextPoll 等到下一次轮询；返回 true 表示已结束（超时或 ctx 取消）。
func (s *singleflight) waitNextPoll(ctx context.Context, deadline time.Time, waitMs int, userKey string, current *call) bool {
	if s.clk.Now().After(deadline) {
		current.err = waitTimeoutError(userKey, waitMs)
		return true
	}
	select {
	case <-ctx.Done():
		current.err = fmt.Errorf("singleflight wait canceled: %w", ctx.Err())
		return true
	case <-s.afterPoll(s.pollInterval):
		return false
	}
}

// afterPoll 默认走注入的 Clock；测试可替换为真实定时器来驱动轮询节奏。
func (s *singleflight) afterPoll(d time.Duration) <-chan time.Time {
	if s.pollAfter != nil {
		return s.pollAfter(d)
	}
	return s.clk.After(d)
}

// tryPreempt 处理租约过期抢占；返回 true 表示 lead 已结束。
func (s *singleflight) tryPreempt(
	ctx context.Context, storageKey, userKey string, current *call, lease leaseRow,
	fn func(context.Context) ([]byte, error), commit func(context.Context, []byte) error, finish func(),
) bool {
	now := s.clk.Now().Unix()
	if lease.ExpiresAt >= now {
		return false
	}
	preempted, preemptErr := s.leases.preemptLease(
		ctx, storageKey, s.owner, now+s.leaseSeconds(storageKey), now)
	if preemptErr != nil {
		s.logLeaseWarning("lease_preempt", preemptErr)
		s.runOwner(ctx, storageKey, userKey, current, fn, commit, false, finish)
		return true
	}
	if preempted {
		s.runAcquired(ctx, storageKey, userKey, current, fn, commit, finish)
		return true
	}
	return false
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
