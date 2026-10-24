// 文件名: error.go
// 用途: 上游错误信封解析、状态码映射、request_id 提取与密钥二次脱敏。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

var requestIDRe = regexp.MustCompile(`\(request_id:\s*([0-9a-fA-F-]{8,64})\)`)

// upstreamError 是上游错误信封。Param 必须可缺省（实测恒不存在）。
// Code/Type 只保留原文供排查，MUST NOT 参与错误分类。
type upstreamError struct {
	Error struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Param   *string `json:"param"`
		Code    string  `json:"code"`
	} `json:"error"`
}

// parseUpstreamError 只按 HTTP status 映射错误码；tool 参数留给调用方做 hint 分流。
func parseUpstreamError(status int, raw []byte, tool string) *domain.UpstreamError { //nolint:unparam // tool 参数保留，错误提示在 domain.ToPayload 分流
	var env upstreamError
	if err := json.Unmarshal(raw, &env); err != nil {
		env = upstreamError{}
	}

	if status == http.StatusOK && env.Error.Message == "" {
		return &domain.UpstreamError{
			Code:        domain.CodeUpParse,
			HttpStatus:  status,
			UpstreamMsg: fmt.Sprintf("响应体 %d 字节缺少 error 信封", len(raw)),
		}
	}
	message := clipRunes(domain.RedactSecrets(env.Error.Message), 512)
	return &domain.UpstreamError{
		Code:         codeForStatus(status),
		HttpStatus:   status,
		RequestId:    extractRequestID(message),
		UpstreamMsg:  message,
		UpstreamType: env.Error.Type,
	}
}

// codeForStatus 的四条规则复用 domain 的同一实现，避免两处映射漂移。
func codeForStatus(status int) int {
	return domain.NewUpstreamError(status, "", "", nil).Code
}

func extractRequestID(message string) string {
	if match := requestIDRe.FindStringSubmatch(message); match != nil {
		return match[1]
	}
	return ""
}

func clipRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

func newResponseTooLargeError(status int, raw []byte, maxBytes int64) *domain.UpstreamError {
	if status != http.StatusOK {
		up := parseUpstreamError(status, raw, "")
		if up.UpstreamMsg == "" {
			up.UpstreamMsg = fmt.Sprintf("响应体超过 %d 字节上限", maxBytes)
		}
		return up
	}
	return &domain.UpstreamError{
		Code:        domain.CodeUpParse,
		HttpStatus:  status,
		UpstreamMsg: fmt.Sprintf("响应体超过 %d 字节上限", maxBytes),
	}
}

// classifyNetErr 把拿不到响应的失败归到五个 cause 之一。
func classifyNetErr(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "attempt_timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "tls_handshake"
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return "tls_handshake"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "attempt_timeout"
		}
		return "tcp_connect"
	}
	return "tcp_connect"
}
