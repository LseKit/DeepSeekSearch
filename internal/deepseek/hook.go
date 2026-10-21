// 文件名: hook.go
// 用途: 提供 ctx 携带的上游报文回调，供 tools 在 call_id 作用域内记录 req/resp。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import "context"

// ExchangeHook 在每次上游往返结束后调用；resp 为 nil 表示没有拿到响应体。
//
// 走 ctx 而不是 Config 全局回调：全局回调拿不到 handler 的 call_id，
// 记出来的报文无法与 call.begin/call.end 对齐（DEBT-009 的原问题）。
type ExchangeHook func(ctx context.Context, req, resp []byte)

type exchangeHookKey struct{}

// WithExchangeHook 把回调挂到 ctx 上；nil 回调等价于不挂，零开销。
func WithExchangeHook(ctx context.Context, hook ExchangeHook) context.Context {
	if hook == nil {
		return ctx
	}
	return context.WithValue(ctx, exchangeHookKey{}, hook)
}

// ExchangeHookFrom 取出 ctx 上的回调；没有则返回 nil。
func ExchangeHookFrom(ctx context.Context) ExchangeHook {
	hook, ok := ctx.Value(exchangeHookKey{}).(ExchangeHook)
	if !ok {
		return nil
	}
	return hook
}
