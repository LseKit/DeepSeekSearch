// 文件名: health_test.go
// 用途: 验证 ds_health 的 8 项检查、降级语义、Ping 诊断映射与注解。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

type fakeClient struct {
	mu        sync.Mutex
	pingCalls int
	pingFn    func(context.Context) error
}

func (f *fakeClient) Search(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
	return domain.SearchResponse{}, nil
}

func (f *fakeClient) Fetch(context.Context, domain.FetchRequest) (domain.FetchResponse, error) {
	return domain.FetchResponse{}, nil
}

func (f *fakeClient) Ping(ctx context.Context) error {
	f.mu.Lock()
	f.pingCalls++
	fn := f.pingFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (f *fakeClient) pingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pingCalls
}

type fakeLimiter struct{}

func (l *fakeLimiter) Wait(context.Context, string) error { return nil }
func (l *fakeLimiter) Release(string)                     {}
func (l *fakeLimiter) Depth(string) int                   { return 0 }

type fakeCircuit struct{ state string }

func (c *fakeCircuit) Allow(string) error   { return nil }
func (c *fakeCircuit) Record(string, error) {}
func (c *fakeCircuit) State(string) string  { return c.state }

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("结果没有 Content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] 类型 = %T", res.Content[0])
	}
	return text.Text
}

type healthCache struct {
	writableErr error
	entries     int
	bytes       int64
	degraded    bool
}

func (c *healthCache) Get(context.Context, string) (data []byte, hit bool, err error) {
	return nil, false, nil
}
func (c *healthCache) Put(context.Context, string, []byte, time.Duration) error {
	return nil
}
func (c *healthCache) Stats() (entries int, bytes int64, degraded bool) {
	return c.entries, c.bytes, c.degraded
}
func (c *healthCache) Prune(context.Context) (int, error) { return 0, nil }
func (c *healthCache) Close() error                       { return nil }
func (c *healthCache) CheckWritable() error               { return c.writableErr }

type diagLimiter struct {
	*fakeLimiter
	diag resilience.LimiterDiagnostics
}

func (l *diagLimiter) Diagnostics(string) resilience.LimiterDiagnostics { return l.diag }

func healthRequest(t *testing.T, probe bool) *mcp.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(healthInput{ProbeUpstream: &probe})
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}}
}

func invokeHealth(t *testing.T, deps *Deps, probe bool) *mcp.CallToolResult {
	t.Helper()
	return handleHealth(context.Background(), deps, healthRequest(t, probe))
}

func decodeHealth(t *testing.T, res *mcp.CallToolResult) healthOutput {
	t.Helper()
	var out healthOutput
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("解析 health = %v", err)
	}
	return out
}

func healthCheckNamed(t *testing.T, out *healthOutput, name string) healthCheck {
	t.Helper()
	for _, check := range out.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("缺少检查项 %s", name)
	return healthCheck{}
}

func healthyDeps(t *testing.T, client *fakeClient) Deps {
	t.Helper()
	cfg := config.Default()
	cfg.ApiKey = "sk-test"
	cfg.Cache.Enabled = true
	clk := resilience.SystemClock()
	return Deps{
		Cfg:       cfg,
		Client:    client,
		Cache:     &healthCache{entries: 0, bytes: 0},
		Clock:     clk,
		StartedAt: clk.Now().Add(-65 * time.Second),
		Build:     domain.BuildInfo{Version: "v0.1.0", Commit: "abc", BuildTime: "2026-10-21T00:00:00Z", GoVersion: "go1.27"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()

	t.Run("默认8项检查且快速", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		start := time.Now()
		res := invokeHealth(t, &deps, false)
		elapsed := time.Since(start)
		if elapsed > 50*time.Millisecond {
			t.Fatalf("健康检查耗时 %v > 50ms", elapsed)
		}
		out := decodeHealth(t, res)
		if res.IsError || out.Status != "ok" || len(out.Checks) != 8 {
			t.Fatalf("status/checks/isError = %q/%d/%v", out.Status, len(out.Checks), res.IsError)
		}
		for _, check := range out.Checks {
			if !check.Ok {
				t.Fatalf("检查项失败: %+v", check)
			}
		}
		if out.Build.UptimeS != 65 || out.Build.Version != "v0.1.0" {
			t.Fatalf("build = %+v", out.Build)
		}
		if out.Limits.MaxInflight != deps.Cfg.RateLimit.MaxInflight {
			t.Fatalf("limits = %+v", out.Limits)
		}
	})

	t.Run("缓存只读时降级不报错", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		deps.Cache = &healthCache{writableErr: errors.New("目录只读"), entries: 1}
		res := invokeHealth(t, &deps, false)
		out := decodeHealth(t, res)
		check := healthCheckNamed(t, &out, "cache_writable")
		if check.Ok || out.Status != "degraded" || res.IsError {
			t.Fatalf("cache_writable/status/isError = %v/%q/%v", check.Ok, out.Status, res.IsError)
		}
	})

	t.Run("ApiKey为空", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		deps.Cfg.ApiKey = ""
		out := decodeHealth(t, invokeHealth(t, &deps, false))
		if healthCheckNamed(t, &out, "api_key_present").Ok {
			t.Fatal("空 ApiKey 应判失败")
		}
	})

	t.Run("effective_config不泄露密钥", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		deps.Cfg.ApiKey = "sk-secret"
		deps.Cfg.HttpToken = "token-secret"
		text := resultText(t, invokeHealth(t, &deps, false))
		if strings.Contains(text, "sk-secret") || strings.Contains(text, "token-secret") {
			t.Fatalf("健康输出泄露密钥: %s", text)
		}
	})

	t.Run("probe_upstream默认不探测", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{}
		deps := healthyDeps(t, client)
		out := decodeHealth(t, invokeHealth(t, &deps, false))
		if client.pingCount() != 0 {
			t.Fatalf("pingCalls = %d, want 0", client.pingCount())
		}
		if detail := healthCheckNamed(t, &out, "upstream_reachable").Detail; detail != "未探测" {
			t.Fatalf("detail = %q", detail)
		}
	})

	t.Run("probe_upstream显式探测", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{}
		deps := healthyDeps(t, client)
		out := decodeHealth(t, invokeHealth(t, &deps, true))
		if client.pingCount() != 1 {
			t.Fatalf("pingCalls = %d, want 1", client.pingCount())
		}
		if !healthCheckNamed(t, &out, "upstream_reachable").Ok {
			t.Fatal("405 应判健康")
		}
	})

	t.Run("Ping404指向base_url", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{pingFn: func(context.Context) error {
			return fmt.Errorf("ping: %w", domain.NewUpstreamError(404, "req", "not found", nil))
		}}
		deps := healthyDeps(t, client)
		out := decodeHealth(t, invokeHealth(t, &deps, true))
		check := healthCheckNamed(t, &out, "upstream_reachable")
		if check.Ok || !strings.Contains(check.Detail, "base_url") {
			t.Fatalf("detail = %q", check.Detail)
		}
	})

	t.Run("Ping网络错误是2998", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{pingFn: func(context.Context) error {
			return fmt.Errorf("ping: %w", domain.NewNetworkError("attempt_timeout", 1, errors.New("timeout")))
		}}
		deps := healthyDeps(t, client)
		out := decodeHealth(t, invokeHealth(t, &deps, true))
		detail := healthCheckNamed(t, &out, "upstream_reachable").Detail
		if !strings.Contains(detail, "2998") || strings.Contains(detail, "2999") {
			t.Fatalf("detail = %q", detail)
		}
	})

	t.Run("熔断Open判失败", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		deps.Circuit = &fakeCircuit{state: "open"}
		res := invokeHealth(t, &deps, false)
		out := decodeHealth(t, res)
		if healthCheckNamed(t, &out, "circuit_state").Ok || out.Status != "degraded" || res.IsError {
			t.Fatalf("circuit/status/isError = %+v/%q/%v", out.Checks, out.Status, res.IsError)
		}
	})

	t.Run("令牌为0但无排队", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		deps.Limiter = &diagLimiter{
			fakeLimiter: &fakeLimiter{},
			diag: resilience.LimiterDiagnostics{
				Key: "search", Inflight: 4, Quota: 4,
				Waiters: 0, EstimatedWaitMs: 0, Stage: "inflight",
			},
		}
		out := decodeHealth(t, invokeHealth(t, &deps, false))
		if !healthCheckNamed(t, &out, "limiter_state").Ok {
			t.Fatal("无排队时不应因令牌为 0 判失败")
		}
	})

	t.Run("缓存关闭是合法配置", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		deps.Cfg.Cache.Enabled = false
		out := decodeHealth(t, invokeHealth(t, &deps, false))
		check := healthCheckNamed(t, &out, "cache_stats")
		if !check.Ok || !strings.Contains(check.Detail, "缓存已关闭") {
			t.Fatalf("cache_stats = %+v", check)
		}
	})

	t.Run("坏时区判失败", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		deps.Cfg.Log.Timezone = "No/Such_Zone"
		out := decodeHealth(t, invokeHealth(t, &deps, false))
		if healthCheckNamed(t, &out, "clock_tz").Ok {
			t.Fatal("坏时区应判失败")
		}
	})

	t.Run("8项全失败才IsError", func(t *testing.T) {
		t.Parallel()
		deps := healthyDeps(t, &fakeClient{})
		checks := make([]healthCheck, 8)
		for i := range checks {
			checks[i] = healthCheck{Name: fmt.Sprintf("c%d", i), Ok: false, Detail: "fail"}
		}
		h := &healthHandler{deps: &deps}
		res := h.render(checks)
		if !res.IsError {
			t.Fatal("8 项全失败必须 IsError=true")
		}
		out := decodeHealth(t, res)
		if len(out.Checks) != 8 || out.Status != "degraded" {
			t.Fatalf("checks/status = %d/%q", len(out.Checks), out.Status)
		}
	})

	t.Run("工具注解与Title", func(t *testing.T) {
		t.Parallel()
		def := healthToolDefinition()
		if def.Title != "DeepSeek 健康诊断" || def.Name != "ds_health" {
			t.Fatalf("name/title = %q/%q", def.Name, def.Title)
		}
		if def.Annotations == nil || !def.Annotations.ReadOnlyHint || !def.Annotations.IdempotentHint {
			t.Fatal("ReadOnly/Idempotent 注解缺失")
		}
		if def.Annotations.OpenWorldHint == nil || !*def.Annotations.OpenWorldHint {
			t.Fatal("OpenWorldHint 必须为 true")
		}
	})
}
