// 文件名: registry.go
// 用途: 定义 Tools 层依赖、Tool 接口、稳定的注册顺序与统一的错误结果渲染。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/deepseek"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/health"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

// Deps 是工具层需要的全部外部依赖。用结构体显式注入，MUST NOT 用全局变量。
type Deps struct {
	Cfg          config.Config
	Client       deepseek.Client
	SearchClient deepseek.Client
	FetchClient  deepseek.Client
	Limiter      resilience.Limiter
	Retry        resilience.Retry
	SearchRetry  resilience.Retry
	FetchRetry   resilience.Retry
	Budget       resilience.Budget
	Cache        resilience.Cache
	Singleflight resilience.Singleflight
	Circuit      resilience.Circuit
	Clock        resilience.Clock
	StartedAt    time.Time
	Usage        *health.UsageTracker
	Build        domain.BuildInfo
	Logger       *slog.Logger
}

func (d *Deps) clientFor(tool string) deepseek.Client {
	if tool == "fetch" && d.FetchClient != nil {
		return d.FetchClient
	}
	if tool == "search" && d.SearchClient != nil {
		return d.SearchClient
	}
	return d.Client
}

func (d *Deps) retryFor(tool string) resilience.Retry {
	if tool == "fetch" && d.FetchRetry != nil {
		return d.FetchRetry
	}
	if tool == "search" && d.SearchRetry != nil {
		return d.SearchRetry
	}
	return d.Retry
}

func checkBudget(b resilience.Budget, key string) error {
	if b == nil {
		return nil
	}
	if err := b.CheckWindow(key); err != nil {
		return fmt.Errorf("check window: %w", err)
	}
	if err := b.CheckDaily(key); err != nil {
		return fmt.Errorf("check daily: %w", err)
	}
	return nil
}

func recordBudget(b resilience.Budget, key string) {
	if b != nil {
		b.Record(key, 1)
	}
}

// Tool 是一个可注册的 MCP 工具。新增工具只需实现本接口并在 Registry 追加。
type Tool interface {
	// Name 返回 MCP 工具名，snake_case。
	Name() string
	// Register 把自己的 schema、annotations 与 handler 注册到 server。
	Register(srv *mcp.Server, deps Deps)
}

// healthTool 把 internal/health 的注册函数适配成 tools.Tool。
// 适配层留在本包：health 不需要知道 tools.Deps，避免反向依赖成环。
type healthTool struct{}

// Name 返回 MCP 工具名。
func (t *healthTool) Name() string { return "ds_health" }

// Register 把 ds_health 注册到 server。
func (t *healthTool) Register(srv *mcp.Server, deps Deps) { //nolint:gocritic // 签名与 MCP 工具注册接口保持一致
	health.Register(srv, health.Deps{
		Cfg:       deps.Cfg,
		Client:    deps.clientFor("search"),
		Limiter:   deps.Limiter,
		Circuit:   deps.Circuit,
		Cache:     deps.Cache,
		Clock:     deps.Clock,
		StartedAt: deps.StartedAt,
		Usage:     deps.Usage,
		Build:     deps.Build,
		Logger:    deps.Logger,
	})
}

// Registry 返回全部待注册工具。
// 字面量顺序本身就是 Name 字典序，保证 tools/list 两次请求完全一致。
func Registry() []Tool {
	return []Tool{&extractTool{}, &fetchTool{}, &healthTool{}, &researchTool{}, &searchTool{}}
}

// RegisterAll 遍历注册全部工具与 MCP Resource。
func RegisterAll(srv *mcp.Server, deps Deps) error { //nolint:gocritic // 签名与 MCP 工具注册接口保持一致
	// 在这里补默认依赖：deps 是值拷贝，改动不会回写调用方，也不会被并发请求共享。
	if deps.Clock == nil {
		deps.Clock = resilience.SystemClock()
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	for _, tool := range Registry() {
		tool.Register(srv, deps)
	}
	return registerResources(srv, deps)
}

const fallbackInternalPayload = `{"code":3099,"name":"LOC_INTERNAL","message":"内部错误，属于本工具自身缺陷","hint":"带上 call_id 与复现步骤提交 issue；不要原样重试。","retryable":false}`

// errorResult 把任意错误渲染成 MCP 工具错误结果。
// MUST NOT 返回 nil, err：SDK 会把它包装成未分类协议错误，调用方拿不到 code。
func errorResult(err error, tool string) *mcp.CallToolResult {
	raw, marshalErr := json.Marshal(domain.ToPayload(err, tool))
	text := fallbackInternalPayload
	if marshalErr == nil {
		text = string(raw)
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func boolPtr(b bool) *bool { return &b }
