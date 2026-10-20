// 文件名: stubs.go
// 用途: 为 Task-09/10 的工具与 Resource 提供最小占位实现，保证注册表三工具可用。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

type fetchTool struct{}

func (t *fetchTool) Name() string { return "ds_fetch" }

func (t *fetchTool) Register(srv *mcp.Server, _ Deps) { //nolint:gocritic // 签名由 11 号 §五 固定
	registerStub(srv, "ds_fetch", "DeepSeek 网页抓取")
}

type healthTool struct{}

func (t *healthTool) Name() string { return "ds_health" }

func (t *healthTool) Register(srv *mcp.Server, _ Deps) { //nolint:gocritic // 签名由 11 号 §五 固定
	registerStub(srv, "ds_health", "DeepSeek 本地诊断")
}

func registerStub(srv *mcp.Server, name, title string) {
	tool := &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: "该工具尚未实装，调用会返回 3099。",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}
	srv.AddTool(tool, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := domain.NewLocalError(domain.CodeLocInternal, name+" 尚未实装", map[string]any{"tool": name})
		return errorResult(err, name), nil
	})
}

// registerResources 由 Task-10 A-02 实装；当前是保持 RegisterAll 契约完整的占位。
func registerResources(_ *mcp.Server, _ *Deps) error { return nil }
