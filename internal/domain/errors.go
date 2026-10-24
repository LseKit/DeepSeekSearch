// 文件名: errors.go
// 用途: 定义三类错误、错误构造函数、9 字段错误体与 ToPayload/CodeOf/IsRetryable 归类逻辑。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
)

// Coded 是能自报错误码的错误。
//
// ToPayload 先按它归类，这样 resilience 等包可以自定义带码错误，
// 而不用让 domain 反向依赖它们的具体类型。
type Coded interface {
	error
	Code() int
}

// UpstreamError 是上游返回的错误。它保留原始 HTTP 状态与上游消息，
// 因为「上游说了什么」是排查的唯一线索。
//
// 它故意不实现 Coded：ToPayload 在 Code 为 0 时才按 HttpStatus 走 codeForStatus，
// 否则 5xx 兜底与 status==0 的 2998 分支永远不会被触发。
//
// HttpStatus / RequestId 沿用契约里的拼写，不按 Go 缩写真正常改名：下游任务
// 逐字引用这些字段，改名会让契约与代码同时漂移。
type UpstreamError struct {
	Code         int    // 例如 2429
	HttpStatus   int    //nolint:revive // 契约字段名，例如 429
	RequestId    string //nolint:revive // 对外字段名固定为 RequestId
	UpstreamMsg  string // 上游原文，可能含 Key 后 4 位，MUST NOT 进 Error()
	UpstreamType string // 上游 error.type 原文，仅供日志排查，MUST NOT 参与分类
	cause        error  // 底层网络错误，可为 nil
	details      map[string]any
}

// Error 返回便于日志解析的固定格式，绝不包含 UpstreamMsg。
//
// 401 的 message 会回显 Key 后 4 位，把它拼进来就等于
// 把凭据片段写进每一条错误日志。
func (e *UpstreamError) Error() string {
	if e.RequestId == "" {
		return fmt.Sprintf("upstream: %d %s (http %d)", e.Code, nameFor(e.Code), e.HttpStatus)
	}
	return fmt.Sprintf("upstream: %d %s (http %d, request_id=%s)",
		e.Code, nameFor(e.Code), e.HttpStatus, e.RequestId)
}

// Unwrap 暴露底层网络错误，供 errors.Is/As 继续下钻。
func (e *UpstreamError) Unwrap() error { return e.cause }

// UpstreamTypeOf 提取上游 error.type 原文，仅供日志排查。
// MUST NOT 用它做错误分类：实测 401/404/429 的 type 都可能是 invalid_request_error。
func UpstreamTypeOf(err error) string {
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return ue.UpstreamType
	}
	return ""
}

// AppendUpstreamType 在 type 非空时把 upstream_type 追加为日志字段。
// 放在 domain 是为了让 search/fetch 共用同一份判断，避免两处漂移。
func AppendUpstreamType(args []any, err error) []any {
	if typ := UpstreamTypeOf(err); typ != "" {
		return append(args, "upstream_type", typ)
	}
	return args
}

// ValidationError 是我方入参校验失败。
//
// 码值通过 Code() 暴露而不是导出字段：结构体不能同时有名为 Code 的字段与方法，
// 而 ToPayload 的第一步按 Coded 接口归类。
type ValidationError struct {
	code   int
	field  string
	detail string
}

// Error 返回校验失败的可读描述。
func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation: %d %s: %s %s", e.code, nameFor(e.code), e.field, e.detail)
}

// Code 返回我方的错误码。
func (e *ValidationError) Code() int { return e.code }

// LocalError 是本地资源/状态问题（排队超时、熔断、缓存降级、配置非法）。
type LocalError struct {
	code   int
	detail string
	extra  map[string]any
}

// Error 返回本地错误的可读描述。
func (e *LocalError) Error() string {
	return fmt.Sprintf("local: %d %s: %s", e.code, nameFor(e.code), e.detail)
}

// Code 返回本地的错误码。
func (e *LocalError) Code() int { return e.code }

// ErrorPayload 是所有工具失败时的统一返回体。
//
// 声明顺序即 JSON 输出顺序，是冻结契约：5 个必带字段在前（Retryable 的 false
// 也必须出现），4 个条件字段在后且带 omitempty。
//
// UpstreamHttp / RequestId 的拼写与上游响应一致，改成正统缩写会让字段名与
// 契约表不一致。
type ErrorPayload struct {
	Code         int            `json:"code"`
	Name         string         `json:"name"`
	Message      string         `json:"message"`
	Hint         string         `json:"hint"`
	Retryable    bool           `json:"retryable"`
	UpstreamHttp int            `json:"upstream_http,omitempty"` //nolint:revive // 对外字段名固定，保持与上游响应一致
	UpstreamMsg  string         `json:"upstream_msg,omitempty"`
	RequestId    string         `json:"request_id,omitempty"` //nolint:revive // 对外字段名固定，保持与上游响应一致
	Details      map[string]any `json:"details,omitempty"`
}

var (
	_ error = (*UpstreamError)(nil)
	_ error = (*ValidationError)(nil)
	_ error = (*LocalError)(nil)
	_ Coded = (*ValidationError)(nil)
	_ Coded = (*LocalError)(nil)
)

// NewUpstreamError 构造上游 HTTP 错误，Code 由 HttpStatus 按 2000+status 推导。
func NewUpstreamError(httpStatus int, requestID, upstreamMsg string, cause error) *UpstreamError {
	return &UpstreamError{
		Code:        codeForStatus(httpStatus),
		HttpStatus:  httpStatus,
		RequestId:   requestID,
		UpstreamMsg: upstreamMsg,
		cause:       cause,
	}
}

// NewUpstreamParseError 构造「上游 200 但响应体不可解析」的错误（2999）。
// 单独导出是因为它不在 HTTP 错误路径上，没有 status 可映射。
func NewUpstreamParseError(bodyLen int, cause error) error {
	return &UpstreamError{
		Code:        CodeUpParse,
		HttpStatus:  200,
		UpstreamMsg: fmt.Sprintf("响应体 %d 字节无法解析", bodyLen),
		cause:       cause,
	}
}

// NewNetworkError 构造「拿不到 HTTP 响应」的错误（2998，可重试）。
// cause MUST 是 dns / tcp_connect / tls_handshake / attempt_timeout / response_reset 之一。
func NewNetworkError(cause string, attempt int, err error) error {
	return &UpstreamError{
		Code:  CodeUpNetwork,
		cause: err,
		details: map[string]any{
			"cause":   cause,
			"attempt": attempt,
		},
	}
}

// NewValidationError 构造我方入参校验失败。
func NewValidationError(code int, field, detail string) *ValidationError {
	return &ValidationError{code: code, field: field, detail: detail}
}

// NewLocalError 构造本地资源/状态问题；extra 会原样透传为 ErrorPayload.Details。
func NewLocalError(code int, detail string, extra map[string]any) *LocalError {
	return &LocalError{code: code, detail: detail, extra: extra}
}

// hintUpNotFoundSearch 是 ds_search 的 2404 hint。
// 搜索端点 404 与 fetch 的 404 语义完全不同：前者几乎总是 base_url 配错，
// 让调用方去换 query 方向只会浪费一次往返。
const hintUpNotFoundSearch = "搜索端点返回 404，通常是 [upstream].base_url 配置错误（不是查询无结果 —— 查询无结果会返回空列表而非错误）。客户端会自动拼接 /v1/tools/search，base_url 只需写到域名或反向代理前缀，不要重复 /v1。"

// ToPayload 把任意错误归类成统一的 9 字段错误体。
//
// tool 只影响 2404 的 hint：ds_search 的 404 是 base_url 配错，ds_fetch 的 404
// 是取不到正文；空串与 ds_health 都按 ds_fetch 处理。
//
// 判定顺序是契约，先命中先返回，MUST NOT 调换：
// Coded → UpstreamError → 网络错误 → 3099。
// 第 3 步不能省：省了以后网络错误会落到第 4 步，被归成不可重试的 3099，
// 于是「网络错误可重试」的承诺永远不生效。
func ToPayload(err error, tool string) ErrorPayload {
	p := classifyPayload(err)
	if p.Code == CodeUpNotFound && tool == "ds_search" {
		p.Hint = hintUpNotFoundSearch
	}
	return p
}

// classifyPayload 按四步顺序归类，不处理按工具分流的 hint。
func classifyPayload(err error) ErrorPayload {
	var coded Coded
	if errors.As(err, &coded) {
		p := payloadFor(coded.Code())
		// 校验错误必须自报字段名：1000 的 message 是兜底文案，只说「参数不合法」，
		// 四种不同错因会返回同一句话，排障只能靠猜（details 必须给出具体字段）。
		if details := validationDetails(err); len(details) > 0 {
			p.Details = details
		}
		// Extra 的结构化数值 MUST 透传到 details，
		// 否则调用方只能从中文字符串里猜 retry_after_ms。
		var local *LocalError
		if errors.As(err, &local) && len(local.extra) > 0 {
			p.Details = local.extra
		}
		return p
	}

	var up *UpstreamError
	if errors.As(err, &up) {
		code := up.Code
		if code == 0 {
			code = codeForStatus(up.HttpStatus)
		}
		p := payloadFor(code)
		p.UpstreamHttp = up.HttpStatus
		p.UpstreamMsg = clipRunes(up.UpstreamMsg, 512)
		p.RequestId = up.RequestId
		if len(up.details) > 0 {
			p.Details = up.details
		}
		return p
	}

	if isNetworkFailure(err) {
		return payloadFor(CodeUpNetwork)
	}
	return payloadFor(CodeLocInternal)
}

// CodeOf 返回错误对应的数字码；分类规则与 ToPayload 完全一致。
func CodeOf(err error) int { return ToPayload(err, "").Code }

// IsRetryable 只查 CodeRegistry 的 Retryable 标志，MUST NOT 用错误字符串判断。
func IsRetryable(err error) bool {
	info, ok := CodeRegistry[CodeOf(err)]
	return ok && info.Retryable
}

// statusCodeMap 是 2xxx 的固定映射。用查表替代 11 分支 switch，
// 把 codeForStatus 的圈复杂度压到 F-S3 的 10 以内。
var statusCodeMap = map[int]int{
	400: CodeUpValidation,
	401: CodeUpAuth,
	402: CodeUpBalance,
	403: CodeUpForbidden,
	404: CodeUpNotFound,
	408: CodeUpTimeout,
	413: CodeUpTooLarge,
	429: CodeUpRateLimited,
	503: CodeUpUnavailable,
	504: CodeUpGatewayTimeout,
}

// codeForStatus 是 2xxx 的唯一入口。
// 未枚举的 5xx 一律归 2500（可重试）；1xx/3xx 与未枚举的 4xx 归 2000（不可重试）。
func codeForStatus(status int) int {
	if status == 0 {
		return CodeUpNetwork
	}
	if code, ok := statusCodeMap[status]; ok {
		return code
	}
	if status >= 500 && status <= 599 {
		return CodeUpServer
	}
	return CodeUpUnexpected
}

// payloadFor 按码查表生成错误体；未知码统一落 3099，避免返回空文案。
// validationDetails 把校验错误的字段名与原因转成机器可读的 details。
// 独立成函数是为了不让 classifyPayload 的圈复杂度继续上涨（F-S3 上限 10）。
func validationDetails(err error) map[string]any {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	details := make(map[string]any, 2)
	if ve.field != "" {
		details["field"] = ve.field
	}
	if ve.detail != "" {
		details["detail"] = ve.detail
	}
	return details
}

func payloadFor(code int) ErrorPayload {
	info, ok := CodeRegistry[code]
	if !ok {
		code = CodeLocInternal
		info = CodeRegistry[CodeLocInternal]
	}
	return ErrorPayload{
		Code:      code,
		Name:      info.Name,
		Message:   info.Message,
		Hint:      info.Hint,
		Retryable: info.Retryable,
	}
}

// isNetworkFailure 识别「根本没有 HTTP 响应」的错误。
// 只认契约列出的四类，不把任意 io 错误都算进来。
func isNetworkFailure(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.EOF)
}

// clipRunes 按 rune 截断，避免把中文或 emoji 切出半个字符。
func clipRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// nameFor 返回码的稳定英文名；未知码返回 UNKNOWN，仅用于错误字符串。
func nameFor(code int) string {
	if info, ok := CodeRegistry[code]; ok {
		return info.Name
	}
	return "UNKNOWN"
}
