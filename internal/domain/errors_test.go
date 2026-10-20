// 文件名: errors_test.go
// 用途: 校验错误归类顺序、可重试判定、构造与 9 字段错误体的 JSON 契约。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

type fakeNetError struct{}

func (fakeNetError) Error() string   { return "network down" }
func (fakeNetError) Timeout() bool   { return true }
func (fakeNetError) Temporary() bool { return false }

type fakeCodedError struct{ code int }

func (e fakeCodedError) Error() string { return "fake coded error" }
func (e fakeCodedError) Code() int     { return e.code }

func TestToPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantRetry  bool
		wantHTTP   int
		wantDetail bool
	}{
		{"nil 归 3099", nil, CodeLocInternal, false, 0, false},
		{"LocalError 走 Coded 分支", NewLocalError(CodeLocQueueTimeout, "排队超时", map[string]any{"queue_depth": 12}), CodeLocQueueTimeout, true, 0, true},
		{"ValidationError 走 Coded 分支", NewValidationError(CodeValCountRange, "count", "超范围"), CodeValCountRange, false, 0, false},
		{"外部 Coded 错误走第一步", fakeCodedError{code: CodeLocCircuitOpen}, CodeLocCircuitOpen, true, 0, false},
		{"UpstreamError 429", NewUpstreamError(429, "rid-1", "rate limited", nil), CodeUpRateLimited, true, 429, false},
		{"UpstreamError status 0 归 2998", &UpstreamError{HttpStatus: 0}, CodeUpNetwork, true, 0, false},
		{"未枚举 5xx 兜底 2500", NewUpstreamError(502, "", "", nil), CodeUpServer, true, 502, false},
		{"3xx 归 2000 而不是 2500", NewUpstreamError(302, "", "", nil), CodeUpUnexpected, false, 302, false},
		{"解析失败 2999", NewUpstreamParseError(128, io.ErrUnexpectedEOF), CodeUpParse, false, 200, false},
		{"网络错误带 details", NewNetworkError("dns", 2, io.ErrUnexpectedEOF), CodeUpNetwork, true, 0, true},
		{"net.Error 归 2998", fakeNetError{}, CodeUpNetwork, true, 0, false},
		{"DeadlineExceeded 归 2998", context.DeadlineExceeded, CodeUpNetwork, true, 0, false},
		{"UnexpectedEOF 归 2998", io.ErrUnexpectedEOF, CodeUpNetwork, true, 0, false},
		{"EOF 归 2998", io.EOF, CodeUpNetwork, true, 0, false},
		{"未知错误归 3099", errors.New("boom"), CodeLocInternal, false, 0, false},
		{"包装后的 Coded 错误仍可识别", fmt.Errorf("wrap: %w", NewLocalError(CodeLocCircuitOpen, "熔断", nil)), CodeLocCircuitOpen, true, 0, false},
		{"包装后的 UpstreamError 仍可识别", fmt.Errorf("wrap: %w", NewUpstreamError(404, "", "", nil)), CodeUpNotFound, false, 404, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := ToPayload(tt.err, "")
			info, ok := CodeRegistry[tt.wantCode]
			if !ok {
				t.Fatalf("CodeRegistry 缺少 %d", tt.wantCode)
			}
			if p.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", p.Code, tt.wantCode)
			}
			if p.Name != info.Name {
				t.Errorf("Name = %q, want %q", p.Name, info.Name)
			}
			if p.Message != info.Message || p.Hint != info.Hint {
				t.Errorf("Message/Hint 未取自 CodeRegistry")
			}
			if p.Retryable != tt.wantRetry {
				t.Errorf("Retryable = %v, want %v", p.Retryable, tt.wantRetry)
			}
			if p.UpstreamHttp != tt.wantHTTP {
				t.Errorf("UpstreamHttp = %d, want %d", p.UpstreamHttp, tt.wantHTTP)
			}
			if got := len(p.Details) > 0; got != tt.wantDetail {
				t.Errorf("有 Details = %v, want %v", got, tt.wantDetail)
			}
		})
	}
}

func TestToPayloadToolHint(t *testing.T) {
	t.Parallel()

	err := NewUpstreamError(404, "rid-1", "failed to fetch URL", nil)
	fetch := ToPayload(err, "ds_fetch")
	search := ToPayload(err, "ds_search")

	if want := CodeRegistry[CodeUpNotFound].Hint; fetch.Hint != want {
		t.Errorf("ds_fetch Hint = %q, want CodeRegistry 的 fetch 版 %q", fetch.Hint, want)
	}
	if search.Hint != hintUpNotFoundSearch {
		t.Errorf("ds_search Hint = %q, want %q", search.Hint, hintUpNotFoundSearch)
	}
	if fetch.Hint == search.Hint {
		t.Error("ds_fetch 与 ds_search 的 2404 hint 必须不同")
	}
	if got := ToPayload(err, "").Hint; got != fetch.Hint {
		t.Errorf("空串 Hint = %q, 应按 ds_fetch 处理", got)
	}
	if got := ToPayload(err, "ds_health").Hint; got != fetch.Hint {
		t.Errorf("ds_health Hint = %q, 应按 ds_fetch 处理", got)
	}

	// tool 只影响 2404，其他码的 hint 不得变化。
	rate := NewUpstreamError(429, "rid-2", "rate limited", nil)
	if a, b := ToPayload(rate, "ds_search").Hint, ToPayload(rate, "ds_fetch").Hint; a != b {
		t.Errorf("非 2404 的 hint 被 tool 影响：%q != %q", a, b)
	}
}

func TestToPayloadPreservesUpstreamFields(t *testing.T) {
	t.Parallel()

	err := NewUpstreamError(404, "9b8f3625", "failed to fetch URL", nil)
	p := ToPayload(err, "")

	if p.UpstreamHttp != 404 {
		t.Errorf("UpstreamHttp = %d, want 404", p.UpstreamHttp)
	}
	if p.UpstreamMsg != "failed to fetch URL" {
		t.Errorf("UpstreamMsg = %q", p.UpstreamMsg)
	}
	if p.RequestId != "9b8f3625" {
		t.Errorf("RequestId = %q", p.RequestId)
	}
}

func TestToPayloadTruncatesUpstreamMsgByRune(t *testing.T) {
	t.Parallel()

	msg := strings.Repeat("错", 600)
	p := ToPayload(NewUpstreamError(500, "rid", msg, nil), "")

	if got, want := len([]rune(p.UpstreamMsg)), 512; got != want {
		t.Fatalf("UpstreamMsg rune 数 = %d, want %d", got, want)
	}
	if !utf8.ValidString(p.UpstreamMsg) {
		t.Error("截断后不是合法 UTF-8，说明按 byte 切了")
	}
}

func TestCodeOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, CodeLocInternal},
		{"上游 401", NewUpstreamError(401, "", "", nil), CodeUpAuth},
		{"参数 1010", NewValidationError(CodeValQueryTooLong, "query", "超长"), CodeValQueryTooLong},
		{"未知错误", errors.New("boom"), CodeLocInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CodeOf(tt.err); got != tt.want {
				t.Errorf("CodeOf() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"429 可重试", NewUpstreamError(429, "", "", nil), true},
		{"504 可重试", NewUpstreamError(504, "", "", nil), true},
		{"404 不可重试（实测 403 也塌缩成 404，重试只会付费）", NewUpstreamError(404, "", "", nil), false},
		{"402 余额不足绝不可重试", NewUpstreamError(402, "", "", nil), false},
		{"3000 排队超时可重试", NewLocalError(CodeLocQueueTimeout, "排队", nil), true},
		{"3010 预算耗尽不可重试", NewLocalError(CodeLocRetryBudget, "预算", nil), false},
		{"错误文本里出现 2429 也不影响判定", errors.New("2429 rate limited"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRetryable(tt.err); got != tt.want {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodeForStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   int
	}{
		{0, CodeUpNetwork},
		{400, CodeUpValidation},
		{401, CodeUpAuth},
		{402, CodeUpBalance},
		{403, CodeUpForbidden},
		{404, CodeUpNotFound},
		{408, CodeUpTimeout},
		{413, CodeUpTooLarge},
		{429, CodeUpRateLimited},
		{500, CodeUpServer},
		{502, CodeUpServer},
		{503, CodeUpUnavailable},
		{504, CodeUpGatewayTimeout},
		{599, CodeUpServer},
		{100, CodeUpUnexpected},
		{302, CodeUpUnexpected},
		{418, CodeUpUnexpected},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			t.Parallel()
			if got := codeForStatus(tt.status); got != tt.want {
				t.Errorf("codeForStatus(%d) = %d, want %d", tt.status, got, tt.want)
			}
		})
	}
}

func TestNewUpstreamError(t *testing.T) {
	t.Parallel()

	e := NewUpstreamError(429, "rid-1", "rate limited", io.ErrUnexpectedEOF)

	if e.Code != CodeUpRateLimited {
		t.Errorf("Code = %d, want %d", e.Code, CodeUpRateLimited)
	}
	if e.HttpStatus != 429 {
		t.Errorf("HttpStatus = %d, want 429", e.HttpStatus)
	}
	if e.RequestId != "rid-1" {
		t.Errorf("RequestId = %q, want rid-1", e.RequestId)
	}
	if e.UpstreamMsg != "rate limited" {
		t.Errorf("UpstreamMsg = %q", e.UpstreamMsg)
	}
	if !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Error("Unwrap 未暴露底层错误")
	}

	got := e.Error()
	for _, want := range []string{"2429", "UP_RATE_LIMITED", "http 429", "request_id=rid-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, 缺少 %q", got, want)
		}
	}
	if strings.Contains(got, "rate limited") {
		t.Error("Error() 不应拼接 UpstreamMsg（可能含 Key 片段）")
	}

	if got := NewUpstreamError(0, "", "", nil).Code; got != CodeUpNetwork {
		t.Errorf("status=0 的 Code = %d, want %d", got, CodeUpNetwork)
	}
	if got := NewUpstreamError(502, "", "", nil).Code; got != CodeUpServer {
		t.Errorf("status=502 的 Code = %d, want %d", got, CodeUpServer)
	}
}

func TestNewUpstreamParseError(t *testing.T) {
	t.Parallel()

	err := NewUpstreamParseError(128, io.ErrUnexpectedEOF)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Error("未暴露底层解析错误")
	}

	var up *UpstreamError
	if !errors.As(err, &up) {
		t.Fatal("NewUpstreamParseError 未返回 *UpstreamError")
	}
	if up.Code != CodeUpParse || up.HttpStatus != 200 {
		t.Errorf("Code/HttpStatus = %d/%d, want %d/200", up.Code, up.HttpStatus, CodeUpParse)
	}
	if p := ToPayload(err, ""); p.Code != CodeUpParse || p.UpstreamHttp != 200 {
		t.Errorf("ToPayload = %d/http %d, want %d/200", p.Code, p.UpstreamHttp, CodeUpParse)
	}
}

func TestNewNetworkError(t *testing.T) {
	t.Parallel()

	err := NewNetworkError("dns", 2, io.ErrUnexpectedEOF)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Error("未暴露底层网络错误")
	}

	p := ToPayload(err, "")
	if p.Code != CodeUpNetwork || !p.Retryable {
		t.Errorf("Code/Retryable = %d/%v, want %d/true", p.Code, p.Retryable, CodeUpNetwork)
	}
	if got := p.Details["cause"]; got != "dns" {
		t.Errorf("Details[cause] = %v, want dns", got)
	}
	if got := p.Details["attempt"]; got != 2 {
		t.Errorf("Details[attempt] = %v, want 2", got)
	}
}

func TestNewValidationError(t *testing.T) {
	t.Parallel()

	e := NewValidationError(CodeValCountRange, "count", "必须在 1~50 之间，实际 100")
	if e.Code() != CodeValCountRange {
		t.Errorf("Code() = %d, want %d", e.Code(), CodeValCountRange)
	}
	for _, want := range []string{"1020", "VAL_COUNT_RANGE", "count", "1~50"} {
		if !strings.Contains(e.Error(), want) {
			t.Errorf("Error() = %q, 缺少 %q", e.Error(), want)
		}
	}
	if p := ToPayload(e, ""); p.Code != CodeValCountRange || p.Retryable {
		t.Errorf("ToPayload = %d/retryable %v", p.Code, p.Retryable)
	}
}

func TestNewLocalError(t *testing.T) {
	t.Parallel()

	extra := map[string]any{"queue_depth": 12, "waited_ms": 30000}
	e := NewLocalError(CodeLocQueueTimeout, "请求排队超时", extra)

	if e.Code() != CodeLocQueueTimeout {
		t.Errorf("Code() = %d, want %d", e.Code(), CodeLocQueueTimeout)
	}
	if !strings.Contains(e.Error(), "LOC_QUEUE_TIMEOUT") {
		t.Errorf("Error() = %q, 缺少错误名", e.Error())
	}

	p := ToPayload(e, "")
	if p.Details["queue_depth"] != 12 || p.Details["waited_ms"] != 30000 {
		t.Errorf("Details = %#v, want 原样透传 Extra", p.Details)
	}
}

func TestErrorPayloadShape(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(ErrorPayload{})
	want := []struct {
		name string
		tag  string
	}{
		{"Code", "code"},
		{"Name", "name"},
		{"Message", "message"},
		{"Hint", "hint"},
		{"Retryable", "retryable"},
		{"UpstreamHttp", "upstream_http,omitempty"},
		{"UpstreamMsg", "upstream_msg,omitempty"},
		{"RequestId", "request_id,omitempty"},
		{"Details", "details,omitempty"},
	}

	if got := typ.NumField(); got != len(want) {
		t.Fatalf("ErrorPayload 字段数 = %d, want %d", got, len(want))
	}
	for i, w := range want {
		f := typ.Field(i)
		if f.Name != w.name {
			t.Errorf("第 %d 个字段 = %s, want %s", i+1, f.Name, w.name)
		}
		if got := f.Tag.Get("json"); got != w.tag {
			t.Errorf("%s 的 json tag = %q, want %q", w.name, got, w.tag)
		}
	}
}

func TestErrorPayloadJSONGolden(t *testing.T) {
	t.Parallel()

	p := ErrorPayload{
		Code:         2404,
		Name:         "UP_NOT_FOUND",
		Message:      "上游未能取到该网页的可用正文",
		Hint:         "建议换来源",
		Retryable:    false,
		UpstreamHttp: 404,
		UpstreamMsg:  "failed to fetch URL",
		RequestId:    "rid-1",
		Details:      map[string]any{"host": "example.com"},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"code":2404,"name":"UP_NOT_FOUND","message":"上游未能取到该网页的可用正文","hint":"建议换来源","retryable":false,"upstream_http":404,"upstream_msg":"failed to fetch URL","request_id":"rid-1","details":{"host":"example.com"}}`
	if got := string(raw); got != want {
		t.Fatalf("JSON = %s\nwant  %s", got, want)
	}

	minimal := ErrorPayload{Code: 1000, Name: "VAL_INVALID_PARAM", Message: "参数不合法", Hint: "修正参数"}
	raw, err = json.Marshal(minimal)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"retryable":false`) {
		t.Errorf("retryable=false 必须显式出现，实际 %s", got)
	}
	for _, absent := range []string{"upstream_http", "upstream_msg", "request_id", "details"} {
		if strings.Contains(got, absent) {
			t.Errorf("零值条件字段 %q 应省略，实际 %s", absent, got)
		}
	}
}
