// 文件名: toolvalidate_test.go
// 用途: 覆盖语言码、日期、短语与混淆 IPv4 四个纯校验函数。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

import (
	"strings"
	"testing"
)

func TestNormalizeLangs(t *testing.T) {
	t.Parallel()

	got, warnings := NormalizeLangs([]string{"EN", " ", "zz"}, nil)
	if len(got) != 2 || got[0] != "en" || got[1] != "zz" {
		t.Fatalf("NormalizeLangs() = %v", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "lang:zz") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestValidDate(t *testing.T) {
	t.Parallel()

	ok := []string{"2026", "2026-10-21"}
	bad := []string{"2026-02-30", "26-10", "2026/10/21", ""}
	for _, v := range ok {
		if !ValidDate(v) {
			t.Fatalf("ValidDate(%q) = false", v)
		}
	}
	for _, v := range bad {
		if ValidDate(v) {
			t.Fatalf("ValidDate(%q) = true", v)
		}
	}
}

func TestValidatePhrase(t *testing.T) {
	t.Parallel()

	if err := ValidatePhrase("exact_phrase", "go generics"); err != nil {
		t.Fatalf("合法短语 = %v", err)
	}
	for _, v := range []string{`has "quote"`, " leading", "trailing "} {
		if err := ValidatePhrase("exact_phrase", v); err == nil {
			t.Fatalf("ValidatePhrase(%q) = nil", v)
		}
	}
}

func TestCombineIPv4(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   []uint64
		want uint32
		ok   bool
	}{
		{[]uint64{0x7F000001}, 0x7F000001, true},
		{[]uint64{127, 1}, 0x7F000001, true},
		{[]uint64{127, 0, 1}, 0x7F000001, true},
		{[]uint64{127, 0, 0, 1}, 0x7F000001, true},
		{[]uint64{256, 1}, 0, false},
		{[]uint64{1, 2, 3, 4, 5}, 0, false},
	}
	for _, tt := range tests {
		got, ok := CombineIPv4(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("CombineIPv4(%v) = %#x,%v want %#x,%v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
