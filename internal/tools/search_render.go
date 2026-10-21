// 文件名: search_render.go
// 用途: ds_search 的 detail 分级、去重、过滤、预算截断、untrusted 包裹与响应渲染。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

type cachedSearchEnvelope struct {
	CachedAt int64                 `json:"cached_at"`
	Response domain.SearchResponse `json:"response"`
}

type searchOutput struct {
	QueryEffective   string             `json:"query_effective"`
	Total            int                `json:"total"`
	Results          []searchOutputItem `json:"results"`
	Warnings         []string           `json:"warnings"`
	FromCache        bool               `json:"from_cache"`
	CachedAt         string             `json:"cached_at"`
	AgeSeconds       int                `json:"age_seconds"`
	SavedBilledCalls int                `json:"saved_billed_calls"`
}

type searchOutputItem struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Date          *string `json:"date"`
	AgeDays       *int    `json:"age_days"`
	DatePrecision string  `json:"date_precision"`
	Authority     int     `json:"authority"`
	RerankScore   float64 `json:"rerank_score"`
	SiteName      *string `json:"site_name"`
	Snippet       *string `json:"snippet,omitempty"`
	Summary       *string `json:"summary,omitempty"`
	Content       *string `json:"content,omitempty"`
}

func decodeSearchEnvelope(raw []byte) (cachedSearchEnvelope, bool) {
	var env cachedSearchEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.CachedAt <= 0 {
		return cachedSearchEnvelope{}, false
	}
	return env, true
}

func (h *searchHandler) render(env cachedSearchEnvelope, fromCache bool) (res *mcp.CallToolResult, total, results int) {
	now := h.deps.Clock.Now()
	total = len(env.Response.Results)
	items := env.Response.Results
	warnings := append([]string{}, h.params.Warnings...)

	if h.deps.Cfg.Dedupe.Enabled {
		var stats domain.DedupeStats
		items, stats = domain.Dedupe(items, h.params.MaxPerDomain, h.deps.Cfg.Dedupe.TrackingParams)
		h.logger.Info("call.dedupe",
			"dropped_url", stats.DroppedURL,
			"dropped_repost", stats.DroppedRepost,
			"dropped_domain", stats.DroppedDomain)
		if stats.DroppedURL+stats.DroppedRepost+stats.DroppedDomain > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"去重丢弃 %d 条（URL %d / 转载 %d / 域名配额 %d）",
				stats.DroppedURL+stats.DroppedRepost+stats.DroppedDomain,
				stats.DroppedURL, stats.DroppedRepost, stats.DroppedDomain))
		}
	}

	items, removed, scoreRange := filterByMin(items, &h.params)
	if removed > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d 条结果被 min_rerank/min_authority 过滤掉，原始分数区间是 %s", removed, scoreRange))
	}

	rendered := make([]searchOutputItem, 0, len(items))
	for i := range items {
		rendered = append(rendered, h.renderItem(&items[i], now))
	}
	rendered, truncated := truncateResults(rendered, h.maxTotalChars())
	if truncated {
		warnings = append(warnings, fmt.Sprintf(
			"结果因 %d rune 预算截断，仅保留 %d/%d 条", h.maxTotalChars(), len(rendered), len(items)))
	}
	if total == 0 {
		warnings = append(warnings, "未找到结果")
	}

	cachedAt := time.Unix(env.CachedAt, 0)
	age := int(now.Sub(cachedAt).Seconds())
	if age < 0 {
		age = 0
	}
	saved := 0
	if fromCache {
		saved = 1
	}
	out := searchOutput{
		QueryEffective:   h.params.EffectiveQuery,
		Total:            total,
		Results:          rendered,
		Warnings:         warnings,
		FromCache:        fromCache,
		CachedAt:         cachedAt.UTC().Format(time.RFC3339),
		AgeSeconds:       age,
		SavedBilledCalls: saved,
	}
	raw, err := json.Marshal(out)
	if err != nil {
		bad := domain.NewLocalError(domain.CodeLocInternal, "序列化搜索结果失败", nil)
		return errorResult(bad, searchToolName), 0, 0
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}},
	}, total, len(rendered)
}

func (h *searchHandler) renderItem(item *domain.SearchResult, now time.Time) searchOutputItem {
	published := domain.ParseDate(item.Date, now)
	out := searchOutputItem{
		Title:         item.Title,
		URL:           item.Url,
		Date:          published.Date,
		AgeDays:       published.AgeDays,
		DatePrecision: published.DatePrecision,
		Authority:     item.Authority,
		RerankScore:   item.RerankScore,
		SiteName:      item.SiteName,
	}
	switch h.params.Detail {
	case "snippet":
		out.Snippet = wrapUntrustedPtr(item.Url, item.Snippet)
	case "summary":
		out.Snippet = wrapUntrustedPtr(item.Url, item.Snippet)
		out.Summary = wrapUntrustedPtr(item.Url, truncateSummary(item.Summary, h.summaryChars()))
	case "content":
		out.Snippet = wrapUntrustedPtr(item.Url, item.Snippet)
		out.Summary = wrapUntrustedPtr(item.Url, truncateSummary(item.Summary, h.summaryChars()))
		out.Content = wrapUntrustedPtr(item.Url, item.Content)
	}
	return out
}

func wrapUntrustedPtr(source, body string) *string {
	wrapped := domain.WrapUntrusted(source, body)
	return &wrapped
}

func truncateSummary(summary string, limit int) string {
	if limit <= 0 {
		return summary
	}
	truncated, _ := domain.SliceByRune(summary, 0, limit)
	return truncated
}

func (h *searchHandler) summaryChars() int {
	if h.deps.Cfg.Limits.SummaryChars > 0 {
		return h.deps.Cfg.Limits.SummaryChars
	}
	return 1200
}

func (h *searchHandler) maxTotalChars() int {
	if h.deps.Cfg.Limits.MaxTotalChars > 0 {
		return h.deps.Cfg.Limits.MaxTotalChars
	}
	return 32000
}

func filterByMin(items []domain.SearchResult, params *searchParams) (kept []domain.SearchResult, removed int, scoreRange string) {
	if params.MinRerank == nil && params.MinAuthority == nil {
		return items, 0, ""
	}
	if len(items) == 0 {
		return items, 0, ""
	}
	scoreRange = fmt.Sprintf("%.3f~%.3f", items[0].RerankScore, items[len(items)-1].RerankScore)
	kept = make([]domain.SearchResult, 0, len(items))
	for _, item := range items {
		if params.MinRerank != nil && item.RerankScore < *params.MinRerank {
			continue
		}
		if params.MinAuthority != nil && item.Authority >= 0 && item.Authority < *params.MinAuthority {
			continue
		}
		kept = append(kept, item)
	}
	return kept, len(items) - len(kept), scoreRange
}

func truncateResults(items []searchOutputItem, limit int) ([]searchOutputItem, bool) {
	if limit <= 0 {
		return items, false
	}
	kept := make([]searchOutputItem, 0, len(items))
	used := 0
	for i := range items {
		cost := resultCost(&items[i])
		if len(kept) > 0 && used+cost > limit {
			return kept, true
		}
		kept = append(kept, items[i])
		used += cost
	}
	return kept, false
}

func resultCost(item *searchOutputItem) int {
	cost := utf8.RuneCountInString(item.Title) + utf8.RuneCountInString(item.URL)
	if item.SiteName != nil {
		cost += utf8.RuneCountInString(*item.SiteName)
	}
	for _, field := range []*string{item.Snippet, item.Summary, item.Content} {
		if field != nil {
			cost += utf8.RuneCountInString(*field)
		}
	}
	return cost
}
