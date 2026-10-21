// 文件名: fetch_render.go
// 用途: ds_fetch 的反转义、三种模式渲染、分节预算、untrusted 包裹与响应字段。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

const (
	inlineMode  = "inline"
	outlineMode = "outline"
	sectionMode = "section"

	sectionPreviewRunes = 200
)

type fetchOutput struct {
	CacheID          string               `json:"cache_id"`
	Title            string               `json:"title"`
	Mode             string               `json:"mode"`
	TotalChars       int                  `json:"total_chars"`
	Content          string               `json:"content,omitempty"`
	Sections         []fetchSectionOutput `json:"sections,omitempty"`
	OutlineKind      string               `json:"outline_kind,omitempty"`
	Warnings         []string             `json:"warnings"`
	Truncated        bool                 `json:"truncated"`
	TruncatedBy      string               `json:"truncated_by,omitempty"`
	FromCache        bool                 `json:"from_cache"`
	CachedAt         string               `json:"cached_at"`
	AgeSeconds       int                  `json:"age_seconds"`
	SavedBilledCalls int                  `json:"saved_billed_calls"`
}

type fetchSectionOutput struct {
	ID      string `json:"id"`
	Level   int    `json:"level"`
	Title   string `json:"title"`
	Chars   int    `json:"chars"`
	Preview string `json:"preview,omitempty"`
}

func (h *fetchHandler) render(entry *cachedFetchEntry, fromCache bool) (*mcp.CallToolResult, string, error) {
	content := entry.Content
	if h.params.Unescape {
		content = domain.Unescape(content)
	}
	totalChars := utf8.RuneCountInString(content)
	if totalChars < h.minContentChars() {
		return nil, "", fmt.Errorf("content too short: %w",
			domain.NewLocalError(domain.CodeLocContentTooShort, "抓到的正文过短，可能是导航页或空页面",
				map[string]any{"chars": totalChars, "min": h.minContentChars()}))
	}

	mode := h.selectMode(totalChars)
	out := fetchOutput{
		CacheID:    entry.CacheID,
		Title:      entry.Title,
		Mode:       mode,
		TotalChars: totalChars,
		FromCache:  fromCache,
	}
	setFetchCacheMeta(&out, entry.CapturedAt, fromCache, h.deps.Clock.Now())

	// 12 号 §4.1：warnings 必带 —— 空数组也要出现，避免客户端写两套分支。
	warnings, err := h.applyMode(&out, content, entry.URL)
	if err != nil {
		return nil, mode, err
	}
	out.Warnings = warnings

	raw, err := json.Marshal(out)
	if err != nil {
		return nil, mode, fmt.Errorf("marshal fetch output: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}},
	}, mode, nil
}

func setFetchCacheMeta(out *fetchOutput, capturedAt int64, fromCache bool, now time.Time) {
	cached := time.Unix(capturedAt, 0)
	age := int(now.Sub(cached).Seconds())
	if age < 0 {
		age = 0
	}
	out.CachedAt = cached.UTC().Format(time.RFC3339)
	out.AgeSeconds = age
	if fromCache {
		out.SavedBilledCalls = 1
	}
}

func (h *fetchHandler) selectMode(totalChars int) string {
	if h.params.Section != "" || len(h.params.Sections) > 0 {
		return sectionMode
	}
	if totalChars <= h.inlineMaxChars() {
		return inlineMode
	}
	return outlineMode
}

// applyMode 按 inline/section/outline 三种模式填充正文与截断标记。
func (h *fetchHandler) applyMode(out *fetchOutput, content, source string) ([]string, error) {
	warnings := make([]string, 0, 2)
	switch out.Mode {
	case inlineMode:
		body, truncated := budgetCut(content, h.params.MaxChars)
		out.Content = domain.WrapUntrusted(source, body)
		out.Truncated = truncated
	case sectionMode:
		body, err := h.selectSections(content)
		if err != nil {
			return nil, err
		}
		body, truncated := budgetCut(body, h.params.MaxChars)
		out.Content = domain.WrapUntrusted(source, body)
		out.Truncated = truncated
	case outlineMode:
		sections, kind, truncated, outlineWarnings := h.renderOutline(source, content)
		out.Sections = sections
		out.OutlineKind = kind
		out.Truncated = truncated
		warnings = append(warnings, outlineWarnings...)
	}
	if out.Truncated {
		out.TruncatedBy = "budget"
	}
	return warnings, nil
}

func (h *fetchHandler) renderOutline(source, content string) (sections []fetchSectionOutput, kind string, truncated bool, warnings []string) {
	outline := domain.SplitSections(content, h.fixedChunkChars())
	all := outline.Sections
	limit := h.outlineMaxSections()
	if limit > 0 && len(all) > limit {
		warnings = append(warnings, fmt.Sprintf("目录已折叠，共 %d 节", len(all)))
		all = all[:limit]
	}
	offsets := sectionOffsets(outline.Sections)
	rendered := make([]fetchSectionOutput, 0, len(all))
	used := 0
	for i, section := range all {
		body, _ := domain.SliceByRune(content, offsets[i], section.Chars)
		preview, _ := domain.SliceByRune(body, 0, sectionPreviewRunes)
		wrapped := domain.WrapUntrusted(source, preview)
		cost := utf8.RuneCountInString(section.Id) + utf8.RuneCountInString(section.Title) + utf8.RuneCountInString(wrapped)
		if len(rendered) > 0 && used+cost > h.params.MaxChars {
			truncated = true
			break
		}
		rendered = append(rendered, fetchSectionOutput{
			ID:      section.Id,
			Level:   section.Level,
			Title:   section.Title,
			Chars:   section.Chars,
			Preview: wrapped,
		})
		used += cost
	}
	if truncated {
		warnings = append(warnings, fmt.Sprintf("目录因 %d rune 预算截断，仅返回 %d/%d 节",
			h.params.MaxChars, len(rendered), len(all)))
	}
	return rendered, outline.OutlineKind, truncated, warnings
}

func sectionOffsets(sections []domain.Section) []int {
	offsets := make([]int, len(sections))
	offset := 0
	for i, section := range sections {
		offsets[i] = offset
		offset += section.Chars
	}
	return offsets
}

func (h *fetchHandler) selectSections(content string) (string, error) {
	outline := domain.SplitSections(content, h.fixedChunkChars())
	offsets := sectionOffsets(outline.Sections)
	index := make(map[string]int, len(outline.Sections))
	for i, section := range outline.Sections {
		index[section.Id] = i
	}
	ids := h.requestedSections()
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		pos, ok := index[id]
		if !ok {
			return "", fmt.Errorf("section not found: %w",
				domain.NewLocalError(domain.CodeValSectionNotFound, "指定的节不存在",
					map[string]any{"section": id, "cache_id": h.params.CacheID}))
		}
		body, _ := domain.SliceByRune(content, offsets[pos], outline.Sections[pos].Chars)
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n---\n\n"), nil
}

func (h *fetchHandler) requestedSections() []string {
	if h.params.Section != "" {
		return []string{h.params.Section}
	}
	return h.params.Sections
}

func budgetCut(content string, maxChars int) (string, bool) {
	if maxChars <= 0 || utf8.RuneCountInString(content) <= maxChars {
		return content, false
	}
	out, _ := domain.SliceByRune(content, 0, maxChars)
	return out, true
}

func (h *fetchHandler) minContentChars() int {
	if h.deps.Cfg.Limits.MinContentChars > 0 {
		return h.deps.Cfg.Limits.MinContentChars
	}
	return 120
}

func (h *fetchHandler) inlineMaxChars() int {
	if h.deps.Cfg.Limits.InlineMaxChars > 0 {
		return h.deps.Cfg.Limits.InlineMaxChars
	}
	return 8000
}

func (h *fetchHandler) fixedChunkChars() int {
	if h.deps.Cfg.Limits.FixedChunkChars > 0 {
		return h.deps.Cfg.Limits.FixedChunkChars
	}
	return 8000
}

func (h *fetchHandler) outlineMaxSections() int {
	if h.deps.Cfg.Limits.OutlineMaxSections > 0 {
		return h.deps.Cfg.Limits.OutlineMaxSections
	}
	return 200
}
