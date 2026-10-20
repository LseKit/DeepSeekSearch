// 文件名: search_handler_budget_test.go
// 用途: 验证 ds_search 的整响应预算截断、保序与 total/results 差异。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func bigResults(n int) []domain.SearchResult {
	items := make([]domain.SearchResult, 0, n)
	for i := range n {
		site := fmt.Sprintf("Site %d", i)
		date := "2026-10-20T10:00:00Z"
		items = append(items, domain.SearchResult{
			RerankScore: 2.5 - float64(i)*0.01,
			Title:       fmt.Sprintf("Big Title %d", i),
			Url:         fmt.Sprintf("https://big%d.example.com/page", i),
			Summary:     strings.Repeat("z", 1200),
			SiteName:    &site,
			Date:        &date,
			Authority:   3,
		})
	}
	return items
}

func TestHandleSearchBudget(t *testing.T) {
	t.Parallel()

	t.Run("总字符预算截断保序", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{searchFn: func(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
			return domain.SearchResponse{Results: bigResults(30)}, nil
		}}
		deps, _ := newTestDeps(t, client, nil)
		out := decodeSuccess(t, invokeSearch(t, &deps, &searchInput{Query: "big"}))
		if out.Total != 30 {
			t.Fatalf("total = %d, want 30", out.Total)
		}
		if len(out.Results) == 0 || len(out.Results) >= 30 {
			t.Fatalf("预算未截断，results = %d", len(out.Results))
		}
		for i := 1; i < len(out.Results); i++ {
			if out.Results[i-1].RerankScore < out.Results[i].RerankScore {
				t.Fatalf("截断后顺序被打乱: [%d]=%v < [%d]=%v",
					i-1, out.Results[i-1].RerankScore, i, out.Results[i].RerankScore)
			}
		}
		if len(out.Warnings) == 0 {
			t.Fatal("预算截断应写入 warnings")
		}
	})

	t.Run("total与results不相等", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{searchFn: func(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
			return domain.SearchResponse{Results: bigResults(30)}, nil
		}}
		deps, _ := newTestDeps(t, client, nil)
		out := decodeSuccess(t, invokeSearch(t, &deps, &searchInput{Query: "big"}))
		if out.Total == len(out.Results) {
			t.Fatalf("total 与 results 应不相等，实际都是 %d", out.Total)
		}
	})
}
