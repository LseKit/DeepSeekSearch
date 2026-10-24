// 文件名: logger_test.go
// 用途: 验证 slog 构建、配置指纹、28 个事件常量与事件必带字段、行内时间格式与 stdout 零输出。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

var eventNameRe = regexp.MustCompile(`^[a-z][a-z0-9_.]*$`)

var allEventNames = []string{
	domain.EvAppStart, domain.EvAppConfig, domain.EvAppDB, domain.EvAppTransport, domain.EvAppReady, domain.EvAppStop,
	domain.EvCallBegin, domain.EvCallCache, domain.EvCallLimitWait, domain.EvCallUpstream, domain.EvCallUpstreamDone,
	domain.EvCallRetry, domain.EvCallCircuit, domain.EvCallDedupe, domain.EvCallSection, domain.EvCallEnd,
	domain.EvErr, domain.EvWarnSlow, domain.EvWarnBudget, domain.EvWarnCacheDegraded, domain.EvWarnCacheOversize,
	domain.EvWarnCacheCommitFailed, domain.EvWarnCacheKeyCollision, domain.EvInfoCacheRecovered, domain.EvWarnLogQuota,
	domain.EvSSRFBlocked, domain.EvCachePrune, domain.EvLogRotate,
}

func TestNew(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
	cfg := testConfig(t, clk)
	cfg.AlsoStderr = false
	logger, closer := newLogger(t, cfg)

	logger.Info(domain.EvAppStart, "tool", "ds_search")
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	line := onlyLogContent(t, cfg.Dir)
	record := parseJSONRecord(t, line)
	if record["msg"] != domain.EvAppStart {
		t.Fatalf("msg = %v, want %s", record["msg"], domain.EvAppStart)
	}
	if record["config_sha256"] != cfg.ConfigSha {
		t.Fatalf("config_sha256 = %v, want %s", record["config_sha256"], cfg.ConfigSha)
	}
	if record["tool"] != "ds_search" {
		t.Fatalf("tool = %v", record["tool"])
	}
}

func TestNew_RedactsUpstreamMsg(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
	cfg := testConfig(t, clk)
	cfg.AlsoStderr = false
	logger, closer := newLogger(t, cfg)

	logger.Info(domain.EvErr, "upstream_msg",
		"Authentication Fails, Your api key: ****0000 is invalid")
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	record := parseJSONRecord(t, onlyLogContent(t, cfg.Dir))
	msg, ok := record["upstream_msg"].(string)
	if !ok {
		t.Fatalf("upstream_msg 字段缺失或类型错误: %#v", record["upstream_msg"])
	}
	if strings.Contains(msg, "0000") || !strings.Contains(msg, "[REDACTED]") {
		t.Fatalf("upstream_msg 未二次脱敏: %q", msg)
	}
}

func TestNew_NoStdout(t *testing.T) {
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() = %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = oldStdout
		if closeErr := writer.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			t.Errorf("writer.Close() = %v", closeErr)
		}
		if closeErr := reader.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			t.Errorf("reader.Close() = %v", closeErr)
		}
	})

	clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
	cfg := testConfig(t, clk)
	cfg.AlsoStderr = false
	logger, closer := newLogger(t, cfg)
	logger.Info(domain.EvAppStart)
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() = %v", err)
	}
	os.Stdout = oldStdout
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("io.ReadAll() = %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("stdout 应完全为空，实际 %q", data)
	}
}

func TestNew_LineTimeFormat(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(time.Date(2026, 10, 20, 12, 34, 56, 0, testTZ))
	cfg := testConfig(t, clk)
	cfg.AlsoStderr = false
	cfg.TsFormat = "2006/01/02 15:04:05"
	logger, closer := newLogger(t, cfg)

	logger.Info(domain.EvAppStart)
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	record := parseJSONRecord(t, onlyLogContent(t, cfg.Dir))
	if record["time"] != "2026/10/20 12:34:56" {
		t.Fatalf("time = %v, want 2026/10/20 12:34:56", record["time"])
	}
}

func TestConfigSha256(t *testing.T) {
	t.Parallel()

	base := config.Default()
	base.ApiKey = "sk-base"
	base.HttpToken = "token-base"
	baseHash := ConfigSha256(base)

	if len(baseHash) != 12 {
		t.Fatalf("指纹长度 = %d, want 12", len(baseHash))
	}
	if got := ConfigSha256(base); got != baseHash {
		t.Fatalf("同配置两次指纹不同: %s vs %s", got, baseHash)
	}

	changed := base
	changed.Log.Level = "debug"
	if ConfigSha256(changed) == baseHash {
		t.Fatal("修改 log.level 后指纹应变化")
	}

	keyChanged := base
	keyChanged.ApiKey = "sk-other"
	if ConfigSha256(keyChanged) != baseHash {
		t.Fatal("只改 ApiKey 不应改变指纹（必须走 Redacted）")
	}

	tokenChanged := base
	tokenChanged.HttpToken = "token-other"
	if ConfigSha256(tokenChanged) != baseHash {
		t.Fatal("只改 HttpToken 不应改变指纹（必须走 Redacted）")
	}
}

func TestEventConstants(t *testing.T) {
	t.Parallel()

	if len(allEventNames) != 28 {
		t.Fatalf("事件数量 = %d, want 28", len(allEventNames))
	}
	seen := make(map[string]bool, len(allEventNames))
	for _, name := range allEventNames {
		if !eventNameRe.MatchString(name) {
			t.Fatalf("事件名不符合小写点分规则: %q", name)
		}
		if seen[name] {
			t.Fatalf("事件名重复: %q", name)
		}
		seen[name] = true
	}
}

func TestEventUniqueness(t *testing.T) {
	t.Parallel()

	seen := make(map[string]string, len(allEventNames))
	for _, name := range allEventNames {
		if prev, ok := seen[name]; ok {
			t.Fatalf("事件 %q 与 %q 重复", name, prev)
		}
		seen[name] = name
	}
}

type eventCase struct {
	name   string
	event  string
	callID bool
	billed bool
	extra  []string
	level  slog.Level
}

func TestEventRequiredFields(t *testing.T) {
	t.Parallel()

	cases := []eventCase{
		{name: "app.start", event: domain.EvAppStart},
		{name: "app.config", event: domain.EvAppConfig},
		{name: "app.db", event: domain.EvAppDB},
		{name: "app.transport", event: domain.EvAppTransport},
		{name: "app.ready", event: domain.EvAppReady},
		{name: "app.stop", event: domain.EvAppStop, extra: []string{"dropped_lines"}},
		{name: "call.begin", event: domain.EvCallBegin, callID: true, extra: []string{"tool"}},
		{name: "call.cache", event: domain.EvCallCache, callID: true, extra: []string{"tool"}},
		{name: "call.limit.wait", event: domain.EvCallLimitWait, callID: true, extra: []string{"tool"}},
		{name: "call.upstream", event: domain.EvCallUpstream, callID: true, extra: []string{"tool"}},
		{name: "call.upstream.done", event: domain.EvCallUpstreamDone, callID: true, extra: []string{"tool", "ms"}},
		{name: "call.retry", event: domain.EvCallRetry, callID: true, extra: []string{"tool"}},
		{name: "call.circuit", event: domain.EvCallCircuit, callID: true, extra: []string{"tool"}},
		{name: "call.dedupe", event: domain.EvCallDedupe, callID: true, extra: []string{"tool"}},
		{name: "call.section", event: domain.EvCallSection, callID: true, extra: []string{"tool"}},
		{name: "call.end", event: domain.EvCallEnd, callID: true, billed: true, extra: []string{"tool", "ms"}},
		{name: "err调用上下文", event: domain.EvErr, callID: true, extra: []string{"code", "code_name"}, level: slog.LevelError},
		{name: "err后台上下文", event: domain.EvErr, extra: []string{"code", "code_name"}, level: slog.LevelError},
		{name: "warn.slow", event: domain.EvWarnSlow, callID: true, extra: []string{"tool", "ms"}, level: slog.LevelWarn},
		{name: "warn.budget", event: domain.EvWarnBudget, callID: true, extra: []string{"tool", "ms"}, level: slog.LevelWarn},
		{name: "warn.cache_degraded", event: domain.EvWarnCacheDegraded, level: slog.LevelWarn},
		{name: "warn.cache_oversize", event: domain.EvWarnCacheOversize, extra: []string{"key_hash", "bytes", "limit_kb"}, level: slog.LevelWarn},
		{name: "info.cache_recovered", event: domain.EvInfoCacheRecovered},
		{name: "warn.cache_commit_failed", event: domain.EvWarnCacheCommitFailed, extra: []string{"key_hash", "err"}, level: slog.LevelWarn},
		{name: "warn.cache_key_collision", event: domain.EvWarnCacheKeyCollision, extra: []string{"key_hash", "url_sha256"}, level: slog.LevelWarn},
		{name: "warn.log_quota", event: domain.EvWarnLogQuota, extra: []string{"dropped_lines"}, level: slog.LevelWarn},
		{name: "ssrf.blocked", event: domain.EvSSRFBlocked, callID: true, extra: []string{"url"}},
		{name: "cache.prune", event: domain.EvCachePrune},
		{name: "log.rotate", event: domain.EvLogRotate, extra: []string{"reason", "dropped_lines"}},
	}

	clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
	cfg := testConfig(t, clk)
	cfg.AlsoStderr = false
	cfg.Level = "debug"
	logger, closer := newLogger(t, cfg)

	for _, tc := range cases {
		level := tc.level
		if level == 0 {
			level = slog.LevelInfo
		}
		logger.Log(context.Background(), level, tc.event, eventArgs(tc)...)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(onlyLogContent(t, cfg.Dir)), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("日志行数 = %d, want %d", len(lines), len(cases))
	}
	for i, tc := range cases {
		record := parseJSONRecord(t, lines[i])
		if record["msg"] != tc.event {
			t.Fatalf("第 %d 行 msg = %v, want %s", i, record["msg"], tc.event)
		}
		if record["config_sha256"] != cfg.ConfigSha {
			t.Fatalf("%s 缺少 config_sha256", tc.name)
		}
		_, hasCallID := record["call_id"]
		if hasCallID != tc.callID {
			t.Fatalf("%s call_id 在场=%v, want %v", tc.name, hasCallID, tc.callID)
		}
		_, hasBilled := record["billed"]
		if hasBilled != tc.billed {
			t.Fatalf("%s billed 在场=%v, want %v", tc.name, hasBilled, tc.billed)
		}
		if tc.billed && record["billed"] != true {
			t.Fatalf("%s billed = %v, want true", tc.name, record["billed"])
		}
		for _, key := range tc.extra {
			if _, ok := record[key]; !ok {
				t.Fatalf("%s 缺少必带字段 %s（record=%v）", tc.name, key, record)
			}
		}
	}
}

func TestSlow(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	Slow(logger, 100, "ds_search", 100)
	if buf.Len() != 0 {
		t.Fatalf("ms == slow_ms 不应打 warn.slow，实际 %q", buf.String())
	}

	Slow(logger, 100, "ds_search", 101, "call_id", "c1")
	record := parseJSONRecord(t, buf.String())
	if record["msg"] != domain.EvWarnSlow {
		t.Fatalf("msg = %v, want %s", record["msg"], domain.EvWarnSlow)
	}
	if record["tool"] != "ds_search" || record["call_id"] != "c1" {
		t.Fatalf("Slow 字段不正确: %v", record)
	}
	if record["ms"] != float64(101) {
		t.Fatalf("ms = %v, want 101", record["ms"])
	}
	Slow(nil, 100, "ds_search", 101)
}

func TestToSlogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want slog.Level
	}{
		{in: "debug", want: slog.LevelDebug},
		{in: "info", want: slog.LevelInfo},
		{in: "warn", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "unknown", want: slog.LevelInfo},
	}
	for _, tt := range tests {
		if got := toSlogLevel(tt.in); got != tt.want {
			t.Fatalf("toSlogLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func eventArgs(tc eventCase) []any {
	args := make([]any, 0, 2+len(tc.extra)*2)
	if tc.callID {
		args = append(args, "call_id", "cid-1")
	}
	if tc.billed {
		args = append(args, "billed", true)
	}
	for _, key := range tc.extra {
		switch key {
		case "tool":
			args = append(args, key, "ds_search")
		case "ms":
			args = append(args, key, 123)
		case "code":
			args = append(args, key, 2404)
		case "code_name":
			args = append(args, key, "UP_NOT_FOUND")
		case "url":
			args = append(args, key, "https://example.com/?token=***")
		case "dropped_lines":
			args = append(args, key, 3)
		case "reason":
			args = append(args, key, "daily")
		case "key_hash":
			args = append(args, key, "abc123def456")
		case "bytes":
			args = append(args, key, 2097153)
		case "limit_kb":
			args = append(args, key, 2048)
		case "err":
			args = append(args, key, "disk full")
		case "url_sha256":
			args = append(args, key, "abcdef0123456789")
		}
	}
	return args
}

func newLogger(t *testing.T, cfg Config) (*slog.Logger, io.Closer) { //nolint:gocritic // 测试 helper
	t.Helper()
	logger, closer, err := New(cfg)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := closer.Close(); closeErr != nil {
			t.Errorf("Close() = %v", closeErr)
		}
	})
	return logger, closer
}

func onlyLogContent(t *testing.T, dir string) string {
	t.Helper()
	names := logFiles(t, dir)
	if len(names) != 1 {
		t.Fatalf("日志文件数 = %d, want 1（%v）", len(names), names)
	}
	return readText(t, filepath.Join(dir, names[0]))
}

func parseJSONRecord(t *testing.T, line string) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &record); err != nil {
		t.Fatalf("JSON 解析失败: %v（line=%q）", err, line)
	}
	return record
}
