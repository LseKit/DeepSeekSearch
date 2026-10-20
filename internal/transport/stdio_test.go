// 文件名: stdio_test.go
// 用途: 校验 stdio 传输的构造、可用列表，以及 stdout 防污染的两段守卫。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/transport"
)

const initializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"stdio-test","version":"0"}}}`

const initializedNotification = `{"jsonrpc":"2.0","method":"notifications/initialized"}`

const noiseCallRequest = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"noise","arguments":{}}}`

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBuild(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	build := domain.BuildInfo{Version: "0.0.0-test"}

	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "stdio"},
		{name: "sse", wantErr: true},
		{name: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := transport.Build(tt.name, cfg, build, discardLogger())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Build(%q) 未返回错误", tt.name)
				}
				if h != nil {
					t.Fatalf("Build(%q) 出错时仍返回了 Handler", tt.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build(%q) 返回错误: %v", tt.name, err)
			}
			if got := h.Name(); got != tt.name {
				t.Fatalf("Handler.Name() = %q, want %q", got, tt.name)
			}
		})
	}
}

func TestAvailable(t *testing.T) {
	t.Parallel()

	want := []string{"stdio", "streamable-http"}
	got := transport.Available()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Available() = %v, want %v", got, want)
	}
}

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// stdioFixture 把进程级的 os.Stdin/os.Stdout/os.Stderr 换成测试管道。
// 这些是包级变量，所以本文件的用例 MUST NOT 并行 —— 否则互相污染。
type stdioFixture struct {
	stdinW  *os.File
	stdoutR *os.File
	stdoutW *os.File
	stderr  *os.File
	out     *bufio.Reader
	cancel  context.CancelFunc
}

func newStdioFixture(t *testing.T) *stdioFixture {
	t.Helper()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建 stdin 管道失败: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建 stdout 管道失败: %v", err)
	}
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("创建 stderr 临时文件失败: %v", err)
	}

	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdinR, stdoutW, stderrFile

	f := &stdioFixture{
		stdinW:  stdinW,
		stdoutR: stdoutR,
		stdoutW: stdoutW,
		stderr:  stderrFile,
		out:     bufio.NewReaderSize(stdoutR, 64*1024),
	}
	t.Cleanup(func() {
		if f.cancel != nil {
			f.cancel()
		}
		os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr
		for _, file := range []*os.File{stdinR, stdinW, stdoutR, stdoutW, stderrFile} {
			if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Errorf("关闭 %s 失败: %v", file.Name(), err)
			}
		}
	})
	return f
}

// serve 启动 stdio 传输。noise 工具是「模拟第三方库」的入口：
// 它的 handler 在 Serve 的连接处理路径里执行 fmt.Fprintln(os.Stdout)，
// 用来验证重定向后库噪音落到 stderr、而协议帧仍能写回 fd 1。
func (f *stdioFixture) serve(t *testing.T) <-chan error {
	t.Helper()

	h, err := transport.Build("stdio", config.Default(),
		domain.BuildInfo{Version: "0.0.0-test"}, discardLogger())
	if err != nil {
		t.Fatalf("Build(stdio) 返回错误: %v", err)
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "DeepSeekSearch", Version: "0.0.0-test"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "noise",
		Description: "tests only",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := fmt.Fprintln(os.Stdout, "library noise"); err != nil {
			t.Errorf("模拟库写 stdout 失败: %v", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel

	errc := make(chan error, 1)
	go func() { errc <- h.Serve(ctx, srv) }()
	return errc
}

func (f *stdioFixture) send(t *testing.T, line string) {
	t.Helper()
	if _, err := fmt.Fprintln(f.stdinW, line); err != nil {
		t.Fatalf("写 stdin 失败: %v", err)
	}
}

func (f *stdioFixture) readFrame(t *testing.T) rpcFrame {
	t.Helper()

	line, err := f.out.ReadString('\n')
	if err != nil {
		t.Fatalf("读取协议帧失败: %v", err)
	}
	return parseFrame(t, strings.TrimSpace(line))
}

func parseFrame(t *testing.T, line string) rpcFrame {
	t.Helper()

	var frame rpcFrame
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("stdout 出现非 JSON-RPC 内容 %q: %v", line, err)
	}
	if frame.JSONRPC != "2.0" {
		t.Fatalf("帧不是 JSON-RPC 2.0: %q", line)
	}
	return frame
}

// finish 关闭 stdin 触发 EOF，等 Serve 返回，再把 stdout 管道读干净。
func (f *stdioFixture) finish(t *testing.T, errc <-chan error) []string {
	t.Helper()

	if err := f.stdinW.Close(); err != nil {
		t.Fatalf("关闭 stdin 失败: %v", err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("stdin EOF 后 Serve 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdin EOF 后 Serve 未退出")
	}

	if err := f.stdoutW.Close(); err != nil {
		t.Fatalf("关闭 stdout 写端失败: %v", err)
	}
	var rest []string
	for {
		line, err := f.out.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			rest = append(rest, line)
		}
		if err == io.EOF {
			return rest
		}
		if err != nil {
			t.Fatalf("读取 stdout 剩余字节失败: %v", err)
		}
	}
}

func (f *stdioFixture) stderrText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(f.stderr.Name())
	if err != nil {
		t.Fatalf("读取 stderr 临时文件失败: %v", err)
	}
	return string(raw)
}

func TestStdio_NoStdoutPollution(t *testing.T) {
	t.Run("第一段_协议管道只有合法帧", func(t *testing.T) {
		f := newStdioFixture(t)
		errc := f.serve(t)

		f.send(t, initializeRequest)
		frame := f.readFrame(t)
		if len(frame.Result) == 0 {
			t.Fatalf("initialize 响应没有 result: id=%s error=%s", frame.ID, frame.Error)
		}

		for _, line := range f.finish(t, errc) {
			parseFrame(t, line)
		}
	})

	t.Run("第二段_库写stdout落到stderr且协议帧不受影响", func(t *testing.T) {
		f := newStdioFixture(t)
		errc := f.serve(t)

		f.send(t, initializeRequest)
		if frame := f.readFrame(t); len(frame.Result) == 0 {
			t.Fatalf("initialize 响应没有 result: error=%s", frame.Error)
		}
		f.send(t, initializedNotification)

		f.send(t, noiseCallRequest)
		noiseFrame := f.readFrame(t)
		if len(noiseFrame.Result) == 0 {
			t.Fatalf("tools/call 响应没有 result: error=%s", noiseFrame.Error)
		}

		rest := f.finish(t, errc)
		for _, line := range rest {
			if strings.Contains(line, "library noise") {
				t.Fatalf("协议管道里混入了库噪音: %q", line)
			}
			parseFrame(t, line)
		}

		stderrText := f.stderrText(t)
		if !strings.Contains(stderrText, "library noise") {
			t.Fatalf("库噪音没有落到 stderr，stderr 内容: %q", stderrText)
		}
	})
}
