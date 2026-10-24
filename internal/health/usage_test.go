// 文件名: usage_test.go
// 用途: 验证用量统计器的五键预置、未知工具丢弃、按小时聚合与 JSON 渲染（含 nil 接收者）。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package health

import (
	"encoding/json"
	"testing"
)

func TestNewUsageTracker(t *testing.T) {
	t.Parallel()

	tracker := NewUsageTracker(nil)
	snapshot := tracker.snapshot()
	if len(snapshot.Tools) != 5 {
		t.Fatalf("tools = %d", len(snapshot.Tools))
	}
	tracker.Record("unknown", true, false, nil)
	if snapshot = tracker.snapshot(); snapshot.Tools["ds_search"].Calls != 0 {
		t.Fatal("未知工具不应被记录")
	}
	tracker.Record("ds_search", true, false, nil)
	tracker.Record("ds_search", false, true, nil)
	snapshot = tracker.snapshot()
	stat := snapshot.Tools["ds_search"]
	if stat.Calls != 2 || stat.Billed != 1 || stat.CacheHits != 1 || stat.Errors != 0 {
		t.Fatalf("stat = %+v", stat)
	}
	if len(snapshot.Hourly) != 1 {
		t.Fatalf("hourly = %d, want 1", len(snapshot.Hourly))
	}
}

func TestUsageTrackerJSON(t *testing.T) {
	t.Parallel()

	t.Run("nil 接收者给全零骨架", func(t *testing.T) {
		t.Parallel()
		var tracker *UsageTracker
		tracker.Record("ds_search", true, false, nil) // MUST 是 no-op，不能 panic
		raw, err := tracker.JSON()
		if err != nil {
			t.Fatalf("JSON() = %v", err)
		}
		var got usageSnapshot
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("输出不是合法 JSON: %v (%s)", err, raw)
		}
		if len(got.Tools) != 5 || len(got.Hourly) != 0 {
			t.Fatalf("骨架 = tools:%d hourly:%d", len(got.Tools), len(got.Hourly))
		}
		if got.Tools["ds_fetch"].Calls != 0 {
			t.Fatal("骨架里每个工具都必须是全零")
		}
	})

	t.Run("记录后带出计数与小时桶", func(t *testing.T) {
		t.Parallel()
		tracker := NewUsageTracker(nil)
		tracker.Record("ds_fetch", true, false, nil)
		raw, err := tracker.JSON()
		if err != nil {
			t.Fatalf("JSON() = %v", err)
		}
		var got usageSnapshot
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("输出不是合法 JSON: %v (%s)", err, raw)
		}
		if got.Tools["ds_fetch"].Calls != 1 || got.Tools["ds_fetch"].Billed != 1 {
			t.Fatalf("ds_fetch = %+v", got.Tools["ds_fetch"])
		}
		if len(got.Hourly) != 1 || got.Hourly[0].Calls != 1 {
			t.Fatalf("hourly = %+v", got.Hourly)
		}
		if got.Hourly[0].Hour == "" {
			t.Fatal("小时桶 MUST 带 RFC3339 的 hour 字段")
		}
	})
}
