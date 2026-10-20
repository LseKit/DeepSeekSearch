// 文件名: fetch_validate_test.go
// 用途: 验证 ds_fetch 的 schema、SSRF 黑名单、参数校验与快照 ID。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

func fetchConfig() *config.Config {
	cfg := config.Default()
	return &cfg
}

func validateFetchURLOnly(t *testing.T, raw string) error {
	t.Helper()
	_, err := validateFetch(&fetchInput{URL: strPtr(raw)}, fetchConfig())
	return err
}

func TestSSRF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		wantCode int
	}{
		{name: "localhost", url: "http://localhost/x", wantCode: domain.CodeValSSRFBlocked},
		{name: "loopback", url: "http://127.0.0.1/x", wantCode: domain.CodeValSSRFBlocked},
		{name: "云元数据", url: "http://169.254.169.254/latest/meta-data/", wantCode: domain.CodeValSSRFBlocked},
		{name: "十进制IP混淆", url: "http://2130706433/", wantCode: domain.CodeValSSRFBlocked},
		{name: "十六进制IP", url: "http://0x7f000001/", wantCode: domain.CodeValSSRFBlocked},
		{name: "八进制IP", url: "http://017700000001/", wantCode: domain.CodeValSSRFBlocked},
		{name: "省略式IP", url: "http://127.1/", wantCode: domain.CodeValSSRFBlocked},
		{name: "混合进制", url: "http://0x7f.0.0.01/", wantCode: domain.CodeValSSRFBlocked},
		{name: "八进制label", url: "http://0177.0.0.1/", wantCode: domain.CodeValSSRFBlocked},
		{name: "IPv4映射IPv6", url: "http://[::ffff:127.0.0.1]/", wantCode: domain.CodeValSSRFBlocked},
		{name: "IPv4映射IPv6十六进制", url: "http://[::ffff:7f00:1]/", wantCode: domain.CodeValSSRFBlocked},
		{name: "IPv6回环", url: "http://[::1]/", wantCode: domain.CodeValSSRFBlocked},
		{name: "全零地址", url: "http://0.0.0.0/", wantCode: domain.CodeValSSRFBlocked},
		{name: "CGNAT", url: "http://100.64.0.1/", wantCode: domain.CodeValSSRFBlocked},
		{name: "私网10", url: "http://10.1.2.3/", wantCode: domain.CodeValSSRFBlocked},
		{name: "私网172", url: "http://172.16.0.1/", wantCode: domain.CodeValSSRFBlocked},
		{name: "私网192", url: "http://192.168.1.1/", wantCode: domain.CodeValSSRFBlocked},
		{name: "IPv6ULA", url: "http://[fc00::1]/", wantCode: domain.CodeValSSRFBlocked},
		{name: "IPv6链路本地", url: "http://[fe80::1]/", wantCode: domain.CodeValSSRFBlocked},
		{name: "internal后缀", url: "http://example.internal/", wantCode: domain.CodeValSSRFBlocked},
		{name: "local后缀", url: "http://example.local/", wantCode: domain.CodeValSSRFBlocked},
		{name: "localhost后缀", url: "http://foo.localhost/", wantCode: domain.CodeValSSRFBlocked},
		{name: "公网IP反例", url: "http://8.8.8.8/", wantCode: 0},
		{name: "公网域名反例", url: "https://example.com/path", wantCode: 0},
		{name: "带端口公网反例", url: "http://8.8.8.8:8080/", wantCode: 0},
		{name: "ftp拒绝", url: "ftp://example.com/", wantCode: domain.CodeValURLScheme},
		{name: "凭证拒绝", url: "https://u" + ":p@example.com/", wantCode: domain.CodeValURLCredentials},
		{name: "超长URL", url: "https://example.com/" + strings.Repeat("a", fetchURLMaxLen), wantCode: domain.CodeValURLTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateFetchURLOnly(t, tt.url)
			if tt.wantCode == 0 {
				if err != nil {
					t.Fatalf("URL %q 应放行: %v", tt.url, err)
				}
				return
			}
			if err == nil || domain.CodeOf(err) != tt.wantCode {
				t.Fatalf("URL %q code = %d, want %d (err=%v)", tt.url, domain.CodeOf(err), tt.wantCode, err)
			}
		})
	}
}

func TestFetchValidate(t *testing.T) {
	t.Parallel()

	t.Run("url与cache_id都缺失", func(t *testing.T) {
		t.Parallel()
		_, err := validateFetch(&fetchInput{}, fetchConfig())
		if domain.CodeOf(err) != domain.CodeValInvalidParam {
			t.Fatalf("code = %d, want 1000", domain.CodeOf(err))
		}
	})

	t.Run("url与cache_id同时给出", func(t *testing.T) {
		t.Parallel()
		_, err := validateFetch(&fetchInput{URL: strPtr("https://example.com"), CacheID: strPtr("snap_x")}, fetchConfig())
		if domain.CodeOf(err) != domain.CodeValInvalidParam {
			t.Fatalf("code = %d, want 1000", domain.CodeOf(err))
		}
	})

	t.Run("cache_id不存在返回3080", func(t *testing.T) {
		t.Parallel()
		cache, err := resilience.NewSqliteCache(resilience.Config{CacheDbPath: ":memory:"})
		if err != nil {
			t.Fatalf("NewSqliteCache() = %v", err)
		}
		t.Cleanup(func() {
			if closeErr := cache.Close(); closeErr != nil {
				t.Errorf("Close() = %v", closeErr)
			}
		})
		_, lookupErr := lookupSnapshot(context.Background(), cache, "snap_不存在")
		if domain.CodeOf(lookupErr) != domain.CodeLocSnapshotGone {
			t.Fatalf("code = %d, want 3080 (err=%v)", domain.CodeOf(lookupErr), lookupErr)
		}
	})

	t.Run("max_chars为0取配置默认", func(t *testing.T) {
		t.Parallel()
		cfg := fetchConfig()
		params, err := validateFetch(&fetchInput{URL: strPtr("https://example.com"), MaxChars: intPtr(0)}, cfg)
		if err != nil {
			t.Fatalf("validateFetch() = %v", err)
		}
		if params.MaxChars != cfg.Limits.MaxTotalChars || params.MaxChars != 32000 {
			t.Fatalf("MaxChars = %d, want 32000", params.MaxChars)
		}
	})

	t.Run("max_chars越界", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []int{-1, 200001} {
			_, err := validateFetch(&fetchInput{URL: strPtr("https://example.com"), MaxChars: intPtr(bad)}, fetchConfig())
			if domain.CodeOf(err) != domain.CodeValInvalidParam {
				t.Fatalf("max_chars=%d code = %d, want 1000", bad, domain.CodeOf(err))
			}
		}
	})

	t.Run("unescape默认与显式false", func(t *testing.T) {
		t.Parallel()
		params, err := validateFetch(&fetchInput{URL: strPtr("https://example.com")}, fetchConfig())
		if err != nil || !params.Unescape {
			t.Fatalf("默认 unescape = %v, err=%v", params.Unescape, err)
		}
		params, err = validateFetch(&fetchInput{URL: strPtr("https://example.com"), Unescape: boolPtr(false)}, fetchConfig())
		if err != nil || params.Unescape {
			t.Fatalf("显式 false unescape = %v, err=%v", params.Unescape, err)
		}
	})

	t.Run("section与sections互斥", func(t *testing.T) {
		t.Parallel()
		_, err := validateFetch(&fetchInput{
			CacheID: strPtr("snap_abcdef0123456789"),
			Section: strPtr("s1"), Sections: []string{"s2"},
		}, fetchConfig())
		if domain.CodeOf(err) != domain.CodeValInvalidParam {
			t.Fatalf("code = %d, want 1000", domain.CodeOf(err))
		}
	})
}

func TestSnapshotID(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	id := snapshotID("https://example.com/a", at)
	if !regexp.MustCompile(`^snap_[0-9a-f]{16}$`).MatchString(id) {
		t.Fatalf("snapshotID = %q", id)
	}
	if same := snapshotID("https://example.com/a", at); same != id {
		t.Fatalf("同一输入生成不同 ID: %q vs %q", id, same)
	}
	if other := snapshotID("https://example.com/b", at); other == id {
		t.Fatal("不同 URL 不应生成同一 ID")
	}
	if other := snapshotID("https://example.com/a", at.Add(time.Second)); other == id {
		t.Fatal("不同抓取时间不应生成同一 ID")
	}
}

func TestFetchSchema(t *testing.T) {
	t.Parallel()

	def := fetchToolDefinition()
	if def.Name != "ds_fetch" || def.Title != "DeepSeek 网页抓取" {
		t.Fatalf("name/title = %q/%q", def.Name, def.Title)
	}
	if def.Annotations == nil || !def.Annotations.ReadOnlyHint || !def.Annotations.IdempotentHint {
		t.Fatal("ReadOnly/Idempotent 注解缺失")
	}
	if def.Annotations.OpenWorldHint == nil || !*def.Annotations.OpenWorldHint {
		t.Fatal("OpenWorldHint 必须为 true")
	}
	desc := def.Description
	for _, want := range []string{"inline", "outline", "section", "cache_id", "3080"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description 缺少 %q", want)
		}
	}
	raw, ok := def.InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("InputSchema 类型 = %T", def.InputSchema)
	}
	var schema struct {
		Properties map[string]struct {
			Default *json.RawMessage `json:"default"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema JSON = %v", err)
	}
	if len(schema.Properties) != 6 {
		t.Fatalf("参数数量 = %d, want 6", len(schema.Properties))
	}
	for _, forbidden := range []string{"no_cache", "offset"} {
		if _, ok := schema.Properties[forbidden]; ok {
			t.Fatalf("schema MUST NOT 含 %s", forbidden)
		}
	}
	for _, want := range []string{"url", "cache_id", "section", "sections", "max_chars", "unescape"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Fatalf("schema 缺少 %s", want)
		}
	}
	if len(schema.Required) != 0 {
		t.Fatalf("required = %v, want 空（url/cache_id 运行时二选一）", schema.Required)
	}
	if string(*schema.Properties["max_chars"].Default) != "32000" {
		t.Fatalf("max_chars default = %s", *schema.Properties["max_chars"].Default)
	}
	if string(*schema.Properties["unescape"].Default) != "true" {
		t.Fatalf("unescape default = %s", *schema.Properties["unescape"].Default)
	}
}
