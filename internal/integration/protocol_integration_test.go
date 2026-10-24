// 文件名: protocol_integration_test.go
// 用途: L3b-4/L3b-5 —— stdio 真实端到端与 P17/P18 两条协议路径（各一次真实调用）。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// L3b-4：SDK client 连 `DeepSeekSearch -transport stdio`，tools/list + tools/call。
func TestStdioE2E(t *testing.T) {
	skipWithoutKey(t)
	dir := t.TempDir()
	writeConfig(t, dir, "stdio", freePort(t), true)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "-config", "config.toml", "-env", ".env") //nolint:gosec // binaryPath 由 TestMain 构建
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DEEPSEEK_API_KEY="+apiKey)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "l3b-stdio", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("stdio 连接失败: %v, stderr=%s", err, stderr.String())
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Logf("关闭 stdio 会话: %v", closeErr)
		}
	}()

	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list 失败: %v", err)
	}
	if len(toolsResult.Tools) != 5 {
		t.Fatalf("tools/list 返回 %d 个工具, want 5", len(toolsResult.Tools))
	}

	billable(t)
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "ds_search",
		Arguments: map[string]any{"query": "golang context", "count": 1},
	})
	if err != nil {
		t.Fatalf("stdio tools/call 失败: %v", err)
	}
	if res.IsError {
		t.Fatalf("stdio tools/call 返回错误: %s", resultText(t, res))
	}
}

// L3b-5：P17（stdio 原始管道）与 P18（HTTP + SSE 首包 <1s）各一次真实调用。
// P18 的负向验证（把 statusWriter.Flush 改成空实现）已有单元用例覆盖；
// 这里不重复，避免再产生一次计费调用。
func TestProtocol_P17_P18(t *testing.T) {
	skipWithoutKey(t)
	t.Run("P17_stdio_真实调用且stdout只有协议帧", func(t *testing.T) {
		testP17Stdio(t)
	})
	t.Run("P18_http_sse_首包小于1s", func(t *testing.T) {
		testP18SSE(t)
	})
}

func testP17Stdio(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	writeConfig(t, dir, "stdio", freePort(t), true)

	procCtx, procCancel := context.WithCancel(context.Background())
	t.Cleanup(procCancel)
	cmd := exec.CommandContext(procCtx, binaryPath, "-config", "config.toml", "-env", ".env") //nolint:gosec // binaryPath 由 TestMain 构建
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DEEPSEEK_API_KEY="+apiKey)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe 失败: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe 失败: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 stdio 服务器失败: %v", err)
	}
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		if killErr := cmd.Process.Kill(); killErr != nil {
			t.Logf("结束 stdio 进程: %v", killErr)
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			t.Logf("等待 stdio 进程: %v", waitErr)
		}
	})

	writer := bufio.NewWriter(stdin)
	reader := bufio.NewReader(stdout)
	billable(t)

	sendLine(t, writer, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"l3b","version":"0"}}}`)
	assertJSONRPC(t, readLine(t, reader))
	sendLine(t, writer, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	sendLine(t, writer, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ds_search","arguments":{"query":"golang","count":1}}}`)
	response := readLine(t, reader)
	assertJSONRPC(t, response)
	if !strings.Contains(response, `"result"`) {
		t.Fatalf("tools/call 没有 result: %s", response)
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("关闭 stdin 失败: %v", err)
	}
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			assertJSONRPC(t, line)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatalf("读取 stdout 失败: %v", readErr)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("stdio 服务器退出异常: %v, stderr=%s", err, stderr.String())
	}
	waited = true
}

func sendLine(t *testing.T, w *bufio.Writer, line string) {
	t.Helper()
	if _, err := fmt.Fprintln(w, line); err != nil {
		t.Fatalf("写 stdin 失败: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush stdin 失败: %v", err)
	}
}

func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("读 stdout 失败: %v", err)
	}
	return strings.TrimSpace(line)
}

func assertJSONRPC(t *testing.T, line string) {
	t.Helper()
	var frame struct {
		JSONRPC string `json:"jsonrpc"`
	}
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("stdout 出现非 JSON-RPC 内容 %q: %v", line, err)
	}
	if frame.JSONRPC != "2.0" {
		t.Fatalf("不是 JSON-RPC 2.0: %q", line)
	}
}

func testP18SSE(t *testing.T) {
	t.Helper()
	srv := startHTTPServer(t, false)
	billable(t)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ds_search","arguments":{"query":"golang","count":1},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"l3b","version":"0"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.endpoint+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "ds_search")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Logf("关闭 SSE 响应体: %v", closeErr)
		}
	}()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("读取 SSE 首行失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("SSE 首包耗时 %v，超过 1s", elapsed)
	}
	if !strings.Contains(line, "event:") && !strings.Contains(line, "data:") {
		t.Fatalf("首行不是 SSE 事件: %q", line)
	}
	if _, drainErr := io.Copy(io.Discard, reader); drainErr != nil {
		t.Fatalf("读取 SSE 剩余内容失败: %v", drainErr)
	}
}
