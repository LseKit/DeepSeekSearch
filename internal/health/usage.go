// 文件名: usage.go
// 用途: 进程内工具用量统计（按工具与按小时两维），供 ds://usage 资源与 ds_health 读取。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package health

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

// trackedTools 是统计器预置的五个工具键。
// 预置而不是懒建：调用方读一个从未被调过的工具时也要拿到全零条目，
// 否则客户端要写两套分支。
var trackedTools = []string{"ds_search", "ds_fetch", "ds_health", "ds_research", "ds_extract"}

type toolStat struct {
	calls      atomic.Int64
	billed     atomic.Int64
	cacheHits  atomic.Int64
	errors     atomic.Int64
	lastCallAt atomic.Value // time.Time
}

type hourCounters struct {
	Calls     int64 `json:"calls"`
	Billed    int64 `json:"billed"`
	CacheHits int64 `json:"cache_hits"`
	Errors    int64 `json:"errors"`
}

// UsageTracker 记录本进程生命周期内的工具调用统计；重启清零，不落盘。
//
// 住在 health 而不是 tools：它只被诊断面（ds_health 与 ds://usage 资源）读取，
// 而 tools 包已经贴着单包行数上限，放不下一块与工具编排无关的统计代码。
type UsageTracker struct {
	mu     sync.Mutex
	clk    resilience.Clock
	stats  map[string]*toolStat
	hourly map[int64]*hourCounters
}

// NewUsageTracker 构造固定五个工具键的统计器。
func NewUsageTracker(clk resilience.Clock) *UsageTracker {
	if clk == nil {
		clk = resilience.SystemClock()
	}
	stats := make(map[string]*toolStat, len(trackedTools))
	for _, tool := range trackedTools {
		stats[tool] = &toolStat{}
	}
	return &UsageTracker{clk: clk, stats: stats, hourly: make(map[int64]*hourCounters)}
}

// Record 在每次工具调用结束时更新统计；billed 与 cacheHit 互斥由调用方保证。
func (u *UsageTracker) Record(tool string, billed, cacheHit bool, err error) {
	// 统计器是可选依赖（诊断面缺它只是少一段数据），nil 接收者 MUST 是 no-op：
	// 它经接口注入，typed-nil 会让调用方的 != nil 判断失效。
	if u == nil {
		return
	}
	stat, ok := u.stats[tool]
	if !ok {
		return
	}
	stat.calls.Add(1)
	if billed {
		stat.billed.Add(1)
	}
	if cacheHit {
		stat.cacheHits.Add(1)
	}
	if err != nil {
		stat.errors.Add(1)
	}
	now := u.clk.Now()
	stat.lastCallAt.Store(now)

	hour := now.UTC().Truncate(time.Hour).Unix()
	u.mu.Lock()
	bucket := u.hourly[hour]
	if bucket == nil {
		bucket = &hourCounters{}
		u.hourly[hour] = bucket
	}
	bucket.Calls++
	if billed {
		bucket.Billed++
	}
	if cacheHit {
		bucket.CacheHits++
	}
	if err != nil {
		bucket.Errors++
	}
	u.pruneLocked(now)
	u.mu.Unlock()
}

// JSON 返回统计快照；nil 接收者给全零骨架，调用方不必判空。
func (u *UsageTracker) JSON() ([]byte, error) {
	snapshot := emptyUsageSnapshot()
	if u != nil {
		snapshot = u.snapshot()
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal usage: %w", err)
	}
	return raw, nil
}

func (u *UsageTracker) pruneLocked(now time.Time) {
	cutoff := now.UTC().Truncate(time.Hour).Add(-23 * time.Hour).Unix()
	for hour := range u.hourly {
		if hour < cutoff {
			delete(u.hourly, hour)
		}
	}
}

type toolUsage struct {
	Calls      int64  `json:"calls"`
	Billed     int64  `json:"billed"`
	CacheHits  int64  `json:"cache_hits"`
	Errors     int64  `json:"errors"`
	LastCallAt string `json:"last_call_at"`
}

type hourUsage struct {
	Hour string `json:"hour"`
	hourCounters
}

type usageSnapshot struct {
	Tools  map[string]toolUsage `json:"tools"`
	Hourly []hourUsage          `json:"hourly"`
}

// snapshot 返回当前统计快照；hourly 只保留最近 24 桶并按时间升序。
func (u *UsageTracker) snapshot() usageSnapshot {
	u.mu.Lock()
	u.pruneLocked(u.clk.Now())
	hours := make([]int64, 0, len(u.hourly))
	for hour := range u.hourly {
		hours = append(hours, hour)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	hourly := make([]hourUsage, 0, len(hours))
	for _, hour := range hours {
		hourly = append(hourly, hourUsage{
			Hour:         time.Unix(hour, 0).UTC().Format(time.RFC3339),
			hourCounters: *u.hourly[hour],
		})
	}
	u.mu.Unlock()

	tools := make(map[string]toolUsage, len(u.stats))
	for name, stat := range u.stats {
		item := toolUsage{
			Calls:     stat.calls.Load(),
			Billed:    stat.billed.Load(),
			CacheHits: stat.cacheHits.Load(),
			Errors:    stat.errors.Load(),
		}
		if value, ok := stat.lastCallAt.Load().(time.Time); ok {
			item.LastCallAt = value.UTC().Format(time.RFC3339)
		}
		tools[name] = item
	}
	return usageSnapshot{Tools: tools, Hourly: hourly}
}

func emptyUsageSnapshot() usageSnapshot {
	tools := make(map[string]toolUsage, len(trackedTools))
	for _, tool := range trackedTools {
		tools[tool] = toolUsage{}
	}
	return usageSnapshot{Tools: tools, Hourly: []hourUsage{}}
}
