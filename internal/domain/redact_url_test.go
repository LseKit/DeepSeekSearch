// 文件名: redact_url_test.go
// 用途: 验证 RedactURL 无条件剥掉 userinfo（凭据会在校验前就进 call.begin 日志）。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import "testing"

func TestRedactURLStripsUserInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		keys []string
		want string
	}{
		//nolint:gosec // 假凭据是测试输入，断言的正是它被剥掉
		{name: "带密码且无 query", in: "http://user:secret@example.com/a", want: "http://example.com/a"},
		//nolint:gosec // 同上
		{
			name: "带密码且 query 命中", in: "http://user:secret@example.com/a?token=abc&x=1",
			keys: []string{"token"}, want: "http://example.com/a?token=***&x=1",
		},
		{name: "只有用户名", in: "https://alice@example.com/", want: "https://example.com/"},
		{name: "无凭据无命中时原样返回", in: "https://example.com/a?x=1", want: "https://example.com/a?x=1"},
		//nolint:gosec // 同上
		{name: "keys 为空也要剥凭据", in: "https://alice:pw@example.com/", keys: nil, want: "https://example.com/"},
		{name: "解析失败原样返回", in: "://bad", want: "://bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RedactURL(tt.in, tt.keys); got != tt.want {
				t.Fatalf("RedactURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
