// 文件名: version_test.go
// 用途: 覆盖 EffectiveVersion 的注入优先/兜底生效/全空三条断言（Contract/17 §八 V5/V6/V7）。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

import "testing"

func TestEffectiveVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		injected string
		fallback string
		want     string
	}{
		{name: "注入值优先于兜底值", injected: "v0.1.0", fallback: "v9.9.9", want: "v0.1.0"},
		{name: "未注入时用兜底值", injected: "v0.0.0-dev", fallback: "v9.9.9", want: "v9.9.9"},
		{name: "都为空时返回开发版标识", injected: "", fallback: "", want: "v0.0.0-dev"},
		{name: "空字符串注入视为未注入", injected: "", fallback: "v1.2.3", want: "v1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := EffectiveVersion(tt.injected, tt.fallback); got != tt.want {
				t.Fatalf("EffectiveVersion(%q, %q) = %q, want %q",
					tt.injected, tt.fallback, got, tt.want)
			}
		})
	}
}
