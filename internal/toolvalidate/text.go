// 文件名: text.go
// 用途: 校验精确短语与排除词：禁止英文双引号与首尾空白。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

import (
	"fmt"
	"strings"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// ValidatePhrase 拒绝会静默改变查询意图的写法。
func ValidatePhrase(field, value string) error {
	if strings.Contains(value, `"`) || strings.TrimSpace(value) != value {
		return fmt.Errorf("validate %s: %w", field,
			domain.NewValidationError(domain.CodeValInvalidParam, field, "不能包含英文双引号或首尾空白"))
	}
	return nil
}
