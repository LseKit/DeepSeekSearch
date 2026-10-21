// 文件名: research_extract.go
// 用途: 实现 ds_research（搜索+并发抓取+带编号材料）与 ds_extract（批量抓取）两个组合工具。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/mcputil"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
	"github.com/LseKit/DeepSeekSearch/internal/toolschema"
)

const (
	researchToolName  = "ds_research"
	extractToolName   = "ds_extract"
	bundleMaxRunes    = 32000
	researchParallel  = 3
	defaultSources    = 3
	defaultSourceChar = 6000
	defaultExtractChr = 4000
	defaultExtractPar = 4
	maxExtractURLs    = 10
)

type researchTool struct{}

// Name 返回 MCP 工具名。
func (t *researchTool) Name() string { return researchToolName }

// Register 把 ds_research 注册到 server。
func (t *researchTool) Register(srv *mcp.Server, deps Deps) { //nolint:gocritic // 签名由 11 号 §五 固定
	srv.AddTool(researchDefinition(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleResearch(ctx, &deps, req), nil
	})
}

func researchDefinition() *mcp.Tool {
	return &mcp.Tool{
		Name:        researchToolName,
		Title:       "DeepSeek 研究材料",
		Description: toolschema.ResearchDescription,
		InputSchema: json.RawMessage(toolschema.ResearchInput),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPtr(true)},
	}
}

type extractTool struct{}

// Name 返回 MCP 工具名。
func (t *extractTool) Name() string { return extractToolName }

// Register 把 ds_extract 注册到 server。
func (t *extractTool) Register(srv *mcp.Server, deps Deps) { //nolint:gocritic // 签名由 11 号 §五 固定
	srv.AddTool(extractDefinition(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleExtract(ctx, &deps, req), nil
	})
}

func extractDefinition() *mcp.Tool {
	return &mcp.Tool{
		Name:        extractToolName,
		Title:       "DeepSeek 批量抓取",
		Description: toolschema.ExtractDescription,
		InputSchema: json.RawMessage(toolschema.ExtractInput),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPtr(true)},
	}
}

type researchInput struct {
	Question       string `json:"question"`
	MaxSources     *int   `json:"max_sources"`
	PerSourceChars *int   `json:"per_source_chars"`
}

type researchSource struct {
	Index       int     `json:"index"`
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Date        *string `json:"date"`
	RerankScore float64 `json:"rerank_score"`
	Chars       int     `json:"chars"`
	Ok          bool    `json:"ok"`
	Error       string  `json:"error,omitempty"`
	Content     string  `json:"-"`
}

type researchOutput struct {
	Question string           `json:"question"`
	Sources  []researchSource `json:"sources"`
	Bundle   string           `json:"bundle"`
	Warnings []string         `json:"warnings"`
}

// handleResearch 复用 ds_search / ds_fetch 的完整管线（缓存、限流、重试、预算）。
// 不直接调 deepseek.Client：那会绕开缓存与限流，组合工具就会成为计费放大器。
func handleResearch(ctx context.Context, deps *Deps, req *mcp.CallToolRequest) *mcp.CallToolResult {
	if deps.Clock == nil {
		deps.Clock = resilience.SystemClock()
	}
	started := deps.Clock.Now()
	logger := deps.Logger.With("call_id", domain.NewCallID(), "tool", researchToolName)
	logger.Info("call.begin")

	var in researchInput
	if err := mcputil.ParseInput(req, &in); err != nil {
		return finishTool(deps, logger, started, researchToolName, errorResult(err, researchToolName), err)
	}
	if err := validateResearch(&in); err != nil {
		return finishTool(deps, logger, started, researchToolName, errorResult(err, researchToolName), err)
	}
	items, searchRes, err := runResearchSearch(ctx, deps, in)
	if err != nil {
		return finishTool(deps, logger, started, researchToolName, searchRes, err)
	}
	sources, warnings := runResearchFetch(ctx, deps, items, *in.PerSourceChars)
	bundle, truncated := buildBundle(sources)
	if truncated {
		warnings = append(warnings, fmt.Sprintf("bundle 因 %d rune 预算截断，其余来源见 sources[]", bundleMaxRunes))
	}
	raw, marshalErr := json.Marshal(researchOutput{Question: in.Question, Sources: sources, Bundle: bundle, Warnings: warnings})
	if marshalErr != nil {
		return finishTool(deps, logger, started, researchToolName, errorResult(marshalErr, researchToolName), marshalErr)
	}
	return finishTool(deps, logger, started, researchToolName, mcputil.TextResult(string(raw)), nil)
}

func runResearchSearch(ctx context.Context, deps *Deps, in researchInput) ([]searchOutputItem, *mcp.CallToolResult, error) {
	count := *in.MaxSources
	detail := "summary"
	req, err := mcputil.Request(searchInput{Query: in.Question, Count: &count, Detail: &detail})
	if err != nil {
		return nil, errorResult(err, researchToolName), err
	}
	res := handleSearch(ctx, deps, req)
	if res.IsError {
		return nil, res, errors.New("research: 搜索子调用失败")
	}
	var out searchOutput
	if err := json.Unmarshal([]byte(mcputil.ResultText(res)), &out); err != nil {
		return nil, errorResult(err, researchToolName), err
	}
	items := out.Results
	if len(items) > count {
		items = items[:count]
	}
	return items, nil, nil
}

func runResearchFetch(ctx context.Context, deps *Deps, items []searchOutputItem, maxChars int) (sources []researchSource, warnings []string) {
	sources = make([]researchSource, len(items))
	sem := make(chan struct{}, researchParallel)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, payload := runFetchSub(ctx, deps, items[i].URL, maxChars)
			src := researchSource{Index: i + 1, Title: items[i].Title, URL: items[i].URL,
				Date: items[i].Date, RerankScore: items[i].RerankScore}
			if payload != nil {
				src.Error = payload.Message
			} else {
				src.Ok, src.Content, src.Chars = true, out.Content, out.TotalChars
			}
			sources[i] = src
		}(i)
	}
	wg.Wait()

	warnings = make([]string, 0, len(sources))
	for _, src := range sources {
		if !src.Ok {
			warnings = append(warnings, fmt.Sprintf("来源 [%d] 抓取失败：%s", src.Index, src.Error))
		}
	}
	return sources, warnings
}

func buildBundle(sources []researchSource) (string, bool) {
	var b strings.Builder
	for _, src := range sources {
		if src.Ok {
			fmt.Fprintf(&b, "## [%d] %s\n%s\n\n%s\n\n", src.Index, src.Title, src.URL, src.Content)
		}
	}
	out := b.String()
	if utf8.RuneCountInString(out) > bundleMaxRunes {
		sliced, _ := domain.SliceByRune(out, 0, bundleMaxRunes)
		return sliced, true
	}
	return out, false
}

func validateResearch(in *researchInput) error {
	in.Question = strings.TrimSpace(in.Question)
	if n := utf8.RuneCountInString(in.Question); n == 0 || n > 128 {
		return invalid("question", "必须 1~128 rune")
	}
	if in.MaxSources == nil {
		n := defaultSources
		in.MaxSources = &n
	} else if *in.MaxSources < 1 || *in.MaxSources > 5 {
		return invalid("max_sources", "必须在 1~5 之间")
	}
	if in.PerSourceChars == nil {
		n := defaultSourceChar
		in.PerSourceChars = &n
	} else if *in.PerSourceChars < 1000 || *in.PerSourceChars > 20000 {
		return invalid("per_source_chars", "必须在 1000~20000 之间")
	}
	return nil
}

type extractInput struct {
	URLs         []string `json:"urls"`
	MaxCharsEach *int     `json:"max_chars_each"`
	Concurrency  *int     `json:"concurrency"`
}

type extractItem struct {
	Index   int                  `json:"index"`
	URL     string               `json:"url"`
	Ok      bool                 `json:"ok"`
	Title   string               `json:"title,omitempty"`
	Content string               `json:"content,omitempty"`
	Chars   int                  `json:"chars"`
	Error   *domain.ErrorPayload `json:"error,omitempty"`
}

type extractOutput struct {
	Items    []extractItem `json:"items"`
	Warnings []string      `json:"warnings"`
}

// handleExtract 与 ds_research 同理：逐条走 ds_fetch 的完整管线。
func handleExtract(ctx context.Context, deps *Deps, req *mcp.CallToolRequest) *mcp.CallToolResult {
	if deps.Clock == nil {
		deps.Clock = resilience.SystemClock()
	}
	started := deps.Clock.Now()
	logger := deps.Logger.With("call_id", domain.NewCallID(), "tool", extractToolName)
	logger.Info("call.begin")

	var in extractInput
	if err := mcputil.ParseInput(req, &in); err != nil {
		return finishTool(deps, logger, started, extractToolName, errorResult(err, extractToolName), err)
	}
	if err := validateExtract(&in); err != nil {
		return finishTool(deps, logger, started, extractToolName, errorResult(err, extractToolName), err)
	}
	items := runExtract(ctx, deps, in)
	warnings := make([]string, 0)
	for _, item := range items {
		if !item.Ok {
			warnings = append(warnings, fmt.Sprintf("URL [%d] 抓取失败：%s", item.Index, item.Error.Message))
		}
	}
	raw, marshalErr := json.Marshal(extractOutput{Items: items, Warnings: warnings})
	if marshalErr != nil {
		return finishTool(deps, logger, started, extractToolName, errorResult(marshalErr, extractToolName), marshalErr)
	}
	return finishTool(deps, logger, started, extractToolName, mcputil.TextResult(string(raw)), nil)
}

func runExtract(ctx context.Context, deps *Deps, in extractInput) []extractItem {
	items := make([]extractItem, len(in.URLs))
	sem := make(chan struct{}, *in.Concurrency)
	var wg sync.WaitGroup
	for i, url := range in.URLs {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, payload := runFetchSub(ctx, deps, url, *in.MaxCharsEach)
			item := extractItem{Index: i + 1, URL: url}
			if payload != nil {
				item.Error = payload
			} else {
				item.Ok, item.Title, item.Content, item.Chars = true, out.Title, out.Content, out.TotalChars
			}
			items[i] = item
		}(i, url)
	}
	wg.Wait()
	return items
}

func validateExtract(in *extractInput) error {
	if err := mcputil.ValidateURLList(in.URLs, maxExtractURLs); err != nil {
		return fmt.Errorf("urls: %w", err)
	}
	if in.MaxCharsEach == nil {
		n := defaultExtractChr
		in.MaxCharsEach = &n
	} else if *in.MaxCharsEach < 1000 || *in.MaxCharsEach > 20000 {
		return invalid("max_chars_each", "必须在 1000~20000 之间")
	}
	if in.Concurrency == nil {
		n := defaultExtractPar
		in.Concurrency = &n
	} else if *in.Concurrency < 1 || *in.Concurrency > 8 {
		return invalid("concurrency", "必须在 1~8 之间")
	}
	return nil
}

// runFetchSub 走 ds_fetch 的完整管线（缓存/限流/重试/反转义），返回解析输出或错误体。
func runFetchSub(ctx context.Context, deps *Deps, url string, maxChars int) (fetchOutput, *domain.ErrorPayload) {
	limit := maxChars
	req, err := mcputil.Request(fetchInput{URL: &url, MaxChars: &limit})
	if err != nil {
		return fetchOutput{}, &domain.ErrorPayload{Code: domain.CodeValInvalidParam, Name: "VAL_INVALID_PARAM", Message: err.Error()}
	}
	res := handleFetch(ctx, deps, req)
	if res.IsError {
		var payload domain.ErrorPayload
		if err := json.Unmarshal([]byte(mcputil.ResultText(res)), &payload); err != nil {
			return fetchOutput{}, &domain.ErrorPayload{Code: domain.CodeLocInternal, Name: "LOC_INTERNAL", Message: "解析错误体失败"}
		}
		return fetchOutput{}, &payload
	}
	var out fetchOutput
	if err := json.Unmarshal([]byte(mcputil.ResultText(res)), &out); err != nil {
		return fetchOutput{}, &domain.ErrorPayload{Code: domain.CodeLocInternal, Name: "LOC_INTERNAL", Message: "解析抓取结果失败"}
	}
	return out, nil
}

// finishTool 统一记录 call.end 与用量；组合工具本身不计费（计费发生在子调用）。
func finishTool(deps *Deps, logger *slog.Logger, started time.Time, tool string, res *mcp.CallToolResult, err error) *mcp.CallToolResult {
	ok := err == nil && res != nil && !res.IsError
	logger.Info("call.end", "ms", deps.Clock.Now().Sub(started).Milliseconds(), "ok", ok)
	if deps.Usage != nil {
		deps.Usage.Record(tool, false, false, err)
	}
	return res
}
