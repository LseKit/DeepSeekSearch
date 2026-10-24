// 文件名: mcp_test.go
// 用途: 覆盖 mcputil 的入参解析、URL 列表校验与文本结果封装。
// 创建日期: 2026-10-24
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package mcputil

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func TestParseInput(t *testing.T) {
	t.Parallel()

	var out struct {
		Query string `json:"query"`
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"query":"go"}`)}}
	if err := ParseInput(req, &out); err != nil {
		t.Fatalf("ParseInput() = %v", err)
	}
	if out.Query != "go" {
		t.Fatalf("Query = %q, want go", out.Query)
	}
	if err := ParseInput(nil, &out); domain.CodeOf(err) != domain.CodeValInvalidParam {
		t.Fatalf("nil request code = %d, want %d", domain.CodeOf(err), domain.CodeValInvalidParam)
	}
	if err := ParseInput(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{`)}}, &out); domain.CodeOf(err) != domain.CodeValInvalidParam {
		t.Fatalf("bad json code = %d, want %d", domain.CodeOf(err), domain.CodeValInvalidParam)
	}
}

func TestValidateURLList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		urls    []string
		limit   int
		wantErr bool
	}{
		{name: "空列表", urls: nil, limit: 3, wantErr: true},
		{name: "超上限", urls: []string{"https://a", "https://b"}, limit: 1, wantErr: true},
		{name: "含空串", urls: []string{"https://a", "  "}, limit: 3, wantErr: true},
		{name: "合法", urls: []string{"https://a"}, limit: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateURLList(tt.urls, tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateURLList() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && domain.CodeOf(err) != domain.CodeValInvalidParam {
				t.Fatalf("code = %d, want %d", domain.CodeOf(err), domain.CodeValInvalidParam)
			}
		})
	}
}

func TestRequest(t *testing.T) {
	t.Parallel()

	req, err := Request(map[string]string{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("Request() = %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(req.Params.Arguments, &got); err != nil {
		t.Fatalf("Arguments 不是合法 JSON: %v", err)
	}
	if got["url"] != "https://example.com" {
		t.Fatalf("url = %q", got["url"])
	}
}

func TestResultText(t *testing.T) {
	t.Parallel()

	if got := ResultText(nil); got != "" {
		t.Fatalf("nil ResultText() = %q", got)
	}
	if got := ResultText(&mcp.CallToolResult{}); got != "" {
		t.Fatalf("empty ResultText() = %q", got)
	}
	res := TextResult("hello")
	if got := ResultText(res); got != "hello" {
		t.Fatalf("ResultText() = %q, want hello", got)
	}
	if got := ResultText(&mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{}}}); got != "" {
		t.Fatalf("非文本 ResultText() = %q", got)
	}
}

func TestTextResult(t *testing.T) {
	t.Parallel()

	res := TextResult("hello")
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("TextResult() = %#v", res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "hello" {
		t.Fatalf("TextResult 内容 = %#v", res.Content[0])
	}
}
