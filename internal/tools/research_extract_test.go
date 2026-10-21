// 文件名: research_extract_test.go
// 用途: 验证 ds_research / ds_extract 的组合编排、部分失败语义与参数校验。
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

func toolCall(t *testing.T, payload any) *mcp.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}}
}

func decodePayload(t *testing.T, res *mcp.CallToolResult) domain.ErrorPayload {
	t.Helper()
	var p domain.ErrorPayload
	if err := json.Unmarshal([]byte(resultText(t, res)), &p); err != nil {
		t.Fatalf("解析错误体 = %v", err)
	}
	return p
}

func TestResearch(t *testing.T) {
	t.Parallel()

	searchOK := func(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
		return domain.SearchResponse{Results: fixtureResults(3)}, nil
	}
	fetchOK := func(context.Context, domain.FetchRequest) (domain.FetchResponse, error) {
		return domain.FetchResponse{Title: "T", Content: strings.Repeat("a", 600)}, nil
	}

	t.Run("三条来源全部成功", func(t *testing.T) {
		t.Parallel()
		deps, _ := newTestDeps(t, &fakeClient{searchFn: searchOK, fetchFn: fetchOK}, nil)
		res := handleResearch(context.Background(), &deps, toolCall(t, researchInput{Question: "go context"}))
		if res.IsError {
			t.Fatalf("研究失败: %s", resultText(t, res))
		}
		var out researchOutput
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatalf("解析输出 = %v", err)
		}
		if len(out.Sources) != 3 || !out.Sources[0].Ok || out.Sources[0].Index != 1 {
			t.Fatalf("sources = %+v", out.Sources)
		}
		if !strings.Contains(out.Bundle, "## [1]") || !strings.Contains(out.Bundle, "## [3]") {
			t.Fatalf("bundle 缺少编号: %s", out.Bundle)
		}
		if len(out.Warnings) != 0 {
			t.Fatalf("warnings = %v, want empty", out.Warnings)
		}
	})

	t.Run("单条失败不影响整体", func(t *testing.T) {
		t.Parallel()
		fetchPartial := func(_ context.Context, req domain.FetchRequest) (domain.FetchResponse, error) {
			if strings.Contains(req.Url, "site1") {
				return domain.FetchResponse{}, domain.NewLocalError(domain.CodeValSSRFBlocked, "模拟失败", nil)
			}
			return domain.FetchResponse{Title: "T", Content: strings.Repeat("a", 600)}, nil
		}
		deps, _ := newTestDeps(t, &fakeClient{searchFn: searchOK, fetchFn: fetchPartial}, nil)
		res := handleResearch(context.Background(), &deps, toolCall(t, researchInput{Question: "go"}))
		if res.IsError {
			t.Fatal("部分失败不应使整体失败")
		}
		var out researchOutput
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatalf("解析输出 = %v", err)
		}
		if out.Sources[1].Ok || len(out.Warnings) != 1 {
			t.Fatalf("sources[1]/warnings = %+v/%v", out.Sources[1], out.Warnings)
		}
		if !strings.Contains(out.Warnings[0], "来源 [2]") {
			t.Fatalf("warning = %q", out.Warnings[0])
		}
	})

	t.Run("参数校验", func(t *testing.T) {
		t.Parallel()
		deps, _ := newTestDeps(t, &fakeClient{}, nil)
		res := handleResearch(context.Background(), &deps, toolCall(t, researchInput{Question: "   "}))
		if !res.IsError {
			t.Fatal("空白 question 应报错")
		}
		if p := decodePayload(t, res); p.Details["field"] != "question" {
			t.Fatalf("payload = %+v", p)
		}
	})

	t.Run("工具定义", func(t *testing.T) {
		t.Parallel()
		def := researchDefinition()
		if def.Name != researchToolName || def.Annotations == nil || def.Annotations.OpenWorldHint == nil || !*def.Annotations.OpenWorldHint {
			t.Fatalf("def = %+v", def)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		raw, ok := def.InputSchema.(json.RawMessage)
		if !ok {
			t.Fatalf("InputSchema 类型 = %T", def.InputSchema)
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("schema = %v", err)
		}
		if len(schema.Properties) != 3 {
			t.Fatalf("属性数 = %d, want 3", len(schema.Properties))
		}
	})
}

func TestExtract(t *testing.T) {
	t.Parallel()

	t.Run("批量抓取两条", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{fetchFn: func(context.Context, domain.FetchRequest) (domain.FetchResponse, error) {
			return domain.FetchResponse{Title: "T", Content: strings.Repeat("a", 600)}, nil
		}}
		deps, _ := newTestDeps(t, client, nil)
		res := handleExtract(context.Background(), &deps, toolCall(t, extractInput{URLs: []string{
			"https://a.example.com/", "https://b.example.com/",
		}}))
		if res.IsError {
			t.Fatalf("批量抓取失败: %s", resultText(t, res))
		}
		var out extractOutput
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatalf("解析输出 = %v", err)
		}
		if len(out.Items) != 2 || !out.Items[0].Ok || !out.Items[1].Ok {
			t.Fatalf("items = %+v", out.Items)
		}
		if out.Items[0].Chars == 0 || out.Items[0].Content == "" {
			t.Fatalf("item[0] = %+v", out.Items[0])
		}
	})

	t.Run("单条失败只影响该条", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{fetchFn: func(_ context.Context, req domain.FetchRequest) (domain.FetchResponse, error) {
			if strings.Contains(req.Url, "bad") {
				return domain.FetchResponse{}, domain.NewLocalError(domain.CodeValSSRFBlocked, "模拟失败", nil)
			}
			return domain.FetchResponse{Title: "T", Content: strings.Repeat("a", 600)}, nil
		}}
		deps, _ := newTestDeps(t, client, nil)
		res := handleExtract(context.Background(), &deps, toolCall(t, extractInput{URLs: []string{
			"https://good.example.com/", "https://bad.example.com/",
		}}))
		if res.IsError {
			t.Fatal("单条失败不应使整体失败")
		}
		var out extractOutput
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatalf("解析输出 = %v", err)
		}
		if !out.Items[0].Ok || out.Items[1].Ok || out.Items[1].Error == nil {
			t.Fatalf("items = %+v", out.Items)
		}
		if len(out.Warnings) != 1 {
			t.Fatalf("warnings = %v", out.Warnings)
		}
	})

	t.Run("参数校验", func(t *testing.T) {
		t.Parallel()
		deps, _ := newTestDeps(t, &fakeClient{}, nil)
		for _, in := range []extractInput{
			{URLs: nil},
			{URLs: make([]string, 11)},
			{URLs: []string{""}},
		} {
			if res := handleExtract(context.Background(), &deps, toolCall(t, in)); !res.IsError {
				t.Fatalf("in = %+v 应被拒绝", in)
			}
		}
	})

	t.Run("工具定义", func(t *testing.T) {
		t.Parallel()
		def := extractDefinition()
		if def.Name != extractToolName {
			t.Fatalf("name = %q", def.Name)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		raw, ok := def.InputSchema.(json.RawMessage)
		if !ok {
			t.Fatalf("InputSchema 类型 = %T", def.InputSchema)
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("schema = %v", err)
		}
		if len(schema.Properties) != 3 {
			t.Fatalf("属性数 = %d, want 3", len(schema.Properties))
		}
	})
}
