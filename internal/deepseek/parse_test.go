// 文件名: parse_test.go
// 用途: 验证错误信封解析、状态码映射、2404 hint 分流、脱敏与真实黄金文件。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func TestParseUpstreamError(t *testing.T) {
	t.Parallel()

	requestID := "123e4567-e89b-12d3-a456-426614174000"
	body := errorEnvelope(t, "upstream failed (request_id: "+requestID+")")

	tests := []struct {
		name      string
		status    int
		wantCode  int
		wantHTTP  int
		wantRetry bool
	}{
		{name: "400", status: 400, wantCode: 2400, wantHTTP: 400, wantRetry: false},
		{name: "401", status: 401, wantCode: 2401, wantHTTP: 401, wantRetry: false},
		{name: "402", status: 402, wantCode: 2402, wantHTTP: 402, wantRetry: false},
		{name: "403", status: 403, wantCode: 2403, wantHTTP: 403, wantRetry: false},
		{name: "404", status: 404, wantCode: 2404, wantHTTP: 404, wantRetry: false},
		{name: "408", status: 408, wantCode: 2408, wantHTTP: 408, wantRetry: true},
		{name: "413", status: 413, wantCode: 2413, wantHTTP: 413, wantRetry: false},
		{name: "429", status: 429, wantCode: 2429, wantHTTP: 429, wantRetry: true},
		{name: "500", status: 500, wantCode: 2500, wantHTTP: 500, wantRetry: true},
		{name: "503", status: 503, wantCode: 2503, wantHTTP: 503, wantRetry: true},
		{name: "504", status: 504, wantCode: 2504, wantHTTP: 504, wantRetry: true},
		{name: "418未枚举4xx", status: 418, wantCode: 2000, wantHTTP: 418, wantRetry: false},
		{name: "502未枚举5xx", status: 502, wantCode: 2500, wantHTTP: 502, wantRetry: true},
		{name: "501未枚举5xx", status: 501, wantCode: 2500, wantHTTP: 501, wantRetry: true},
		{name: "507未枚举5xx", status: 507, wantCode: 2500, wantHTTP: 507, wantRetry: true},
		{name: "302重定向耗尽", status: 302, wantCode: 2000, wantHTTP: 302, wantRetry: false},
		{name: "100异常1xx", status: 100, wantCode: 2000, wantHTTP: 100, wantRetry: false},
		{name: "status0", status: 0, wantCode: 2998, wantHTTP: 0, wantRetry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			up := parseUpstreamError(tt.status, body, "ds_search")
			if up.Code != tt.wantCode {
				t.Fatalf("Code = %d, want %d", up.Code, tt.wantCode)
			}
			if up.HttpStatus != tt.wantHTTP {
				t.Fatalf("HttpStatus = %d, want %d", up.HttpStatus, tt.wantHTTP)
			}
			if got := domain.IsRetryable(up); got != tt.wantRetry {
				t.Fatalf("IsRetryable = %v, want %v", got, tt.wantRetry)
			}
		})
	}

	t.Run("request_id提取", func(t *testing.T) {
		t.Parallel()
		up := parseUpstreamError(400, body, "ds_search")
		if up.RequestId != requestID {
			t.Fatalf("RequestId = %q, want %q", up.RequestId, requestID)
		}
	})

	t.Run("200无error信封为2999", func(t *testing.T) {
		t.Parallel()
		up := parseUpstreamError(200, []byte(`{}`), "ds_search")
		if up.Code != domain.CodeUpParse {
			t.Fatalf("Code = %d, want %d", up.Code, domain.CodeUpParse)
		}
		if domain.IsRetryable(up) {
			t.Fatal("2999 不可重试")
		}
	})

	t.Run("2404_fetch_hint", func(t *testing.T) {
		t.Parallel()
		up := parseUpstreamError(404, body, "ds_fetch")
		payload := domain.ToPayload(up, "ds_fetch")
		if !strings.Contains(payload.Hint, "维基百科") || !strings.Contains(payload.Hint, "上游不区分") {
			t.Fatalf("fetch 2404 hint 不完整: %q", payload.Hint)
		}
	})

	t.Run("2404_search_hint分流", func(t *testing.T) {
		t.Parallel()
		up := parseUpstreamError(404, body, "ds_search")
		payload := domain.ToPayload(up, "ds_search")
		if !strings.Contains(payload.Hint, "base_url") {
			t.Fatalf("search 2404 hint 应指向 base_url: %q", payload.Hint)
		}
		if strings.Contains(payload.Hint, "维基百科") {
			t.Fatalf("search hint 不应含 fetch 站点清单: %q", payload.Hint)
		}
	})

	t.Run("2402_hint", func(t *testing.T) {
		t.Parallel()
		up := parseUpstreamError(402, body, "ds_search")
		payload := domain.ToPayload(up, "ds_search")
		if !strings.Contains(payload.Hint, "重试不会成功") {
			t.Fatalf("2402 hint = %q", payload.Hint)
		}
	})

	t.Run("429按status不按code", func(t *testing.T) {
		t.Parallel()
		raw := errorEnvelopeWithTypeAndCode(t, "rate limited", "rate_limit_error", "invalid_request_error")
		up := parseUpstreamError(429, raw, "ds_search")
		if up.Code != domain.CodeUpRateLimited {
			t.Fatalf("Code = %d, want %d", up.Code, domain.CodeUpRateLimited)
		}
		if up.UpstreamType != "rate_limit_error" {
			t.Fatalf("UpstreamType = %q, want rate_limit_error", up.UpstreamType)
		}
	})

	t.Run("密钥二次脱敏", func(t *testing.T) {
		t.Parallel()
		raw := errorEnvelope(t, "Authentication Fails, Your api key: ****0000 is invalid and sk-abcdefghijkl")
		up := parseUpstreamError(401, raw, "ds_search")
		if strings.Contains(up.UpstreamMsg, "0000") || strings.Contains(up.UpstreamMsg, "sk-abcdefghijkl") {
			t.Fatalf("UpstreamMsg 泄露密钥片段: %q", up.UpstreamMsg)
		}
		if !strings.Contains(up.UpstreamMsg, "[REDACTED]") || !strings.Contains(up.UpstreamMsg, "sk-***") {
			t.Fatalf("UpstreamMsg = %q, 应含脱敏标记", up.UpstreamMsg)
		}
	})

	t.Run("消息截断到512rune", func(t *testing.T) {
		t.Parallel()
		raw := errorEnvelope(t, strings.Repeat("中", 800))
		up := parseUpstreamError(400, raw, "ds_search")
		if got := utf8.RuneCountInString(up.UpstreamMsg); got != 512 {
			t.Fatalf("UpstreamMsg rune 数 = %d, want 512", got)
		}
		if !utf8.ValidString(up.UpstreamMsg) {
			t.Fatal("UpstreamMsg 不是合法 UTF-8")
		}
	})

	t.Run("codeForStatus_0", func(t *testing.T) {
		t.Parallel()
		code := codeForStatus(0)
		if code != domain.CodeUpNetwork {
			t.Fatalf("codeForStatus(0) = %d, want %d", code, domain.CodeUpNetwork)
		}
		if !domain.CodeRegistry[code].Retryable {
			t.Fatal("2998 必须可重试")
		}
	})
}

func TestParseSearchResponse(t *testing.T) {
	t.Parallel()

	t.Run("真实黄金文件", func(t *testing.T) {
		t.Parallel()
		raw, err := os.ReadFile("testdata/search_ok_20261021.json")
		if err != nil {
			t.Fatalf("ReadFile() = %v", err)
		}
		resp, err := parseSearchResponse(raw)
		if err != nil {
			t.Fatalf("parseSearchResponse() = %v", err)
		}
		if len(resp.Results) != 3 {
			t.Fatalf("results = %d, want 3", len(resp.Results))
		}
		if resp.Results[0].Url == "" || resp.Results[0].Title == "" {
			t.Fatalf("首条结果字段为空: %+v", resp.Results[0])
		}
	})

	t.Run("空results返回空切片", func(t *testing.T) {
		t.Parallel()
		resp, err := parseSearchResponse([]byte(`{"results":[]}`))
		if err != nil {
			t.Fatalf("parseSearchResponse() = %v", err)
		}
		if resp.Results == nil || len(resp.Results) != 0 {
			t.Fatalf("results = %#v, want 非 nil 空切片", resp.Results)
		}
	})

	t.Run("null字段可解析", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"results":[{"rerank_score":0,"title":"","url":"https://x","snippet":"","summary":"","content":"","site_name":null,"site_icon":null,"date":null,"authority":-1}]}`)
		resp, err := parseSearchResponse(raw)
		if err != nil {
			t.Fatalf("parseSearchResponse() = %v", err)
		}
		item := resp.Results[0]
		if item.Date != nil || item.SiteName != nil || item.Authority != -1 || item.Title != "" {
			t.Fatalf("null 字段解析不正确: %+v", item)
		}
	})

	t.Run("非法JSON为2999", func(t *testing.T) {
		t.Parallel()
		_, err := parseSearchResponse([]byte("not json"))
		if got := domain.CodeOf(err); got != domain.CodeUpParse {
			t.Fatalf("Code = %d, want %d", got, domain.CodeUpParse)
		}
	})

	t.Run("空响应为2999", func(t *testing.T) {
		t.Parallel()
		_, err := parseSearchResponse(nil)
		if got := domain.CodeOf(err); got != domain.CodeUpParse {
			t.Fatalf("Code = %d, want %d", got, domain.CodeUpParse)
		}
	})
}

func TestParseFetchResponse(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/fetch_ok_20261021.json")
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}
	resp, err := parseFetchResponse(raw)
	if err != nil {
		t.Fatalf("parseFetchResponse() = %v", err)
	}
	if resp.Title != "Example Domain" || resp.Content == "" {
		t.Fatalf("fetch 黄金文件解析不正确: title=%q content_len=%d", resp.Title, len(resp.Content))
	}

	if _, err := parseFetchResponse([]byte("not json")); domain.CodeOf(err) != domain.CodeUpParse {
		t.Fatalf("非法 JSON Code = %d, want %d", domain.CodeOf(err), domain.CodeUpParse)
	}
	if _, err := parseFetchResponse(nil); domain.CodeOf(err) != domain.CodeUpParse {
		t.Fatalf("空响应 Code = %d, want %d", domain.CodeOf(err), domain.CodeUpParse)
	}
}

func TestParseGoldenFiles(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatalf("Glob() = %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("testdata 下没有黄金文件")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(path) //nolint:gosec // 测试固定目录
			if err != nil {
				t.Fatalf("ReadFile() = %v", err)
			}
			var value any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatalf("黄金文件不是合法 JSON（采集信息必须放 .meta）: %v", err)
			}
			meta := strings.TrimSuffix(path, ".json") + ".meta"
			if _, err := os.Stat(meta); err != nil {
				t.Fatalf("缺少同名 .meta: %v", err)
			}
		})
	}
}

func errorEnvelope(t *testing.T, message string) []byte {
	t.Helper()
	type envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	var env envelope
	env.Error.Message = message
	env.Error.Type = "invalid_request_error"
	env.Error.Code = "invalid_request_error"
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	return data
}

func errorEnvelopeWithTypeAndCode(t *testing.T, message, typ, code string) []byte {
	t.Helper()
	type envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	var env envelope
	env.Error.Message = message
	env.Error.Type = typ
	env.Error.Code = code
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	return data
}
