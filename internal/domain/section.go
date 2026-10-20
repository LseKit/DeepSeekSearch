// 文件名: section.go
// 用途: 按 ATX 标题或固定 rune 长度切分正文，输出不含正文的 Section/Outline 元信息。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	prefixHeading = "s"
	prefixFixed   = "c"

	outlineHeadings = "headings"
	outlineFixed    = "fixed"
	prefaceTitle    = "(前言)"
)

// fenceRe 匹配 ``` 围栏的开关行。用它跟踪「当前是否在代码块内」，
// 因为代码块里的 # 是 Shell/Python 注释，不是 Markdown 标题。
var fenceRe = regexp.MustCompile("^\\s*```")

// Section 是目录里的一个节，只含元信息；正文由调用方按快照切取。
type Section struct {
	Id    string `json:"id"` //nolint:revive // 契约字段名，见 12 号 §5.3
	Level int    `json:"level"`
	Title string `json:"title"`
	Chars int    `json:"chars"`
}

// Outline 是一次切分的结果，只含目录元信息，不含正文。
type Outline struct {
	OutlineKind string    `json:"outline_kind"`
	Sections    []Section `json:"sections"`
}

// SplitSections 把正文切成节。判定顺序：标题 → 固定分块 → 单节兜底。
//
// 只负责「切」，不负责选 inline/outline/section 模式；那个判断属于调用方
// （12 号 §5.1）。fixedChunk <= 0 时整篇作为一节，避免死循环。
func SplitSections(content string, fixedChunk int) Outline {
	heads := scanHeadings(content)
	if len(heads) >= 2 {
		return buildHeadingOutline(content, heads)
	}
	return buildFixedOutline(content, fixedChunk)
}

type headingMark struct {
	start int
	level int
	title string
}

func scanHeadings(content string) []headingMark {
	var marks []headingMark
	inFence := false
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		text := strings.TrimSuffix(line, "\n")
		switch {
		case fenceRe.MatchString(text):
			inFence = !inFence
		case !inFence:
			if level, title, ok := parseATX(text); ok {
				marks = append(marks, headingMark{start: offset, level: level, title: title})
			}
		}
		offset += len(line)
	}
	return marks
}

// parseATX 只认 1~6 个 # 加一个空格开头的行；#hashtag 与 7 个 # 都不算。
func parseATX(line string) (level int, title string, ok bool) {
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

func buildHeadingOutline(content string, heads []headingMark) Outline {
	sections := make([]Section, 0, len(heads)+1)

	// 第一个标题前只要有非空白正文就合成前言，否则这段内容会从目录里消失。
	// 最常见的触发是文档直接从 h2 开始（缺 h1）。
	if pre := content[:heads[0].start]; strings.TrimSpace(pre) != "" {
		sections = append(sections, Section{
			Id:    prefixHeading + strconv.Itoa(len(sections)),
			Level: 0,
			Title: prefaceTitle,
			Chars: utf8.RuneCountInString(pre),
		})
	}

	for i, h := range heads {
		end := len(content)
		if i+1 < len(heads) {
			end = heads[i+1].start
		}
		sections = append(sections, Section{
			Id:    prefixHeading + strconv.Itoa(len(sections)),
			Level: h.level,
			Title: h.title,
			Chars: utf8.RuneCountInString(content[h.start:end]),
		})
	}

	return Outline{OutlineKind: outlineHeadings, Sections: sections}
}

func buildFixedOutline(content string, fixedChunk int) Outline {
	runes := []rune(content)
	if len(runes) == 0 {
		return Outline{OutlineKind: outlineFixed, Sections: []Section{{
			Id:    prefixFixed + "0",
			Level: 1,
		}}}
	}
	if fixedChunk <= 0 {
		fixedChunk = len(runes)
	}

	sections := make([]Section, 0, (len(runes)+fixedChunk-1)/fixedChunk)
	for start := 0; start < len(runes); start += fixedChunk {
		end := start + fixedChunk
		if end > len(runes) {
			end = len(runes)
		}
		sections = append(sections, Section{
			Id:    prefixFixed + strconv.Itoa(len(sections)),
			Level: 1,
			Chars: end - start,
		})
	}

	return Outline{OutlineKind: outlineFixed, Sections: sections}
}
