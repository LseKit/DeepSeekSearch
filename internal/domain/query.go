// 文件名: query.go
// 用途: 按固定优先级把 ds_search 的结构化字段拼成上游 query，并回报软告警。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SearchRequest 是 ds_search 中参与语法拼装的入参。
// detail/count/min_rerank 等展示与过滤参数不改变 query 语法，由 Tools 层处理。
// Count/NeedContent/OriQuestion/Localization 是上游透传字段，BuildQuery 不读它们。
type SearchRequest struct {
	Query        string   `json:"query"`
	Site         []string `json:"site,omitempty"`
	ExcludeSites []string `json:"exclude_sites,omitempty"`
	Lang         []string `json:"lang,omitempty"`
	After        string   `json:"after,omitempty"`
	Before       string   `json:"before,omitempty"`
	ExactPhrase  string   `json:"exact_phrase,omitempty"`
	ExcludeTerms []string `json:"exclude_terms,omitempty"`
	RawQuery     bool     `json:"raw_query,omitempty"`

	Count        *int    `json:"count,omitempty"`
	NeedContent  *bool   `json:"need_content,omitempty"`
	OriQuestion  *string `json:"ori_question,omitempty"`
	Localization string  `json:"localization,omitempty"`
}

// BuildQuery 按 12 号 §4.1 的优先级把结构化字段拼成上游 query。
//
// rawQuery 为 true 时原样返回 req.Query，MUST NOT 追加任何操作符；
// query 本身 MUST 原样透传，不做任何转义或编码。超长只记 warning，
// 由 Tools 层转成 1010 VAL_QUERY_TOO_LONG 拒绝。
func BuildQuery(req SearchRequest) (effective string, warnings []string) { //nolint:gocritic // 契约签名，见 11 号 §一
	if req.RawQuery {
		return withLengthWarning(req.Query, nil)
	}

	var b strings.Builder
	b.WriteString(req.Query)
	lower := strings.ToLower(req.Query)

	warnings = appendSites(&b, lower, req.Site, warnings)
	warnings = appendExcludeSites(&b, lower, req.ExcludeSites, warnings)
	warnings = appendLang(&b, lower, req.Lang, warnings)
	warnings = appendAfter(&b, lower, req.After, warnings)
	warnings = appendBefore(&b, lower, req.Before, warnings)
	if req.ExactPhrase != "" {
		appendWord(&b, `"`+req.ExactPhrase+`"`)
	}
	appendExcludeTerms(&b, req.ExcludeTerms)

	return withLengthWarning(b.String(), warnings)
}

func appendSites(b *strings.Builder, lower string, sites, warnings []string) []string {
	if len(sites) == 0 {
		return warnings
	}
	if hasOperator(lower, "site:") {
		return append(warnings, "query 已含 site: 操作符，忽略 site 参数")
	}
	parts := make([]string, 0, len(sites))
	for _, site := range sites {
		parts = append(parts, "site:"+site)
	}
	appendWord(b, strings.Join(parts, " OR "))
	return warnings
}

func appendExcludeSites(b *strings.Builder, lower string, sites, warnings []string) []string {
	if len(sites) == 0 {
		return warnings
	}
	if hasOperator(lower, "-site:") {
		return append(warnings, "query 已含 -site: 操作符，忽略 exclude_sites 参数")
	}
	for _, site := range sites {
		appendWord(b, "-site:"+site)
	}
	return warnings
}

func appendLang(b *strings.Builder, lower string, langs, warnings []string) []string {
	if len(langs) == 0 {
		return warnings
	}
	if hasOperator(lower, "lang:") {
		return append(warnings, "query 已含 lang: 操作符，忽略 lang 参数")
	}
	appendWord(b, "lang:"+strings.Join(langs, ","))
	return warnings
}

func appendAfter(b *strings.Builder, lower, after string, warnings []string) []string {
	if after == "" {
		return warnings
	}
	if hasOperator(lower, "after:") {
		return append(warnings, "query 已含 after: 操作符，忽略 after 参数")
	}

	// after:YYYY 在上游的真实语义是「晚于该年」，会把整年静默丢掉；
	// 展开成 YYYY-01-01 才符合调用方直觉（12 号 §4.1.3）。
	if isBareYear(after) {
		expanded := after + "-01-01"
		warnings = append(warnings, "after:"+after+" 已展开为 after:"+expanded)
		after = expanded
	}
	appendWord(b, "after:"+after)
	return warnings
}

func appendBefore(b *strings.Builder, lower, before string, warnings []string) []string {
	if before == "" {
		return warnings
	}
	if hasOperator(lower, "before:") {
		return append(warnings, "query 已含 before: 操作符，忽略 before 参数")
	}
	// before:YYYY 的语义已是「早于该年起始」，与直觉一致，不展开。
	appendWord(b, "before:"+before)
	return warnings
}

func appendExcludeTerms(b *strings.Builder, terms []string) {
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.IndexFunc(term, unicode.IsSpace) >= 0 {
			appendWord(b, `-"`+term+`"`)
			continue
		}
		appendWord(b, "-"+term)
	}
}

func appendWord(b *strings.Builder, word string) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(word)
}

// hasOperator 判断操作符是否独立出现：必须在串首或空白之后。
// 这样 -site: 里的 site: 不会被当成独立的 site:（前一字符是 - 而非空白）。
// 这是启发式，不解析引号：引号内但紧跟空白的 site: 仍会命中。
func hasOperator(lowerQuery, op string) bool {
	for start := 0; start < len(lowerQuery); {
		idx := strings.Index(lowerQuery[start:], op)
		if idx < 0 {
			return false
		}
		idx += start
		if idx == 0 || isQuerySpace(lowerQuery[idx-1]) {
			return true
		}
		start = idx + 1
	}
	return false
}

func isQuerySpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isBareYear(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func withLengthWarning(effective string, warnings []string) (out string, outWarnings []string) {
	out = effective
	outWarnings = warnings
	if n := utf8.RuneCountInString(out); n > 128 {
		outWarnings = append(outWarnings,
			fmt.Sprintf("拼装后的 query 为 %d rune，超过 128，将被拒绝（1010 VAL_QUERY_TOO_LONG）", n))
	}
	return out, outWarnings
}
