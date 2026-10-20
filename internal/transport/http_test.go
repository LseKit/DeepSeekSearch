// 文件名: http_test.go
// 用途: 覆盖协议契约 P1~P26 中随 HTTP 传输与中间件链生效的断言（P17 在 stdio_test.go）。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/tools"
)

var testBuild = domain.BuildInfo{
	Version:   "v9.9.9-test",
	Commit:    "deadbee",
	BuildTime: "2026-10-21T00:00:00Z",
	GoVersion: "go1.27.2",
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Server.Http.Host = "127.0.0.1"
	cfg.Server.Http.Port = 0
	cfg.Server.Http.Path = "/mcp"
	cfg.Server.Http.JsonResponse = true
	cfg.Server.Http.MaxRequestBodyBytes = 1 << 20
	cfg.Server.Http.EnableCrossOriginProtection = true
	cfg.Server.Http.AllowedOrigins = nil
	cfg.Server.Http.AllowNonLocalHost = false
	cfg.Server.Http.HealthzPath = "/healthz"
	cfg.Server.Http.AuthEnabled = false
	cfg.HttpToken = ""
	return cfg
}

func stubToolDefinition(name string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: "stub for transport tests",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}
}

func boolPtr(b bool) *bool { return &b }

func newTestMCP(build domain.BuildInfo) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "DeepSeekSearch", Version: build.Version}, &mcp.ServerOptions{
		SetCacheable: func(_ context.Context, r mcp.Request, c *mcp.Cacheable) {
			ttl, scope := tools.CacheableForRequest(r)
			c.TTLMs, c.CacheScope = int(ttl), scope
		},
	})
	handler := func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "stub-ok"}}}, nil
	}
	for _, name := range []string{"ds_search", "ds_fetch", "ds_health"} {
		srv.AddTool(stubToolDefinition(name), handler)
	}
	return srv
}

func newTestHandler(t *testing.T, cfg *config.Config, srv *mcp.Server) http.Handler {
	t.Helper()
	h, err := newHTTPHandler(cfg, testBuild, discardLogger(), time.Now(), nil, srv)
	if err != nil {
		t.Fatalf("newHTTPHandler 返回错误: %v", err)
	}
	return h
}

func newTestServer(t *testing.T, cfg config.Config) *httptest.Server { //nolint:gocritic // 测试辅助按值传配置，与生产 Build 同口径
	t.Helper()
	ts := httptest.NewServer(newTestHandler(t, &cfg, newTestMCP(testBuild)))
	t.Cleanup(ts.Close)
	return ts
}

type httpResult struct {
	Status int
	Header http.Header
	Body   []byte
}

func doRequest(t *testing.T, ts *httptest.Server, method, path string, headers map[string]string, body string) httpResult {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	raw, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("关闭响应体失败: %v", closeErr)
	}
	if readErr != nil {
		t.Fatalf("读取响应体失败: %v", readErr)
	}
	return httpResult{Status: resp.StatusCode, Header: resp.Header, Body: raw}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func decodeRPC(t *testing.T, raw []byte) rpcResponse {
	t.Helper()
	var resp rpcResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("响应不是 JSON-RPC: %v, body=%q", err, raw)
	}
	return resp
}

func modernHeaders(method, name string) map[string]string {
	h := map[string]string{
		"MCP-Protocol-Version": "2026-07-28",
		"Mcp-Method":           method,
	}
	if name != "" {
		h["Mcp-Name"] = name
	}
	return h
}

func modernMeta() string {
	return `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"probe","version":"0.0.1"},"io.modelcontextprotocol/clientCapabilities":{}}`
}

func modernCallBody(id int, method, extraParams string) string {
	params := extraParams
	if params != "" {
		params += ","
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%s,"params":{%s%s}}`, id, quoteJSON(method), params, modernMeta())
}

func quoteJSON(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestProtocol(t *testing.T) {
	cfg := testConfig()
	ts := newTestServer(t, cfg)

	t.Run("P1_P2_discover版本与ttl", func(t *testing.T) {
		body := modernCallBody(1, "server/discover", "")
		res := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("server/discover", ""), body)
		if res.Status != http.StatusOK {
			t.Fatalf("server/discover = %d, body=%s", res.Status, res.Body)
		}
		var out struct {
			ResultType        string   `json:"resultType"`
			TTLMs             int64    `json:"ttlMs"`
			CacheScope        string   `json:"cacheScope"`
			SupportedVersions []string `json:"supportedVersions"`
			Meta              struct {
				ServerInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"io.modelcontextprotocol/serverInfo"`
			} `json:"_meta"`
		}
		rpc := decodeRPC(t, res.Body)
		if err := json.Unmarshal(rpc.Result, &out); err != nil {
			t.Fatalf("解析 discover result 失败: %v", err)
		}
		if len(out.SupportedVersions) == 0 || out.SupportedVersions[0] != "2026-07-28" {
			t.Fatalf("supportedVersions[0] = %v, want 2026-07-28", out.SupportedVersions)
		}
		if out.TTLMs <= 0 {
			t.Fatalf("ttlMs = %d, MUST > 0", out.TTLMs)
		}
		if out.ResultType != "complete" {
			t.Fatalf("resultType = %q, want complete", out.ResultType)
		}
		// P26：serverInfo.version 与构造 server 时的 BuildInfo 一致（--version 同源）。
		if out.Meta.ServerInfo.Version != testBuild.Version {
			t.Fatalf("serverInfo.version = %q, want %q", out.Meta.ServerInfo.Version, testBuild.Version)
		}
	})

	t.Run("P3_modern_tools_list", func(t *testing.T) {
		res := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("tools/list", ""),
			modernCallBody(3, "tools/list", ""))
		if res.Status != http.StatusOK {
			t.Fatalf("tools/list = %d, body=%s", res.Status, res.Body)
		}
		var out struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnlyHint bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
		}
		rpc := decodeRPC(t, res.Body)
		if err := json.Unmarshal(rpc.Result, &out); err != nil {
			t.Fatalf("解析 tools/list 失败: %v", err)
		}
		if len(out.Tools) != 3 {
			t.Fatalf("tools/list 返回 %d 个工具, want 3", len(out.Tools))
		}
		// P25：ds_search 必须带 readOnlyHint=true。
		found := false
		for _, tool := range out.Tools {
			if tool.Name == "ds_search" {
				found = true
				if !tool.Annotations.ReadOnlyHint {
					t.Fatalf("ds_search.annotations.readOnlyHint = false, want true")
				}
			}
		}
		if !found {
			t.Fatal("tools/list 里没有 ds_search")
		}
	})

	t.Run("P4_modern_tools_call", func(t *testing.T) {
		body := modernCallBody(4, "tools/call", `"name":"ds_search","arguments":{}`)
		res := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("tools/call", "ds_search"), body)
		if res.Status != http.StatusOK {
			t.Fatalf("tools/call = %d, body=%s", res.Status, res.Body)
		}
	})

	t.Run("P5_缺Mcp_Method", func(t *testing.T) {
		res := doRequest(t, ts, http.MethodPost, "/mcp", map[string]string{"MCP-Protocol-Version": "2026-07-28"},
			modernCallBody(5, "tools/list", ""))
		if res.Status != http.StatusBadRequest {
			t.Fatalf("缺 Mcp-Method 状态 = %d, want 400, body=%s", res.Status, res.Body)
		}
		if rpc := decodeRPC(t, res.Body); rpc.Error == nil || rpc.Error.Code != -32020 {
			t.Fatalf("错误码 = %+v, want -32020", rpc.Error)
		}
	})

	t.Run("P6_Mcp_Name不一致", func(t *testing.T) {
		body := modernCallBody(6, "tools/call", `"name":"ds_search","arguments":{}`)
		res := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("tools/call", "ds_fetch"), body)
		if res.Status != http.StatusBadRequest {
			t.Fatalf("Mcp-Name 不一致状态 = %d, want 400, body=%s", res.Status, res.Body)
		}
		if rpc := decodeRPC(t, res.Body); rpc.Error == nil || rpc.Error.Code != -32020 {
			t.Fatalf("错误码 = %+v, want -32020", rpc.Error)
		}
	})

	t.Run("P7_缺_meta_protocolVersion", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`
		res := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("tools/list", ""), body)
		if res.Status != http.StatusBadRequest {
			t.Fatalf("缺 _meta.protocolVersion 状态 = %d, want 400, body=%s", res.Status, res.Body)
		}
		if rpc := decodeRPC(t, res.Body); rpc.Error == nil || rpc.Error.Code != -32602 {
			t.Fatalf("错误码 = %+v, want -32602", rpc.Error)
		}
	})

	t.Run("P8_不支持的版本", func(t *testing.T) {
		headers := map[string]string{"MCP-Protocol-Version": "1999-01-01", "Mcp-Method": "tools/list"}
		res := doRequest(t, ts, http.MethodPost, "/mcp", headers, modernCallBody(8, "tools/list", ""))
		if res.Status != http.StatusBadRequest {
			t.Fatalf("不支持版本状态 = %d, want 400, body=%s", res.Status, res.Body)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("Content-Type = %q, want text/plain", ct)
		}
	})

	t.Run("P9_未知方法", func(t *testing.T) {
		headers := modernHeaders("tools/bogus", "")
		res := doRequest(t, ts, http.MethodPost, "/mcp", headers, modernCallBody(9, "tools/bogus", ""))
		if res.Status != http.StatusNotFound {
			t.Fatalf("未知方法状态 = %d, want 404, body=%s", res.Status, res.Body)
		}
		if rpc := decodeRPC(t, res.Body); rpc.Error == nil || rpc.Error.Code != -32601 {
			t.Fatalf("错误码 = %+v, want -32601", rpc.Error)
		}
	})

	t.Run("P10_GET_mcp", func(t *testing.T) {
		res := doRequest(t, ts, http.MethodGet, "/mcp", nil, "")
		if res.Status != http.StatusMethodNotAllowed {
			t.Fatalf("GET /mcp = %d, want 405", res.Status)
		}
	})

	t.Run("P11_DELETE_mcp", func(t *testing.T) {
		res := doRequest(t, ts, http.MethodDelete, "/mcp", nil, "")
		if res.Status != http.StatusMethodNotAllowed {
			t.Fatalf("DELETE /mcp = %d, want 405", res.Status)
		}
	})

	t.Run("P12_legacy_initialize", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":12,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"legacy","version":"0"}}}`
		res := doRequest(t, ts, http.MethodPost, "/mcp", nil, body)
		if res.Status != http.StatusOK {
			t.Fatalf("legacy initialize = %d, body=%s", res.Status, res.Body)
		}
		var out struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		rpc := decodeRPC(t, res.Body)
		if err := json.Unmarshal(rpc.Result, &out); err != nil {
			t.Fatalf("解析 initialize result 失败: %v", err)
		}
		if out.ProtocolVersion != "2024-11-05" {
			t.Fatalf("protocolVersion = %q, want 2024-11-05", out.ProtocolVersion)
		}
	})

	t.Run("P13_legacy_tools_call", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"ds_search","arguments":{}}}`
		headers := map[string]string{"MCP-Protocol-Version": "2025-11-25"}
		res := doRequest(t, ts, http.MethodPost, "/mcp", headers, body)
		if res.Status != http.StatusOK {
			t.Fatalf("legacy tools/call = %d, body=%s", res.Status, res.Body)
		}
	})

	t.Run("P14_notifications_initialized", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
		headers := map[string]string{"MCP-Protocol-Version": "2025-11-25"}
		res := doRequest(t, ts, http.MethodPost, "/mcp", headers, body)
		if res.Status != http.StatusAccepted {
			t.Fatalf("notifications/initialized = %d, want 202, body=%s", res.Status, res.Body)
		}
		if len(res.Body) != 0 {
			t.Fatalf("202 响应体应为空，实际 %q", res.Body)
		}
	})

	t.Run("P15_crossOrigin", func(t *testing.T) {
		// ① 恶意 Origin 的 POST 必须被拒（非 200）。
		res := doRequest(t, ts, http.MethodPost, "/mcp",
			map[string]string{
				"MCP-Protocol-Version": "2026-07-28",
				"Mcp-Method":           "tools/list",
				"Origin":               "https://evil.example.com",
			}, modernCallBody(15, "tools/list", ""))
		if res.Status == http.StatusOK {
			t.Fatalf("恶意 Origin 的 POST 返回 200，crossOrigin 未生效: %s", res.Body)
		}

		// ② preflight 不带 Authorization，auth 开启时 MUST NOT 401（crossOrigin 在 authGuard 之外）。
		authCfg := testConfig()
		authCfg.Server.Http.AuthEnabled = true
		authCfg.HttpToken = "test-token-0123456789"
		authTS := newTestServer(t, authCfg)
		res2 := doRequest(t, authTS, http.MethodOptions, "/mcp", map[string]string{
			"Origin":                        "https://evil.example.com",
			"Access-Control-Request-Method": "POST",
		}, "")
		if res2.Status == http.StatusUnauthorized {
			t.Fatalf("preflight 返回 401 —— crossOrigin 被装进了 authGuard 之内")
		}
	})

	t.Run("P16_tools_list两次字节一致", func(t *testing.T) {
		first := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("tools/list", ""),
			modernCallBody(161, "tools/list", ""))
		second := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("tools/list", ""),
			modernCallBody(162, "tools/list", ""))
		// id 不同会导致字节不同，所以比较 result 段。
		r1 := decodeRPC(t, first.Body)
		r2 := decodeRPC(t, second.Body)
		if string(r1.Result) != string(r2.Result) {
			t.Fatalf("两次 tools/list 的 result 不一致:\n%s\n%s", r1.Result, r2.Result)
		}
	})
}

func TestHealthz(t *testing.T) {
	ts := newTestServer(t, testConfig())

	t.Run("P19_GET_healthz", func(t *testing.T) {
		res := doRequest(t, ts, http.MethodGet, "/healthz", nil, "")
		if res.Status != http.StatusOK {
			t.Fatalf("GET /healthz = %d, body=%s", res.Status, res.Body)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		var out struct {
			Status   string `json:"status"`
			Version  string `json:"version"`
			Protocol string `json:"protocol"`
			Cache    struct {
				Entries int `json:"entries"`
			} `json:"cache"`
		}
		if err := json.Unmarshal(res.Body, &out); err != nil {
			t.Fatalf("healthz 不是 JSON: %v, body=%s", err, res.Body)
		}
		if out.Status != "ok" {
			t.Fatalf("status = %q, want ok", out.Status)
		}
		if out.Version != testBuild.Version {
			t.Fatalf("version = %q, want %q", out.Version, testBuild.Version)
		}
		if out.Protocol != "2026-07-28" {
			t.Fatalf("protocol = %q, want 2026-07-28", out.Protocol)
		}
	})

	t.Run("P20_POST_healthz", func(t *testing.T) {
		res := doRequest(t, ts, http.MethodPost, "/healthz", nil, "")
		if res.Status != http.StatusMethodNotAllowed {
			t.Fatalf("POST /healthz = %d, want 405", res.Status)
		}
	})
}

type fakeCacheStats struct {
	entries  int
	bytes    int64
	degraded bool
}

func (f fakeCacheStats) Stats() (entries int, bytes int64, degraded bool) {
	return f.entries, f.bytes, f.degraded
}

// TestHealthzCacheStats 锁住 Contract 15 §七之三 的 cache 三字段：
// 它们 MUST 来自注入的统计源，不能是恒零的占位值（degraded 恒 false 会掩盖缓存降级）。
func TestHealthzCacheStats(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	h, err := newHTTPHandler(&cfg, testBuild, discardLogger(), time.Now(),
		fakeCacheStats{entries: 42, bytes: 1048576, degraded: true}, newTestMCP(testBuild))
	if err != nil {
		t.Fatalf("newHTTPHandler() = %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	res := doRequest(t, ts, http.MethodGet, "/healthz", nil, "")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /healthz = %d, body=%s", res.Status, res.Body)
	}
	var out struct {
		Cache struct {
			Entries  int   `json:"entries"`
			Bytes    int64 `json:"bytes"`
			Degraded bool  `json:"degraded"`
		} `json:"cache"`
	}
	if unmarshalErr := json.Unmarshal(res.Body, &out); unmarshalErr != nil {
		t.Fatalf("healthz 不是 JSON: %v, body=%s", unmarshalErr, res.Body)
	}
	if out.Cache.Entries != 42 || out.Cache.Bytes != 1048576 || !out.Cache.Degraded {
		t.Fatalf("cache = %+v, want {42 1048576 true}", out.Cache)
	}
}

func TestBuildWithCache(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	h, err := BuildWithCache("streamable-http", cfg, testBuild, discardLogger(), fakeCacheStats{})
	if err != nil {
		t.Fatalf("BuildWithCache(streamable-http) = %v", err)
	}
	if h.Name() != "streamable-http" {
		t.Fatalf("Name() = %q, want streamable-http", h.Name())
	}
	if _, err := BuildWithCache("sse", cfg, testBuild, discardLogger(), nil); err == nil {
		t.Fatal("BuildWithCache(sse) 未返回错误")
	}
}

func TestAuth(t *testing.T) {
	cfg := testConfig()
	cfg.Server.Http.AuthEnabled = true
	cfg.HttpToken = "test-token-0123456789"
	ts := newTestServer(t, cfg)

	body := modernCallBody(21, "tools/list", "")

	t.Run("P21_无头401", func(t *testing.T) {
		res := doRequest(t, ts, http.MethodPost, "/mcp", modernHeaders("tools/list", ""), body)
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("无 Authorization = %d, want 401", res.Status)
		}
		if !strings.Contains(string(res.Body), "3091") {
			t.Fatalf("401 body 不含 3091: %s", res.Body)
		}
	})

	t.Run("P22_错token401", func(t *testing.T) {
		headers := modernHeaders("tools/list", "")
		headers["Authorization"] = "Bearer wrong-token-xxxxxxxx"
		res := doRequest(t, ts, http.MethodPost, "/mcp", headers, body)
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("错 token = %d, want 401", res.Status)
		}
	})

	t.Run("P23_对token200", func(t *testing.T) {
		headers := modernHeaders("tools/list", "")
		headers["Authorization"] = "Bearer " + cfg.HttpToken
		res := doRequest(t, ts, http.MethodPost, "/mcp", headers, body)
		if res.Status != http.StatusOK {
			t.Fatalf("对 token = %d, want 200, body=%s", res.Status, res.Body)
		}
	})
}

func TestHostGuard(t *testing.T) {
	ts := newTestServer(t, testConfig())

	t.Run("P24_非回环Host_403_3093", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			ts.URL+"/mcp", strings.NewReader(modernCallBody(24, "tools/list", "")))
		if err != nil {
			t.Fatalf("构造请求失败: %v", err)
		}
		req.Host = "evil.example.com"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "tools/list")

		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		raw, readErr := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatalf("关闭响应体失败: %v", closeErr)
		}
		if readErr != nil {
			t.Fatalf("读取响应体失败: %v", readErr)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("非回环 Host = %d, want 403, body=%s", resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), "3093") {
			t.Fatalf("403 body 不含 3093: %s", raw)
		}
	})
}

func TestMiddlewareOrder(t *testing.T) {
	cfg := testConfig()
	policy, err := newOriginPolicy(&cfg)
	if err != nil {
		t.Fatalf("newOriginPolicy 返回错误: %v", err)
	}
	h := chain(&cfg, testBuild, discardLogger(), time.Now(), nil, policy,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	got := make([]string, 0, len(middlewareNames))
	for {
		mw, ok := h.(*middleware)
		if !ok {
			break
		}
		got = append(got, mw.name)
		h = mw.next
	}
	got = append(got, "mcp")

	if strings.Join(got, ">") != strings.Join(middlewareNames, ">") {
		t.Fatalf("实际装配顺序 = %v\n期望 = %v", got, middlewareNames)
	}
}

// P2.1：SetCacheable 的取值必须按资源 URI 分派，三个值互不相同。
func TestCacheableForRequestDispatch(t *testing.T) {
	tests := []struct {
		uri   string
		ttl   int64
		scope string
	}{
		{uri: "ds://error-codes", ttl: 300000, scope: "public"},
		{uri: "ds://effective-config", ttl: 300000, scope: "private"},
		{uri: "ds://usage", ttl: 1000, scope: "private"},
	}

	seen := make(map[string]string, len(tests))
	for _, tt := range tests {
		req := &mcp.ServerRequest[*mcp.ReadResourceParams]{Params: &mcp.ReadResourceParams{URI: tt.uri}}
		ttl, scope := tools.CacheableForRequest(req)
		if ttl != tt.ttl || scope != tt.scope {
			t.Fatalf("%s 的 cacheable = (%d,%q), want (%d,%q)", tt.uri, ttl, scope, tt.ttl, tt.scope)
		}
		key := fmt.Sprintf("%d/%s", ttl, scope)
		if prev, ok := seen[key]; ok {
			t.Fatalf("%s 与 %s 的 cacheable 完全相同 (%s)，MUST 按 URI 分派", prev, tt.uri, key)
		}
		seen[key] = tt.uri
	}
}

// P18：HTTP 中间件链下的 SSE 流首包必须在 1 秒内到达。
// 用 legacy batch 让快请求的响应在慢请求尚未完成时先写出，从而观察到「中途 flush」；
// 若 statusWriter.Flush 缺失，首包会一直等到慢请求返回才被 net/http 一次性刷出。
func TestProtocol_P18_SSEFlush(t *testing.T) {
	cfg := testConfig()
	cfg.Server.Http.JsonResponse = false

	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)

	srv := newTestMCP(testBuild)
	srv.AddTool(stubToolDefinition("fast"), func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "fast"}}}, nil
	})
	srv.AddTool(stubToolDefinition("slow"), func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "slow"}}}, nil
	})

	ts := httptest.NewServer(newTestHandler(t, &cfg, srv))
	t.Cleanup(ts.Close)

	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fast","arguments":{}}},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slow","arguments":{}}}]`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	type probe struct {
		line        string
		contentType string
		elapsed     time.Duration
		err         error
	}
	probeCh := make(chan probe, 1)
	go func() {
		start := time.Now()
		resp, doErr := ts.Client().Do(req)
		if doErr != nil {
			probeCh <- probe{err: doErr}
			return
		}
		reader := bufio.NewReader(resp.Body)
		line, readErr := reader.ReadString('\n')
		probeCh <- probe{
			line:        line,
			contentType: resp.Header.Get("Content-Type"),
			elapsed:     time.Since(start),
			err:         readErr,
		}
		if _, drainErr := io.Copy(io.Discard, reader); drainErr != nil {
			return
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			return
		}
	}()

	select {
	case got := <-probeCh:
		if got.err != nil {
			t.Fatalf("读取 SSE 首包失败: %v", got.err)
		}
		if !strings.HasPrefix(got.contentType, "text/event-stream") {
			t.Fatalf("Content-Type = %q, want text/event-stream（该实例没走 SSE 路径）", got.contentType)
		}
		if got.elapsed >= time.Second {
			t.Fatalf("SSE 首包耗时 %v，超过 1s —— statusWriter.Flush 未生效", got.elapsed)
		}
		if !strings.Contains(got.line, "event:") && !strings.Contains(got.line, "data:") {
			t.Fatalf("首行不是 SSE 事件: %q", got.line)
		}
	case <-time.After(time.Second):
		unblock()
		t.Fatal("SSE 首包 1s 内未到达 —— statusWriter.Flush 未生效")
	}
	unblock()
}
