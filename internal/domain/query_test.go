// 文件名: query_test.go
// 用途: 校验 BuildQuery 的拼接顺序、操作符冲突、裸年份展开与 128 rune 告警。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"strings"
	"testing"
)

func TestBuildQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		req          SearchRequest
		want         string
		wantWarn     int
		wantContains string
	}{
		{
			name: "只传 query 原样透传",
			req:  SearchRequest{Query: "go"},
			want: "go",
		},
		{
			name: "多个 site 用 OR 连接",
			req:  SearchRequest{Query: "go", Site: []string{"a.com", "b.com"}},
			want: "go site:a.com OR site:b.com",
		},
		{
			name:     "query 已含 site 不追加并告警",
			req:      SearchRequest{Query: "go site:x.com", Site: []string{"a.com"}},
			want:     "go site:x.com",
			wantWarn: 1,
		},
		{
			name: "raw_query 绕过拼装",
			req:  SearchRequest{Query: "go", RawQuery: true, Site: []string{"a.com"}},
			want: "go",
		},
		{
			name: "含空格的 exclude_term 加引号",
			req:  SearchRequest{Query: "go", ExcludeTerms: []string{"foo bar"}},
			want: `go -"foo bar"`,
		},
		{
			name: "不含空格的 exclude_term 不加引号",
			req:  SearchRequest{Query: "go", ExcludeTerms: []string{"foo"}},
			want: "go -foo",
		},
		{
			name: "exclude_sites 逐个追加",
			req:  SearchRequest{Query: "go", ExcludeSites: []string{"a.com", "b.com"}},
			want: "go -site:a.com -site:b.com",
		},
		{
			name: "lang 逗号拼接",
			req:  SearchRequest{Query: "go", Lang: []string{"zh", "en"}},
			want: "go lang:zh,en",
		},
		{
			name:         "after 裸年份展开并告警",
			req:          SearchRequest{Query: "go", After: "2025"},
			want:         "go after:2025-01-01",
			wantWarn:     1,
			wantContains: "已展开",
		},
		{
			name: "after 完整日期不展开",
			req:  SearchRequest{Query: "go", After: "2025-01-01"},
			want: "go after:2025-01-01",
		},
		{
			name: "before 裸年份不展开",
			req:  SearchRequest{Query: "go", Before: "2025"},
			want: "go before:2025",
		},
		{
			name: "exact_phrase 加引号",
			req:  SearchRequest{Query: "go", ExactPhrase: "machine learning"},
			want: `go "machine learning"`,
		},
		{
			name: "完整优先级顺序",
			req: SearchRequest{
				Query:        "go",
				Site:         []string{"a.com"},
				ExcludeSites: []string{"b.com"},
				Lang:         []string{"zh"},
				After:        "2025-01-01",
				Before:       "2026",
				ExactPhrase:  "x y",
				ExcludeTerms: []string{"bad", "foo bar"},
			},
			want: `go site:a.com -site:b.com lang:zh after:2025-01-01 before:2026 "x y" -bad -"foo bar"`,
		},
		{
			name: "query 已含 -site 时仍可追加 site",
			req:  SearchRequest{Query: "go -site:x.com", Site: []string{"a.com"}},
			want: "go -site:x.com site:a.com",
		},
		{
			name: "空 query 追加 site 不产生前导空格",
			req:  SearchRequest{Query: "", Site: []string{"a.com"}},
			want: "site:a.com",
		},
		{
			name:     "多个冲突各记一条",
			req:      SearchRequest{Query: "go site:x.com after:2020", Site: []string{"a.com"}, After: "2025"},
			want:     "go site:x.com after:2020",
			wantWarn: 2,
		},
		{
			name:         "超过 128 rune 告警",
			req:          SearchRequest{Query: strings.Repeat("中", 129)},
			want:         strings.Repeat("中", 129),
			wantWarn:     1,
			wantContains: "128",
		},
		{
			name:         "raw_query 超长也告警",
			req:          SearchRequest{Query: strings.Repeat("中", 129), RawQuery: true},
			want:         strings.Repeat("中", 129),
			wantWarn:     1,
			wantContains: "128",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, warnings := BuildQuery(tt.req)
			if got != tt.want {
				t.Errorf("effective = %q, want %q", got, tt.want)
			}
			if len(warnings) != tt.wantWarn {
				t.Errorf("warnings = %v (len %d), want len %d", warnings, len(warnings), tt.wantWarn)
			}
			if tt.wantContains != "" {
				joined := strings.Join(warnings, "\n")
				if !strings.Contains(joined, tt.wantContains) {
					t.Errorf("warnings = %q, 缺少 %q", joined, tt.wantContains)
				}
			}
		})
	}
}

func TestBuildQueryNoWarningsIsNil(t *testing.T) {
	t.Parallel()

	effective, warnings := BuildQuery(SearchRequest{Query: "go"})
	if effective != "go" {
		t.Errorf("effective = %q, want go", effective)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want nil", warnings)
	}
}
