// 文件名: search_validate_test.go
// 用途: 验证 ds_search 的 18 个参数校验、默认值、语法拼装、年份展开与软告警。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"strings"
	"testing"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func strPtr(s string) *string     { return &s }
func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }

func baseInput() *searchInput { return &searchInput{Query: "go"} }

func validate(t *testing.T, in *searchInput) (searchParams, error) {
	t.Helper()
	cfg := config.Default()
	return validateSearch(in, &cfg)
}

func requireCode(t *testing.T, in *searchInput, code int, field string) {
	t.Helper()
	_, err := validate(t, in)
	if err == nil {
		t.Fatalf("validateSearch() = nil, want code %d", code)
	}
	if got := domain.CodeOf(err); got != code {
		t.Fatalf("code = %d, want %d (err=%v)", got, code, err)
	}
	if field != "" && !strings.Contains(err.Error(), field) {
		t.Fatalf("错误文案缺少字段 %q: %v", field, err)
	}
}

func TestSearchValidate(t *testing.T) {
	t.Parallel()

	t.Run("默认值", func(t *testing.T) {
		t.Parallel()
		params, err := validate(t, baseInput())
		if err != nil {
			t.Fatalf("validateSearch() = %v", err)
		}
		if params.Count != 10 || params.Detail != "summary" || params.MaxPerDomain != 3 {
			t.Fatalf("默认值 = count:%d detail:%q max:%d", params.Count, params.Detail, params.MaxPerDomain)
		}
		if params.NeedContent || params.RawQuery || params.Synthesize {
			t.Fatal("布尔默认值必须为 false")
		}
		if params.EffectiveQuery != "go" {
			t.Fatalf("effective = %q", params.EffectiveQuery)
		}
	})

	t.Run("query全空白", func(t *testing.T) {
		t.Parallel()
		requireCode(t, &searchInput{Query: "   "}, domain.CodeValInvalidParam, "query")
	})

	t.Run("query129emoji超长", func(t *testing.T) {
		t.Parallel()
		requireCode(t, &searchInput{Query: strings.Repeat("😀", 129)}, domain.CodeValQueryTooLong, "query")
	})

	t.Run("query128汉字通过", func(t *testing.T) {
		t.Parallel()
		in := &searchInput{Query: strings.Repeat("汉", 128)}
		if _, err := validate(t, in); err != nil {
			t.Fatalf("128 个汉字应通过: %v", err)
		}
	})

	t.Run("count越界", func(t *testing.T) {
		t.Parallel()
		for _, n := range []int{0, 51} {
			in := baseInput()
			in.Count = intPtr(n)
			requireCode(t, in, domain.CodeValCountRange, "count")
		}
	})

	t.Run("after非法日期", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.After = strPtr("2026-13")
		requireCode(t, in, domain.CodeValDateFormat, "after")
	})

	t.Run("after裸年份展开", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.After = strPtr("2025")
		params, err := validate(t, in)
		if err != nil {
			t.Fatalf("validateSearch() = %v", err)
		}
		if !strings.Contains(params.EffectiveQuery, "after:2025-01-01") {
			t.Fatalf("effective = %q", params.EffectiveQuery)
		}
		if len(params.Warnings) != 1 || !strings.Contains(params.Warnings[0], "展开") {
			t.Fatalf("warnings = %v", params.Warnings)
		}
	})

	t.Run("before裸年份不展开", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.Before = strPtr("2025")
		params, err := validate(t, in)
		if err != nil {
			t.Fatalf("validateSearch() = %v", err)
		}
		if !strings.Contains(params.EffectiveQuery, "before:2025") ||
			strings.Contains(params.EffectiveQuery, "before:2025-01-01") {
			t.Fatalf("effective = %q", params.EffectiveQuery)
		}
	})

	t.Run("synthesize返回1095", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.Synthesize = boolPtr(true)
		requireCode(t, in, domain.CodeValSynthDisabled, "synthesize")
	})

	t.Run("lang未知只warning", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.Lang = []string{"xx"}
		params, err := validate(t, in)
		if err != nil {
			t.Fatalf("未知语言码不应报错: %v", err)
		}
		if !strings.Contains(params.EffectiveQuery, "lang:xx") {
			t.Fatalf("effective = %q", params.EffectiveQuery)
		}
		if len(params.Warnings) != 1 || !strings.Contains(params.Warnings[0], "xx") {
			t.Fatalf("warnings = %v", params.Warnings)
		}
	})

	t.Run("need_content升级detail", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.NeedContent = boolPtr(true)
		in.Detail = strPtr("snippet")
		params, err := validate(t, in)
		if err != nil {
			t.Fatalf("validateSearch() = %v", err)
		}
		if params.Detail != "content" {
			t.Fatalf("detail = %q, want content", params.Detail)
		}
	})

	t.Run("content强制need_content", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.Detail = strPtr("content")
		in.NeedContent = boolPtr(false)
		params, err := validate(t, in)
		if err != nil {
			t.Fatalf("validateSearch() = %v", err)
		}
		if !params.NeedContent {
			t.Fatal("detail=content 时 need_content 必须强制为 true")
		}
	})

	t.Run("ori_question超长", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.OriQuestion = strPtr(strings.Repeat("问", 129))
		requireCode(t, in, domain.CodeValInvalidParam, "ori_question")
	})

	t.Run("ori_question空串置nil", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.OriQuestion = strPtr("")
		params, err := validate(t, in)
		if err != nil || params.OriQuestion != nil {
			t.Fatalf("空串应置 nil: params=%v err=%v", params.OriQuestion, err)
		}
	})

	t.Run("localization校验", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{"not-an-ip", "300.1.1.1"} {
			in := baseInput()
			in.Localization = strPtr(bad)
			requireCode(t, in, domain.CodeValInvalidParam, "localization")
		}
		for _, good := range []string{"1.2.3.4", "::1"} {
			in := baseInput()
			in.Localization = strPtr(good)
			params, err := validate(t, in)
			if err != nil || params.Localization != good {
				t.Fatalf("合法 IP %q: params=%q err=%v", good, params.Localization, err)
			}
		}
	})

	t.Run("exact_phrase含引号", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.ExactPhrase = strPtr(`a"b`)
		requireCode(t, in, domain.CodeValInvalidParam, "exact_phrase")
	})

	t.Run("exclude_terms含引号", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.ExcludeTerms = []string{`a"b`}
		requireCode(t, in, domain.CodeValInvalidParam, "exclude_terms")
	})

	t.Run("site非法域名", func(t *testing.T) {
		t.Parallel()
		longLabel := strings.Repeat("a", 64) + ".com"
		tooLong := strings.Repeat(strings.Repeat("a", 63)+".", 5) + "com"
		for _, bad := range []string{".example.com", "a..b.com", "-x.com", longLabel, tooLong} {
			in := baseInput()
			in.Site = []string{bad}
			requireCode(t, in, domain.CodeValInvalidParam, "site")
		}
	})

	t.Run("site合法域名", func(t *testing.T) {
		t.Parallel()
		for _, good := range []string{"example.com", "a-b.co.uk", "sub.example.co.uk"} {
			in := baseInput()
			in.Site = []string{good}
			params, err := validate(t, in)
			if err != nil || !strings.Contains(params.EffectiveQuery, "site:"+good) {
				t.Fatalf("合法域名 %q: effective=%q err=%v", good, params.EffectiveQuery, err)
			}
		}
	})

	t.Run("所有字符串禁止换行", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			in    *searchInput
			field string
		}{
			{name: "query", in: &searchInput{Query: "go\nx"}, field: "query"},
			{name: "ori_question", in: &searchInput{Query: "go", OriQuestion: strPtr("x\n")}, field: "ori_question"},
			{name: "localization", in: &searchInput{Query: "go", Localization: strPtr("1.2.3.4\n")}, field: "localization"},
			{name: "after", in: &searchInput{Query: "go", After: strPtr("2025\n")}, field: "after"},
			{name: "before", in: &searchInput{Query: "go", Before: strPtr("2025\n")}, field: "before"},
			{name: "site", in: &searchInput{Query: "go", Site: []string{"a.com\n"}}, field: "site"},
			{name: "lang", in: &searchInput{Query: "go", Lang: []string{"en\n"}}, field: "lang"},
			{name: "exact_phrase", in: &searchInput{Query: "go", ExactPhrase: strPtr("a\n")}, field: "exact_phrase"},
			{name: "exclude_terms", in: &searchInput{Query: "go", ExcludeTerms: []string{"a\n"}}, field: "exclude_terms"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				requireCode(t, tt.in, domain.CodeValInvalidParam, tt.field)
			})
		}
	})

	t.Run("min_rerank边界", func(t *testing.T) {
		t.Parallel()
		for _, good := range []float64{0, 3} {
			in := baseInput()
			in.MinRerank = floatPtr(good)
			params, err := validate(t, in)
			if err != nil || params.MinRerank == nil || *params.MinRerank != good {
				t.Fatalf("min_rerank=%v: params=%v err=%v", good, params.MinRerank, err)
			}
		}
		for _, bad := range []float64{-0.1, 3.1} {
			in := baseInput()
			in.MinRerank = floatPtr(bad)
			requireCode(t, in, domain.CodeValInvalidParam, "min_rerank")
		}
	})

	t.Run("min_authority边界", func(t *testing.T) {
		t.Parallel()
		for _, good := range []int{0, 4} {
			in := baseInput()
			in.MinAuthority = intPtr(good)
			if _, err := validate(t, in); err != nil {
				t.Fatalf("min_authority=%d 应通过: %v", good, err)
			}
		}
		for _, bad := range []int{-1, 5} {
			in := baseInput()
			in.MinAuthority = intPtr(bad)
			requireCode(t, in, domain.CodeValInvalidParam, "min_authority")
		}
	})

	t.Run("raw_query绕过拼装", func(t *testing.T) {
		t.Parallel()
		in := &searchInput{Query: "go site:x", Site: []string{"example.com"}, RawQuery: boolPtr(true)}
		params, err := validate(t, in)
		if err != nil {
			t.Fatalf("validateSearch() = %v", err)
		}
		if params.EffectiveQuery != "go site:x" {
			t.Fatalf("effective = %q", params.EffectiveQuery)
		}
		if len(params.Warnings) != 0 {
			t.Fatalf("warnings = %v", params.Warnings)
		}
	})

	t.Run("detail非法", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.Detail = strPtr("full")
		requireCode(t, in, domain.CodeValInvalidParam, "detail")
	})

	t.Run("exact_phrase首尾空白", func(t *testing.T) {
		t.Parallel()
		in := baseInput()
		in.ExactPhrase = strPtr(" a ")
		requireCode(t, in, domain.CodeValInvalidParam, "exact_phrase")
	})
}
