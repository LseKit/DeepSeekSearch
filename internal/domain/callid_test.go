// 文件名: callid_test.go
// 用途: 验证 NewCallID 的格式与唯一性。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"regexp"
	"testing"
)

func TestNewCallID(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`^[0-9a-f]{16}$`)
	a, b := NewCallID(), NewCallID()
	if !pattern.MatchString(a) {
		t.Fatalf("NewCallID() = %q, 不符合 16 位 hex", a)
	}
	if a == b {
		t.Fatalf("两次调用返回相同 ID: %q", a)
	}
}
