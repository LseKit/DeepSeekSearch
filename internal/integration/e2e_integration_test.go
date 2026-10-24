// 文件名: e2e_integration_test.go
// 用途: L3b 真实上游端到端（计费）。build tag 在文件头之后、package 之前，默认 go test 不会编译到这里。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxUpstreamCalls = 10

var upstreamCalls atomic.Int64

var (
	binaryPath string
	apiKey     string
)

// TestMain 构建带版本注入的二进制，并从部署根 Config/.env 读取测试 Key。
// Key 只在此处读取、只经环境变量传给子进程，MUST NOT 打进日志。
func TestMain(m *testing.M) {
	apiKey = readAPIKey("../../../Config/.env")
	dir, err := os.MkdirTemp("", "dss-l3b-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建临时目录失败: %v\n", err)
		os.Exit(1)
	}
	binaryPath = filepath.Join(dir, "DeepSeekSearch")
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析仓库根失败: %v\n", err)
		os.Exit(1)
	}
	version := readMakefileVersion(filepath.Join(repoRoot, "Makefile"))
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 120*time.Second)
	build := exec.CommandContext(buildCtx, "go", "build", "-trimpath", //nolint:gosec // 参数全部来自测试内的固定字符串
		"-ldflags", "-X main.version="+version, "-o", binaryPath, "./cmd/deepseeksearch")
	build.Dir = repoRoot
	out, buildErr := build.CombinedOutput()
	buildCancel()
	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "构建二进制失败: %v\n%s\n", buildErr, out)
		os.Exit(1)
	}
	code := m.Run()
	if cleanErr := os.RemoveAll(dir); cleanErr != nil {
		fmt.Fprintf(os.Stderr, "清理临时目录失败: %v\n", cleanErr)
	}
	os.Exit(code)
}

func readAPIKey(path string) string {
	values, err := godotenv.Read(path)
	if err != nil {
		return ""
	}
	return values["DEEPSEEK_API_KEY"]
}

func readMakefileVersion(path string) string {
	raw, err := os.ReadFile(path) //nolint:gosec // path 是测试内的固定相对路径
	if err != nil {
		return "v0.1.0"
	}
	re := regexp.MustCompile(`(?m)^VERSION\s*:=\s*(\S+)`)
	if match := re.FindSubmatch(raw); len(match) == 2 {
		return string(match[1])
	}
	return "v0.1.0"
}

func skipWithoutKey(t *testing.T) {
	t.Helper()
	if apiKey == "" {
		t.Skip("未在 Config/.env 找到 DEEPSEEK_API_KEY，跳过真实调用测试")
	}
	if _, err := os.Stat(binaryPath); err != nil {
		t.Fatalf("测试二进制不存在: %v", err)
	}
}

func billable(t *testing.T) {
	t.Helper()
	if n := upstreamCalls.Add(1); n > maxUpstreamCalls {
		t.Fatalf("上游调用次数 %d 超过上限 %d", n, maxUpstreamCalls)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口失败: %v", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("监听地址不是 TCPAddr: %v", ln.Addr())
	}
	if closeErr := ln.Close(); closeErr != nil {
		t.Fatalf("关闭临时监听失败: %v", closeErr)
	}
	return addr.Port
}

func writeConfig(t *testing.T, dir, transport string, port int, jsonResponse bool) {
	t.Helper()
	content := fmt.Sprintf(`[server]
transport = %q

[server.http]
host = "127.0.0.1"
port = %d
json_response = %t
auth_enabled = false
allow_non_local_host = false

[cache]
enabled = true
db_path = "Data/Cache.db"

[log]
dir = "Logs"
also_stderr = false
`, transport, port, jsonResponse)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("写 config.toml 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), nil, 0o600); err != nil {
		t.Fatalf("写 .env 失败: %v", err)
	}
}

type httpServer struct {
	endpoint string
}

func startHTTPServer(t *testing.T, jsonResponse bool) *httpServer {
	t.Helper()
	skipWithoutKey(t)

	dir := t.TempDir()
	port := freePort(t)
	writeConfig(t, dir, "streamable-http", port, jsonResponse)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binaryPath, "-config", "config.toml", "-env", ".env")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DEEPSEEK_API_KEY="+apiKey)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("启动服务器失败: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if err := cmd.Wait(); err != nil {
			t.Logf("服务器退出: %v", err)
		}
	})

	srv := &httpServer{endpoint: fmt.Sprintf("http://127.0.0.1:%d", port)}
	waitHealthy(t, srv.endpoint, &stderr)
	return srv
}

func waitHealthy(t *testing.T, endpoint string, stderr *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if healthzOK(endpoint) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务器 15s 内未就绪，stderr=%s", stderr.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func healthzOK(endpoint string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/healthz", http.NoBody)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			return
		}
	}()
	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		return false
	}
	return resp.StatusCode == http.StatusOK
}

func connectHTTP(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "l3b-http", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("HTTP 连接失败: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Logf("关闭 HTTP 会话: %v", closeErr)
		}
	})
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s 调用失败: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s 返回错误: %s", name, resultText(t, res))
	}
	return resultText(t, res)
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("工具结果没有 content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("工具结果不是 TextContent: %T", res.Content[0])
	}
	return text.Text
}

type searchOutput struct {
	Results []struct {
		RerankScore float64 `json:"rerank_score"`
	} `json:"results"`
	FromCache bool `json:"from_cache"`
}

type fetchOutput struct {
	CacheID   string `json:"cache_id"`
	Content   string `json:"content"`
	Mode      string `json:"mode"`
	FromCache bool   `json:"from_cache"`
	Sections  []struct {
		ID string `json:"id"`
	} `json:"sections"`
}

// L3b-1：真实 ds_search，count=3，断言 ≥1 条且 rerank_score 降序。
func TestSearch_Real(t *testing.T) {
	srv := startHTTPServer(t, true)
	session := connectHTTP(t, srv.endpoint)
	billable(t)

	text := callTool(t, session, "ds_search", map[string]any{
		"query": "golang context 用法",
		"count": 3,
	})
	var out searchOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("ds_search 结果不是 JSON: %v, text=%.300s", err, text)
	}
	if len(out.Results) == 0 {
		t.Fatalf("ds_search 返回 0 条结果: %.300s", text)
	}
	for i := 1; i < len(out.Results); i++ {
		if out.Results[i].RerankScore > out.Results[i-1].RerankScore {
			t.Fatalf("rerank_score 未降序: [%d]=%v > [%d]=%v", i, out.Results[i].RerankScore, i-1, out.Results[i-1].RerankScore)
		}
	}
}

// L3b-2：真实 ds_fetch example.com，正文与 cache_id 非空。
func TestFetch_Real(t *testing.T) {
	srv := startHTTPServer(t, true)
	session := connectHTTP(t, srv.endpoint)
	billable(t)

	text := callTool(t, session, "ds_fetch", map[string]any{"url": "https://example.com/"})
	var out fetchOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("ds_fetch 结果不是 JSON: %v, text=%.300s", err, text)
	}
	if out.CacheID == "" {
		t.Fatal("cache_id 为空")
	}
	if strings.TrimSpace(out.Content) == "" {
		t.Fatalf("正文为空: %.300s", text)
	}
}

// L3b-3：快照复用。第一次 fetch 拿 cache_id 与节 ID，第二次用 cache_id+section，
// 断言 cache_id 不变、from_cache=true（重新抓取会生成新 cache_id）。
func TestFetch_SnapshotReuse(t *testing.T) {
	srv := startHTTPServer(t, true)
	session := connectHTTP(t, srv.endpoint)
	billable(t)

	text := callTool(t, session, "ds_fetch", map[string]any{"url": "https://go.dev/blog/context"})
	var first fetchOutput
	if err := json.Unmarshal([]byte(text), &first); err != nil {
		t.Fatalf("首次 ds_fetch 不是 JSON: %v", err)
	}
	if first.CacheID == "" || len(first.Sections) == 0 {
		t.Fatalf("首次抓取缺少 cache_id 或节: cache_id=%q sections=%d", first.CacheID, len(first.Sections))
	}

	sectionID := first.Sections[0].ID
	text = callTool(t, session, "ds_fetch", map[string]any{"cache_id": first.CacheID, "section": sectionID})
	var second fetchOutput
	if err := json.Unmarshal([]byte(text), &second); err != nil {
		t.Fatalf("快照 ds_fetch 不是 JSON: %v", err)
	}
	if second.CacheID != first.CacheID {
		t.Fatalf("cache_id 变化：%q → %q（说明重新抓取了）", first.CacheID, second.CacheID)
	}
	if !second.FromCache {
		t.Fatal("from_cache=false，说明没有复用快照")
	}
	if strings.TrimSpace(second.Content) == "" {
		t.Fatal("指定节返回空正文")
	}
}

// L3b-6：/healthz、serverInfo、--version 三处版本一致。
func TestVersionConsistency_P26(t *testing.T) {
	srv := startHTTPServer(t, true)
	session := connectHTTP(t, srv.endpoint)

	init := session.InitializeResult()
	if init == nil || init.ServerInfo == nil {
		t.Fatal("session 没有 serverInfo")
	}
	serverVersion := init.ServerInfo.Version

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.endpoint+"/healthz", http.NoBody)
	if err != nil {
		t.Fatalf("构造 healthz 请求失败: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 healthz 失败: %v", err)
	}
	raw, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("关闭 healthz 响应失败: %v", closeErr)
	}
	if readErr != nil {
		t.Fatalf("读取 healthz 失败: %v", readErr)
	}
	var health struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &health); err != nil {
		t.Fatalf("healthz 不是 JSON: %v", err)
	}

	versionCtx, versionCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer versionCancel()
	versionOut, err := exec.CommandContext(versionCtx, binaryPath, "--version").Output() //nolint:gosec // binaryPath 由 TestMain 构建
	if err != nil {
		t.Fatalf("运行 --version 失败: %v", err)
	}
	fields := strings.Fields(string(versionOut))
	if len(fields) < 2 {
		t.Fatalf("--version 输出格式异常: %q", versionOut)
	}
	cliVersion := fields[1]

	if serverVersion != health.Version || serverVersion != cliVersion {
		t.Fatalf("版本不一致: serverInfo=%q healthz=%q --version=%q",
			serverVersion, health.Version, cliVersion)
	}
}
