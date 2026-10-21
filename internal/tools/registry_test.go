// 文件名: registry_test.go
// 用途: 验证工具注册顺序稳定、ds_search 注解与 description 禁令、schema 与契约字面量逐字节一致。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	t.Parallel()

	want := []string{"ds_fetch", "ds_health", "ds_search"}
	for round := range 3 {
		got := make([]string, 0, 3)
		for _, tool := range Registry() {
			got = append(got, tool.Name())
		}
		if !slices.Equal(got, want) {
			t.Fatalf("第 %d 次 Registry() = %v, want %v", round+1, got, want)
		}
	}
}

func TestSearchSchema(t *testing.T) {
	t.Parallel()

	def := searchToolDefinition()
	if def.Name != "ds_search" {
		t.Fatalf("Name = %q", def.Name)
	}
	if def.Title != "DeepSeek 网页搜索" {
		t.Fatalf("Title = %q", def.Title)
	}
	if def.Annotations == nil {
		t.Fatal("Annotations 为 nil")
	}
	if !def.Annotations.ReadOnlyHint || !def.Annotations.IdempotentHint {
		t.Fatalf("ReadOnly/Idempotent = %v/%v", def.Annotations.ReadOnlyHint, def.Annotations.IdempotentHint)
	}
	if def.Annotations.OpenWorldHint == nil || !*def.Annotations.OpenWorldHint {
		t.Fatal("OpenWorldHint 必须为非 nil 的 true")
	}

	desc := def.Description
	if strings.Contains(desc, "1xxx") || strings.Contains(desc, "UPPER_SNAKE") {
		t.Fatal("工具级 description 不应含错误码表")
	}
	if !strings.Contains(desc, "<untrusted_content>") {
		t.Fatal("description 缺少 untrusted_content 规则")
	}
	if !strings.Contains(desc, "total") || !strings.Contains(desc, "results") {
		t.Fatal("description 缺少 total/results 截断说明")
	}

	raw, ok := def.InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("InputSchema 类型 = %T, want json.RawMessage", def.InputSchema)
	}
	wantRaw, err := os.ReadFile(filepath.Join("testdata", "search_input_schema.json"))
	if err != nil {
		t.Fatalf("读取 schema 黄金文件 = %v", err)
	}
	if !bytes.Equal(raw, wantRaw) {
		t.Fatalf("schema 与契约字面量不一致\n got=%s\nwant=%s", raw, wantRaw)
	}
	if bytes.Contains(raw, []byte("anyOf")) || bytes.Contains(raw, []byte("oneOf")) {
		t.Fatal("schema MUST NOT 使用 anyOf/oneOf")
	}

	var parsed struct {
		Type                 string `json:"type"`
		AdditionalProperties bool   `json:"additionalProperties"`
		Required             []string
		Properties           map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("schema JSON 解析 = %v", err)
	}
	if parsed.Type != "object" || parsed.AdditionalProperties {
		t.Fatalf("schema type/additionalProperties = %q/%v", parsed.Type, parsed.AdditionalProperties)
	}
	if len(parsed.Properties) != 19 {
		t.Fatalf("属性数 = %d, want 19", len(parsed.Properties))
	}
	if !slices.Equal(parsed.Required, []string{"query"}) {
		t.Fatalf("required = %v, want [query]", parsed.Required)
	}
	afterDesc := parsed.Properties["after"].Description
	if !strings.Contains(afterDesc, "展开") || !strings.Contains(afterDesc, "2025-01-01") {
		t.Fatalf("after description 缺少年份展开陷阱: %q", afterDesc)
	}
	if !strings.Contains(parsed.Properties["min_rerank"].Description, "可能返回 0 条") {
		t.Fatal("min_rerank description 缺少长尾清空警告")
	}
	if strings.Contains(parsed.Properties["ori_question"].Description, "提升") {
		t.Fatal("ori_question description MUST NOT 声称能提升效果")
	}
	if !strings.Contains(parsed.Properties["synthesize"].Description, "1095") {
		t.Fatal("synthesize description 缺少 1095")
	}
}
