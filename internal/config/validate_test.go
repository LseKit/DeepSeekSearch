// 文件名: validate_test.go
// 用途: 验证 Validate 的枚举、数值区间、跨字段约束、错误聚合与 warning 通道。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"strings"
	"testing"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Config)
		wantErr  []string
		wantCode int
	}{
		{name: "默认配置", mutate: func(*Config) {}},
		{
			name:    "旧传输名",
			mutate:  func(c *Config) { c.Server.Transport = "http" },
			wantErr: []string{"server.transport", "streamable-http"},
		},
		{
			name:    "search rps 为 0",
			mutate:  func(c *Config) { c.RateLimit.Search.Rps = 0 },
			wantErr: []string{"ratelimit.search.rps"},
		},
		{
			name:    "端口越界",
			mutate:  func(c *Config) { c.Server.Http.Port = 70000 },
			wantErr: []string{"server.http.port"},
		},
		{
			name: "鉴权 token 为空",
			mutate: func(c *Config) {
				c.Server.Http.Host = "0.0.0.0"
				c.Server.Http.AuthEnabled = true
				c.HttpToken = ""
			},
			wantErr:  []string{"http_token", "16"},
			wantCode: domain.CodeLocHTTPTokenMissing,
		},
		{
			name: "鉴权 token 过短",
			mutate: func(c *Config) {
				c.Server.Http.Host = "0.0.0.0"
				c.Server.Http.AuthEnabled = true
				c.HttpToken = "12345678"
			},
			wantErr:  []string{"http_token", "8 字节"},
			wantCode: domain.CodeLocHTTPTokenMissing,
		},
		{
			name: "一次返回多个错误",
			mutate: func(c *Config) {
				c.Server.Http.Port = 70000
				c.RateLimit.Search.Rps = 0
			},
			wantErr: []string{"server.http.port", "ratelimit.search.rps"},
		},
		{
			name:     "缺少 API Key",
			mutate:   func(c *Config) { c.ApiKey = "" },
			wantErr:  []string{"DEEPSEEK_API_KEY"},
			wantCode: domain.CodeLocNoAPIKey,
		},
		{
			name:    "search_share 取上界",
			mutate:  func(c *Config) { c.RateLimit.SearchShare = 1 },
			wantErr: []string{"ratelimit.search_share"},
		},
		{
			name:    "max_inflight 低于下限",
			mutate:  func(c *Config) { c.RateLimit.MaxInflight = 2 },
			wantErr: []string{"ratelimit.max_inflight"},
		},
		{
			name:    "search 总超时不足",
			mutate:  func(c *Config) { c.Timeout.Search.TotalTimeoutMs = 60000 },
			wantErr: []string{"timeout.search.total_timeout_ms", "67000"},
		},
		{
			name:    "fetch 单次尝试低于网关超时",
			mutate:  func(c *Config) { c.Timeout.Fetch.PerAttemptMs = 30000 },
			wantErr: []string{"timeout.fetch.per_attempt_ms", "31000"},
		},
		{
			name:    "排队时长不小于总超时",
			mutate:  func(c *Config) { c.RateLimit.MaxWaitMs = 70000 },
			wantErr: []string{"ratelimit.max_wait_ms"},
		},
		{
			name: "退避表长度不足",
			mutate: func(c *Config) {
				c.Retry.FixedMs = []int{1000}
				c.Retry.SpreadMs = []int{1000}
			},
			wantErr: []string{"retry.fixed_ms", "max_attempts-1"},
		},
		{
			name: "退避表长度不等",
			mutate: func(c *Config) {
				c.Retry.FixedMs = []int{1, 2, 3}
				c.Retry.SpreadMs = []int{1, 2}
			},
			wantErr: []string{"retry.fixed_ms", "长度必须相等"},
		},
		{
			name:    "base_url 非 https",
			mutate:  func(c *Config) { c.Upstream.BaseURL = "http://api.deepseek.com" },
			wantErr: []string{"upstream.base_url"},
		},
		{
			name:    "path 不以斜杠开头",
			mutate:  func(c *Config) { c.Server.Http.Path = "mcp" },
			wantErr: []string{"server.http.path"},
		},
		{
			name:    "非法日志级别",
			mutate:  func(c *Config) { c.Log.Level = "trace" },
			wantErr: []string{"log.level"},
		},
		{
			name:    "非法缓存后端",
			mutate:  func(c *Config) { c.Cache.Backend = "memory" },
			wantErr: []string{"cache.backend"},
		},
		{
			name:    "非法退避模式",
			mutate:  func(c *Config) { c.Retry.JitterMode = "random" },
			wantErr: []string{"retry.jitter_mode"},
		},
		{
			name:    "非法详细程度",
			mutate:  func(c *Config) { c.Limits.DefaultDetail = "full" },
			wantErr: []string{"limits.default_detail"},
		},
		{
			name:    "default_count 越界",
			mutate:  func(c *Config) { c.Limits.DefaultCount = 0 },
			wantErr: []string{"limits.default_count"},
		},
		{
			name:    "max_total_chars 越界",
			mutate:  func(c *Config) { c.Limits.MaxTotalChars = 999 },
			wantErr: []string{"limits.max_total_chars"},
		},
		{
			name:    "时区非法",
			mutate:  func(c *Config) { c.Log.Timezone = "Mars/Olympus" },
			wantErr: []string{"log.timezone"},
		},
		{
			name: "配额之和超总额度",
			mutate: func(c *Config) {
				c.RateLimit.MaxInflight = 8
				c.RateLimit.SearchShare = 0.9
			},
			wantErr: []string{"ratelimit.max_inflight", "配额之和"},
		},
		{
			name:    "熔断阈值低于下限",
			mutate:  func(c *Config) { c.Circuit.FailThreshold = 0 },
			wantErr: []string{"circuit.fail_threshold"},
		},
		{
			name:    "cache max_entry_kb 越界",
			mutate:  func(c *Config) { c.Cache.MaxEntryKb = 0 },
			wantErr: []string{"cache.max_entry_kb"},
		},
		{
			name:    "dedupe 负数",
			mutate:  func(c *Config) { c.Dedupe.MaxPerDomain = -1 },
			wantErr: []string{"dedupe.max_per_domain"},
		},
		{
			name:    "retry budget ratio 为 0",
			mutate:  func(c *Config) { c.Retry.Budget.Ratio = 0 },
			wantErr: []string{"retry.budget.ratio"},
		},
		{
			name: "token 恰好 16 字节通过",
			mutate: func(c *Config) {
				c.Server.Http.Host = "0.0.0.0"
				c.Server.Http.AuthEnabled = true
				c.HttpToken = "1234567890123456"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := baseValidConfig()
			tt.mutate(&cfg)
			_, err := cfg.Validate()

			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Validate() 返回错误: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() 期望失败，实际通过")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, 缺少 %q", err, want)
				}
			}
			if tt.wantCode != 0 {
				if got := domain.CodeOf(err); got != tt.wantCode {
					t.Fatalf("错误码 = %d, want %d（error: %v）", got, tt.wantCode, err)
				}
			}
		})
	}
}

func TestValidateWarnings(t *testing.T) {
	t.Parallel()

	cfg := baseValidConfig()
	warnings, err := cfg.Validate()
	if err != nil {
		t.Fatalf("默认配置 Validate() 返回错误: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "timeout.search.per_attempt_ms") {
		t.Fatalf("默认配置应提示分层超时压过 per_attempt，实际 warnings=%v", warnings)
	}
	if !strings.Contains(joined, "cache.wait_ms") {
		t.Fatalf("默认配置应提示 cache.wait_ms 远小于 fetch 时长，实际 warnings=%v", warnings)
	}

	// 暴露 + 不鉴权由「拒启」改为「warning」——必须既不返回 error，
	// 又留下 3092 留痕，否则裸奔在日志里完全不可见。
	exposed := baseValidConfig()
	exposed.Server.Http.Host = "0.0.0.0"
	exposed.Server.Http.AuthEnabled = false
	warnings, err = exposed.Validate()
	if err != nil {
		t.Fatalf("暴露且不鉴权不应拒绝启动，实际 error=%v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "3092") {
		t.Fatalf("暴露且不鉴权应产生 3092 warning，实际 warnings=%v", warnings)
	}

	slowClient := baseValidConfig()
	slowClient.Timeout.Fetch.TotalTimeoutMs = 120000
	warnings, err = slowClient.Validate()
	if err != nil {
		t.Fatalf("120000 应只是 warning，实际 error=%v", err)
	}
	joined = strings.Join(warnings, "\n")
	if !strings.Contains(joined, "timeout.fetch.total_timeout_ms") || !strings.Contains(joined, "115000") {
		t.Fatalf("total_timeout_ms=120000 应产生 warning，实际 warnings=%v", warnings)
	}
}

func TestLoadRejectsMissingAPIKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")

	_, err := LoadFromEnvOnly()
	if err == nil {
		t.Fatal("缺少 API Key 时 LoadFromEnvOnly 应失败")
	}
	if got := domain.CodeOf(err); got != domain.CodeLocNoAPIKey {
		t.Fatalf("错误码 = %d, want %d（error: %v）", got, domain.CodeLocNoAPIKey, err)
	}
}

func baseValidConfig() Config {
	cfg := Default()
	cfg.ApiKey = "sk-valid-test-key"
	return cfg
}
