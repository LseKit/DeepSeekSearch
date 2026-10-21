// 文件名: callid.go
// 用途: 生成日志与错误体共用的调用标识。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"crypto/rand"
	"encoding/hex"
)

// NewCallID 返回 16 位十六进制调用标识。
// 随机源失败时返回全零串而不是空串：空串会让日志里 call_id 字段消失，
// 排障时无法判断「这条日志属于哪次调用」。
func NewCallID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}
