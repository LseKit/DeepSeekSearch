// 文件名: validate.go
// 用途: 全量收集配置错误并输出 warning，覆盖枚举、数值区间与跨字段约束。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// Validate 收集全部配置错误后一次性返回，并把可启动但需提醒的项放进 warnings。
// .env 的权限检查在加载阶段完成（见 applyDotEnv）。
func (c Config) Validate() (warnings []string, err error) { //nolint:gocritic // 契约要求值接收者
	errs := make([]error, 0, 16)
	if e := checkPhaseOne(&c); e != nil {
		errs = append(errs, e)
	}
	errs = append(errs, c.validateServer()...)
	errs = append(errs, c.validateUpstream()...)
	errs = append(errs, c.validateEnums()...)
	errs = append(errs, c.validateRateLimit()...)
	errs = append(errs, c.validateTimeout()...)
	errs = append(errs, c.validateRetry()...)
	errs = append(errs, c.validateCircuit()...)
	errs = append(errs, c.validateLimits()...)
	errs = append(errs, c.validateCache()...)
	errs = append(errs, c.validateDedupe()...)
	errs = append(errs, c.validateSearch()...)
	errs = append(errs, c.validateSecurity()...)
	errs = append(errs, c.validateLog()...)
	return c.collectWarnings(), errors.Join(errs...)
}

func (c *Config) validateServer() []error {
	var errs []error
	hc := &c.Server.Http
	if hc.Port < 1 || hc.Port > 65535 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"server.http.port 必须 1~65535，实际 %d", hc.Port))
	}
	if !strings.HasPrefix(hc.Path, "/") || strings.ContainsAny(hc.Path, " \t\r\n") {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"server.http.path 必须以 / 开头且不含空白，实际 %q", hc.Path))
	}
	if hc.MaxRequestBodyBytes < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"server.http.max_request_body_bytes 必须 ≥1，实际 %d", hc.MaxRequestBodyBytes))
	}
	if (hc.Host == "0.0.0.0" || hc.AllowNonLocalHost) && !hc.AuthEnabled {
		errs = append(errs, configErr(domain.CodeLocHTTPExposedNoAuth,
			"server.http: host=0.0.0.0 或 allow_non_local_host=true 时必须 auth_enabled=true"))
	}
	if hc.AuthEnabled && len(c.HttpToken) < 16 {
		errs = append(errs, configErr(domain.CodeLocHTTPTokenMissing,
			"server.http.auth_enabled=true 时 http_token 必须非空且 ≥16 字节，实际 %d 字节", len(c.HttpToken)))
	}
	return errs
}

func (c *Config) validateUpstream() []error {
	var errs []error
	validURL := false
	if u, err := url.Parse(c.Upstream.BaseURL); err == nil && u != nil {
		validURL = strings.EqualFold(u.Scheme, "https") && u.Host != ""
	}
	if !validURL {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"upstream.base_url 必须是合法的 https URL 且包含 host，实际 %q", c.Upstream.BaseURL))
	}
	if c.Upstream.MaxResponseBytes < 1024 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"upstream.max_response_bytes 必须 ≥1024，实际 %d", c.Upstream.MaxResponseBytes))
	}
	return errs
}

func (c *Config) validateEnums() []error {
	var errs []error
	if !oneOf(c.Server.Transport, "stdio", "streamable-http") {
		errs = append(errs, enumErr("server.transport", c.Server.Transport, "stdio", "streamable-http"))
	}
	if !oneOf(c.Cache.Backend, "sqlite") {
		errs = append(errs, enumErr("cache.backend", c.Cache.Backend, "sqlite"))
	}
	if !oneOf(c.Log.Level, "debug", "info", "warn", "error") {
		errs = append(errs, enumErr("log.level", c.Log.Level, "debug", "info", "warn", "error"))
	}
	if !oneOf(c.Log.Format, "text", "json") {
		errs = append(errs, enumErr("log.format", c.Log.Format, "text", "json"))
	}
	if !oneOf(c.Retry.JitterMode, "fixed_plus_random", "full", "none") {
		errs = append(errs, enumErr("retry.jitter_mode", c.Retry.JitterMode, "fixed_plus_random", "full", "none"))
	}
	if !oneOf(c.Limits.DefaultDetail, "minimal", "snippet", "summary", "content") {
		errs = append(errs, enumErr("limits.default_detail", c.Limits.DefaultDetail,
			"minimal", "snippet", "summary", "content"))
	}
	return errs
}

func (c *Config) validateRateLimit() []error {
	var errs []error
	if c.RateLimit.MaxInflight < 3 || c.RateLimit.MaxInflight > 64 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"ratelimit.max_inflight 必须 3~64，实际 %d", c.RateLimit.MaxInflight))
	}
	if c.RateLimit.SearchShare <= 0 || c.RateLimit.SearchShare >= 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"ratelimit.search_share 必须满足 0 < x < 1，实际 %v", c.RateLimit.SearchShare))
	}
	if c.RateLimit.MaxWaitMs < 1 || c.RateLimit.MaxWaitMs > 300000 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"ratelimit.max_wait_ms 必须 1~300000，实际 %d", c.RateLimit.MaxWaitMs))
	}
	errs = append(errs, validateBucket("ratelimit.search", c.RateLimit.Search)...)
	errs = append(errs, validateBucket("ratelimit.fetch", c.RateLimit.Fetch)...)
	errs = append(errs, c.validateWarnCalls()...)
	errs = append(errs, c.validateInflightQuota()...)

	minTotal := min(c.Timeout.Search.TotalTimeoutMs, c.Timeout.Fetch.TotalTimeoutMs)
	if minTotal > 0 && c.RateLimit.MaxWaitMs >= minTotal {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"ratelimit.max_wait_ms 必须 < timeout.search/fetch.total_timeout_ms 的较小值 %d，实际 %d",
			minTotal, c.RateLimit.MaxWaitMs))
	}
	return errs
}

func validateBucket(name string, bucket RateLimitBucketConfig) []error {
	var errs []error
	if bucket.Rps <= 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "%s.rps 必须 > 0，实际 %v", name, bucket.Rps))
	}
	if bucket.Burst < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "%s.burst 必须 ≥1，实际 %d", name, bucket.Burst))
	}
	return errs
}

func (c *Config) validateWarnCalls() []error {
	var errs []error
	if c.RateLimit.Warn.WindowS < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"ratelimit.warn.window_s 必须 ≥1，实际 %d", c.RateLimit.Warn.WindowS))
	}
	prev := 0
	for i, n := range c.RateLimit.Warn.WarnAtCalls {
		if n < 1 {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid,
				"ratelimit.warn.warn_at_calls[%d] 必须 ≥1，实际 %d", i, n))
		}
		if i > 0 && n <= prev {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid,
				"ratelimit.warn.warn_at_calls 必须严格升序，第 %d 项 %d ≤ 前一项 %d", i, n, prev))
		}
		prev = n
	}
	return errs
}

// validateInflightQuota 按 Task-06 §A-01 3.2 的公式校验两个配额之和不超过总额度。
func (c *Config) validateInflightQuota() []error {
	share := c.RateLimit.SearchShare
	total := c.RateLimit.MaxInflight
	if share <= 0 || share >= 1 || total < 1 {
		return nil
	}
	searchQuota := max(1, int(math.Floor(float64(total)*share)))
	fetchQuota := max(2, total-searchQuota)
	if searchQuota+fetchQuota > total {
		return []error{configErr(domain.CodeLocConfigInvalid,
			"ratelimit.max_inflight=%d 小于 search_share 推导出的配额之和 %d（search=%d, fetch=%d）：下限把配额顶超了总额度",
			total, searchQuota+fetchQuota, searchQuota, fetchQuota)}
	}
	return nil
}

func (c *Config) validateTimeout() []error {
	var errs []error
	if c.Timeout.DialMs < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "timeout.dial_ms 必须 ≥1，实际 %d", c.Timeout.DialMs))
	}
	if c.Timeout.TLSHandshakeMs < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"timeout.tls_handshake_ms 必须 ≥1，实际 %d", c.Timeout.TLSHandshakeMs))
	}
	if c.Timeout.ResponseHeaderMs < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"timeout.response_header_ms 必须 ≥1，实际 %d", c.Timeout.ResponseHeaderMs))
	}
	errs = append(errs, c.validateToolTimeout("timeout.search", c.Timeout.Search)...)
	errs = append(errs, c.validateToolTimeout("timeout.fetch", c.Timeout.Fetch)...)
	if c.Timeout.Fetch.PerAttemptMs <= 31000 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"timeout.fetch.per_attempt_ms 必须 > 31000（上游网关 30s），实际 %d", c.Timeout.Fetch.PerAttemptMs))
	}
	return errs
}

func (c *Config) validateToolTimeout(name string, t TimeoutToolConfig) []error {
	var errs []error
	if t.PerAttemptMs < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "%s.per_attempt_ms 必须 ≥1，实际 %d", name, t.PerAttemptMs))
	}
	if t.MaxAttempts < 1 || t.MaxAttempts > 6 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "%s.max_attempts 必须 1~6，实际 %d", name, t.MaxAttempts))
	}
	if t.TotalTimeoutMs < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "%s.total_timeout_ms 必须 ≥1，实际 %d", name, t.TotalTimeoutMs))
	}
	if t.PerAttemptMs >= 1 && t.MaxAttempts >= 1 {
		need := t.PerAttemptMs*t.MaxAttempts + sumInts(c.Retry.FixedMs) + sumInts(c.Retry.SpreadMs)
		if need > t.TotalTimeoutMs {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid,
				"%s.total_timeout_ms 必须 ≥ per_attempt_ms × max_attempts + Σ(fixed_ms+spread_ms) = %d，实际 %d",
				name, need, t.TotalTimeoutMs))
		}
	}
	if t.MaxAttempts > 1 && len(c.Retry.FixedMs) < t.MaxAttempts-1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"retry.fixed_ms 长度必须 ≥ %s.max_attempts-1 = %d，实际 %d",
			name, t.MaxAttempts-1, len(c.Retry.FixedMs)))
	}
	return errs
}

func (c *Config) validateRetry() []error {
	var errs []error
	if len(c.Retry.FixedMs) != len(c.Retry.SpreadMs) {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"retry.fixed_ms 与 retry.spread_ms 长度必须相等，实际 %d vs %d",
			len(c.Retry.FixedMs), len(c.Retry.SpreadMs)))
	}
	for i, v := range c.Retry.FixedMs {
		if v < 0 {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid, "retry.fixed_ms[%d] 必须 ≥0，实际 %d", i, v))
		}
	}
	for i, v := range c.Retry.SpreadMs {
		if v < 0 {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid, "retry.spread_ms[%d] 必须 ≥0，实际 %d", i, v))
		}
	}
	if c.Retry.Budget.Ratio <= 0 || c.Retry.Budget.Ratio > 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"retry.budget.ratio 必须满足 0 < ratio ≤ 1，实际 %v", c.Retry.Budget.Ratio))
	}
	if c.Retry.Budget.WindowS < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"retry.budget.window_s 必须 ≥1，实际 %d", c.Retry.Budget.WindowS))
	}
	return errs
}

func (c *Config) validateCircuit() []error {
	var errs []error
	if c.Circuit.FailThreshold < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"circuit.fail_threshold 必须 ≥1，实际 %d", c.Circuit.FailThreshold))
	}
	if c.Circuit.OpenMs < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "circuit.open_ms 必须 ≥1，实际 %d", c.Circuit.OpenMs))
	}
	if c.Circuit.HalfOpenProbes < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"circuit.half_open_probes 必须 ≥1，实际 %d", c.Circuit.HalfOpenProbes))
	}
	return errs
}

func (c *Config) validateLimits() []error {
	var errs []error
	if c.Limits.MaxTotalChars < 1000 || c.Limits.MaxTotalChars > 500000 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.max_total_chars 必须 1000~500000，实际 %d", c.Limits.MaxTotalChars))
	}
	if c.Limits.DefaultCount < 1 || c.Limits.DefaultCount > 50 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.default_count 必须 1~50，实际 %d", c.Limits.DefaultCount))
	}
	if c.Limits.SummaryChars < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.summary_chars 必须 ≥0，实际 %d", c.Limits.SummaryChars))
	}
	if c.Limits.MinContentChars < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.min_content_chars 必须 ≥1，实际 %d", c.Limits.MinContentChars))
	}
	if c.Limits.InlineMaxChars < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.inline_max_chars 必须 ≥0，实际 %d", c.Limits.InlineMaxChars))
	}
	if c.Limits.FixedChunkChars < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.fixed_chunk_chars 必须 ≥0，实际 %d", c.Limits.FixedChunkChars))
	}
	if c.Limits.OutlineMaxSections < 20 || c.Limits.OutlineMaxSections > 2000 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.outline_max_sections 必须 20~2000，实际 %d", c.Limits.OutlineMaxSections))
	}
	if c.Limits.Budget.MaxCallsPerWindow < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.budget.max_calls_per_window 必须 ≥0，实际 %d", c.Limits.Budget.MaxCallsPerWindow))
	}
	if c.Limits.Budget.WindowS < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.budget.window_s 必须 ≥0，实际 %d", c.Limits.Budget.WindowS))
	}
	if c.Limits.Budget.MaxCallsPerDay < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"limits.budget.max_calls_per_day 必须 ≥0，实际 %d", c.Limits.Budget.MaxCallsPerDay))
	}
	return errs
}

func (c *Config) validateCache() []error {
	var errs []error
	if c.Cache.TtlFetchS < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "cache.ttl_fetch_s 必须 ≥0，实际 %d", c.Cache.TtlFetchS))
	}
	if c.Cache.TtlSearchS < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "cache.ttl_search_s 必须 ≥0，实际 %d", c.Cache.TtlSearchS))
	}
	if c.Cache.MaxEntries < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "cache.max_entries 必须 ≥1，实际 %d", c.Cache.MaxEntries))
	}
	if c.Cache.MaxBytesMb < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "cache.max_bytes_mb 必须 ≥1，实际 %d", c.Cache.MaxBytesMb))
	}
	if c.Cache.MaxEntryKb < 1 || c.Cache.MaxEntryKb > 102400 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"cache.max_entry_kb 必须 1~102400，实际 %d", c.Cache.MaxEntryKb))
	}
	if c.Cache.WaitMs < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "cache.wait_ms 必须 ≥0，实际 %d", c.Cache.WaitMs))
	}
	if c.Cache.PruneIntervalS < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"cache.prune_interval_s 必须 ≥0，实际 %d", c.Cache.PruneIntervalS))
	}
	return errs
}

func (c *Config) validateDedupe() []error {
	if c.Dedupe.MaxPerDomain < 0 {
		return []error{configErr(domain.CodeLocConfigInvalid,
			"dedupe.max_per_domain 必须 ≥0，实际 %d", c.Dedupe.MaxPerDomain)}
	}
	return nil
}

func (c *Config) validateSearch() []error {
	var errs []error
	if c.Search.MinRerank != nil && (*c.Search.MinRerank < 0 || *c.Search.MinRerank > 3) {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"search.min_rerank 必须为 null 或 0~3，实际 %v", *c.Search.MinRerank))
	}
	if c.Search.MinAuthority != nil && (*c.Search.MinAuthority < 0 || *c.Search.MinAuthority > 4) {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"search.min_authority 必须为 null 或 0~4，实际 %d", *c.Search.MinAuthority))
	}
	if c.Search.MinRerankRatio != nil && (*c.Search.MinRerankRatio <= 0 || *c.Search.MinRerankRatio > 1) {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"search.min_rerank_ratio 必须为 null 或 0 < r ≤ 1，实际 %v", *c.Search.MinRerankRatio))
	}
	return errs
}

func (c *Config) validateSecurity() []error {
	var errs []error
	for i, cidr := range c.Security.BlockedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid,
				"security.blocked_cidrs[%d] 无法解析为 CIDR：%q", i, cidr))
		}
	}
	return errs
}

func (c *Config) validateLog() []error {
	var errs []error
	if c.Log.MaxSizeMb < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "log.max_size_mb 必须 ≥1，实际 %d", c.Log.MaxSizeMb))
	}
	if c.Log.DailyMaxMb < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "log.daily_max_mb 必须 ≥1，实际 %d", c.Log.DailyMaxMb))
	}
	if c.Log.MaxAgeDays < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "log.max_age_days 必须 ≥1，实际 %d", c.Log.MaxAgeDays))
	}
	if c.Log.MaxBackups < 1 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "log.max_backups 必须 ≥1，实际 %d", c.Log.MaxBackups))
	}
	if c.Log.SlowMs < 0 {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "log.slow_ms 必须 ≥0，实际 %d", c.Log.SlowMs))
	}
	if c.Log.Timezone == "" {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid, "log.timezone 不能为空"))
	} else if _, err := time.LoadLocation(c.Log.Timezone); err != nil {
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"log.timezone 无法加载：%q（%v）", c.Log.Timezone, err))
	}
	return errs
}

func (c *Config) collectWarnings() []string {
	warnings := make([]string, 0, 3)
	warnings = append(warnings, c.timeoutWarnings("timeout.search", c.Timeout.Search)...)
	warnings = append(warnings, c.timeoutWarnings("timeout.fetch", c.Timeout.Fetch)...)
	if c.Cache.WaitMs*4 < c.Timeout.Fetch.PerAttemptMs {
		warnings = append(warnings, fmt.Sprintf(
			"cache.wait_ms=%d 远小于 timeout.fetch.per_attempt_ms=%d：跨进程等待者很可能在 cache.wait_ms 后拿到 3075；若频繁出现，调大 cache.wait_ms 或确认这是有意取舍",
			c.Cache.WaitMs, c.Timeout.Fetch.PerAttemptMs))
	}
	return warnings
}

func (c *Config) timeoutWarnings(name string, t TimeoutToolConfig) []string {
	var warnings []string
	layer := c.Timeout.DialMs + c.Timeout.TLSHandshakeMs + c.Timeout.ResponseHeaderMs
	if t.PerAttemptMs < layer {
		warnings = append(warnings, fmt.Sprintf(
			"%s.per_attempt_ms=%d < dial_ms+tls_handshake_ms+response_header_ms=%d：分层超时会先于单次尝试生效，per_attempt_ms 形同虚设",
			name, t.PerAttemptMs, layer))
	}
	if t.TotalTimeoutMs > 115000 {
		warnings = append(warnings, fmt.Sprintf(
			"%s.total_timeout_ms=%d > 115000：超过常见 MCP 客户端 120s 工具超时，客户端可能先掐断",
			name, t.TotalTimeoutMs))
	}
	return warnings
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func enumErr(field, value string, allowed ...string) error {
	return configErr(domain.CodeLocConfigInvalid, "%s 必须是 %s 之一，实际 %q",
		field, strings.Join(allowed, " / "), value)
}

func sumInts(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}
