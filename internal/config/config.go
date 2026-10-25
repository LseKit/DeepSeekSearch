// 文件名: config.go
// 用途: 定义配置结构体、内置默认值与脱敏/来源查询，字段与 config.example.toml 逐键对应。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"maps"
	"reflect"
	"strings"
)

// SourceKind 标识配置来源层级。
type SourceKind string

const (
	// SourceDefault 表示该值来自内置默认值。
	SourceDefault SourceKind = "default"
	// SourceFile 表示该值来自 config.toml。
	SourceFile SourceKind = "file"
	// SourceDotEnv 表示该值来自 Config/.env。
	SourceDotEnv SourceKind = "dotenv"
	// SourceEnv 表示该值来自真实环境变量。
	SourceEnv SourceKind = "env"
	// SourceUnknown 表示 key 不是已知配置项（多半是拼错了）。
	SourceUnknown SourceKind = "unknown"
)

// Config 是启动后不再变更的配置快照。
// 全部字段在 Load 中填充、在 Validate 中校验，运行期只读。
// 顶层 section 字段故意不带 toml tag：CheckDocDrift 会把 tag 当作配置键，
// 给段名加 tag 会让「代码有而配置模板无」。子段按字段名大小写不敏感匹配。
type Config struct {
	App       appConfig             `json:"app"`
	Server    serverConfig          `json:"server"`
	Upstream  upstreamConfig        `json:"upstream"`
	RateLimit rateLimitConfig       `json:"ratelimit"`
	Timeout   timeoutConfig         `json:"timeout"`
	Retry     retryConfig           `json:"retry"`
	Circuit   circuitConfig         `json:"circuit"`
	Limits    limitsConfig          `json:"limits"`
	Cache     cacheConfig           `json:"cache"`
	Dedupe    dedupeConfig          `json:"dedupe"`
	Security  securityConfig        `json:"security"`
	Log       logConfig             `json:"log"`
	ApiKey    string                `toml:"-" json:"-"` //nolint:revive // 对外字段名固定为 ApiKey/HttpToken
	HttpToken string                `toml:"-" json:"-"` //nolint:revive // 对外字段名固定为 ApiKey/HttpToken
	sources   map[string]SourceKind `toml:"-" json:"-"`
}

// appConfig 管程序自身标识，只影响日志与 MCP serverInfo 的展示。
type appConfig struct {
	Name    string `toml:"name" json:"name"`
	Version string `toml:"version" json:"version"`
}

// serverConfig 管「怎么和客户端说话」。
type serverConfig struct {
	Transport string           `toml:"transport" json:"transport"`
	Http      serverHttpConfig `json:"http"` //nolint:revive // 对外字段名固定为 Http
}

// serverHttpConfig 只在 Transport == "streamable-http" 时生效。
// 默认值全部指向最安全的一端：只听回环、开 Origin 校验、开 Host 校验、不鉴权。
type serverHttpConfig struct { //nolint:revive // 对外类型名固定为 serverHttpConfig
	Host                        string   `toml:"host" json:"host"`
	Port                        int      `toml:"port" json:"port"`
	Path                        string   `toml:"path" json:"path"`
	JsonResponse                bool     `toml:"json_response" json:"json_response"` //nolint:revive // 契约字段名
	MaxRequestBodyBytes         int64    `toml:"max_request_body_bytes" json:"max_request_body_bytes"`
	MaxConcurrentRequests       int      `toml:"max_concurrent_requests" json:"max_concurrent_requests"`
	DisableLocalhostProtection  bool     `toml:"disable_localhost_protection" json:"disable_localhost_protection"`
	EnableCrossOriginProtection bool     `toml:"enable_cross_origin_protection" json:"enable_cross_origin_protection"`
	AllowedOrigins              []string `toml:"allowed_origins" json:"allowed_origins"`
	AllowNonLocalHost           bool     `toml:"allow_non_local_host" json:"allow_non_local_host"`
	HealthzPath                 string   `toml:"healthz_path" json:"healthz_path"`
	AuthEnabled                 bool     `toml:"auth_enabled" json:"auth_enabled"`
	AuthTokenEnv                string   `toml:"auth_token_env" json:"auth_token_env"`
}

// upstreamConfig 管上游 DeepSeek HTTP API 的连接参数。
type upstreamConfig struct {
	BaseURL          string `toml:"base_url" json:"base_url"`
	SelfTestOnStart  bool   `toml:"self_test_on_start" json:"self_test_on_start"`
	UserAgent        string `toml:"user_agent" json:"user_agent"`
	MaxResponseBytes int64  `toml:"max_response_bytes" json:"max_response_bytes"`
}

// rateLimitConfig 管令牌桶与在飞并发额度。
type rateLimitConfig struct {
	MaxInflight int                   `toml:"max_inflight" json:"max_inflight"`
	SearchShare float64               `toml:"search_share" json:"search_share"`
	MaxWaitMs   int                   `toml:"max_wait_ms" json:"max_wait_ms"`
	Search      rateLimitBucketConfig `json:"search"`
	Fetch       rateLimitBucketConfig `json:"fetch"`
}

// rateLimitBucketConfig 是单个工具的令牌桶参数；search 与 fetch 各自独立。
type rateLimitBucketConfig struct {
	Rps   float64 `toml:"rps" json:"rps"`
	Burst int     `toml:"burst" json:"burst"`
}

// timeoutConfig 管分层超时：底层连接参数与两个工具各自的尝试预算。
type timeoutConfig struct {
	DialMs           int               `toml:"dial_ms" json:"dial_ms"`
	TLSHandshakeMs   int               `toml:"tls_handshake_ms" json:"tls_handshake_ms"`
	ResponseHeaderMs int               `toml:"response_header_ms" json:"response_header_ms"`
	Search           timeoutToolConfig `json:"search"`
	Fetch            timeoutToolConfig `json:"fetch"`
}

// timeoutToolConfig 是单个工具的单次尝试与总时长预算。
type timeoutToolConfig struct {
	PerAttemptMs   int `toml:"per_attempt_ms" json:"per_attempt_ms"`
	MaxAttempts    int `toml:"max_attempts" json:"max_attempts"`
	TotalTimeoutMs int `toml:"total_timeout_ms" json:"total_timeout_ms"`
}

// retryConfig 管重试开关、退避表与重试预算。
type retryConfig struct {
	Enabled    bool              `toml:"enabled" json:"enabled"`
	JitterMode string            `toml:"jitter_mode" json:"jitter_mode"`
	FixedMs    []int             `toml:"fixed_ms" json:"fixed_ms"`
	SpreadMs   []int             `toml:"spread_ms" json:"spread_ms"`
	Budget     retryBudgetConfig `json:"budget"`
}

// retryBudgetConfig 用于封顶重试放大比例，防止上游变慢时形成重试风暴。
type retryBudgetConfig struct {
	Enabled bool    `toml:"enabled" json:"enabled"`
	Ratio   float64 `toml:"ratio" json:"ratio"`
	WindowS int     `toml:"window_s" json:"window_s"`
}

// circuitConfig 管熔断器的失败阈值、打开时长与半开探测数。
type circuitConfig struct {
	Enabled        bool `toml:"enabled" json:"enabled"`
	FailThreshold  int  `toml:"fail_threshold" json:"fail_threshold"`
	OpenMs         int  `toml:"open_ms" json:"open_ms"`
	HalfOpenProbes int  `toml:"half_open_probes" json:"half_open_probes"`
}

// limitsConfig 管返回内容的字符预算与分节粒度。
type limitsConfig struct {
	MaxTotalChars      int                `toml:"max_total_chars" json:"max_total_chars"`
	DefaultCount       int                `toml:"default_count" json:"default_count"`
	DefaultDetail      string             `toml:"default_detail" json:"default_detail"`
	SummaryChars       int                `toml:"summary_chars" json:"summary_chars"`
	MinContentChars    int                `toml:"min_content_chars" json:"min_content_chars"`
	InlineMaxChars     int                `toml:"inline_max_chars" json:"inline_max_chars"`
	FixedChunkChars    int                `toml:"fixed_chunk_chars" json:"fixed_chunk_chars"`
	OutlineMaxSections int                `toml:"outline_max_sections" json:"outline_max_sections"`
	Budget             limitsBudgetConfig `json:"budget"`
}

// limitsBudgetConfig 管时间窗与自然日的调用次数硬顶，默认全关。
type limitsBudgetConfig struct {
	Enabled           bool `toml:"enabled" json:"enabled"`
	MaxCallsPerWindow int  `toml:"max_calls_per_window" json:"max_calls_per_window"`
	WindowS           int  `toml:"window_s" json:"window_s"`
	MaxCallsPerDay    int  `toml:"max_calls_per_day" json:"max_calls_per_day"`
}

// cacheConfig 是省钱的核心：上游原文的 SQLite 快照缓存。
type cacheConfig struct {
	Enabled        bool   `toml:"enabled" json:"enabled"`
	Backend        string `toml:"backend" json:"backend"`
	DbPath         string `toml:"db_path" json:"db_path"`
	TtlFetchS      int    `toml:"ttl_fetch_s" json:"ttl_fetch_s"`   //nolint:revive // 契约字段名
	TtlSearchS     int    `toml:"ttl_search_s" json:"ttl_search_s"` //nolint:revive // 契约字段名
	MaxEntries     int    `toml:"max_entries" json:"max_entries"`
	MaxBytesMb     int    `toml:"max_bytes_mb" json:"max_bytes_mb"`
	MaxEntryKb     int    `toml:"max_entry_kb" json:"max_entry_kb"`
	WaitMs         int    `toml:"wait_ms" json:"wait_ms"`
	PruneIntervalS int    `toml:"prune_interval_s" json:"prune_interval_s"`
	DegradedOk     bool   `toml:"degraded_ok" json:"degraded_ok"`
}

// dedupeConfig 管无损去重：URL 归一化、单域名配额与跟踪参数表。
// 转载合并恒定开启、不可配 —— 见 domain.Dedupe 的第二层。
type dedupeConfig struct {
	Enabled        bool     `toml:"enabled" json:"enabled"`
	MaxPerDomain   int      `toml:"max_per_domain" json:"max_per_domain"`
	TrackingParams []string `toml:"tracking_params" json:"tracking_params"`
}

// securityConfig 是可追加的主机名黑名单。IP 网段基线（私网/回环/链路本地/CGNAT 等）
// 是代码硬编码的固定纵深防御（见 internal/toolvalidate/ssrf.go 的 blockedNets），不可配。
type securityConfig struct {
	BlockedHosts []string `toml:"blocked_hosts" json:"blocked_hosts"`
}

// logConfig 管结构化日志的级别、轮转与脱敏。
type logConfig struct {
	Level           string   `toml:"level" json:"level"`
	Format          string   `toml:"format" json:"format"`
	Dir             string   `toml:"dir" json:"dir"`
	FilePrefix      string   `toml:"file_prefix" json:"file_prefix"`
	Timezone        string   `toml:"timezone" json:"timezone"`
	TsFormat        string   `toml:"ts_format" json:"ts_format"`
	MaxSizeMb       int      `toml:"max_size_mb" json:"max_size_mb"`
	DailyMaxMb      int      `toml:"daily_max_mb" json:"daily_max_mb"`
	MaxAgeDays      int      `toml:"max_age_days" json:"max_age_days"`
	MaxBackups      int      `toml:"max_backups" json:"max_backups"`
	Compress        bool     `toml:"compress" json:"compress"`
	AlsoStderr      bool     `toml:"also_stderr" json:"also_stderr"`
	SlowMs          int      `toml:"slow_ms" json:"slow_ms"`
	MaxBodyLogKb    int      `toml:"max_body_log_kb" json:"max_body_log_kb"`
	RedactQueryKeys []string `toml:"redact_query_keys" json:"redact_query_keys"`
}

// Default 返回内置默认配置，与 config.example.toml 的默认值逐格一致。
func Default() Config {
	cfg := Config{
		App:       defaultApp(),
		Server:    defaultServer(),
		Upstream:  defaultUpstream(),
		RateLimit: defaultRateLimit(),
		Timeout:   defaultTimeout(),
		Retry:     defaultRetry(),
		Circuit:   defaultCircuit(),
		Limits:    defaultLimits(),
		Cache:     defaultCache(),
		Dedupe:    defaultDedupe(),
		Security:  defaultSecurity(),
		Log:       defaultLog(),
	}
	cfg.sources = defaultSources(&cfg)
	return cfg
}

// Redacted 返回可安全打印与外发的副本：ApiKey/HttpToken 一律替换为 ***。
// 即使字段为空也替换，避免泄漏「是否配置过凭据」。
// 值接收者由接口约定决定，不能改成指针。
func (c Config) Redacted() Config { //nolint:gocritic // 值接收者由契约固定
	c.ApiKey = "***"
	c.HttpToken = "***"
	c.sources = maps.Clone(c.sources)
	return c
}

// Source 返回某个配置项最终取自哪一层；未知 key 返回 SourceUnknown。
// 值接收者由接口约定决定，不能改成指针。
func (c Config) Source(key string) SourceKind { //nolint:gocritic // 值接收者由契约固定
	if kind, ok := c.sources[key]; ok {
		return kind
	}
	return SourceUnknown
}

func defaultApp() appConfig {
	return appConfig{Name: "DeepSeekSearch", Version: "v0.1.0"}
}

func defaultServer() serverConfig {
	return serverConfig{
		Transport: "streamable-http",
		Http: serverHttpConfig{
			Host:                        "127.0.0.1",
			Port:                        8765,
			Path:                        "/mcp",
			JsonResponse:                true,
			MaxRequestBodyBytes:         1048576,
			MaxConcurrentRequests:       64,
			DisableLocalhostProtection:  false,
			EnableCrossOriginProtection: true,
			AllowedOrigins:              []string{},
			AllowNonLocalHost:           false,
			HealthzPath:                 "/healthz",
			AuthEnabled:                 false,
			AuthTokenEnv:                "DEEPSEEKSEARCH_HTTP_TOKEN",
		},
	}
}

func defaultUpstream() upstreamConfig {
	return upstreamConfig{
		BaseURL:          "https://api.deepseek.com",
		SelfTestOnStart:  false,
		UserAgent:        "",
		MaxResponseBytes: 8388608,
	}
}

func defaultRateLimit() rateLimitConfig {
	return rateLimitConfig{
		MaxInflight: 8,
		SearchShare: 0.5,
		MaxWaitMs:   30000,
		Search:      rateLimitBucketConfig{Rps: 30.0, Burst: 60},
		Fetch:       rateLimitBucketConfig{Rps: 20.0, Burst: 40},
	}
}

func defaultTimeout() timeoutConfig {
	return timeoutConfig{
		DialMs:           5000,
		TLSHandshakeMs:   5000,
		ResponseHeaderMs: 30000,
		Search:           timeoutToolConfig{PerAttemptMs: 12000, MaxAttempts: 4, TotalTimeoutMs: 67000},
		Fetch:            timeoutToolConfig{PerAttemptMs: 45000, MaxAttempts: 2, TotalTimeoutMs: 110000},
	}
}

func defaultRetry() retryConfig {
	return retryConfig{
		Enabled:    true,
		JitterMode: "fixed_plus_random",
		FixedMs:    []int{1000, 3000, 8000},
		SpreadMs:   []int{1000, 2000, 4000},
		Budget:     retryBudgetConfig{Enabled: true, Ratio: 0.10, WindowS: 60},
	}
}

func defaultCircuit() circuitConfig {
	return circuitConfig{Enabled: true, FailThreshold: 5, OpenMs: 15000, HalfOpenProbes: 2}
}

func defaultLimits() limitsConfig {
	return limitsConfig{
		MaxTotalChars:      32000,
		DefaultCount:       10,
		DefaultDetail:      "summary",
		SummaryChars:       1200,
		MinContentChars:    120,
		InlineMaxChars:     8000,
		FixedChunkChars:    8000,
		OutlineMaxSections: 200,
		Budget:             limitsBudgetConfig{Enabled: false, MaxCallsPerWindow: 0, WindowS: 3600, MaxCallsPerDay: 0},
	}
}

func defaultCache() cacheConfig {
	return cacheConfig{
		Enabled:        true,
		Backend:        "sqlite",
		DbPath:         "Data/Cache.db",
		TtlFetchS:      21600,
		TtlSearchS:     180,
		MaxEntries:     500,
		MaxBytesMb:     256,
		MaxEntryKb:     2048,
		WaitMs:         8000,
		PruneIntervalS: 300,
		DegradedOk:     true,
	}
}

func defaultDedupe() dedupeConfig {
	return dedupeConfig{
		Enabled:      true,
		MaxPerDomain: 3,
		TrackingParams: []string{
			"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "utm_id",
			"fbclid", "gclid", "gbraid", "wbraid", "msclkid", "yclid", "dclid", "twclid", "igshid",
			"mc_cid", "mc_eid", "_hsenc", "_hsmi", "spm", "from", "source", "ref", "ref_src",
			"share_token", "si",
		},
	}
}

func defaultSecurity() securityConfig {
	return securityConfig{
		BlockedHosts: []string{"metadata.google.internal", "metadata", "instance-data"},
	}
}

func defaultLog() logConfig {
	return logConfig{
		Level:           "info",
		Format:          "json",
		Dir:             "Logs",
		FilePrefix:      "DeepSeekSearch",
		Timezone:        "Asia/Shanghai",
		TsFormat:        "2006-01-02 15:04:05.000 -07:00",
		MaxSizeMb:       50,
		DailyMaxMb:      500,
		MaxAgeDays:      7,
		MaxBackups:      50,
		Compress:        true,
		AlsoStderr:      true,
		SlowMs:          3000,
		RedactQueryKeys: []string{"token", "key", "secret", "password", "code", "sig", "auth"},
	}
}

// defaultSources 预置全部已知 toml 路径为 default，
// 让 Source 能区分「未覆盖的已知键」与「拼错的未知键」。
func defaultSources(cfg *Config) map[string]SourceKind {
	out := make(map[string]SourceKind, 128)
	collectSources(reflect.ValueOf(cfg).Elem(), "", out)
	return out
}

func collectSources(v reflect.Value, prefix string, out map[string]SourceKind) {
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("toml")
		if tag == "-" {
			continue
		}
		name := tag
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		value := v.Field(i)
		if value.Kind() == reflect.Struct {
			collectSources(value, path, out)
			continue
		}
		out[path] = SourceDefault
	}
}
