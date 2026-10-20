// 文件名: main_test.go
// 用途: 覆盖 run 的版本/校验/装配六类用例；装配用例注入 fake client 断言零上游调用。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LseKit/DeepSeekSearch/internal/deepseek"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

type fakeClient struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeClient) record() {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
}

func (f *fakeClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeClient) Search(context.Context, domain.SearchRequest) (domain.SearchResponse, error) {
	f.record()
	return domain.SearchResponse{}, nil
}

func (f *fakeClient) Fetch(context.Context, domain.FetchRequest) (domain.FetchResponse, error) {
	f.record()
	return domain.FetchResponse{}, nil
}

func (f *fakeClient) Ping(context.Context) error {
	f.record()
	return nil
}

func writeTestFiles(t *testing.T, port int) (cfgPath, envPath string) {
	t.Helper()

	dir := t.TempDir()
	// 配置里的 log.dir 用相对路径：t.TempDir 的绝对路径随机段较多，
	// 会被 config 的“值形态疑似密钥”启发式误判。
	t.Chdir(dir)
	cfgPath = filepath.Join(dir, "config.toml")
	envPath = filepath.Join(dir, ".env")
	content := fmt.Sprintf(`[server]
transport = "streamable-http"

[server.http]
host = "127.0.0.1"
port = %d
auth_enabled = false
allow_non_local_host = false

[log]
dir = "Logs"
also_stderr = false

[cache]
enabled = false
`, port)
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写 config.toml 失败: %v", err)
	}
	if err := os.WriteFile(envPath, nil, 0o600); err != nil {
		t.Fatalf("写 .env 失败: %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-0123456789abcdef")
	return cfgPath, envPath
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
	port := addr.Port
	if closeErr := ln.Close(); closeErr != nil {
		t.Fatalf("关闭临时监听失败: %v", closeErr)
	}
	return port
}

func TestRunVersion(t *testing.T) {
	oldVersion := version
	version = "v9.9.9-test"
	t.Cleanup(func() { version = oldVersion })

	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("run(--version) = %d, want 0, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), version) {
		t.Fatalf("stdout = %q, want 包含 %q", stdout.String(), version)
	}
	if lines := strings.Count(strings.TrimSpace(stdout.String()), "\n"); lines != 0 {
		t.Fatalf("--version 必须是单行，实际 %d 行", lines+1)
	}
}

func TestRunVersionWithoutConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version", "-config", missing, "-env", missing}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("配置路径不存在时 --version = %d, want 0, stderr=%s", code, stderr.String())
	}
}

func TestRunCheckBadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(cfgPath, []byte("[server]\ntransport = \"sse\"\n"), 0o600); err != nil {
		t.Fatalf("写 config.toml 失败: %v", err)
	}
	if err := os.WriteFile(envPath, nil, 0o600); err != nil {
		t.Fatalf("写 .env 失败: %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-0123456789abcdef")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-check", "-config", cfgPath, "-env", envPath}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("坏配置 -check = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "server.transport") {
		t.Fatalf("stderr = %q, want 含字段路径 server.transport", stderr.String())
	}
}

func TestRunTransportOverrideInvalid(t *testing.T) {
	cfgPath, envPath := writeTestFiles(t, freePort(t))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-check", "-config", cfgPath, "-env", envPath, "-transport", "sse"}, &stdout, &stderr, nil)
	if code != 1 {
		t.Fatalf("-transport sse = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "server.transport") {
		t.Fatalf("stderr = %q, want 提示合法值", stderr.String())
	}
}

func TestRunCheckSuccess(t *testing.T) {
	cfgPath, envPath := writeTestFiles(t, freePort(t))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-check", "-config", cfgPath, "-env", envPath}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("合法配置 -check = %d, want 0, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "transport=streamable-http") {
		t.Fatalf("stdout = %q, want 含 transport 摘要", stdout.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-nope"}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("未知 flag = %d, want 2", code)
	}
}

func TestRunAssemblySuccess(t *testing.T) {
	cfgPath, envPath := writeTestFiles(t, freePort(t))
	fake := &fakeClient{}
	stop := make(chan struct{})
	close(stop) // ctx 立即取消：装配完成后 Serve 立刻走优雅关闭

	var stdout, stderr bytes.Buffer
	code := runWith([]string{"-config", cfgPath, "-env", envPath}, &stdout, &stderr, stop,
		func(deepseek.Config) deepseek.Client { return fake })
	if code != 0 {
		t.Fatalf("装配成功 = %d, want 0, stderr=%s", code, stderr.String())
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("未设 self_test 时上游调用 = %d, want 0", got)
	}
}
