// 文件名: snapshot.go
// 用途: 生成绑定「一次具体抓取」的快照 ID，而不是只绑定 URL。
// 创建日期: 2026-10-24
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// SnapshotID 把 URL 与抓取时刻一起哈希；同一 URL 的多次抓取得到不同快照。
func SnapshotID(rawURL string, capturedAt time.Time) string {
	sum := sha256.Sum256([]byte(rawURL + strconv.FormatInt(capturedAt.Unix(), 10)))
	return "snap_" + hex.EncodeToString(sum[:8])
}
