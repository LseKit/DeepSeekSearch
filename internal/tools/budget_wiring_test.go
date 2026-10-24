// 文件名: budget_wiring_test.go
// 用途: 验证 ds_search / ds_fetch 在调用上游前真正执行调用次数预算。
// 创建日期: 2026-10-24
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"testing"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

type rejectingBudget struct {
	recorded int
}

func (b *rejectingBudget) CheckWindow(string) error { return budgetRejected() }
func (b *rejectingBudget) CheckDaily(string) error  { return budgetRejected() }
func (b *rejectingBudget) Record(string, int)       { b.recorded++ }
func (b *rejectingBudget) TrimChars(s string) (string, bool) {
	return s, false
}
func (b *rejectingBudget) Reset() {}

func budgetRejected() error {
	//nolint:wrapcheck // 测试构造器直接返回领域错误，不需要再包一层
	return domain.NewLocalError(domain.CodeLocBudgetExceeded, "调用次数超限", map[string]any{"kind": "window"})
}

func TestSearchBudgetRejectsBeforeUpstream(t *testing.T) {
	t.Parallel()

	client := &fakeClient{searchFn: func(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
		return domain.SearchResponse{}, nil
	}}
	deps, _ := newTestDeps(t, client, func(d *Deps) {
		d.Budget = &rejectingBudget{}
	})
	noCache := true
	res := invokeSearch(t, &deps, &searchInput{Query: "budget", NoCache: &noCache})
	if got := decodeError(t, res).Code; got != domain.CodeLocBudgetExceeded {
		t.Fatalf("code = %d, want %d", got, domain.CodeLocBudgetExceeded)
	}
	if client.callCount() != 0 {
		t.Fatalf("预算拒绝后仍调用上游 %d 次", client.callCount())
	}
}

func TestFetchBudgetRejectsBeforeUpstream(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	deps, _ := newTestDeps(t, client, func(d *Deps) {
		d.Budget = &rejectingBudget{}
	})
	noCache := true
	res := invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/a"), NoCache: &noCache})
	if got := decodeError(t, res).Code; got != domain.CodeLocBudgetExceeded {
		t.Fatalf("code = %d, want %d", got, domain.CodeLocBudgetExceeded)
	}
	if client.fetchCount() != 0 {
		t.Fatalf("预算拒绝后仍调用上游 %d 次", client.fetchCount())
	}
}

var _ resilience.Budget = (*rejectingBudget)(nil)
