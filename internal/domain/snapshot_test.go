// 文件名: snapshot_test.go
// 用途: 验证快照 ID 对 URL 与抓取时刻的绑定。
// 创建日期: 2026-10-24
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"testing"
	"time"
)

func TestSnapshotID(t *testing.T) {
	t.Parallel()

	at := time.Unix(1780000000, 0)
	first := SnapshotID("https://example.com/a", at)
	if again := SnapshotID("https://example.com/a", at); again != first {
		t.Fatalf("同一 URL 与时刻的 ID 不稳定: %q != %q", again, first)
	}
	if other := SnapshotID("https://example.com/b", at); other == first {
		t.Fatal("不同 URL 不应得到同一快照 ID")
	}
	if other := SnapshotID("https://example.com/a", at.Add(time.Second)); other == first {
		t.Fatal("不同抓取时刻不应得到同一快照 ID")
	}
}
