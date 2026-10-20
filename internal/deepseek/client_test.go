// 文件名: client_test.go
// 用途: 验证上游客户端的一次往返、请求序列化、响应上限、错误映射、Ping 与网络错误 2998。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func TestClient(t *testing.T) {
	t.Run("正常解析10条", func(t *testing.T) {
		item := `{"rerank_score":2.5,"title":"T","url":"https://example.com/a","snippet":"S","summary":"M","content":"","site_name":null,"site_icon":null,"date":null,"authority":3}`
		body := `{"results":[` + strings.Repeat(item+",", 9) + item + `]}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			writeBody(t, w, body)
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, nil)
		resp, err := client.Search(context.Background(), domain.SearchRequest{Query: "go"})
		if err != nil {
			t.Fatalf("Search() = %v", err)
		}
		if len(resp.Results) != 10 {
			t.Fatalf("results = %d, want 10", len(resp.Results))
		}
		if resp.Results[0].Title != "T" || resp.Results[0].Url != "https://example.com/a" {
			t.Fatalf("首条结果 = %+v", resp.Results[0])
		}
	})

	t.Run("ori_question空串不发", func(t *testing.T) {
		got := captureSearchBody(t, domain.SearchRequest{Query: "q", OriQuestion: strPtr("")})
		assertNoKey(t, got, "ori_question")
	})

	t.Run("count为零不发", func(t *testing.T) {
		got := captureSearchBody(t, domain.SearchRequest{Query: "q", Count: intPtr(0)})
		assertNoKey(t, got, "count")
	})

	t.Run("localization空串不发", func(t *testing.T) {
		got := captureSearchBody(t, domain.SearchRequest{Query: "q", Localization: ""})
		assertNoKey(t, got, "localization")
	})

	t.Run("500映射为2500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			writeBody(t, w, `{"error":{"message":"boom (request_id: 123e4567-e89b-12d3-a456-426614174000)","type":"server_error"}}`)
		}))
		defer srv.Close()

		_, err := newTestClient(srv.URL, nil).Search(context.Background(), domain.SearchRequest{Query: "q"})
		up := asUpstreamError(t, err)
		if up.Code != domain.CodeUpServer {
			t.Fatalf("Code = %d, want %d", up.Code, domain.CodeUpServer)
		}
		if up.HttpStatus != http.StatusInternalServerError {
			t.Fatalf("HttpStatus = %d", up.HttpStatus)
		}
		if !domain.IsRetryable(err) {
			t.Fatal("500 应可重试")
		}
		if up.RequestId != "123e4567-e89b-12d3-a456-426614174000" {
			t.Fatalf("RequestId = %q", up.RequestId)
		}
	})

	t.Run("非JSON返回2999", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeBody(t, w, "not json")
		}))
		defer srv.Close()

		_, err := newTestClient(srv.URL, nil).Search(context.Background(), domain.SearchRequest{Query: "q"})
		if got := domain.CodeOf(err); got != domain.CodeUpParse {
			t.Fatalf("Code = %d, want %d（err=%v）", got, domain.CodeUpParse, err)
		}
	})

	t.Run("恰好上限解析成功", func(t *testing.T) {
		payload := exactJSON(t, 64)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeBody(t, w, payload)
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, func(c *Config) { c.MaxResponseBytes = int64(len(payload)) })
		if _, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"}); err != nil {
			t.Fatalf("恰好 max 字节应解析成功，实际 %v", err)
		}
	})

	t.Run("超上限返回2999", func(t *testing.T) {
		payload := exactJSON(t, 64)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeBody(t, w, payload)
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, func(c *Config) { c.MaxResponseBytes = int64(len(payload) - 1) })
		_, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"})
		if got := domain.CodeOf(err); got != domain.CodeUpParse {
			t.Fatalf("Code = %d, want %d（err=%v）", got, domain.CodeUpParse, err)
		}
		payloadErr := domain.ToPayload(err, "ds_search")
		if !strings.Contains(payloadErr.UpstreamMsg, "已截断") {
			t.Fatalf("UpstreamMsg = %q, 应含「已截断」", payloadErr.UpstreamMsg)
		}
	})

	t.Run("请求头正确", func(t *testing.T) {
		var auth, contentType, ua string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			contentType = r.Header.Get("Content-Type")
			ua = r.Header.Get("User-Agent")
			writeBody(t, w, `{"results":[]}`)
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, func(c *Config) { c.UserAgent = "custom-agent/1.0" })
		if _, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"}); err != nil {
			t.Fatalf("Search() = %v", err)
		}
		if auth != "Bearer sk-test-key" {
			t.Fatalf("Authorization = %q", auth)
		}
		if contentType != "application/json" {
			t.Fatalf("Content-Type = %q", contentType)
		}
		if ua != "custom-agent/1.0" {
			t.Fatalf("User-Agent = %q", ua)
		}
	})

	t.Run("空UserAgent", func(t *testing.T) {
		var ua string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ua = r.Header.Get("User-Agent")
			writeBody(t, w, `{"results":[]}`)
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, func(c *Config) { c.UserAgent = "" })
		if _, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"}); err != nil {
			t.Fatalf("Search() = %v", err)
		}
		if ua != "DeepSeekSearch/v0.1.0-test" {
			t.Fatalf("User-Agent = %q", ua)
		}
	})

	t.Run("DNS失败2998", func(t *testing.T) {
		client := newTestClient("http://deepseeksearch-no-such-host.invalid", func(c *Config) {
			c.PerAttempt = 500 * time.Millisecond
			c.Dial = 200 * time.Millisecond
		})
		_, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"})
		assertNetworkCause(t, err, "dns")
	})

	t.Run("连接被拒2998", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		base := srv.URL
		srv.Close()

		client := newTestClient(base, func(c *Config) {
			c.PerAttempt = 500 * time.Millisecond
			c.Dial = 200 * time.Millisecond
		})
		_, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"})
		assertNetworkCause(t, err, "tcp_connect")
	})

	t.Run("单次尝试超时2998", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			writeBody(t, w, `{"results":[]}`)
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, func(c *Config) {
			c.PerAttempt = 50 * time.Millisecond
			c.ResponseHeader = time.Second
			c.Dial = time.Second
		})
		_, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"})
		assertNetworkCause(t, err, "attempt_timeout")
		if got := domain.CodeOf(err); got == 3020 {
			t.Fatal("单次尝试超时 MUST NOT 是 3020（整链预算）")
		}
	})

	t.Run("读体中断2998", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("ResponseWriter 不支持 Hijack")
				return
			}
			conn, buf, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("Hijack() = %v", err)
				return
			}
			defer func() {
				if closeErr := conn.Close(); closeErr != nil {
					t.Errorf("conn.Close() = %v", closeErr)
				}
			}()
			if _, writeErr := buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n0123456789"); writeErr != nil {
				t.Errorf("buf.WriteString() = %v", writeErr)
			}
			if flushErr := buf.Flush(); flushErr != nil {
				t.Errorf("buf.Flush() = %v", flushErr)
			}
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, func(c *Config) {
			c.PerAttempt = 2 * time.Second
			c.ResponseHeader = time.Second
		})
		_, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"})
		assertNetworkCause(t, err, "response_reset")
	})

	t.Run("Ping405健康", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/v1/tools/search" {
				t.Errorf("Ping 请求 = %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		}))
		defer srv.Close()

		if err := newTestClient(srv.URL, nil).Ping(context.Background()); err != nil {
			t.Fatalf("Ping() = %v, want nil", err)
		}
	})

	t.Run("Ping404提示base_url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			writeBody(t, w, `{"error":{"message":"not found (request_id: 123e4567-e89b-12d3-a456-426614174000)","type":"not_found_error"}}`)
		}))
		defer srv.Close()

		err := newTestClient(srv.URL, nil).Ping(context.Background())
		if err == nil {
			t.Fatal("Ping 404 应返回错误")
		}
		if !strings.Contains(err.Error(), "base_url") {
			t.Fatalf("error = %q, 应含 base_url", err)
		}
		if got := domain.CodeOf(err); got != domain.CodeUpNotFound {
			t.Fatalf("Code = %d, want %d", got, domain.CodeUpNotFound)
		}
	})

	t.Run("Ping401不含Key", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			writeBody(t, w, `{"error":{"message":"Authentication Fails, Your api key: ****0000 is invalid (request_id: 123e4567-e89b-12d3-a456-426614174000)","type":"authentication_error"}}`)
		}))
		defer srv.Close()

		err := newTestClient(srv.URL, nil).Ping(context.Background())
		if err == nil {
			t.Fatal("Ping 401 应返回错误")
		}
		if strings.Contains(err.Error(), "0000") {
			t.Fatalf("error 泄露 Key 片段: %q", err)
		}
	})

	t.Run("401消息脱敏", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			writeBody(t, w, `{"error":{"message":"Authentication Fails, Your api key: ****0000 is invalid (request_id: 123e4567-e89b-12d3-a456-426614174000)","type":"authentication_error"}}`)
		}))
		defer srv.Close()

		_, err := newTestClient(srv.URL, nil).Search(context.Background(), domain.SearchRequest{Query: "q"})
		payload := domain.ToPayload(err, "ds_search")
		if strings.Contains(payload.UpstreamMsg, "0000") {
			t.Fatalf("UpstreamMsg 泄露 Key 片段: %q", payload.UpstreamMsg)
		}
		if !strings.Contains(payload.UpstreamMsg, "[REDACTED]") {
			t.Fatalf("UpstreamMsg = %q, 应含 [REDACTED]", payload.UpstreamMsg)
		}
		if payload.RequestId == "" {
			t.Fatal("request_id 应被提取")
		}
	})

	t.Run("attempt从ctx传入", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
		}))
		defer srv.Close()

		client := newTestClient(srv.URL, func(c *Config) {
			c.PerAttempt = 50 * time.Millisecond
			c.ResponseHeader = time.Second
			c.Dial = time.Second
		})
		ctx := domain.ContextWithAttempt(context.Background(), 3)
		_, err := client.Search(ctx, domain.SearchRequest{Query: "q"})
		details := domain.ToPayload(err, "ds_search").Details
		if details["attempt"] != 3 {
			t.Fatalf("details.attempt = %v, want 3", details["attempt"])
		}

		_, err = client.Search(context.Background(), domain.SearchRequest{Query: "q"})
		details = domain.ToPayload(err, "ds_search").Details
		if details["attempt"] != 1 {
			t.Fatalf("无 ctx 值时 details.attempt = %v, want 1", details["attempt"])
		}
	})
}

func newTestClient(baseURL string, mutate func(*Config)) Client {
	cfg := Config{
		BaseUrl:          baseURL,
		ApiKey:           "sk-test-key",
		PerAttempt:       2 * time.Second,
		Dial:             time.Second,
		TLSHandshake:     time.Second,
		ResponseHeader:   time.Second,
		MaxResponseBytes: 1 << 20,
		BuildInfo:        domain.BuildInfo{Version: "v0.1.0-test"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg)
}

func captureSearchBody(t *testing.T, req domain.SearchRequest) map[string]json.RawMessage { //nolint:gocritic // 测试 helper
	t.Helper()
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() = %v", err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("请求体不是合法 JSON: %v", err)
		}
		writeBody(t, w, `{"results":[]}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(srv.URL, nil).Search(context.Background(), req); err != nil {
		t.Fatalf("Search() = %v", err)
	}
	return body
}

func assertNoKey(t *testing.T, body map[string]json.RawMessage, key string) {
	t.Helper()
	if _, ok := body[key]; ok {
		t.Fatalf("请求体不应包含 %q: %v", key, body)
	}
}

func assertNetworkCause(t *testing.T, err error, cause string) {
	t.Helper()
	if got := domain.CodeOf(err); got != domain.CodeUpNetwork {
		t.Fatalf("Code = %d, want %d（err=%v）", got, domain.CodeUpNetwork, err)
	}
	if !domain.IsRetryable(err) {
		t.Fatal("2998 必须可重试（ADR-016）")
	}
	details := domain.ToPayload(err, "ds_search").Details
	if details["cause"] != cause {
		t.Fatalf("details.cause = %v, want %s", details["cause"], cause)
	}
}

func asUpstreamError(t *testing.T, err error) *domain.UpstreamError {
	t.Helper()
	var up *domain.UpstreamError
	if !errors.As(err, &up) {
		t.Fatalf("err = %v, want *domain.UpstreamError", err)
	}
	return up
}

func exactJSON(t *testing.T, n int) string {
	t.Helper()
	prefix := `{"results":[],"pad":"`
	suffix := `"}`
	fill := n - len(prefix) - len(suffix)
	if fill < 0 {
		t.Fatalf("目标长度 %d 太小", n)
	}
	return prefix + strings.Repeat("x", fill) + suffix
}

func writeBody(t *testing.T, w io.Writer, s string) {
	t.Helper()
	if _, err := io.WriteString(w, s); err != nil {
		t.Errorf("WriteString() = %v", err)
	}
}

func strPtr(s string) *string { return &s }

func intPtr(v int) *int { return &v }
