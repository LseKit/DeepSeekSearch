// 文件名: attempt_test.go
// 用途: 验证尝试序号经 context 传递的缺省与越界语义。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"context"
	"testing"
)

func TestContextWithAttempt(t *testing.T) {
	t.Parallel()

	ctx := ContextWithAttempt(context.Background(), 3)
	if got := AttemptFromContext(ctx); got != 3 {
		t.Fatalf("AttemptFromContext() = %d, want 3", got)
	}
}

func TestAttemptFromContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{name: "nil ctx 返回 1", ctx: nil, want: 1},
		{name: "无值返回 1", ctx: context.Background(), want: 1},
		{name: "非法值返回 1", ctx: ContextWithAttempt(context.Background(), 0), want: 1},
		{name: "正常值", ctx: ContextWithAttempt(context.Background(), 2), want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := AttemptFromContext(tt.ctx); got != tt.want {
				t.Fatalf("AttemptFromContext() = %d, want %d", got, tt.want)
			}
		})
	}
}
