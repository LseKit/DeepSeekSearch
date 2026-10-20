// 文件名: fetch_validate.go
// 用途: ds_fetch 的参数校验、SSRF 黑名单、快照 ID 生成与快照查询。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

const (
	fetchURLMaxLen    = 8192
	fetchMaxCharsCap  = 200000
	defaultFetchChars = 32000
)

// blockedNets 是 SSRF 黑名单。定位是纵深防御：抓取由上游执行，我方不直连目标 URL，
// 但 base_url 改成自建代理时它就是第一道防线。
var blockedNets = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

var blockedSuffixes = []string{".internal", ".local", ".localhost"}

type fetchParams struct {
	URL      string
	CacheID  string
	Section  string
	Sections []string
	MaxChars int
	Unescape bool
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
	params := fetchParams{
		MaxChars: defaultFetchMaxChars(cfg),
		Unescape: true,
	}
	if in == nil {
		return params, invalid("arguments", "缺少请求参数")
	}
	rawURL := derefString(in.URL)
	cacheID := derefString(in.CacheID)
	if rawURL == "" && cacheID == "" {
		return params, invalid("url/cache_id", "必须提供 url 或 cache_id 之一")
	}
	if rawURL != "" && cacheID != "" {
		return params, invalid("url/cache_id", "url 与 cache_id 只能二选一")
	}
	if in.MaxChars != nil {
		if *in.MaxChars < 0 || *in.MaxChars > fetchMaxCharsCap {
			return params, invalid("max_chars", "必须在 0~200000 之间（0 表示使用配置默认值）")
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
		return params, invalid("section/sections", "section 与 sections 只能二选一")
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
	if rule, blocked := ssrfRule(host, cfg); blocked {
		return fmt.Errorf("ssrf blocked: %w",
			domain.NewLocalError(domain.CodeValSSRFBlocked, "该 URL 指向内网地址，已被本地安全策略拦截",
				map[string]any{"host": host, "matched_prefix": rule}))
	}
	return nil
}

func ssrfRule(host string, cfg *config.Config) (string, bool) {
	if host == "localhost" {
		return "localhost", true
	}
	for _, suffix := range blockedSuffixes {
		if strings.HasSuffix(host, suffix) {
			return suffix, true
		}
	}
	for _, blocked := range cfg.Security.BlockedHosts {
		if host == strings.ToLower(strings.TrimSuffix(blocked, ".")) {
			return blocked, true
		}
	}
	if addr, ok := parseHostIP(host); ok {
		return addrBlockedPrefix(addr)
	}
	return "", false
}

func addrBlockedPrefix(addr netip.Addr) (string, bool) {
	addr = addr.Unmap()
	for _, prefix := range blockedNets {
		if prefix.Contains(addr) {
			return prefix.String(), true
		}
	}
	return "", false
}

// parseHostIP 处理十进制/八进制/十六进制 IP 与 inet_aton 省略写法。
// netip.ParseAddr("2130706433") 会直接失败；若就此放过，它会被当成普通域名。
func parseHostIP(host string) (netip.Addr, bool) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr, true
	}
	if strings.Contains(host, ":") {
		return netip.Addr{}, false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 1 || len(labels) > 4 {
		return netip.Addr{}, false
	}
	values := make([]uint64, len(labels))
	for i, label := range labels {
		if label == "" {
			return netip.Addr{}, false
		}
		value, err := strconv.ParseUint(label, 0, 64)
		if err != nil {
			return netip.Addr{}, false
		}
		values[i] = value
	}
	number, ok := combineIPv4(values)
	if !ok {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte{ //nolint:gosec // combineIPv4 已逐段校验范围，转换不会溢出
		byte(number >> 24), byte(number >> 16), byte(number >> 8), byte(number), //nolint:gosec // 已逐段校验范围
	}), true
}

func combineIPv4(values []uint64) (uint32, bool) {
	switch len(values) {
	case 1:
		if values[0] > 0xFFFFFFFF {
			return 0, false
		}
		return uint32(values[0]), true //nolint:gosec // 已校验 ≤0xFFFFFFFF
	case 2:
		if values[0] > 0xFF || values[1] > 0xFFFFFF {
			return 0, false
		}
		return uint32(values[0]<<24 | values[1]), true //nolint:gosec // 已校验各段范围
	case 3:
		if values[0] > 0xFF || values[1] > 0xFF || values[2] > 0xFFFF {
			return 0, false
		}
		return uint32(values[0]<<24 | values[1]<<16 | values[2]), true //nolint:gosec // 已校验各段范围
	case 4:
		if values[0] > 0xFF || values[1] > 0xFF || values[2] > 0xFF || values[3] > 0xFF {
			return 0, false
		}
		return uint32(values[0]<<24 | values[1]<<16 | values[2]<<8 | values[3]), true //nolint:gosec // 已校验各段范围
	default:
		return 0, false
	}
}

// snapshotID 绑定「一次具体抓取」而不是「一个 URL」。
func snapshotID(rawURL string, capturedAt time.Time) string {
	sum := sha256.Sum256([]byte(rawURL + strconv.FormatInt(capturedAt.Unix(), 10)))
	return "snap_" + hex.EncodeToString(sum[:8])
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
	indexRaw, hit, err := cache.Get(ctx, snapshotIndexKey(id))
	if err != nil {
		return cachedFetchEntry{}, fmt.Errorf("read snapshot index: %w", err)
	}
	if !hit {
		return cachedFetchEntry{}, snapshotGoneError(id)
	}
	var index snapshotIndex
	if json.Unmarshal(indexRaw, &index) != nil || index.CacheID != id || index.EntryKey == "" {
		return cachedFetchEntry{}, snapshotGoneError(id)
	}
	entryRaw, hit, err := cache.Get(ctx, index.EntryKey)
	if err != nil {
		return cachedFetchEntry{}, fmt.Errorf("read snapshot entry: %w", err)
	}
	if !hit {
		return cachedFetchEntry{}, snapshotGoneError(id)
	}
	var entry cachedFetchEntry
	if json.Unmarshal(entryRaw, &entry) != nil || entry.CacheID != id {
		return cachedFetchEntry{}, snapshotGoneError(id)
	}
	return entry, nil
}
