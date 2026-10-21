// 文件名: circuit.go
// 用途: 定义按 key 隔离的熔断器接口；实现见子包 circuit。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

// Circuit 是按 key 隔离的熔断器。
type Circuit interface {
	// Allow 返回是否放行；Open 时返回 3030 并带 retry_after_ms。
	Allow(key string) error
	// Record 记录一次调用结果；只有白名单错误推进连续失败计数。
	Record(key string, err error)
	// State 返回 "closed" | "open" | "half-open"。
	State(key string) string
}
