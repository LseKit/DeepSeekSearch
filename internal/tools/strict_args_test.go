// 文件名: strict_args_test.go
// 用途: 验证入参严格性（未知键必拒、端口越界本地拦、反向日期区间告警、配额取配置）与组合工具的标题/摘录兜底。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/deepseek"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func rawArgsRequest(args string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: []byte(args)}}
}

func TestParseSearchRequestRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	_, err := parseSearchRequest(rawArgsRequest(`{"query":"go","countt":5}`))
	if err == nil {
		t.Fatal("拼错的参数名 MUST 被拒：静默用默认值就是「给了参数但不生效」")
	}
	if got := domain.CodeOf(err); got != domain.CodeValInvalidParam {
		t.Fatalf("code = %d, want %d", got, domain.CodeValInvalidParam)
	}
	if !strings.Contains(err.Error(), "countt") {
		t.Fatalf("错误里 MUST 点出未知键名，调用方才知道改哪个: %v", err)
	}
}

func TestParseFetchRequestRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	_, err := parseFetchRequest(rawArgsRequest(`{"url":"https://example.com","no_cachee":true}`))
	if err == nil {
		t.Fatal("拼错的参数名 MUST 被拒")
	}
	if !strings.Contains(err.Error(), "no_cachee") {
		t.Fatalf("错误里 MUST 点出未知键名: %v", err)
	}
}

func TestParseSearchRequestToleratesEmptyArguments(t *testing.T) {
	t.Parallel()

	in, err := parseSearchRequest(rawArgsRequest(""))
	if err != nil {
		t.Fatalf("空 arguments 该交给下游按「query 为空」报错，而不是解析失败: %v", err)
	}
	if in == nil || in.Query != "" {
		t.Fatalf("in = %+v", in)
	}
}

func TestValidateFetchURLRejectsPortOutOfRange(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	for _, raw := range []string{"http://example.com:99999/", "http://example.com:70000/", "http://example.com:0/"} {
		err := validateFetchURL(raw, &cfg)
		if err == nil {
			t.Fatalf("%s MUST 被本地拒绝：放过去会白花一次计费，拿回来的是上游 400（2400）", raw)
		}
		if got := domain.CodeOf(err); got != domain.CodeValInvalidParam {
			t.Fatalf("%s code = %d, want %d", raw, got, domain.CodeValInvalidParam)
		}
	}
	if err := validateFetchURL("https://example.com:8443/x", &cfg); err != nil {
		t.Fatalf("合法端口被拒: %v", err)
	}
	if err := validateFetchURL("https://example.com/x", &cfg); err != nil {
		t.Fatalf("无端口被拒: %v", err)
	}
}

func TestValidateSearchWarnsOnReversedDateRange(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	params, err := validateSearch(&searchInput{
		Query: "go", After: strPtr("2025-01-01"), Before: strPtr("2020-01-01"),
	}, &cfg)
	if err != nil {
		t.Fatalf("反向区间只该告警不该拒绝: %v", err)
	}
	joined := strings.Join(params.Warnings, "|")
	if !strings.Contains(joined, "after=2025-01-01 晚于 before=2020-01-01") {
		t.Fatalf("warnings = %v", params.Warnings)
	}

	ordered, err := validateSearch(&searchInput{
		Query: "go", After: strPtr("2020"), Before: strPtr("2025-01-01"),
	}, &cfg)
	if err != nil {
		t.Fatalf("正向区间不该报错: %v", err)
	}
	if strings.Contains(strings.Join(ordered.Warnings, "|"), "晚于") {
		t.Fatalf("正向区间不该告警: %v", ordered.Warnings)
	}
}

func TestValidateSearchTakesDomainQuotaFromConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Dedupe.MaxPerDomain = 5
	params, err := validateSearch(&searchInput{Query: "go"}, &cfg)
	if err != nil {
		t.Fatalf("validateSearch: %v", err)
	}
	if params.MaxPerDomain != 5 {
		t.Fatalf("配额默认值没取配置: %d", params.MaxPerDomain)
	}

	cfg.Dedupe.MaxPerDomain = 0 // 契约：0 = 不做域名配额
	params, err = validateSearch(&searchInput{Query: "go"}, &cfg)
	if err != nil {
		t.Fatalf("配置成 0 不该被判非法: %v", err)
	}
	if params.MaxPerDomain != 0 {
		t.Fatalf("0 必须原样透传，否则「不限配额」不可达: %d", params.MaxPerDomain)
	}

	// 调用方显式传参时仍按 schema 的 1~10 校验。
	if _, err = validateSearch(&searchInput{Query: "go", MaxPerDomain: ptrInt(0)}, &cfg); err == nil {
		t.Fatal("显式传 0 MUST 被 schema 区间拒绝")
	}
}

func ptrInt(n int) *int { return &n }

func TestBuildBundleFallsBackToHostForEmptyTitle(t *testing.T) {
	t.Parallel()

	sources := []researchSource{{
		Index: 1, Title: "   ", Ok: true,
		URL:     "https://tip.golang.org/src/context/context.go?m=text",
		Content: "正文",
	}}
	bundle, omitted := buildBundle(sources)
	if omitted != 0 {
		t.Fatalf("omitted = %d, want 0", omitted)
	}
	if !strings.Contains(bundle, "## [1] tip.golang.org") {
		t.Fatalf("空标题没有兜底成主机名: %q", bundle)
	}
	if strings.Contains(bundle, "## [1] \n") {
		t.Fatalf("MUST NOT 留下空标题行: %q", bundle)
	}
}

func TestContentOrOutlineCarriesPreviews(t *testing.T) {
	t.Parallel()

	out := &fetchOutput{Sections: []fetchSectionOutput{
		{ID: "c0", Chars: 8000, Preview: "<untrusted_content id=\"a\" source=\"https://x\">首段摘录</untrusted_content>"},
		{ID: "c1", Title: "有标题的节", Chars: 1200, Preview: "摘录二"},
	}}
	got := contentOrOutline(out)
	if !strings.Contains(got, "首段摘录") || !strings.Contains(got, "摘录二") {
		t.Fatalf("大文档降级后 MUST 带上每节摘录，否则调用方给了预算却拿到零正文: %q", got)
	}
	if !strings.Contains(got, "- [c1] 有标题的节") {
		t.Fatalf("有标题的节要连标题一起给: %q", got)
	}
	if strings.Contains(got, "- [c0] \n") {
		t.Fatalf("无标题的节 MUST NOT 留下空标题: %q", got)
	}
}

func TestBodyLogRedactsUpstreamSecrets(t *testing.T) {
	t.Parallel()

	const leaked = "sk-abcdef1234567890"
	client := &fakeClient{searchFn: func(ctx context.Context, _ domain.SearchRequest) (domain.SearchResponse, error) {
		if hook := deepseek.ExchangeHookFrom(ctx); hook != nil {
			hook(ctx, []byte(`{"query":"q"}`), []byte(`{"error":{"message":"invalid api key: `+leaked+`"}}`))
		}
		return domain.SearchResponse{}, errors.New("upstream rejected")
	}}
	deps, buf := newTestDeps(t, client, func(d *Deps) { d.Cfg.Log.MaxBodyLogKb = 4 })
	_ = invokeSearch(t, &deps, &searchInput{Query: "q"})

	logs := buf.String()
	if !strings.Contains(logs, "resp_body") {
		t.Fatalf("报文日志没开起来，这条断言等于没测: %s", logs)
	}
	if strings.Contains(logs, leaked) {
		t.Fatalf("上游响应体里的 Key 原样落盘: %s", logs)
	}
}
