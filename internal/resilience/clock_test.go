// 文件名: clock_test.go
// 用途: 验证生产时钟的三个方法都返回可用值。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"testing"
	"time"
)

func TestClock(t *testing.T) {
	clk := SystemClock()
	if clk.Now().IsZero() {
		t.Fatal("SystemClock.Now() 为零值")
	}
	select {
	case <-clk.After(0):
	case <-time.After(time.Second):
		t.Fatal("SystemClock.After(0) 未立即触发")
	}
	clk.Sleep(0)
}
