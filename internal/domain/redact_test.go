// 文件名: redact_test.go
// 用途: 校验 URL query 脱敏的大小写不敏感、精确匹配与失败原样返回。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import "testing"

func TestRedactURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		keys []string
		want string
	}{
		{
			name: "大小写不敏感并保持顺序",
			raw:  "https://x/cb?token=abc&Token=def&keep=1",
			keys: []string{"token"},
			want: "https://x/cb?token=***&Token=***&keep=1",
		},
		{
			name: "keys 为 nil 原样返回",
			raw:  "https://x/a?b=1",
			want: "https://x/a?b=1",
		},
		{
			name: "非法 URL 原样返回且不 panic",
			raw:  "not a url",
			keys: []string{"token"},
			want: "not a url",
		},
		{
			name: "精确匹配不误伤 sig2",
			raw:  "https://x/?sig=abc&sig2=def",
			keys: []string{"sig"},
			want: "https://x/?sig=***&sig2=def",
		},
		{
			name: "同名键全部替换",
			raw:  "https://x/a?token=abc&token=def&x=1",
			keys: []string{"token"},
			want: "https://x/a?token=***&token=***&x=1",
		},
		{
			name: "大写键命中",
			raw:  "https://x/a?TOKEN=abc",
			keys: []string{"token"},
			want: "https://x/a?TOKEN=***",
		},
		{
			name: "无 query 原样返回",
			raw:  "https://x/a",
			keys: []string{"token"},
			want: "https://x/a",
		},
		{
			name: "解析失败原样返回",
			raw:  "://bad",
			keys: []string{"token"},
			want: "://bad",
		},
		{
			name: "无值参数也补上掩码",
			raw:  "https://x/a?token",
			keys: []string{"token"},
			want: "https://x/a?token=***",
		},
		{
			name: "值里含等号只替换整段",
			raw:  "https://x/a?token=a=b",
			keys: []string{"token"},
			want: "https://x/a?token=***",
		},
		{
			name: "fragment 保留",
			raw:  "https://x/p?token=a#frag",
			keys: []string{"token"},
			want: "https://x/p?token=***#frag",
		},
		{
			name: "空 keys 原样返回",
			raw:  "https://x/a?token=abc",
			keys: []string{},
			want: "https://x/a?token=abc",
		},
		{
			name: "百分号编码的键名也能命中",
			raw:  "https://x/a?to%6Ben=abc",
			keys: []string{"token"},
			want: "https://x/a?to%6Ben=***",
		},
		{
			name: "只替换命中键",
			raw:  "https://x/a?token=abc&keep=1&token2=2",
			keys: []string{"token"},
			want: "https://x/a?token=***&keep=1&token2=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := RedactURL(tt.raw, tt.keys); got != tt.want {
				t.Errorf("RedactURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
