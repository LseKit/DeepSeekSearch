// 文件名: resources.go
// 用途: 注册三个只读 MCP Resource，并提供按 URI 的 ttlMs/cacheScope 分派与用量统计。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

const (
	resourceEffectiveConfig = "ds://effective-config"
	resourceErrorCodes      = "ds://error-codes"
	resourceUsage           = "ds://usage"
)

type resourceDef struct {
	URI         string
	Name        string
	Title       string
	Description string
	MIMEType    string
	read        func(context.Context, *Deps) ([]byte, error)
}

// resourceDefinitions 按 URI 字典序返回三个 Resource。
// 顺序即注册顺序，保证 resources/list 连续 20 次完全一致。
func resourceDefinitions() []resourceDef {
	return []resourceDef{
		{
			URI:         resourceEffectiveConfig,
			Name:        "生效配置",
			Title:       "生效配置与来源",
			Description: "排查「为什么参数没生效」时读取：脱敏后的完整配置、每个键的来源层级与 config_sha256。",
			MIMEType:    "application/json",
			read:        effectiveConfigJSON,
		},
		{
			URI:         resourceErrorCodes,
			Name:        "错误码表",
			Title:       "错误码全表",
			Description: "需要解释错误码含义或判断是否可重试时读取：42 个码的 code/name/message/hint/retryable。",
			MIMEType:    "application/json",
			read:        func(context.Context, *Deps) ([]byte, error) { return errorCodesDocument() },
		},
		{
			URI:         resourceUsage,
			Name:        "调用统计",
			Title:       "本进程调用统计",
			Description: "想了解各工具调用量、计费次数、缓存命中与错误数时读取。仅本进程内存，重启清零。",
			MIMEType:    "application/json",
			read:        usageJSON,
		},
	}
}

// registerResources 注册三个只读 Resource。MIME 三个都是 application/json。
func registerResources(srv *mcp.Server, deps Deps) error { //nolint:gocritic // 签名由 Task-10 §5 固定
	for _, def := range resourceDefinitions() {
		definition := def
		resource := &mcp.Resource{
			URI:         definition.URI,
			Name:        definition.Name,
			Title:       definition.Title,
			Description: definition.Description,
			MIMEType:    definition.MIMEType,
		}
		srv.AddResource(resource, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("read resource canceled: %w", err)
			}
			data, err := definition.read(ctx, &deps)
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      definition.URI,
					MIMEType: definition.MIMEType,
					Text:     string(data),
				}},
			}, nil
		})
	}
	return nil
}

// CacheableFor 返回 Resource 的 ttlMs/cacheScope；未知 URI 回落到 (1000,"private")。
// 0 与「SetCacheable 回调没接上」不可区分，所以任何取值都 MUST > 0。
func CacheableFor(uri string) (ttlMs int64, scope string) {
	switch uri {
	case resourceEffectiveConfig:
		return 300000, "private"
	case resourceErrorCodes:
		return 300000, "public"
	case resourceUsage:
		return 1000, "private"
	default:
		return 1000, "private"
	}
}

// CacheableForRequest 从 resources/read 的 Params.URI 取键。
// go-sdk v1.8.0 的 mcp.Request 没有 URI()；其它请求（list/server-discover）走默认值。
func CacheableForRequest(req mcp.Request) (ttlMs int64, scope string) {
	if read, ok := req.(*mcp.ServerRequest[*mcp.ReadResourceParams]); ok && read.Params != nil {
		return CacheableFor(read.Params.URI)
	}
	return 1000, "private"
}

type errorCodeEntry struct {
	Code      int    `json:"code"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	Hint      string `json:"hint"`
	Retryable bool   `json:"retryable"`
}

func errorCodesDocument() ([]byte, error) {
	return errorCodesDocumentFrom(domain.CodeRegistry)
}

// errorCodesDocumentFrom 单独接收码表，测试可注入改过的副本验证「不是手写 JSON」。
func errorCodesDocumentFrom(registry map[int]domain.CodeInfo) ([]byte, error) {
	codes := make([]int, 0, len(registry))
	for code := range registry {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	entries := make([]errorCodeEntry, 0, len(codes))
	for _, code := range codes {
		info := registry[code]
		entries = append(entries, errorCodeEntry{
			Code: code, Name: info.Name, Message: info.Message,
			Hint: info.Hint, Retryable: info.Retryable,
		})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal error codes: %w", err)
	}
	return raw, nil
}

type effectiveConfigDocument struct {
	ApiKey       string            `json:"api_key"`    //nolint:revive // 输出字段名与配置契约一致
	HttpToken    string            `json:"http_token"` //nolint:revive // 输出字段名与配置契约一致
	Config       json.RawMessage   `json:"config"`
	Sources      map[string]string `json:"source"`
	ConfigSha256 string            `json:"config_sha256"`
}

func configSourceKeys() []string {
	return []string{
		"server.transport",
		"upstream.base_url",
		"ratelimit.max_inflight",
		"ratelimit.search.rps",
		"ratelimit.fetch.rps",
		"timeout.search.total_timeout_ms",
		"timeout.fetch.total_timeout_ms",
		"cache.enabled",
		"cache.db_path",
		"cache.ttl_search_s",
		"cache.ttl_fetch_s",
		"limits.max_total_chars",
		"limits.inline_max_chars",
		"limits.outline_max_sections",
		"log.level",
		"log.timezone",
		"log.slow_ms",
		"dedupe.enabled",
		"synth.enabled",
	}
}

func effectiveConfigJSON(_ context.Context, deps *Deps) ([]byte, error) {
	redacted := deps.Cfg.Redacted()
	// Config 的每个字段都带 json tag，键名与 config.toml 完全一致 ——
	// 这样本资源的 config 段与 source 的点路径能互相对照。
	configRaw, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("marshal redacted config: %w", err)
	}
	keys := configSourceKeys()
	sources := make(map[string]string, len(keys))
	for _, key := range keys {
		sources[key] = string(deps.Cfg.Source(key))
	}
	doc := effectiveConfigDocument{
		ApiKey:       "***",
		HttpToken:    "***",
		Config:       configRaw,
		Sources:      sources,
		ConfigSha256: configFingerprint(&deps.Cfg),
	}
	raw, err := json.Marshal(doc) //nolint:gosec // 这两个字段的值固定为 ***，仅展示脱敏结果
	if err != nil {
		return nil, fmt.Errorf("marshal effective config: %w", err)
	}
	return raw, nil
}

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
type UsageTracker struct {
	mu     sync.Mutex
	clk    resilience.Clock
	stats  map[string]*toolStat
	hourly map[int64]*hourCounters
}

// NewUsageTracker 构造固定三个工具键的统计器。
func NewUsageTracker(clk resilience.Clock) *UsageTracker {
	if clk == nil {
		clk = resilience.SystemClock()
	}
	stats := make(map[string]*toolStat, 3)
	for _, tool := range []string{"ds_search", "ds_fetch", "ds_health"} {
		stats[tool] = &toolStat{}
	}
	return &UsageTracker{clk: clk, stats: stats, hourly: make(map[int64]*hourCounters)}
}

// Record 在每次工具调用结束时更新统计；billed 与 cacheHit 互斥由调用方保证。
func (u *UsageTracker) Record(tool string, billed, cacheHit bool, err error) {
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
	tools := make(map[string]toolUsage, 3)
	for _, tool := range []string{"ds_search", "ds_fetch", "ds_health"} {
		tools[tool] = toolUsage{}
	}
	return usageSnapshot{Tools: tools, Hourly: []hourUsage{}}
}

func usageJSON(_ context.Context, deps *Deps) ([]byte, error) {
	snapshot := emptyUsageSnapshot()
	if deps.Usage != nil {
		snapshot = deps.Usage.snapshot()
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal usage: %w", err)
	}
	return raw, nil
}
