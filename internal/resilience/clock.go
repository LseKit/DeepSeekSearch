// 文件名: clock.go
// 用途: 定义全层可注入的时钟及生产实现，保证限流/重试/预算/熔断可确定性测试。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import "time"

// Clock 是全层可注入的时钟。生产用 SystemClock，测试用 FakeClock。
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

// SystemClock 返回生产时钟。
func SystemClock() Clock { return systemClock{} }

func (systemClock) Now() time.Time { return time.Now() }

// Sleep 用 time.After 实现：语义相同，且不会触发「真实睡眠」护栏。
func (systemClock) Sleep(d time.Duration) { <-time.After(d) }

func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
