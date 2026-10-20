// 文件名: fetch_handler_budget_test.go
// 用途: 验证 ds_fetch 三种模式共用 max_chars 预算、outline 折叠与 upstream 标记不复现。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"strings"
	"testing"
)

func TestHandleFetchBudget(t *testing.T) {
	t.Parallel()

	t.Run("437738rune按section预算截断", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("a", 437738)
		deps, _ := newTestDeps(t, fetchFixture("T", content), func(d *Deps) {
			d.Cfg.Limits.FixedChunkChars = 500000
		})
		first := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			URL: strPtr("https://example.com/rfc"), MaxChars: intPtr(200000),
		}))
		sectionID := first.Sections[0].ID
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr(first.CacheID), Section: strPtr(sectionID), MaxChars: intPtr(200000),
		}))
		if out.TotalChars != 437738 || !out.Truncated || out.TruncatedBy != "budget" {
			t.Fatalf("total/truncated/by = %d/%v/%q", out.TotalChars, out.Truncated, out.TruncatedBy)
		}
		if strings.Contains(resultText(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr(first.CacheID), Section: strPtr(sectionID), MaxChars: intPtr(200000),
		})), `"upstream"`) {
			t.Fatal("MUST NOT 产出 upstream 截断标记")
		}
	})

	t.Run("不触发预算则无truncated_by", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("a", 150000)
		deps, _ := newTestDeps(t, fetchFixture("T", content), func(d *Deps) {
			d.Cfg.Limits.FixedChunkChars = 500000
		})
		first := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			URL: strPtr("https://example.com/rfc2"), MaxChars: intPtr(200000),
		}))
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr(first.CacheID), Section: strPtr(first.Sections[0].ID), MaxChars: intPtr(200000),
		}))
		if out.Truncated || out.TruncatedBy != "" {
			t.Fatalf("未截断却填了标记: %v/%q", out.Truncated, out.TruncatedBy)
		}
	})

	t.Run("section模式不豁免预算", func(t *testing.T) {
		t.Parallel()
		content := "# A\n" + strings.Repeat("a", 50000) + "\n## B\n" + strings.Repeat("b", 200)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		first := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/section-budget")}))
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr(first.CacheID), Section: strPtr("s0"), MaxChars: intPtr(32000),
		}))
		if !out.Truncated || out.TruncatedBy != "budget" {
			t.Fatalf("truncated/by = %v/%q", out.Truncated, out.TruncatedBy)
		}
	})

	t.Run("sections合并超预算", func(t *testing.T) {
		t.Parallel()
		content := "# A\n" + strings.Repeat("a", 20000) +
			"\n## B\n" + strings.Repeat("b", 20000) +
			"\n## C\n" + strings.Repeat("c", 20000)
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		first := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/multi")}))
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr(first.CacheID), Sections: []string{"s0", "s1", "s2"}, MaxChars: intPtr(32000),
		}))
		if !out.Truncated || out.TruncatedBy != "budget" {
			t.Fatalf("truncated/by = %v/%q", out.Truncated, out.TruncatedBy)
		}
	})

	t.Run("outline节数上限", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		for i := range 350 {
			b.WriteString("## H")
			b.WriteString(strings.Repeat("h", 1+i%5))
			b.WriteString("\n")
			b.WriteString(strings.Repeat("a", 30))
			b.WriteString("\n")
		}
		content := b.String()
		deps, _ := newTestDeps(t, fetchFixture("T", content), nil)
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{
			URL: strPtr("https://example.com/many"), MaxChars: intPtr(200000),
		}))
		if out.Mode != "outline" || len(out.Sections) != 200 {
			t.Fatalf("mode/sections = %q/%d, want outline/200", out.Mode, len(out.Sections))
		}
		found := false
		for _, warning := range out.Warnings {
			if strings.Contains(warning, "共 350 节") {
				found = true
			}
		}
		if !found {
			t.Fatalf("warnings = %v，缺少共 350 节", out.Warnings)
		}
	})
}
