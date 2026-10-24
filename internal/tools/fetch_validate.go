// 文件名: fetch_validate.go
// 用途: ds_fetch 的参数校验、SSRF 黑名单、快照 ID 生成与快照查询。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
	"github.com/LseKit/DeepSeekSearch/internal/toolvalidate"
)

const (
	fetchURLMaxLen    = 8192
	fetchMaxCharsCap  = 200000
	defaultFetchChars = 32000
)

type fetchParams struct {
	URL      string
	CacheID  string
	Section  string
	Sections []string
	MaxChars int
	Unescape bool
	NoCache  bool
}

type cachedFetchEntry struct {
	CacheID    string `json:"cache_id"`
	URL        string `json:"url"`
	URLSHA256  string `json:"url_sha256"`
	CapturedAt int64  `json:"captured_at"`
	Title      string `json:"title"`
	Content    string `json:"content"`
}

type snapshotIndex struct {
	EntryKey string `json:"entry_key"`
	CacheID  string `json:"cache_id"`
}

func validateFetch(in *fetchInput, cfg *config.Config) (fetchParams, error) {
	if in == nil {
		return fetchParams{MaxChars: defaultFetchMaxChars(cfg), Unescape: true}, invalid("arguments", "缺少请求参数")
	}
	params := fetchParams{
		MaxChars: defaultFetchMaxChars(cfg),
		Unescape: true,
		NoCache:  noCacheRequested(in.NoCache),
	}
	rawURL := derefString(in.URL)
	cacheID := derefString(in.CacheID)
	if rawURL == "" && cacheID == "" {
		return params, invalid("url/cache_id", "必须提供 url 或 cache_id 之一")
	}
	if err := validateFetchExclusivity(rawURL, cacheID, params.NoCache); err != nil {
		return params, err
	}
	if err := applyFetchOptions(&params, in); err != nil {
		return params, err
	}
	if rawURL != "" {
		if err := validateFetchURL(rawURL, cfg); err != nil {
			return params, err
		}
		params.URL = rawURL
		return params, nil
	}
	params.CacheID = cacheID
	return params, nil
}

func defaultFetchMaxChars(cfg *config.Config) int {
	if cfg.Limits.MaxTotalChars > 0 {
		return cfg.Limits.MaxTotalChars
	}
	return defaultFetchChars
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateFetchURL(raw string, cfg *config.Config) error {
	if len(raw) > fetchURLMaxLen {
		return fmt.Errorf("url too long: %w",
			domain.NewValidationError(domain.CodeValURLTooLong, "url", "URL 长度不能超过 8192 字符"))
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return invalid("url", "URL 无法解析")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("url scheme: %w",
			domain.NewValidationError(domain.CodeValURLScheme, "url", "只允许 http/https"))
	}
	if parsed.User != nil {
		return fmt.Errorf("url credentials: %w",
			domain.NewValidationError(domain.CodeValURLCredentials, "url", "URL 不能包含 user:pass@"))
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return fmt.Errorf("url host: %w",
			domain.NewValidationError(domain.CodeValURLScheme, "url", "URL 必须包含 host"))
	}
	// 端口越界本地就能判死。放过去会白花一次计费，拿回来的还是上游 400（2400），
	// 把「你 URL 写错了」说成「上游判定请求不合法」，调用方只会原样重试。
	if err := validateURLPort(parsed); err != nil {
		return err
	}
	if rule, blocked := toolvalidate.SSRFBlocked(host, cfg.Security.BlockedHosts); blocked {
		return fmt.Errorf("ssrf blocked: %w",
			domain.NewLocalError(domain.CodeValSSRFBlocked, "该 URL 指向内网地址，已被本地安全策略拦截",
				map[string]any{"host": host, "matched_prefix": rule}))
	}
	return nil
}

// validateURLPort 单独成函数：validateFetchURL 的圈复杂度已在上限边上。
func validateURLPort(parsed *url.URL) error {
	port := parsed.Port()
	if port == "" {
		return nil
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return invalid("url", "端口必须在 1~65535 之间")
	}
	return nil
}

// applyFetchOptions 处理 max_chars/unescape/section(s) 四个可选参数。
func applyFetchOptions(params *fetchParams, in *fetchInput) error {
	if in.MaxChars != nil {
		if *in.MaxChars < 0 || *in.MaxChars > fetchMaxCharsCap {
			return invalid("max_chars", "必须在 0~200000 之间（0 表示使用配置默认值）")
		}
		if *in.MaxChars > 0 {
			params.MaxChars = *in.MaxChars
		}
	}
	if in.Unescape != nil {
		params.Unescape = *in.Unescape
	}
	if in.Section != nil {
		params.Section = *in.Section
	}
	if in.Sections != nil {
		params.Sections = append([]string(nil), in.Sections...)
	}
	if params.Section != "" && len(params.Sections) > 0 {
		return invalid("section/sections", "section 与 sections 只能二选一")
	}
	return nil
}

func snapshotIndexKey(id string) string { return "snapshot:" + id }

func snapshotGoneError(id string) error {
	return fmt.Errorf("snapshot gone: %w",
		domain.NewLocalError(domain.CodeLocSnapshotGone, "该快照已过期，请重新 fetch",
			map[string]any{"cache_id": id}))
}

func lookupSnapshot(ctx context.Context, cache resilience.Cache, id string) (cachedFetchEntry, error) {
	if cache == nil {
		return cachedFetchEntry{}, snapshotGoneError(id)
	}
	index, err := readSnapshotIndex(ctx, cache, id)
	if err != nil {
		return cachedFetchEntry{}, err
	}
	return readSnapshotEntry(ctx, cache, id, index)
}

func readSnapshotIndex(ctx context.Context, cache resilience.Cache, id string) (snapshotIndex, error) {
	indexRaw, hit, err := cache.Get(ctx, snapshotIndexKey(id))
	if err != nil {
		return snapshotIndex{}, fmt.Errorf("read snapshot index: %w", err)
	}
	var index snapshotIndex
	if !hit || json.Unmarshal(indexRaw, &index) != nil || index.CacheID != id || index.EntryKey == "" {
		return snapshotIndex{}, snapshotGoneError(id)
	}
	return index, nil
}

func readSnapshotEntry(ctx context.Context, cache resilience.Cache, id string, index snapshotIndex) (cachedFetchEntry, error) {
	entryRaw, hit, err := cache.Get(ctx, index.EntryKey)
	if err != nil {
		return cachedFetchEntry{}, fmt.Errorf("read snapshot entry: %w", err)
	}
	var entry cachedFetchEntry
	if !hit || json.Unmarshal(entryRaw, &entry) != nil || entry.CacheID != id {
		return cachedFetchEntry{}, snapshotGoneError(id)
	}
	return entry, nil
}
