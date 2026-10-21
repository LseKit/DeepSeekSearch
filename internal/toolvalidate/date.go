// 文件名: date.go
// 用途: 校验 after/before 的 YYYY 与 YYYY-MM-DD 两种写法。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

import (
	"regexp"
	"time"
)

var datePattern = regexp.MustCompile(`^\d{4}(-\d{2}-\d{2})?$`)

// ValidDate 检查格式与真实日期（拒绝 2026-02-30 这类）。
func ValidDate(value string) bool {
	if !datePattern.MatchString(value) {
		return false
	}
	layout := "2006"
	if len(value) == len("2006-01-02") {
		layout = "2006-01-02"
	}
	_, err := time.Parse(layout, value)
	return err == nil
}
