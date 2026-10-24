// 文件名: mcp.go
// 用途: 实现入参解析、URL 列表校验、子调用请求构造与文本结果读写的五个薄封装。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package mcputil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// ParseInput 把工具入参解析到 out；缺参数、非法 JSON、类型不符或含未知键都归 1000（字段 arguments）。
//
// MUST 用 DisallowUnknownFields：schema 里写了 additionalProperties:false，但 SDK 不强制、
// encoding/json 默认也静默忽略未知键。调用方把 count 拼成 counts 时会拿到「默认值 + 零报错」
// 的结果 —— 「给了参数但不生效」正是本项目批评上游的那条罪状，MUST NOT 自己犯。
func ParseInput(req *mcp.CallToolRequest, out any) error {
	if req == nil || req.Params == nil {
		return fmt.Errorf("arguments: %w", domain.NewValidationError(domain.CodeValInvalidParam, "arguments", "缺少请求参数"))
	}
	dec := json.NewDecoder(bytes.NewReader(req.Params.Arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("arguments: %w", domain.NewValidationError(domain.CodeValInvalidParam, "arguments", decodeDetail(err)))
	}
	return nil
}

// decodeDetail 把解码错误翻成调用方能直接照着改的中文。
// 未知键的键名与合法键清单由编码库给出，原样带上比自行解析错误文本更可靠。
func decodeDetail(err error) string {
	var typo *json.UnmarshalTypeError
	if errors.As(err, &typo) && typo.Field != "" {
		return "字段 " + typo.Field + " 的类型不合法"
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "请求参数不是合法 JSON"
	}
	return "请求参数含未知键：" + strings.TrimPrefix(err.Error(), "json: ")
}

// ValidateURLList 校验组合工具的 URL 列表：数量与空串。
func ValidateURLList(urls []string, limit int) error {
	if len(urls) < 1 || len(urls) > limit {
		return fmt.Errorf("urls: %w", domain.NewValidationError(domain.CodeValInvalidParam, "urls", fmt.Sprintf("必须 1~%d 个 URL", limit)))
	}
	for i, url := range urls {
		if strings.TrimSpace(url) == "" {
			return fmt.Errorf("urls[%d]: %w", i, domain.NewValidationError(domain.CodeValInvalidParam, fmt.Sprintf("urls[%d]", i), "URL 不能为空"))
		}
	}
	return nil
}

// Request 用任意 payload 构造一次子调用请求（组合工具复用现有 handler 时用）。
func Request(payload any) (*mcp.CallToolRequest, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化子调用参数: %w", err)
	}
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}}, nil
}

// ResultText 取出工具结果的文本；结构不符时返回空串（调用方按解析失败处理）。
func ResultText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return text.Text
}

// TextResult 用文本构造一次成功的工具结果。
func TextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
