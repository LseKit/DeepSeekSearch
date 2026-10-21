// 文件名: search_validate.go
// 用途: ds_search 的本地校验、默认值、语法拼装与软告警，全部在发上游前完成。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/toolvalidate"
)

type searchParams struct {
	Query          string
	Count          int
	Detail         string
	NeedContent    bool
	OriQuestion    *string
	Localization   string
	Site           []string
	ExcludeSites   []string
	Lang           []string
	After          string
	Before         string
	ExactPhrase    string
	ExcludeTerms   []string
	MinRerank      *float64
	MinAuthority   *int
	MaxPerDomain   int
	RawQuery       bool
	NoCache        bool
	Synthesize     bool
	EffectiveQuery string
	Warnings       []string
}

var (
	labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ISO 639-1 全量码表；未知码只记 warning，不拒绝。

type namedString struct {
	name  string
	value string
}

// noCacheRequested 解析可选布尔开关；抽出来是为了不让调用方的圈复杂度计数变高。
func noCacheRequested(v *bool) bool { return v != nil && *v }

// validateFetchExclusivity 校验 fetch 的互斥参数组合。
// 抽成子函数的原因同 noCacheRequested：validateFetch 的复杂度已在基线边上。
func validateFetchExclusivity(rawURL, cacheID string, noCache bool) error {
	if rawURL != "" && cacheID != "" {
		return invalid("url/cache_id", "url 与 cache_id 只能二选一")
	}
	if noCache && cacheID != "" {
		return invalid("no_cache", "no_cache 不能与 cache_id 同传")
	}
	return nil
}

func validateSearch(in *searchInput, cfg *config.Config) (searchParams, error) {
	params := searchParams{
		Query:        in.Query,
		Count:        defaultCount(cfg),
		Detail:       defaultDetail(cfg),
		MaxPerDomain: 3,
		NoCache:      noCacheRequested(in.NoCache),
	}
	if err := validateNoNewlines(in); err != nil {
		return params, err
	}
	if err := validateQuery(in.Query); err != nil {
		return params, err
	}
	if err := applySearchPaging(&params, in); err != nil {
		return params, err
	}
	if err := validateOptionalText(&params, in); err != nil {
		return params, err
	}
	if err := validateCollections(&params, in); err != nil {
		return params, err
	}
	if err := validateFilters(&params, in); err != nil {
		return params, err
	}
	if in.Synthesize != nil && *in.Synthesize {
		return params, validationErr(domain.CodeValSynthDisabled, "synthesize", "阶段一不支持答案合成")
	}
	return buildEffectiveQuery(&params)
}

// applySearchPaging 处理 count/detail/need_content 三者的联动。
func applySearchPaging(params *searchParams, in *searchInput) error {
	if in.Count != nil {
		params.Count = *in.Count
	}
	if params.Count < 1 || params.Count > 50 {
		return validationErr(domain.CodeValCountRange, "count", "必须在 1~50 之间")
	}
	if in.Detail != nil {
		params.Detail = *in.Detail
	}
	if !validDetail(params.Detail) {
		return invalid("detail", "必须是 minimal/snippet/summary/content 之一")
	}
	if in.NeedContent != nil {
		params.NeedContent = *in.NeedContent
	}
	if params.NeedContent {
		params.Detail = "content"
	}
	if params.Detail == "content" {
		params.NeedContent = true
	}
	return nil
}

func defaultCount(cfg *config.Config) int {
	if cfg.Limits.DefaultCount > 0 {
		return cfg.Limits.DefaultCount
	}
	return 10
}

func defaultDetail(cfg *config.Config) string {
	if cfg.Limits.DefaultDetail != "" {
		return cfg.Limits.DefaultDetail
	}
	return "summary"
}

func validDetail(detail string) bool {
	switch detail {
	case "minimal", "snippet", "summary", "content":
		return true
	default:
		return false
	}
}

func invalid(field, detail string) error {
	return validationErr(domain.CodeValInvalidParam, field, detail)
}

func validationErr(code int, field, detail string) error {
	return fmt.Errorf("validate %s: %w", field, domain.NewValidationError(code, field, detail))
}

func validateQuery(query string) error {
	if strings.TrimSpace(query) == "" {
		return invalid("query", "不能为空或全为空白")
	}
	if utf8.RuneCountInString(query) > 128 {
		return validationErr(domain.CodeValQueryTooLong, "query", "长度必须为 1~128 rune")
	}
	return nil
}

func validateNoNewlines(in *searchInput) error {
	for _, field := range searchStringFields(in) {
		if strings.ContainsAny(field.value, "\r\n") {
			return invalid(field.name, "不能包含换行符")
		}
	}
	return nil
}

func searchStringFields(in *searchInput) []namedString {
	fields := make([]namedString, 0, 9+len(in.Site)+len(in.ExcludeSites)+len(in.Lang)+len(in.ExcludeTerms))
	fields = append(fields, namedString{"query", in.Query})
	if in.OriQuestion != nil {
		fields = append(fields, namedString{"ori_question", *in.OriQuestion})
	}
	if in.Localization != nil {
		fields = append(fields, namedString{"localization", *in.Localization})
	}
	if in.After != nil {
		fields = append(fields, namedString{"after", *in.After})
	}
	if in.Before != nil {
		fields = append(fields, namedString{"before", *in.Before})
	}
	if in.ExactPhrase != nil {
		fields = append(fields, namedString{"exact_phrase", *in.ExactPhrase})
	}
	for _, value := range in.Site {
		fields = append(fields, namedString{"site", value})
	}
	for _, value := range in.ExcludeSites {
		fields = append(fields, namedString{"exclude_sites", value})
	}
	for _, value := range in.Lang {
		fields = append(fields, namedString{"lang", value})
	}
	for _, value := range in.ExcludeTerms {
		fields = append(fields, namedString{"exclude_terms", value})
	}
	return fields
}

func validateOptionalText(params *searchParams, in *searchInput) error {
	if err := applyOriQuestion(params, in.OriQuestion); err != nil {
		return err
	}
	if err := applyLocalization(params, in.Localization); err != nil {
		return err
	}
	if err := applyDates(params, in); err != nil {
		return err
	}
	return applyExactPhrase(params, in.ExactPhrase)
}

func applyOriQuestion(params *searchParams, value *string) error {
	if value == nil || *value == "" {
		return nil
	}
	if utf8.RuneCountInString(*value) > 128 {
		return invalid("ori_question", "长度不能超过 128 rune")
	}
	params.OriQuestion = value
	return nil
}

func applyLocalization(params *searchParams, value *string) error {
	if value == nil || *value == "" {
		return nil
	}
	if _, err := netip.ParseAddr(*value); err != nil {
		return invalid("localization", "必须是合法的 IPv4 或 IPv6 地址")
	}
	params.Localization = *value
	return nil
}

func applyDates(params *searchParams, in *searchInput) error {
	dates := []struct {
		field string
		in    *string
		out   *string
	}{
		{"after", in.After, &params.After},
		{"before", in.Before, &params.Before},
	}
	for _, date := range dates {
		if date.in == nil || *date.in == "" {
			continue
		}
		if !toolvalidate.ValidDate(*date.in) {
			return validationErr(domain.CodeValDateFormat, date.field, "必须是 YYYY 或 YYYY-MM-DD 且日期合法")
		}
		*date.out = *date.in
	}
	return nil
}

func applyExactPhrase(params *searchParams, value *string) error {
	if value == nil || *value == "" {
		return nil
	}
	if err := toolvalidate.ValidatePhrase("exact_phrase", *value); err != nil {
		return fmt.Errorf("exact_phrase: %w", err)
	}
	params.ExactPhrase = *value
	return nil
}

func validateCollections(params *searchParams, in *searchInput) error {
	if err := validateDomains("site", in.Site); err != nil {
		return err
	}
	if err := validateDomains("exclude_sites", in.ExcludeSites); err != nil {
		return err
	}
	params.Site = in.Site
	params.ExcludeSites = in.ExcludeSites
	terms := make([]string, 0, len(in.ExcludeTerms))
	for _, term := range in.ExcludeTerms {
		if term == "" {
			continue
		}
		if err := toolvalidate.ValidatePhrase("exclude_terms", term); err != nil {
			return fmt.Errorf("exclude_terms: %w", err)
		}
		terms = append(terms, term)
	}
	params.ExcludeTerms = terms
	return nil
}

func validateDomains(field string, values []string) error {
	for _, value := range values {
		if !validDomain(value) {
			return invalid(field, "非法域名: "+value)
		}
	}
	return nil
}

func validDomain(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	labels := strings.Split(strings.ToLower(value), ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || !labelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func validateFilters(params *searchParams, in *searchInput) error {
	if err := applyMinFilters(params, in); err != nil {
		return err
	}
	if err := applyDomainQuota(params, in); err != nil {
		return err
	}
	if in.RawQuery != nil {
		params.RawQuery = *in.RawQuery
	}
	params.Lang, params.Warnings = toolvalidate.NormalizeLangs(in.Lang, params.Warnings)
	return nil
}

func applyMinFilters(params *searchParams, in *searchInput) error {
	if in.MinRerank != nil {
		if *in.MinRerank < 0 || *in.MinRerank > 3 {
			return invalid("min_rerank", "必须在 0~3 之间")
		}
		params.MinRerank = in.MinRerank
	}
	if in.MinAuthority != nil {
		if *in.MinAuthority < 0 || *in.MinAuthority > 4 {
			return invalid("min_authority", "必须在 0~4 之间")
		}
		params.MinAuthority = in.MinAuthority
	}
	return nil
}

func applyDomainQuota(params *searchParams, in *searchInput) error {
	if in.MaxPerDomain != nil {
		params.MaxPerDomain = *in.MaxPerDomain
	}
	if params.MaxPerDomain < 1 || params.MaxPerDomain > 10 {
		return invalid("max_per_domain", "必须在 1~10 之间")
	}
	return nil
}

func buildEffectiveQuery(params *searchParams) (searchParams, error) {
	req := domain.SearchRequest{
		Query:        params.Query,
		Site:         params.Site,
		ExcludeSites: params.ExcludeSites,
		Lang:         params.Lang,
		After:        params.After,
		Before:       params.Before,
		ExactPhrase:  params.ExactPhrase,
		ExcludeTerms: params.ExcludeTerms,
		RawQuery:     params.RawQuery,
		Count:        &params.Count,
		NeedContent:  &params.NeedContent,
		OriQuestion:  params.OriQuestion,
		Localization: params.Localization,
	}
	effective, warnings := domain.BuildQuery(req)
	warnings = append(params.Warnings, warnings...)
	if n := utf8.RuneCountInString(effective); n > 128 {
		return *params, validationErr(domain.CodeValQueryTooLong, "query",
			fmt.Sprintf("拼装后的 query 为 %d rune，超过 128", n))
	}
	params.EffectiveQuery = effective
	params.Warnings = warnings
	return *params, nil
}
