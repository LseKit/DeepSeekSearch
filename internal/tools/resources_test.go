// 文件名: resources_test.go
// 用途: 验证三个 Resource 的内容、顺序、ttlMs/cacheScope 分派与用量统计。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func resourceDeps(t *testing.T) Deps {
	t.Helper()
	client := &fakeClient{searchFn: func(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
		return domain.SearchResponse{Results: fixtureResults(5)}, nil
	}}
	deps := healthyDeps(t, client)
	deps.Usage = NewUsageTracker(deps.Clock)
	return deps
}

func readResource(t *testing.T, deps *Deps, uri string) []byte {
	t.Helper()
	for _, def := range resourceDefinitions() {
		if def.URI != uri {
			continue
		}
		raw, err := def.read(context.Background(), deps)
		if err != nil {
			t.Fatalf("read %s = %v", uri, err)
		}
		return raw
	}
	t.Fatalf("未知 Resource %s", uri)
	return nil
}

func decodeUsage(t *testing.T, raw []byte) usageSnapshot {
	t.Helper()
	var snapshot usageSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("解析 usage = %v", err)
	}
	return snapshot
}

func TestResources(t *testing.T) {
	t.Parallel()

	t.Run("error_codes_与注册表一致", func(t *testing.T) {
		t.Parallel()
		deps := resourceDeps(t)
		raw := readResource(t, &deps, resourceErrorCodes)
		var entries []errorCodeEntry
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("error-codes 不是合法 JSON: %v", err)
		}
		if len(entries) != len(domain.CodeRegistry) || len(entries) != 41 {
			t.Fatalf("条目数 = %d, registry=%d", len(entries), len(domain.CodeRegistry))
		}
	})

	t.Run("error_codes_由注册表生成", func(t *testing.T) {
		t.Parallel()
		copyRegistry := make(map[int]domain.CodeInfo, len(domain.CodeRegistry))
		for code, info := range domain.CodeRegistry {
			copyRegistry[code] = info
		}
		changed := copyRegistry[domain.CodeUpNotFound]
		changed.Message = "CHANGED_MESSAGE"
		copyRegistry[domain.CodeUpNotFound] = changed
		raw, err := errorCodesDocumentFrom(copyRegistry)
		if err != nil {
			t.Fatalf("errorCodesDocumentFrom() = %v", err)
		}
		if !strings.Contains(string(raw), "CHANGED_MESSAGE") {
			t.Fatal("修改码表后输出未变化，说明不是从注册表生成")
		}
	})

	t.Run("effective_config_已脱敏", func(t *testing.T) {
		t.Parallel()
		deps := resourceDeps(t)
		deps.Cfg.ApiKey = "sk-secret"
		deps.Cfg.HttpToken = "token-secret"
		raw := readResource(t, &deps, resourceEffectiveConfig)
		text := string(raw)
		if strings.Contains(text, "sk-secret") || strings.Contains(text, "token-secret") {
			t.Fatalf("Resource 泄露密钥: %s", text)
		}
		if !strings.Contains(text, `"source"`) || !strings.Contains(text, `"config_sha256"`) {
			t.Fatalf("缺少 source/config_sha256: %s", text)
		}
		if !strings.Contains(text, `"api_key":"***"`) {
			t.Fatalf("api_key 未显示为 ***: %s", text)
		}
	})

	t.Run("usage初始全零", func(t *testing.T) {
		t.Parallel()
		deps := resourceDeps(t)
		snapshot := decodeUsage(t, readResource(t, &deps, resourceUsage))
		if len(snapshot.Tools) != 3 {
			t.Fatalf("tools = %d, want 3", len(snapshot.Tools))
		}
		for name, stat := range snapshot.Tools {
			if stat.Calls != 0 || stat.Billed != 0 || stat.CacheHits != 0 || stat.Errors != 0 {
				t.Fatalf("%s 初始统计非零: %+v", name, stat)
			}
		}
	})

	t.Run("usage记录调用", func(t *testing.T) {
		t.Parallel()
		deps := resourceDeps(t)
		_ = decodeSuccess(t, invokeSearch(t, &deps, &searchInput{Query: "go"}))
		snapshot := decodeUsage(t, readResource(t, &deps, resourceUsage))
		stat := snapshot.Tools["ds_search"]
		if stat.Calls != 1 || stat.Billed != 1 {
			t.Fatalf("ds_search 统计 = %+v", stat)
		}
		if stat.LastCallAt == "" {
			t.Fatal("last_call_at 未记录")
		}
	})

	t.Run("usage缓存命中不计费", func(t *testing.T) {
		t.Parallel()
		deps := resourceDeps(t)
		_ = decodeSuccess(t, invokeSearch(t, &deps, &searchInput{Query: "go"}))
		_ = decodeSuccess(t, invokeSearch(t, &deps, &searchInput{Query: "go"}))
		stat := decodeUsage(t, readResource(t, &deps, resourceUsage)).Tools["ds_search"]
		if stat.Calls != 2 || stat.Billed != 1 || stat.CacheHits != 1 {
			t.Fatalf("统计 = %+v, want calls=2 billed=1 cache_hits=1", stat)
		}
	})

	t.Run("URI常量", func(t *testing.T) {
		t.Parallel()
		want := []string{"ds://effective-config", "ds://error-codes", "ds://usage"}
		defs := resourceDefinitions()
		if len(defs) != 3 {
			t.Fatalf("Resource 数量 = %d", len(defs))
		}
		for i, def := range defs {
			if def.URI != want[i] {
				t.Fatalf("URI[%d] = %q, want %q", i, def.URI, want[i])
			}
			if def.Name == "" || def.Title == "" || def.Description == "" {
				t.Fatalf("Resource 元数据不完整: %+v", def)
			}
			if def.MIMEType != "application/json" {
				t.Fatalf("%s MIME = %q", def.URI, def.MIMEType)
			}
		}
	})

	t.Run("SetCacheable按URI分派", func(t *testing.T) {
		t.Parallel()
		type policy struct {
			ttl   int64
			scope string
		}
		seen := map[policy]bool{}
		for _, uri := range []string{resourceEffectiveConfig, resourceErrorCodes, resourceUsage} {
			ttl, scope := CacheableFor(uri)
			if ttl <= 0 {
				t.Fatalf("%s ttlMs = %d, MUST > 0", uri, ttl)
			}
			if scope != "public" && scope != "private" {
				t.Fatalf("%s cacheScope = %q", uri, scope)
			}
			seen[policy{ttl, scope}] = true
		}
		if len(seen) != 3 {
			t.Fatalf("三个 Resource 的缓存策略不互异: %v", seen)
		}
		exact := map[string]policy{
			resourceEffectiveConfig: {300000, "private"},
			resourceErrorCodes:      {300000, "public"},
			resourceUsage:           {1000, "private"},
		}
		for uri, want := range exact {
			ttl, scope := CacheableFor(uri)
			if got := (policy{ttl, scope}); got != want {
				t.Fatalf("%s policy = %+v, want %+v", uri, got, want)
			}
		}
		if ttl, scope := CacheableFor("ds://unknown"); ttl != 1000 || scope != "private" {
			t.Fatalf("未知 URI 回落 = (%d,%q)", ttl, scope)
		}
	})

	t.Run("CacheableForRequest从ReadParams取URI", func(t *testing.T) {
		t.Parallel()
		req := &mcp.ServerRequest[*mcp.ReadResourceParams]{
			Params: &mcp.ReadResourceParams{URI: resourceErrorCodes},
		}
		if ttl, scope := CacheableForRequest(req); ttl != 300000 || scope != "public" {
			t.Fatalf("CacheableForRequest = (%d,%q)", ttl, scope)
		}
		if ttl, scope := CacheableForRequest(nil); ttl != 1000 || scope != "private" {
			t.Fatalf("nil request 回落 = (%d,%q)", ttl, scope)
		}
	})

	t.Run("resources_list顺序确定", func(t *testing.T) {
		t.Parallel()
		want := []string{"ds://effective-config", "ds://error-codes", "ds://usage"}
		for range 20 {
			defs := resourceDefinitions()
			for i, def := range defs {
				if def.URI != want[i] {
					t.Fatalf("第 %d 次顺序 = %q, want %q", i, def.URI, want[i])
				}
			}
		}
	})
}

func TestNewUsageTracker(t *testing.T) {
	t.Parallel()

	tracker := NewUsageTracker(nil)
	snapshot := tracker.snapshot()
	if len(snapshot.Tools) != 3 {
		t.Fatalf("tools = %d", len(snapshot.Tools))
	}
	tracker.Record("unknown", true, false, nil)
	if snapshot = tracker.snapshot(); snapshot.Tools["ds_search"].Calls != 0 {
		t.Fatal("未知工具不应被记录")
	}
	tracker.Record("ds_search", true, false, nil)
	tracker.Record("ds_search", false, true, nil)
	snapshot = tracker.snapshot()
	stat := snapshot.Tools["ds_search"]
	if stat.Calls != 2 || stat.Billed != 1 || stat.CacheHits != 1 || stat.Errors != 0 {
		t.Fatalf("stat = %+v", stat)
	}
	if len(snapshot.Hourly) != 1 {
		t.Fatalf("hourly = %d, want 1", len(snapshot.Hourly))
	}
}

func TestRegisterResources(t *testing.T) {
	t.Parallel()

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	deps := resourceDeps(t)
	if err := registerResources(srv, deps); err != nil {
		t.Fatalf("registerResources() = %v", err)
	}
}
