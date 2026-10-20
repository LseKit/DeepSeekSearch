// 文件名: config_test.go
// 用途: 验证配置结构体的默认值与脱敏语义。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"reflect"
	"testing"
)

func TestDefault(t *testing.T) {
	t.Parallel()

	cfg := Default()
	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "传输方式默认 streamable-http", got: cfg.Server.Transport, want: "streamable-http"},
		{name: "监听地址默认回环", got: cfg.Server.Http.Host, want: "127.0.0.1"},
		{name: "监听端口默认 8765", got: cfg.Server.Http.Port, want: 8765},
		{name: "上游地址默认官方", got: cfg.Upstream.BaseURL, want: "https://api.deepseek.com"},
		{name: "上游超时默认 60000", got: cfg.Upstream.TimeoutMs, want: 60000},
		{name: "search 的 rps 默认 30", got: cfg.RateLimit.Search.Rps, want: 30.0},
		{name: "fetch 的 rps 默认 20", got: cfg.RateLimit.Fetch.Rps, want: 20.0},
		{name: "在飞总额度默认 8", got: cfg.RateLimit.MaxInflight, want: 8},
		{name: "search 最大尝试次数默认 4", got: cfg.Timeout.Search.MaxAttempts, want: 4},
		{name: "search 总超时默认 67000", got: cfg.Timeout.Search.TotalTimeoutMs, want: 67000},
		{name: "fetch 单次尝试默认 45000", got: cfg.Timeout.Fetch.PerAttemptMs, want: 45000},
		{name: "fetch 缓存 TTL 默认 21600", got: cfg.Cache.TtlFetchS, want: 21600},
		{name: "search 缓存 TTL 默认 180", got: cfg.Cache.TtlSearchS, want: 180},
		{name: "返回字符预算默认 32000", got: cfg.Limits.MaxTotalChars, want: 32000},
		{name: "日志级别默认 info", got: cfg.Log.Level, want: "info"},
		{name: "结果合成默认关闭", got: cfg.Synth.Enabled, want: false},
		{name: "相关性绝对阈值默认 nil", got: cfg.Search.MinRerank, want: (*float64)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Fatalf("默认值 = %#v, want %#v", tt.got, tt.want)
			}
		})
	}
}

func TestRedacted(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.ApiKey = "sk-secret"
	cfg.HttpToken = "token-secret"

	red := cfg.Redacted()
	if red.ApiKey != "***" {
		t.Fatalf("Redacted().ApiKey = %q, want %q", red.ApiKey, "***")
	}
	if red.HttpToken != "***" {
		t.Fatalf("Redacted().HttpToken = %q, want %q", red.HttpToken, "***")
	}
	if cfg.ApiKey != "sk-secret" || cfg.HttpToken != "token-secret" {
		t.Fatalf("Redacted() 修改了原 Config: ApiKey=%q HttpToken=%q", cfg.ApiKey, cfg.HttpToken)
	}

	empty := Default().Redacted()
	if empty.ApiKey != "***" || empty.HttpToken != "***" {
		t.Fatalf("空凭据也必须替换为 ***，实际 ApiKey=%q HttpToken=%q", empty.ApiKey, empty.HttpToken)
	}
}
