// 文件名: time.go
// 用途: 按 Asia/Shanghai 解析上游日期，输出对外 PublishedDate 与距今天数。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import "time"

// cst8 是 Asia/Shanghai。MUST NOT 用固定偏移时区（如 UTC+8）：
// 那种写法不带时区名，历史偏移不参与计算，日志里也只显示 "+0800"。
var cst8 = mustLoadCST8()

func mustLoadCST8() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// 容器缺 tzdata 时会在包初始化期失败。这是不可恢复的部署错误，
		// 必须在任何请求前暴露，因此这里允许 panic（20 号 §G6 的例外）。
		panic("domain: 无法加载 Asia/Shanghai 时区，请安装 tzdata: " + err.Error())
	}
	return loc
}

// PublishedDate 是对外的日期表示。
//
// Date 与 AgeDays 必须可空：实测 20% 的上游结果 date 为 null，
// 用空串或 0 填充会把「未知」伪造成真实日期。
type PublishedDate struct {
	Date          *string `json:"date"`
	AgeDays       *int    `json:"age_days"`
	DatePrecision string  `json:"date_precision"`
}

// ParseDate 解析上游 RFC3339 日期，并按北京时间取日期与距今天数。
//
// now 由调用方用注入的 Clock 传入；domain MUST NOT 自己调 time.Now()，
// 否则「今天是哪天」不可测，跨零点与时区边界无法写成确定性断言（H8/T4）。
// 无效日期与 null 一样输出空字段，不猜值。
func ParseDate(s *string, now time.Time) PublishedDate {
	out := PublishedDate{DatePrecision: "day"}
	if s == nil {
		return out
	}

	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return out
	}

	local := t.In(cst8)
	date := local.Format("2006-01-02")
	out.Date = &date

	today := now.In(cst8)
	todayMidnight := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, cst8)
	dateMidnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, cst8)
	age := int(todayMidnight.Sub(dateMidnight).Hours() / 24)
	out.AgeDays = &age

	return out
}
