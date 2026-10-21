// 文件名: budget.go
// 用途: 定义三层调用预算接口；实现见子包 budget。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

// Budget 是三层硬顶：单次响应字符数 / 时间窗内调用次数 / 日累计调用次数。
type Budget interface {
	// CheckWindow 在发起上游请求前调用；超限返回 3070。
	CheckWindow(key string) error
	// CheckDaily 在发起上游请求前调用；超限返回 3070。
	CheckDaily(key string) error
	// Record 在请求成功后调用一次，累加窗口与日计数。
	Record(key string, upstreamCalls int)
	// TrimChars 截断一段文本到剩余字符预算，返回截断后文本与是否被截断。
	TrimChars(s string) (string, bool)
	// Reset 清零全部计数（测试用）。
	Reset()
}
