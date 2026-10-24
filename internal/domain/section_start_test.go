// 文件名: section_start_test.go
// 用途: 验证 Section.Start 在「空白前言不生成节」时仍然对齐，按 Start/Chars 取回的正文与原文逐字相同。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"testing"
	"unicode/utf8"
)

func TestSplitSectionsStartSkipsBlankPreface(t *testing.T) {
	t.Parallel()

	// 前言只有两个换行：不会生成 s0，但它占的 2 个 rune 必须算进第一节的 Start。
	content := "\n\n## A\naaa\n## B\nbbb\n"
	outline := SplitSections(content, 8000)

	if len(outline.Sections) != 2 {
		t.Fatalf("空白前言不应生成节，sections = %d", len(outline.Sections))
	}
	want := []Section{
		{Id: "s0", Level: 2, Title: "A", Start: 2, Chars: 9},
		{Id: "s1", Level: 2, Title: "B", Start: 11, Chars: 9},
	}
	for i, sec := range outline.Sections {
		if sec != want[i] {
			t.Fatalf("sections[%d] = %+v, want %+v", i, sec, want[i])
		}
	}

	// 真正的验收：按 Start/Chars 切出来的必须是该节自己的正文。
	wantBody := []string{"## A\naaa\n", "## B\nbbb\n"}
	for i, sec := range outline.Sections {
		got, _ := SliceByRune(content, sec.Start, sec.Chars)
		if got != wantBody[i] {
			t.Fatalf("按 Start 取 %s = %q, want %q", sec.Id, got, wantBody[i])
		}
	}
}

func TestSplitSectionsStartTilesWholeContent(t *testing.T) {
	t.Parallel()

	content := "前言文字\n\n## A\naaa\n## B\nbbb\n"
	outline := SplitSections(content, 8000)

	covered := 0
	for _, sec := range outline.Sections {
		if sec.Start != covered {
			t.Fatalf("%s 的 Start=%d，上一节末尾在 %d：节之间 MUST 无缝衔接", sec.Id, sec.Start, covered)
		}
		covered += sec.Chars
	}
	if covered != utf8.RuneCountInString(content) {
		t.Fatalf("各节合计 %d rune，正文共 %d rune", covered, utf8.RuneCountInString(content))
	}
}
