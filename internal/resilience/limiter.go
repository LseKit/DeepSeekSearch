// 文件名: limiter.go
// 用途: 定义按 key 隔离的限流器接口；实现见子包 ratelimit。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import "context"

// Limiter 是按 key 隔离的令牌桶集合。
type Limiter interface {
	// Wait 取得令牌与在飞名额；成功后调用方 MUST 配对调用 Release。
	Wait(ctx context.Context, key string) error
	// Release 释放在飞名额；未持有时为 no-op。
	Release(key string)
	// Depth 返回当前排队深度。
	Depth(key string) int
}
