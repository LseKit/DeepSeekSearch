// 文件名: time_test.go
// 用途: 校验北京时间日期解析、AgeDays 边界与 PublishedDate 的 JSON 形态。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func intPtr(i int) *int { return &i }

func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestParseDate(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 4, 28, 0, 30, 0, 0, cst8)
	tests := []struct {
		name     string
		in       *string
		now      time.Time
		wantDate *string
		wantAge  *int
	}{
		{
			name: "null 日期三字段全空",
			in:   nil,
			now:  fixedNow,
		},
		{
			name:     "UTC 16 点等于北京次日零点",
			in:       strPtr("2026-04-26T16:00:00+00:00"),
			now:      fixedNow,
			wantDate: strPtr("2026-04-27"),
			wantAge:  intPtr(1),
		},
		{
			name:     "当天 17:53 仍是同一天",
			in:       strPtr("2026-04-27T09:53:15+00:00"),
			now:      time.Date(2026, 4, 27, 23, 0, 0, 0, cst8),
			wantDate: strPtr("2026-04-27"),
			wantAge:  intPtr(0),
		},
		{
			name:     "今天日期 AgeDays 为 0",
			in:       strPtr("2026-04-26T16:00:00+00:00"),
			now:      time.Date(2026, 4, 27, 0, 30, 0, 0, cst8),
			wantDate: strPtr("2026-04-27"),
			wantAge:  intPtr(0),
		},
		{
			name:     "跨零点前一天为负数",
			in:       strPtr("2026-04-26T16:00:00+00:00"),
			now:      time.Date(2026, 4, 26, 23, 59, 0, 0, cst8),
			wantDate: strPtr("2026-04-27"),
			wantAge:  intPtr(-1),
		},
		{
			name:     "now 是 UTC 也按北京时间判定",
			in:       strPtr("2026-04-26T16:00:00+00:00"),
			now:      time.Date(2026, 4, 27, 16, 30, 0, 0, time.UTC),
			wantDate: strPtr("2026-04-27"),
			wantAge:  intPtr(1),
		},
		{
			name:     "带 +08:00 偏移的日期同样归一",
			in:       strPtr("2026-04-27T00:30:00+08:00"),
			now:      fixedNow,
			wantDate: strPtr("2026-04-27"),
			wantAge:  intPtr(1),
		},
		{
			name:     "23:59:59 仍算当天",
			in:       strPtr("2026-04-27T23:59:59+08:00"),
			now:      fixedNow,
			wantDate: strPtr("2026-04-27"),
			wantAge:  intPtr(1),
		},
		{
			name:     "未来日期 AgeDays 为负",
			in:       strPtr("2026-04-29T00:00:00+08:00"),
			now:      fixedNow,
			wantDate: strPtr("2026-04-29"),
			wantAge:  intPtr(-1),
		},
		{
			name:     "闰日按北京时间进位",
			in:       strPtr("2024-02-29T16:00:00+00:00"),
			now:      time.Date(2024, 3, 2, 12, 0, 0, 0, cst8),
			wantDate: strPtr("2024-03-01"),
			wantAge:  intPtr(1),
		},
		{
			name: "非法日期输出 null",
			in:   strPtr("not-a-date"),
			now:  fixedNow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ParseDate(tt.in, tt.now)
			if got.DatePrecision != "day" {
				t.Errorf("DatePrecision = %q, want day", got.DatePrecision)
			}
			if !equalStrPtr(got.Date, tt.wantDate) {
				t.Errorf("Date = %v, want %v", ptrValue(got.Date), ptrValue(tt.wantDate))
			}
			if !equalIntPtr(got.AgeDays, tt.wantAge) {
				t.Errorf("AgeDays = %v, want %v", intPtrValue(got.AgeDays), intPtrValue(tt.wantAge))
			}
		})
	}
}

func ptrValue(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func intPtrValue(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestPublishedDateJSON(t *testing.T) {
	t.Parallel()

	full := PublishedDate{
		Date:          strPtr("2026-04-27"),
		AgeDays:       intPtr(1),
		DatePrecision: "day",
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if want := `{"date":"2026-04-27","age_days":1,"date_precision":"day"}`; string(raw) != want {
		t.Errorf("JSON = %s, want %s", raw, want)
	}

	empty := PublishedDate{DatePrecision: "day"}
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if want := `{"date":null,"age_days":null,"date_precision":"day"}`; string(raw) != want {
		t.Errorf("null JSON = %s, want %s", raw, want)
	}
}

func TestCST8(t *testing.T) {
	t.Parallel()

	if got, want := cst8.String(), "Asia/Shanghai"; got != want {
		t.Errorf("cst8 = %q, want %q", got, want)
	}
}
