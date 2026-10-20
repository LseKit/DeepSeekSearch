// 文件名: search_handler_error_test.go
// 用途: 验证 ds_search 的上游错误、重试、总超时、取消与熔断错误码。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

func TestHandleSearchErrors(t *testing.T) {
	t.Parallel()

	t.Run("上游429", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{searchFn: func(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
			return domain.SearchResponse{}, domain.NewUpstreamError(429, "req-429", "rate limited", nil)
		}}
		deps, _ := newTestDeps(t, client, nil)
		payload := decodeError(t, invokeSearch(t, &deps, &searchInput{Query: "go"}))
		if payload.Code != domain.CodeUpRateLimited || !payload.Retryable {
			t.Fatalf("payload = %+v", payload)
		}
		if client.callCount() != 1 {
			t.Fatalf("未配置重试时上游调用 = %d, want 1", client.callCount())
		}
	})

	t.Run("单次尝试超时重试后2998", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{searchFn: func(ctx context.Context, _ domain.SearchRequest) (domain.SearchResponse, error) {
			attempt := domain.AttemptFromContext(ctx)
			return domain.SearchResponse{}, domain.NewNetworkError("attempt_timeout", attempt, errors.New("timeout"))
		}}
		deps, _ := newTestDeps(t, client, func(d *Deps) {
			d.Retry = resilience.NewRetry(resilience.Config{
				RetryEnabled:       true,
				MaxAttempts:        2,
				JitterMode:         "none",
				FixedMs:            []int{0},
				RetryBudgetEnabled: false,
				Clock:              d.Clock,
			}, d.Clock, nil)
		})
		payload := decodeError(t, invokeSearch(t, &deps, &searchInput{Query: "go"}))
		if payload.Code != domain.CodeUpNetwork || !payload.Retryable {
			t.Fatalf("payload = %+v", payload)
		}
		if payload.Details["cause"] != "attempt_timeout" {
			t.Fatalf("cause = %v", payload.Details["cause"])
		}
		if payload.Details["attempt"] != float64(2) {
			t.Fatalf("attempt = %v, want 2", payload.Details["attempt"])
		}
		if client.callCount() != 2 {
			t.Fatalf("上游调用 = %d, want 2", client.callCount())
		}
	})

	t.Run("整链超时3020", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{searchFn: func(ctx context.Context, _ domain.SearchRequest) (domain.SearchResponse, error) {
			<-ctx.Done()
			return domain.SearchResponse{}, domain.NewNetworkError("attempt_timeout", 1, ctx.Err())
		}}
		deps, _ := newTestDeps(t, client, func(d *Deps) {
			d.Cfg.Timeout.Search.TotalTimeoutMs = 10
		})
		payload := decodeError(t, invokeSearch(t, &deps, &searchInput{Query: "go"}))
		if payload.Code != domain.CodeLocTotalTimeout {
			t.Fatalf("code = %d, want 3020 (payload=%+v)", payload.Code, payload)
		}
		if _, ok := payload.Details["elapsed_ms"]; !ok {
			t.Fatalf("details 缺少 elapsed_ms: %v", payload.Details)
		}
		if client.callCount() != 1 {
			t.Fatalf("整链超时后不应再发起尝试，calls = %d", client.callCount())
		}
	})

	t.Run("限流等待中取消", func(t *testing.T) {
		t.Parallel()
		started := make(chan struct{})
		limiter := &fakeLimiter{waitFn: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}
		client := &fakeClient{}
		deps, _ := newTestDeps(t, client, func(d *Deps) { d.Limiter = limiter })
		ctx, cancel := context.WithCancel(context.Background())
		req := searchCall(t, &searchInput{Query: "go"})
		done := make(chan *mcp.CallToolResult, 1)
		go func() { done <- handleSearch(ctx, &deps, req) }()
		<-started
		cancel()
		var res *mcp.CallToolResult
		select {
		case res = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("取消后未及时返回")
		}
		payload := decodeError(t, res)
		if payload.Code != domain.CodeLocTotalTimeout {
			t.Fatalf("code = %d, want 3020", payload.Code)
		}
		if client.callCount() != 0 {
			t.Fatalf("限流等待中取消，上游调用 = %d, want 0", client.callCount())
		}
		if _, releases := limiter.counts(); releases != 0 {
			t.Fatalf("未取得令牌不应 Release，实际 %d 次", releases)
		}
	})

	t.Run("熔断打开3030", func(t *testing.T) {
		t.Parallel()
		limiter := &fakeLimiter{}
		circuit := &fakeCircuit{allowErr: domain.NewLocalError(
			domain.CodeLocCircuitOpen, "熔断器处于打开状态",
			map[string]any{"retry_after_ms": 5000})}
		client := &fakeClient{}
		deps, _ := newTestDeps(t, client, func(d *Deps) {
			d.Limiter = limiter
			d.Circuit = circuit
		})
		payload := decodeError(t, invokeSearch(t, &deps, &searchInput{Query: "go"}))
		if payload.Code != domain.CodeLocCircuitOpen || !payload.Retryable {
			t.Fatalf("payload = %+v", payload)
		}
		if payload.Details["retry_after_ms"] != float64(5000) {
			t.Fatalf("retry_after_ms = %v", payload.Details["retry_after_ms"])
		}
		if waits, _ := limiter.counts(); waits != 0 {
			t.Fatalf("熔断打开时不应进入限流，waits = %d", waits)
		}
		if client.callCount() != 0 {
			t.Fatalf("熔断打开时上游调用 = %d, want 0", client.callCount())
		}
	})
}
