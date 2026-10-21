// 文件名: fetch_handler_test.go
// 用途: 验证 ds_fetch 的 inline/outline/section 模式、反转义、缓存与并发单飞。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/deepseek"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func fetchCall(t *testing.T, in *fetchInput) *mcp.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}}
}

func invokeFetch(t *testing.T, deps *Deps, in *fetchInput) *mcp.CallToolResult {
	t.Helper()
	return handleFetch(context.Background(), deps, fetchCall(t, in))
}

func decodeFetchSuccess(t *testing.T, res *mcp.CallToolResult) fetchOutput {
	t.Helper()
	if res.IsError {
		t.Fatalf("IsError = true: %s", resultText(t, res))
	}
	var out fetchOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("解析 fetch 响应 = %v", err)
	}
	return out
}

func fetchFixture(title, content string) *fakeClient {
	return &fakeClient{fetchFn: func(context.Context, domain.FetchRequest) (domain.FetchResponse, error) {
		return domain.FetchResponse{Title: title, Content: content}, nil
	}}
}

func padContent(s string) string { return strings.Repeat("x", 200) + "\n" + s }

func TestHandleFetch(t *testing.T) {
	t.Parallel()

	t.Run("3000字符inline", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("a", 3000)
		deps, _ := newTestDeps(t, fetchFixture("", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/a")}))
		if out.Mode != "inline" || out.TotalChars != 3000 || out.Title != "" {
			t.Fatalf("mode/total/title = %q/%d/%q", out.Mode, out.TotalChars, out.Title)
		}
		if !strings.Contains(out.Content, content) {
			t.Fatal("inline 未返回全文")
		}
	})

	t.Run("no_cache 跳过读缓存但仍写回", func(t *testing.T) {
		t.Parallel()
		client := fetchFixture("T", strings.Repeat("a", 600))
		deps, _ := newTestDeps(t, client, nil)
		url := strPtr("https://example.com/nc")
		_ = decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: url}))
		if cached := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: url})); !cached.FromCache {
			t.Fatal("第二次应命中缓存")
		}
		forced := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: url, NoCache: boolPtr(true)}))
		if forced.FromCache {
			t.Fatal("no_cache=true 不得读缓存")
		}
		if got := client.fetchCount(); got != 2 {
			t.Fatalf("fetch 上游调用 = %d, want 2（首次 + no_cache）", got)
		}
		if again := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: url})); !again.FromCache {
			t.Fatal("no_cache 的结果必须写回缓存")
		}
	})

	t.Run("报文日志 URL 脱敏", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{fetchFn: func(ctx context.Context, req domain.FetchRequest) (domain.FetchResponse, error) {
			if hook := deepseek.ExchangeHookFrom(ctx); hook != nil {
				hook(ctx, []byte(`{"url":"`+req.Url+`"}`), []byte(`{"title":"t","content":"c"}`))
			}
			return domain.FetchResponse{Title: "t", Content: strings.Repeat("a", 600)}, nil
		}}
		deps, buf := newTestDeps(t, client, func(d *Deps) { d.Cfg.Log.MaxBodyLogKb = 1 })
		_ = invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/a?token=SECRET&x=1")})
		logs := buf.String()
		if strings.Contains(logs, "SECRET") {
			t.Fatalf("报文中的 URL 未脱敏: %s", logs)
		}
		if !strings.Contains(logs, `"req_body"`) {
			t.Fatalf("开启报文日志后应带 req_body: %s", logs)
		}
	})

	t.Run("warnings 必带（空数组也要出现）", func(t *testing.T) {
		t.Parallel()
		deps, _ := newTestDeps(t, fetchFixture("T", strings.Repeat("a", 600)), nil)
		res := invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/w")})
		if raw := resultText(t, res); !strings.Contains(raw, `"warnings":[]`) {
			t.Fatalf("warnings 为空时 MUST 序列化为 []（12 号 §4.1），实际: %s", raw)
		}
	})

	t.Run("20000字符按标题分节", func(t *testing.T) {
		t.Parallel()
		content := "# A\n" + strings.Repeat("a", 10000) + "\n## B\n" + strings.Repeat("b", 10000)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/heads")}))
		if out.Mode != "outline" || out.OutlineKind != "headings" || len(out.Sections) < 2 {
			t.Fatalf("outline = mode:%q kind:%q sections:%d", out.Mode, out.OutlineKind, len(out.Sections))
		}
	})

	t.Run("20000字符无标题固定分块", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("a", 20000)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/fixed")}))
		if out.Mode != "outline" || out.OutlineKind != "fixed" || len(out.Sections) == 0 {
			t.Fatalf("outline = mode:%q kind:%q sections:%d", out.Mode, out.OutlineKind, len(out.Sections))
		}
	})

	t.Run("按cache_id取节", func(t *testing.T) {
		t.Parallel()
		content := "# A\n" + strings.Repeat("a", 10000) + "\n## B\n" + strings.Repeat("b", 10000)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		first := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/sections")}))
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr(first.CacheID), Section: strPtr("s1"),
		}))
		if out.Mode != "section" || out.CacheID != first.CacheID {
			t.Fatalf("section = mode:%q cache_id:%q", out.Mode, out.CacheID)
		}
		if !strings.Contains(out.Content, "## B") || !strings.Contains(out.Content, strings.Repeat("b", 100)) {
			t.Fatal("未取到 s1 全文")
		}
	})

	t.Run("节不存在返回1090", func(t *testing.T) {
		t.Parallel()
		content := "# A\n" + strings.Repeat("a", 10000) + "\n## B\n" + strings.Repeat("b", 10000)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		first := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/missing")}))
		payload := decodeError(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr(first.CacheID), Section: strPtr("s99"),
		}))
		if payload.Code != domain.CodeValSectionNotFound {
			t.Fatalf("code = %d, want 1090", payload.Code)
		}
	})

	t.Run("unescape默认反转义", func(t *testing.T) {
		t.Parallel()
		content := padContent(`\#\# Title`)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/unescape")}))
		if !strings.Contains(out.Content, "## Title") {
			t.Fatal("unescape=true 未反转义 ##")
		}
	})

	t.Run("unescape_false保留反斜杠", func(t *testing.T) {
		t.Parallel()
		content := padContent(`\#\# Title`)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			URL: strPtr("https://example.com/raw"), Unescape: boolPtr(false),
		}))
		if !strings.Contains(out.Content, `\#\# Title`) {
			t.Fatal("unescape=false 不应改动正文")
		}
	})

	t.Run("反转义不动字面量反斜杠n", func(t *testing.T) {
		t.Parallel()
		content := padContent(`code \n literal`)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/literal")}))
		if !strings.Contains(out.Content, `\n`) {
			t.Fatal("字面量 \\n 被破坏")
		}
	})

	t.Run("反转义下划线", func(t *testing.T) {
		t.Parallel()
		content := padContent(`a\_b`)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/underscore")}))
		if !strings.Contains(out.Content, "a_b") {
			t.Fatal(`\_ 未反转义为 _`)
		}
	})

	t.Run("正文过短返回3050", func(t *testing.T) {
		t.Parallel()
		deps, _ := newTestDeps(t, fetchFixture("T", strings.Repeat("a", 100)), nil)
		payload := decodeError(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/short")}))
		if payload.Code != domain.CodeLocContentTooShort {
			t.Fatalf("code = %d, want 3050", payload.Code)
		}
	})

	t.Run("6405rune按rune判定inline", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("汉", 6405)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/rune")}))
		if out.Mode != "inline" || out.TotalChars != 6405 {
			t.Fatalf("mode/total = %q/%d, want inline/6405", out.Mode, out.TotalChars)
		}
	})

	t.Run("max_chars截断inline", func(t *testing.T) {
		t.Parallel()
		deps, _ := newTestDeps(t, fetchFixture("T", strings.Repeat("a", 5000)), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			URL: strPtr("https://example.com/budget"), MaxChars: intPtr(4000),
		}))
		if !out.Truncated || out.TruncatedBy != "budget" {
			t.Fatalf("truncated/by = %v/%q", out.Truncated, out.TruncatedBy)
		}
	})

	t.Run("缓存命中", func(t *testing.T) {
		t.Parallel()
		deps, buf := newTestDeps(t, fetchFixture("T", strings.Repeat("a", 3000)), nil)
		first := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/cache")}))
		second := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/cache")}))
		if first.FromCache || !second.FromCache || second.SavedBilledCalls != 1 {
			t.Fatalf("from_cache/saved = %v/%v/%d", first.FromCache, second.FromCache, second.SavedBilledCalls)
		}
		if second.CacheID != first.CacheID {
			t.Fatalf("缓存命中 cache_id 变化: %q vs %q", first.CacheID, second.CacheID)
		}
		if end := lastEvent(t, buf, "call.end"); end["billed"] != false {
			t.Fatalf("缓存命中 billed = %v", end["billed"])
		}
	})

	t.Run("5并发同URL只抓一次", func(t *testing.T) {
		t.Parallel()
		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		client := &fakeClient{fetchFn: func(context.Context, domain.FetchRequest) (domain.FetchResponse, error) {
			once.Do(func() { close(started) })
			<-release
			return domain.FetchResponse{Title: "T", Content: strings.Repeat("a", 3000)}, nil
		}}
		deps, _ := newTestDeps(t, client, nil)
		req := fetchCall(t, &fetchInput{URL: strPtr("https://example.com/concurrent")})
		results := make(chan *mcp.CallToolResult, 5)
		for range 5 {
			go func() { results <- handleFetch(context.Background(), &deps, req) }()
		}
		<-started
		<-time.After(20 * time.Millisecond)
		close(release)
		for range 5 {
			res := <-results
			if res.IsError {
				t.Fatalf("并发 fetch 失败: %s", resultText(t, res))
			}
		}
		if client.fetchCount() != 1 {
			t.Fatalf("上游 fetch 次数 = %d, want 1", client.fetchCount())
		}
	})
}
