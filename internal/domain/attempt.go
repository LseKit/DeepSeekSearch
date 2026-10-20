// 文件名: attempt.go
// 用途: 通过 context 传递「第几次尝试」，作为 deepseek 与 resilience 的共同叶子依赖。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import "context"

type attemptKey struct{}

// ContextWithAttempt 把 1 起算的尝试序号写入 ctx。
func ContextWithAttempt(ctx context.Context, n int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if n < 1 {
		n = 1
	}
	return context.WithValue(ctx, attemptKey{}, n)
}

// AttemptFromContext 读取尝试序号；无值或非法值时返回 1。
func AttemptFromContext(ctx context.Context) int {
	if ctx == nil {
		return 1
	}
	if n, ok := ctx.Value(attemptKey{}).(int); ok && n >= 1 {
		return n
	}
	return 1
}
