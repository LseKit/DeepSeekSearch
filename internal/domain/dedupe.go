// 文件名: dedupe.go
// 用途: 搜索结果三层去重（URL hash / 转载标题 / 域名配额），输出保持 rerank_score 降序。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"net/url"
	"sort"
	"strings"
)

// SearchResult 是上游 searchItem 的对外表示，字段与 12 号 §1.2 逐字对应。
type SearchResult struct {
	RerankScore float64 `json:"rerank_score"`
	Title       string  `json:"title"`
	Url         string  `json:"url"` //nolint:revive // 契约字段名，见 12 号 §1.2
	Snippet     string  `json:"snippet"`
	Summary     string  `json:"summary"`
	Content     string  `json:"content"`
	SiteName    *string `json:"site_name"`
	SiteIcon    *string `json:"site_icon"`
	Date        *string `json:"date"`
	Authority   int     `json:"authority"`
}

// DedupeStats 记录三层各丢弃多少条。
type DedupeStats struct {
	DroppedURL    int `json:"dropped_url"`
	DroppedRepost int `json:"dropped_repost"`
	DroppedDomain int `json:"dropped_domain"`
}

// Dedupe 按三层（URL hash / 转载 / 域名配额）去重，保持 rerank_score 降序。
//
// trackingParams 由调用方从 [dedupe].tracking_params 注入：L1 用 NormalizeUrl
// 算 hash，传 nil 时 utm_* 不会被剥离，同页多链会被当成不同条目。
// MUST NOT 在 domain 硬编码那张表，理由同 NormalizeUrl。
func Dedupe(items []SearchResult, maxPerDomain int, trackingParams []string) (kept []SearchResult, stats DedupeStats) {
	ordered := rankResults(items)
	kept = make([]SearchResult, 0, len(ordered))
	seenURL := make(map[uint64]struct{}, len(ordered))
	seenRepost := make(map[repostKey]struct{}, len(ordered))
	perHost := make(map[string]int, len(ordered))

	for i := range ordered {
		item := &ordered[i].item

		hash := urlHash(item.Url, trackingParams)
		if _, ok := seenURL[hash]; ok {
			stats.DroppedURL++
			continue
		}
		seenURL[hash] = struct{}{}

		key := repostOf(item)
		if _, ok := seenRepost[key]; ok {
			stats.DroppedRepost++
			continue
		}
		seenRepost[key] = struct{}{}

		if maxPerDomain > 0 {
			host := hostOf(item.Url)
			if host != "" && perHost[host] >= maxPerDomain {
				stats.DroppedDomain++
				continue
			}
			if host != "" {
				perHost[host]++
			}
		}

		kept = append(kept, *item)
	}

	return kept, stats
}

type rankedResult struct {
	item SearchResult
	idx  int
}

// rankResults 按分数降序排列；同分时用原始下标兜底，避免 sort.Slice 的不稳定性打乱同分顺序。
func rankResults(items []SearchResult) []rankedResult {
	ordered := make([]rankedResult, len(items))
	for i, item := range items {
		ordered[i] = rankedResult{item: item, idx: i}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].item.RerankScore == ordered[j].item.RerankScore {
			return ordered[i].idx < ordered[j].idx
		}
		return ordered[i].item.RerankScore > ordered[j].item.RerankScore
	})
	return ordered
}

type repostKey struct {
	title   string
	site    string
	hasSite bool
}

func repostOf(item *SearchResult) repostKey {
	key := repostKey{title: NormalizeTitle(item.Title)}
	if item.SiteName != nil {
		key.site = *item.SiteName
		key.hasSite = true
	}
	return key
}

func urlHash(raw string, trackingParams []string) uint64 {
	normalized, err := NormalizeUrl(raw, trackingParams)
	if err != nil {
		normalized = raw
	}
	return HashUrl(normalized)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
