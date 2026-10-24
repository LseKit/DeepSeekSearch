// 文件名: section_test.go
// 用途: 校验 ATX 标题识别、围栏抑制、前言合成、固定分块与 Outline/Section JSON。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"encoding/json"
	"testing"
)

func TestSplitSections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		fixedChunk int
		wantKind   string
		wantIDs    []string
		wantLevels []int
		wantTitles []string
		wantChars  []int
	}{
		{
			name:       "两个二级标题",
			content:    "## A\nx\n## B\ny",
			fixedChunk: 8000,
			wantKind:   "headings",
			wantIDs:    []string{"s0", "s1"},
			wantLevels: []int{2, 2},
			wantTitles: []string{"A", "B"},
			wantChars:  []int{7, 6},
		},
		{
			name:       "一级与二级标题",
			content:    "# 嵌套\n## 子\n",
			fixedChunk: 8000,
			wantKind:   "headings",
			wantIDs:    []string{"s0", "s1"},
			wantLevels: []int{1, 2},
			wantTitles: []string{"嵌套", "子"},
			wantChars:  []int{5, 5},
		},
		{
			name:       "围栏内井号不是标题",
			content:    "```\n# 不是标题\n```\n",
			fixedChunk: 8000,
			wantKind:   "fixed",
			wantIDs:    []string{"c0"},
			wantLevels: []int{1},
			wantTitles: []string{""},
			wantChars:  []int{15},
		},
		{
			name:       "hashtag 不是标题",
			content:    "#hashtag 内容",
			fixedChunk: 8000,
			wantKind:   "fixed",
			wantIDs:    []string{"c0"},
			wantLevels: []int{1},
			wantTitles: []string{""},
			wantChars:  []int{11},
		},
		{
			name:       "空正文兜底一节",
			content:    "",
			fixedChunk: 8000,
			wantKind:   "fixed",
			wantIDs:    []string{"c0"},
			wantLevels: []int{1},
			wantTitles: []string{""},
			wantChars:  []int{0},
		},
		{
			name:       "只有一个标题走固定分块",
			content:    "# A\nx",
			fixedChunk: 8000,
			wantKind:   "fixed",
			wantIDs:    []string{"c0"},
			wantLevels: []int{1},
			wantTitles: []string{""},
			wantChars:  []int{5},
		},
		{
			name:       "第一个标题前有正文时合成前言",
			content:    "intro\n## A\nx\n## B\ny",
			fixedChunk: 8000,
			wantKind:   "headings",
			wantIDs:    []string{"s0", "s1", "s2"},
			wantLevels: []int{0, 2, 2},
			wantTitles: []string{"(前言)", "A", "B"},
			wantChars:  []int{6, 7, 6},
		},
		{
			name:       "纯空白前言不生成节",
			content:    "\n\n## A\nx\n## B\ny",
			fixedChunk: 8000,
			wantKind:   "headings",
			wantIDs:    []string{"s0", "s1"},
			wantLevels: []int{2, 2},
			wantTitles: []string{"A", "B"},
			wantChars:  []int{7, 6},
		},
		{
			name:       "围栏外的标题正常切分",
			content:    "# A\n```\n# not\n```\n## B\n",
			fixedChunk: 8000,
			wantKind:   "headings",
			wantIDs:    []string{"s0", "s1"},
			wantLevels: []int{1, 2},
			wantTitles: []string{"A", "B"},
			wantChars:  []int{18, 5},
		},
		{
			name:       "Setext 下划线不是标题",
			content:    "Title\n===\n",
			fixedChunk: 8000,
			wantKind:   "fixed",
			wantIDs:    []string{"c0"},
			wantLevels: []int{1},
			wantTitles: []string{""},
			wantChars:  []int{10},
		},
		{
			name:       "固定分块按 rune 均分",
			content:    "中文😀abc",
			fixedChunk: 2,
			wantKind:   "fixed",
			wantIDs:    []string{"c0", "c1", "c2"},
			wantLevels: []int{1, 1, 1},
			wantTitles: []string{"", "", ""},
			wantChars:  []int{2, 2, 2},
		},
		{
			name:       "fixedChunk 非正数时整篇一节",
			content:    "abc",
			fixedChunk: 0,
			wantKind:   "fixed",
			wantIDs:    []string{"c0"},
			wantLevels: []int{1},
			wantTitles: []string{""},
			wantChars:  []int{3},
		},
		{
			name:       "围栏内多个井号全部忽略",
			content:    "```\n## A\n## B\n```\n",
			fixedChunk: 8000,
			wantKind:   "fixed",
			wantIDs:    []string{"c0"},
			wantLevels: []int{1},
			wantTitles: []string{""},
			wantChars:  []int{18},
		},
		{
			name:       "混排 hashtag 不影响两个真标题",
			content:    "# A\n#notheading\n## B\n",
			fixedChunk: 8000,
			wantKind:   "headings",
			wantIDs:    []string{"s0", "s1"},
			wantLevels: []int{1, 2},
			wantTitles: []string{"A", "B"},
			wantChars:  []int{16, 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			outline := SplitSections(tt.content, tt.fixedChunk)
			if outline.OutlineKind != tt.wantKind {
				t.Errorf("OutlineKind = %q, want %q", outline.OutlineKind, tt.wantKind)
			}
			if len(outline.Sections) == 0 {
				t.Fatal("Sections 为空，违反单节兜底")
			}
			if len(outline.Sections) != len(tt.wantIDs) {
				t.Fatalf("Sections 数量 = %d, want %d", len(outline.Sections), len(tt.wantIDs))
			}
			for i, sec := range outline.Sections {
				if sec.Id != tt.wantIDs[i] {
					t.Errorf("第 %d 节 Id = %q, want %q", i, sec.Id, tt.wantIDs[i])
				}
				if sec.Level != tt.wantLevels[i] {
					t.Errorf("第 %d 节 Level = %d, want %d", i, sec.Level, tt.wantLevels[i])
				}
				if sec.Title != tt.wantTitles[i] {
					t.Errorf("第 %d 节 Title = %q, want %q", i, sec.Title, tt.wantTitles[i])
				}
				if sec.Chars != tt.wantChars[i] {
					t.Errorf("第 %d 节 Chars = %d, want %d", i, sec.Chars, tt.wantChars[i])
				}
			}
		})
	}
}

func TestSectionAndOutlineJSON(t *testing.T) {
	t.Parallel()

	outline := Outline{
		OutlineKind: "fixed",
		Sections: []Section{
			{Id: "c0", Level: 1, Title: "", Start: 0, Chars: 3},
		},
	}
	raw, err := json.Marshal(outline)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"outline_kind":"fixed","sections":[{"id":"c0","level":1,"title":"","start":0,"chars":3}]}`
	if string(raw) != want {
		t.Errorf("JSON = %s, want %s", raw, want)
	}
}
