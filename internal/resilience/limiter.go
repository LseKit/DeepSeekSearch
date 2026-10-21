// 文件名: limiter.go
// 用途: per-key 令牌桶 + per-key 在飞配额，两段等待共享一个 maxWait 截止时间。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// Limiter 是按 key 隔离的令牌桶集合。
type Limiter interface {
	// Wait 取得令牌与在飞名额；成功后调用方 MUST 配对调用 Release。
	Wait(ctx context.Context, key string) error
	// Release 释放在飞名额；未持有时为 no-op。
	Release(key string)
	// Depth 返回当前排队深度。
	Depth(key string) int
}

// BucketSpec 描述单个桶。
type BucketSpec struct {
	Rps   float64
	Burst int
}

type limiter struct {
	clk     Clock
	buckets map[string]*bucket
	sems    map[string]chan struct{}
	depths  map[string]*atomic.Int32
	maxWait time.Duration
}

type bucket struct {
	mu          sync.Mutex
	rps         float64
	burst       int
	interval    time.Duration
	nextFree    time.Time
	initialized bool
}

// NewLimiter 构造限流器；配置非法返回 3090，不 panic。
func NewLimiter(spec map[string]BucketSpec, maxInflight int, searchShare float64, maxWait time.Duration, clk Clock) (Limiter, error) {
	if err := validateLimiterArgs(clk, maxWait, maxInflight, searchShare); err != nil {
		return nil, err
	}
	searchQuota, fetchQuota, err := splitQuota(maxInflight, searchShare)
	if err != nil {
		return nil, err
	}
	if err := checkBuckets(spec); err != nil {
		return nil, err
	}
	l := &limiter{
		clk:     clk,
		buckets: make(map[string]*bucket, 2),
		sems:    make(map[string]chan struct{}, 2),
		depths:  make(map[string]*atomic.Int32, 2),
		maxWait: maxWait,
	}
	l.buckets["search"], l.buckets["fetch"] = newBucket(spec["search"]), newBucket(spec["fetch"])
	l.sems["search"], l.sems["fetch"] = make(chan struct{}, searchQuota), make(chan struct{}, fetchQuota)
	l.depths["search"], l.depths["fetch"] = &atomic.Int32{}, &atomic.Int32{}
	return l, nil
}

func validateLimiterArgs(clk Clock, maxWait time.Duration, maxInflight int, searchShare float64) error {
	if clk == nil {
		return configError("Clock 未注入")
	}
	if maxWait <= 0 {
		return configError("maxWait 必须 > 0")
	}
	if maxInflight < 3 || maxInflight > 64 {
		return configError("maxInflight 必须 3~64，实际 %d", maxInflight)
	}
	if searchShare <= 0 || searchShare >= 1 {
		return configError("searchShare 必须满足 0 < x < 1，实际 %v", searchShare)
	}
	return nil
}

func splitQuota(maxInflight int, searchShare float64) (searchQuota, fetchQuota int, err error) {
	searchQuota = max(1, int(math.Floor(float64(maxInflight)*searchShare)))
	fetchQuota = max(2, maxInflight-searchQuota)
	if searchQuota+fetchQuota > maxInflight {
		return 0, 0, configError("per-key 配额之和 %d 超过 maxInflight %d", searchQuota+fetchQuota, maxInflight)
	}
	return searchQuota, fetchQuota, nil
}

func checkBuckets(spec map[string]BucketSpec) error {
	for _, key := range []string{"search", "fetch"} {
		item, ok := spec[key]
		if !ok {
			return configError("缺少限流 key %q", key)
		}
		if item.Rps <= 0 {
			return configError("%s.rps 必须 > 0", key)
		}
		if item.Burst < 1 {
			return configError("%s.burst 必须 ≥ 1", key)
		}
	}
	return nil
}

func newBucket(spec BucketSpec) *bucket {
	interval := time.Duration(float64(time.Second) / spec.Rps)
	if interval < time.Nanosecond {
		interval = time.Nanosecond
	}
	return &bucket{rps: spec.Rps, burst: spec.Burst, interval: interval}
}

// reserve 预约一个令牌，返回需要等待的时长与归还函数。
// 取消时把预约从队尾撤回；bucket 的 burst 通过初始 nextFree 向前回拨实现。
func (b *bucket) reserve(now time.Time) (wait time.Duration, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	minNext := now.Add(-time.Duration(b.burst-1) * b.interval)
	if !b.initialized {
		b.nextFree = minNext
		b.initialized = true
	}
	if b.nextFree.Before(minNext) {
		b.nextFree = minNext
	}
	wait = 0
	if b.nextFree.After(now) {
		wait = b.nextFree.Sub(now)
	}
	b.nextFree = b.nextFree.Add(b.interval)

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			b.mu.Lock()
			b.nextFree = b.nextFree.Add(-b.interval)
			b.mu.Unlock()
		})
	}
	return wait, cancel
}

// Wait 先等令牌、再等在飞名额；两段共享同一个 deadline（最坏 ≤ maxWait）。
func (l *limiter) Wait(ctx context.Context, key string) error {
	bucket, sem, depth, ok := l.lookup(key)
	if !ok {
		return fmt.Errorf("resilience: 未知限流 key %q", key)
	}
	start := l.clk.Now()
	deadline := start.Add(l.maxWait)
	depth.Add(1)
	defer depth.Add(-1)

	tokenWait, cancelToken := bucket.reserve(start)
	keepToken := false
	defer func() {
		if !keepToken {
			cancelToken()
		}
	}()
	if err := l.waitToken(ctx, tokenWait, deadline, depth, start); err != nil {
		return err
	}
	if l.acquireSem(sem) {
		keepToken = true
		return nil
	}
	cancelToken()
	remaining := deadline.Sub(l.clk.Now())
	if remaining <= 0 {
		return l.queueTimeout("inflight", depth, start)
	}
	return l.waitInflight(ctx, sem, remaining, depth, start)
}

func (l *limiter) waitToken(ctx context.Context, tokenWait time.Duration, deadline time.Time, depth *atomic.Int32, start time.Time) error {
	remaining := deadline.Sub(l.clk.Now())
	if remaining <= 0 {
		return l.queueTimeout("token", depth, start)
	}
	if tokenWait > remaining {
		if err := waitClock(ctx, l.clk, remaining); err != nil {
			return err
		}
		return l.queueTimeout("token", depth, start)
	}
	return waitClock(ctx, l.clk, tokenWait)
}

func (l *limiter) acquireSem(sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *limiter) waitInflight(ctx context.Context, sem chan struct{}, remaining time.Duration, depth *atomic.Int32, start time.Time) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("resilience: 排队被取消: %w", ctx.Err())
	case <-l.clk.After(remaining):
		return l.queueTimeout("inflight", depth, start)
	}
}

// Release 释放在飞名额；重复调用或未持有均为 no-op。
func (l *limiter) Release(key string) {
	sem, ok := l.sems[key]
	if !ok {
		return
	}
	select {
	case <-sem:
	default:
	}
}

// LimiterDiagnostics 是健康诊断用的 per-key 限流运行态。
// 令牌余量不是判据：瞬时 0 令牌是高负载常态，真正危险的是排队即将超时。
type LimiterDiagnostics struct {
	Key             string
	RPS             float64
	Burst           int
	Inflight        int
	Quota           int
	Waiters         int
	EstimatedWaitMs int64
	Stage           string
}

// Diagnostics 返回指定 key 的在飞/配额/排队与预计等待。
func (l *limiter) Diagnostics(key string) LimiterDiagnostics {
	bucket, sem, depth, ok := l.lookup(key)
	if !ok {
		return LimiterDiagnostics{Key: key, Stage: "unknown"}
	}
	bucket.mu.Lock()
	nextFree := bucket.nextFree
	initialized := bucket.initialized
	rps := bucket.rps
	burst := bucket.burst
	bucket.mu.Unlock()

	now := l.clk.Now()
	var wait time.Duration
	if initialized && nextFree.After(now) {
		wait = nextFree.Sub(now)
	}
	inflight := len(sem)
	quota := cap(sem)
	waiters := int(depth.Load())
	stage := "none"
	switch {
	case wait > 0:
		stage = "token"
	case inflight >= quota:
		stage = "inflight"
	}
	estimated := wait.Milliseconds()
	if stage == "inflight" && waiters > 0 {
		estimated = l.maxWait.Milliseconds()
	}
	return LimiterDiagnostics{
		Key: key, RPS: rps, Burst: burst,
		Inflight: inflight, Quota: quota, Waiters: waiters,
		EstimatedWaitMs: estimated, Stage: stage,
	}
}

// Depth 返回当前排队深度；未知 key 返回 0。
func (l *limiter) Depth(key string) int {
	depth, ok := l.depths[key]
	if !ok {
		return 0
	}
	return int(depth.Load())
}

func (l *limiter) lookup(key string) (*bucket, chan struct{}, *atomic.Int32, bool) {
	bucket, ok := l.buckets[key]
	if !ok {
		return nil, nil, nil, false
	}
	return bucket, l.sems[key], l.depths[key], true
}

func (l *limiter) queueTimeout(stage string, depth *atomic.Int32, start time.Time) error {
	return fmt.Errorf("resilience: %w", domain.NewLocalError(domain.CodeLocQueueTimeout, "排队等待超时", map[string]any{
		"stage":       stage,
		"queue_depth": int(depth.Load()),
		"waited_ms":   l.clk.Now().Sub(start).Milliseconds(),
	}))
}

func waitClock(ctx context.Context, clk Clock, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("resilience: 排队被取消: %w", ctx.Err())
	case <-clk.After(d):
		return nil
	}
}

func configError(format string, args ...any) error {
	return domain.NewLocalError(domain.CodeLocConfigInvalid, fmt.Sprintf(format, args...), nil) //nolint:wrapcheck // 错误构造器
}
