// 文件名: dedupe_test.go
// 用途: 校验三层去重、域名配额、分数降序、同分保序与统计计数。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"encoding/json"
	"testing"
)

func mkResult(score float64, title, rawURL, site string) SearchResult {
	r := SearchResult{RerankScore: score, Title: title, Url: rawURL}
	if site != "" {
		r.SiteName = strPtr(site)
	}
	return r
}

func TestDedupe(t *testing.T) {
	t.Parallel()

	t.Run("L1 同 URL 不同 utm 只留高分", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "A", "https://x.com/a?utm_source=1", "x"),
			mkResult(1.0, "B", "https://x.com/a?utm_source=2", "x"),
		}
		kept, stats := Dedupe(items, 0, []string{"utm_source"})

		if len(kept) != 1 {
			t.Fatalf("kept = %d 条, want 1: %#v", len(kept), kept)
		}
		if kept[0].RerankScore != 2.0 {
			t.Errorf("保留的分数 = %v, want 2.0", kept[0].RerankScore)
		}
		if stats.DroppedURL != 1 {
			t.Errorf("DroppedURL = %d, want 1", stats.DroppedURL)
		}
	})

	t.Run("L1 tracking 为 nil 时不合并 utm", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "A", "https://x.com/a?utm_source=1", "x"),
			mkResult(1.0, "B", "https://x.com/a?utm_source=2", "x"),
		}
		kept, stats := Dedupe(items, 0, nil)

		if len(kept) != 2 {
			t.Fatalf("kept = %d 条, want 2（nil 不剥离任何参数）", len(kept))
		}
		if stats.DroppedURL != 0 {
			t.Errorf("DroppedURL = %d, want 0", stats.DroppedURL)
		}
	})

	t.Run("L2 同标题同站点只留高分", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "Same", "https://a.com/1", "x"),
			mkResult(1.0, "Same", "https://b.com/2", "x"),
		}
		kept, stats := Dedupe(items, 0, nil)

		if len(kept) != 1 || kept[0].RerankScore != 2.0 {
			t.Fatalf("kept = %#v, want 仅 2.0 一条", kept)
		}
		if stats.DroppedRepost != 1 {
			t.Errorf("DroppedRepost = %d, want 1", stats.DroppedRepost)
		}
	})

	t.Run("L2 标题归一后相同", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "  Hello   World ", "https://a.com/1", "x"),
			mkResult(1.0, "hello world", "https://b.com/2", "x"),
		}
		kept, stats := Dedupe(items, 0, nil)

		if len(kept) != 1 || kept[0].RerankScore != 2.0 {
			t.Fatalf("kept = %#v, want 仅 2.0 一条", kept)
		}
		if stats.DroppedRepost != 1 {
			t.Errorf("DroppedRepost = %d, want 1", stats.DroppedRepost)
		}
	})

	t.Run("L2 站点不同不合并", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "Same", "https://a.com/1", "x"),
			mkResult(1.0, "Same", "https://b.com/2", "y"),
		}
		kept, stats := Dedupe(items, 0, nil)

		if len(kept) != 2 {
			t.Errorf("kept = %d 条, want 2", len(kept))
		}
		if stats.DroppedRepost != 0 {
			t.Errorf("DroppedRepost = %d, want 0", stats.DroppedRepost)
		}
	})

	t.Run("L3 同域名配额", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(5.0, "T1", "https://example.com/1", "s1"),
			mkResult(4.0, "T2", "https://example.com/2", "s2"),
			mkResult(3.0, "T3", "https://example.com/3", "s3"),
			mkResult(2.0, "T4", "https://example.com/4", "s4"),
			mkResult(1.0, "T5", "https://example.com/5", "s5"),
		}
		kept, stats := Dedupe(items, 3, nil)

		if len(kept) != 3 {
			t.Fatalf("kept = %d 条, want 3", len(kept))
		}
		for i, want := range []float64{5.0, 4.0, 3.0} {
			if kept[i].RerankScore != want {
				t.Errorf("kept[%d] = %v, want %v", i, kept[i].RerankScore, want)
			}
		}
		if stats.DroppedDomain != 2 {
			t.Errorf("DroppedDomain = %d, want 2", stats.DroppedDomain)
		}
	})

	t.Run("L3 maxPerDomain 非正数不限制", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(5.0, "T1", "https://example.com/1", "s1"),
			mkResult(4.0, "T2", "https://example.com/2", "s2"),
			mkResult(3.0, "T3", "https://example.com/3", "s3"),
		}
		for _, limit := range []int{0, -1} {
			kept, stats := Dedupe(items, limit, nil)
			if len(kept) != 3 {
				t.Errorf("maxPerDomain=%d 时 kept = %d, want 3", limit, len(kept))
			}
			if stats.DroppedDomain != 0 {
				t.Errorf("maxPerDomain=%d 时 DroppedDomain = %d, want 0", limit, stats.DroppedDomain)
			}
		}
	})

	t.Run("输出按分数降序", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(1.0, "Low", "https://a.com/1", "x"),
			mkResult(3.0, "High", "https://b.com/2", "y"),
			mkResult(2.0, "Mid", "https://c.com/3", "z"),
		}
		kept, _ := Dedupe(items, 0, nil)

		want := []float64{3.0, 2.0, 1.0}
		if len(kept) != len(want) {
			t.Fatalf("kept = %d 条, want %d", len(kept), len(want))
		}
		for i, score := range want {
			if kept[i].RerankScore != score {
				t.Errorf("kept[%d] = %v, want %v", i, kept[i].RerankScore, score)
			}
		}
	})

	t.Run("同分保持原始顺序", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "A", "https://a.com/1", "x"),
			mkResult(2.0, "B", "https://b.com/2", "y"),
		}
		kept, _ := Dedupe(items, 0, nil)

		if len(kept) != 2 || kept[0].Title != "A" || kept[1].Title != "B" {
			t.Errorf("kept = %#v, want 同分保序 A,B", kept)
		}
	})

	t.Run("http 与 https 不合并", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "A", "http://example.com/p", "x"),
			mkResult(1.0, "B", "https://example.com/p", "y"),
		}
		kept, stats := Dedupe(items, 0, nil)

		if len(kept) != 2 {
			t.Errorf("kept = %d 条, want 2（scheme 不归一）", len(kept))
		}
		if stats.DroppedURL != 0 {
			t.Errorf("DroppedURL = %d, want 0", stats.DroppedURL)
		}
	})

	t.Run("三层统计各记一次", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(5.0, "A", "https://a.com/1?utm_source=x", "a"),
			mkResult(4.0, "A", "https://a.com/1?utm_source=y", "a"),
			mkResult(3.0, "A", "https://a.com/2", "a"),
			mkResult(2.0, "B", "https://a.com/3", "a"),
			mkResult(1.0, "C", "https://a.com/4", "c"),
		}
		kept, stats := Dedupe(items, 2, []string{"utm_source"})

		if len(kept) != 2 {
			t.Fatalf("kept = %d 条, want 2", len(kept))
		}
		if kept[0].RerankScore != 5.0 || kept[1].RerankScore != 2.0 {
			t.Errorf("kept 分数 = %v,%v, want 5,2", kept[0].RerankScore, kept[1].RerankScore)
		}
		if stats.DroppedURL != 1 || stats.DroppedRepost != 1 || stats.DroppedDomain != 1 {
			t.Errorf("stats = %#v, want 1/1/1", stats)
		}
	})

	t.Run("非法 URL 走原始串哈希", func(t *testing.T) {
		t.Parallel()

		items := []SearchResult{
			mkResult(2.0, "A", "not a url", "x"),
			mkResult(1.0, "B", "not a url", "y"),
		}
		kept, stats := Dedupe(items, 0, nil)

		if len(kept) != 1 {
			t.Errorf("kept = %d 条, want 1", len(kept))
		}
		if stats.DroppedURL != 1 {
			t.Errorf("DroppedURL = %d, want 1", stats.DroppedURL)
		}
	})

	t.Run("空输入", func(t *testing.T) {
		t.Parallel()

		kept, stats := Dedupe(nil, 3, nil)
		if len(kept) != 0 {
			t.Errorf("kept = %d 条, want 0", len(kept))
		}
		if stats != (DedupeStats{}) {
			t.Errorf("stats = %#v, want 零值", stats)
		}
	})
}

func TestDedupeStatsJSON(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(DedupeStats{DroppedURL: 1, DroppedRepost: 2, DroppedDomain: 3})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"dropped_url":1,"dropped_repost":2,"dropped_domain":3}`
	if string(raw) != want {
		t.Errorf("JSON = %s, want %s", raw, want)
	}
}

func TestSearchResultJSON(t *testing.T) {
	t.Parallel()

	r := SearchResult{
		RerankScore: 2.5,
		Title:       "T",
		Url:         "https://x.com",
		SiteName:    strPtr("x"),
		Authority:   3,
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"rerank_score":2.5,"title":"T","url":"https://x.com","snippet":"","summary":"","content":"","site_name":"x","site_icon":null,"date":null,"authority":3}`
	if string(raw) != want {
		t.Errorf("JSON = %s, want %s", raw, want)
	}
}
