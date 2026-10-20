// 文件名: writer_test.go
// 用途: 验证日轮转、双上限、配额、保留期、压缩、并发与调度 goroutine 生命周期。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package logging

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testTZ = time.FixedZone("CST", 8*60*60)

func TestRotatingWriter(t *testing.T) {
	t.Run("同日写3次同一文件", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w := newWriter(t, cfg)

		for i := 0; i < 3; i++ {
			writeString(t, w, "line\n")
		}
		if got := len(logFiles(t, cfg.Dir)); got != 1 {
			t.Fatalf("文件数 = %d, want 1", got)
		}
		if w.curBytes != 15 {
			t.Fatalf("curBytes = %d, want 15", w.curBytes)
		}
	})

	t.Run("同秒多writer不同文件", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w1 := newWriter(t, cfg)
		w2 := newWriter(t, cfg)

		if w1.curName == w2.curName {
			t.Fatalf("同秒两个 writer 得到同一文件: %s", w1.curName)
		}
		pidPart := fmt.Sprintf("_%d", os.Getpid())
		for _, name := range []string{w1.curName, w2.curName} {
			if !strings.Contains(name, pidPart) {
				t.Fatalf("文件名缺少 PID: %s", name)
			}
			if _, ok := parseLogName(name, testTZ); !ok {
				t.Fatalf("文件名不可解析: %s", name)
			}
		}
	})

	t.Run("text格式配额事件", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		cfg.Format = "text"
		w := newWriter(t, cfg)
		w.dailyMaxBytes = 10
		var stderr bytes.Buffer
		w.stderr = &stderr

		writeString(t, w, strings.Repeat("x", 12)+"\n")
		writeString(t, w, "drop\n")

		if !strings.Contains(stderr.String(), "event=warn.log_quota") ||
			!strings.Contains(stderr.String(), "config_sha256=sha-test-1234") {
			t.Fatalf("text 格式事件不正确: %q", stderr.String())
		}
	})

	t.Run("跨日切分", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 23, 59, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w := newWriter(t, cfg)

		writeString(t, w, "a\n")
		oldFile := w.file
		clk.Advance(2 * time.Minute)
		writeString(t, w, "b\n")

		if w.curDate != "2026-10-21" {
			t.Fatalf("curDate = %q, want 2026-10-21", w.curDate)
		}
		if _, err := oldFile.WriteString("x"); err == nil {
			t.Fatal("跨日后旧文件应已关闭，但仍可写")
		}
		if got := len(logFiles(t, cfg.Dir)); got != 2 {
			t.Fatalf("文件数 = %d, want 2", got)
		}
	})

	t.Run("单文件上限", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w := newWriter(t, cfg)
		w.maxFileBytes = 10

		writeString(t, w, "12345\n")
		writeString(t, w, "67890\n")

		if got := len(logFiles(t, cfg.Dir)); got != 2 {
			t.Fatalf("单文件超限后文件数 = %d, want 2", got)
		}
		if w.dayBytes < 12 {
			t.Fatalf("dayBytes = %d, 应继续累计至少 12", w.dayBytes)
		}
	})

	t.Run("单日配额", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w := newWriter(t, cfg)
		w.dailyMaxBytes = 10
		var stderr bytes.Buffer
		w.stderr = &stderr

		writeString(t, w, "12345\n")
		writeString(t, w, "12345\n")
		writeString(t, w, "drop1\n")
		writeString(t, w, "drop2\n")

		if w.DroppedLines() != 2 {
			t.Fatalf("droppedLines = %d, want 2", w.DroppedLines())
		}
		if got := strings.Count(stderr.String(), "warn.log_quota"); got != 1 {
			t.Fatalf("warn.log_quota 次数 = %d, want 1（stderr=%q）", got, stderr.String())
		}
		if w.curBytes != 12 {
			t.Fatalf("curBytes = %d, want 12（被丢弃的行不应写入文件）", w.curBytes)
		}
	})

	t.Run("保留7天含今天", func(t *testing.T) {
		t.Parallel()
		today := time.Date(2026, 10, 21, 12, 0, 0, 0, testTZ)
		clk := newFakeClock(today)
		cfg := testConfig(t, clk)
		mustMkdir(t, cfg.Dir)
		old8 := writeManualLog(t, cfg.Dir, cfg.FilePrefix, today.AddDate(0, 0, -8), 9001, false, "old8")
		old6 := writeManualLog(t, cfg.Dir, cfg.FilePrefix, today.AddDate(0, 0, -6), 9002, false, "old6")
		todayManual := writeManualLog(t, cfg.Dir, cfg.FilePrefix, today, 9003, false, "today")

		_ = newWriter(t, cfg)

		if _, err := os.Stat(old8); !os.IsNotExist(err) {
			t.Fatalf("8 天前的文件应被删除: %v", err)
		}
		if _, err := os.Stat(old6); err != nil {
			t.Fatalf("6 天前的文件应保留: %v", err)
		}
		if _, err := os.Stat(todayManual); err != nil {
			t.Fatalf("今天的文件应保留: %v", err)
		}
	})

	t.Run("max_backups兜底", func(t *testing.T) {
		t.Parallel()
		today := time.Date(2026, 10, 21, 12, 0, 0, 0, testTZ)
		clk := newFakeClock(today)
		cfg := testConfig(t, clk)
		cfg.MaxBackups = 2
		mustMkdir(t, cfg.Dir)

		var oldPaths []string
		for i := 0; i < 5; i++ {
			at := today.AddDate(0, 0, -5+i)
			oldPaths = append(oldPaths, writeManualLog(t, cfg.Dir, cfg.FilePrefix, at, 9100+i, false, "old"))
		}
		_ = newWriter(t, cfg)

		for i := 0; i < 3; i++ {
			if _, err := os.Stat(oldPaths[i]); !os.IsNotExist(err) {
				t.Fatalf("最老的 3 个文件应被删除: %s err=%v", oldPaths[i], err)
			}
		}
		for i := 3; i < 5; i++ {
			if _, err := os.Stat(oldPaths[i]); err != nil {
				t.Fatalf("最新的 2 个旧文件应保留: %s err=%v", oldPaths[i], err)
			}
		}
	})

	t.Run("compress旧文件", func(t *testing.T) {
		t.Parallel()
		today := time.Date(2026, 10, 21, 12, 0, 0, 0, testTZ)
		clk := newFakeClock(today)
		cfg := testConfig(t, clk)
		cfg.Compress = true
		mustMkdir(t, cfg.Dir)
		yesterday := writeManualLog(t, cfg.Dir, cfg.FilePrefix, today.AddDate(0, 0, -1), 9201, false, "yesterday-content")
		todayManual := writeManualLog(t, cfg.Dir, cfg.FilePrefix, today, 9202, false, "today-content")
		older := writeManualLog(t, cfg.Dir, cfg.FilePrefix, today.AddDate(0, 0, -2), 9203, false, "older-original")
		_ = writeManualLog(t, cfg.Dir, cfg.FilePrefix, today.AddDate(0, 0, -2), 9203, true, "already-compressed")

		_ = newWriter(t, cfg)

		if _, err := os.Stat(older); !os.IsNotExist(err) {
			t.Fatalf("已有 .gz 时旧 .log 应被删除: %v", err)
		}
		if got := readGzip(t, older+".gz"); got != "already-compressed" {
			t.Fatalf("已有 .gz 不应被覆盖: %q", got)
		}
		gzPath := yesterday + ".gz"
		if _, err := os.Stat(gzPath); err != nil {
			t.Fatalf("旧文件应生成 .gz: %v", err)
		}
		if got := readGzip(t, gzPath); got != "yesterday-content" {
			t.Fatalf("gzip 内容 = %q", got)
		}
		if _, err := os.Stat(todayManual); err != nil {
			t.Fatalf("今天的文件 MUST NOT 被压缩: %v", err)
		}
		if _, err := os.Stat(todayManual + ".gz"); !os.IsNotExist(err) {
			t.Fatalf("今天的文件不应出现 .gz: %v", err)
		}
	})

	t.Run("并发100写", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w := newWriter(t, cfg)

		var total atomic.Int64
		var wg sync.WaitGroup
		for g := 0; g < 100; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 10; i++ {
					line := fmt.Sprintf("g%03d-%03d\n", g, i)
					total.Add(int64(len(line)))
					if _, err := w.Write([]byte(line)); err != nil {
						t.Errorf("Write() 返回错误: %v", err)
					}
				}
			}(g)
		}
		wg.Wait()

		if w.curBytes != total.Load() {
			t.Fatalf("curBytes = %d, want %d", w.curBytes, total.Load())
		}
		info, err := w.file.Stat()
		if err != nil {
			t.Fatalf("Stat() 失败: %v", err)
		}
		if info.Size() != total.Load() {
			t.Fatalf("文件大小 = %d, want %d", info.Size(), total.Load())
		}
	})

	t.Run("Close连调两次", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		w := newWriter(t, testConfig(t, clk))
		writeString(t, w, "x\n")

		if err := w.Close(); err != nil {
			t.Fatalf("第一次 Close() = %v", err)
		}
		if n, err := w.Write([]byte("after\n")); err != nil || n != len("after\n") {
			t.Fatalf("Close 后 Write() = (%d, %v), want (%d, nil)", n, err, len("after\n"))
		}
		if err := w.Close(); err != nil {
			t.Fatalf("第二次 Close() = %v, want nil", err)
		}
	})

	t.Run("文件名解析", func(t *testing.T) {
		t.Parallel()
		got, ok := parseLogName("DeepSeekSearch_20261020_130512_4821.log", testTZ)
		if !ok {
			t.Fatal("标准文件名应解析成功")
		}
		if got.prefix != "DeepSeekSearch" || got.pid != 4821 || got.seq != 0 || got.compressed {
			t.Fatalf("解析结果 = %+v", got)
		}
		if got.date.Format("2006-01-02 15:04:05") != "2026-10-20 13:05:12" {
			t.Fatalf("日期 = %v", got.date)
		}

		gz, ok := parseLogName("DeepSeekSearch_20261020_130512_4821_2.log.gz", testTZ)
		if !ok || !gz.compressed || gz.seq != 2 {
			t.Fatalf("带序号与 .gz 的文件名解析失败: %+v ok=%v", gz, ok)
		}
		if _, ok := parseLogName("random.log", testTZ); ok {
			t.Fatal("非本格式文件名不应解析成功")
		}
	})

	t.Run("Close回收调度goroutine", func(t *testing.T) {
		t.Parallel()
		for i := 0; i < 3; i++ {
			clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
			w := newWriter(t, testConfig(t, clk))
			if err := w.Close(); err != nil {
				t.Fatalf("Close() = %v", err)
			}
			select {
			case <-w.schedDone:
			default:
				t.Fatal("Close() 返回后调度 goroutine 仍在运行")
			}
		}
	})

	t.Run("quotaHit跨日重置", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w := newWriter(t, cfg)
		w.dailyMaxBytes = 1000
		var stderr bytes.Buffer
		w.stderr = &stderr
		clk.WaitTimer(t)

		writeString(t, w, strings.Repeat("a", 1200)+"\n")
		writeString(t, w, "drop1\n")
		if got := strings.Count(stderr.String(), "warn.log_quota"); got != 1 {
			t.Fatalf("第 1 天 warn.log_quota = %d, want 1", got)
		}

		clk.Advance(24 * time.Hour)
		waitMaintenance(t, w)
		writeString(t, w, strings.Repeat("b", 1200)+"\n")
		writeString(t, w, "drop2\n")

		if got := strings.Count(stderr.String(), "warn.log_quota"); got != 2 {
			t.Fatalf("跨日后 warn.log_quota = %d, want 2", got)
		}
	})

	t.Run("配额后stderr采样", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		w := newWriter(t, cfg)
		w.dailyMaxBytes = 10
		w.stderr = &bytes.Buffer{}

		var sampled bytes.Buffer
		sampler := &samplingWriter{dst: &sampled, quota: w.quotaExceeded, every: 100}
		mw := io.MultiWriter(w, sampler)
		writeString(t, mw, "before\n")
		if !strings.Contains(sampled.String(), "before") {
			t.Fatalf("配额未达标时 stderr 应全量透传，实际 %q", sampled.String())
		}

		writeString(t, w, strings.Repeat("x", 12)+"\n")
		writeString(t, w, "drop\n")

		sampled.Reset()
		for i := 0; i < 1000; i++ {
			writeString(t, mw, "line\n")
		}

		lines := strings.Count(sampled.String(), "\n")
		if lines == 0 || lines > 20 {
			t.Fatalf("stderr 采样行数 = %d, want 1~20（不能全量 1000）", lines)
		}
	})

	t.Run("空闲自转heartbeat", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(time.Date(2026, 10, 18, 12, 0, 0, 0, testTZ))
		cfg := testConfig(t, clk)
		cfg.Compress = false
		w := newWriter(t, cfg)
		clk.WaitTimer(t)
		oldName := w.curName

		clk.Advance(12*time.Hour + 5*time.Minute)
		waitMaintenance(t, w)

		if w.curDate != "2026-10-19" {
			t.Fatalf("curDate = %q, want 2026-10-19", w.curDate)
		}
		content := readText(t, filepath.Join(cfg.Dir, w.curName))
		if !strings.Contains(content, "log.rotate") || !strings.Contains(content, "daily-idle") {
			t.Fatalf("新文件缺少 log.rotate/daily-idle: %q", content)
		}
		if _, err := os.Stat(filepath.Join(cfg.Dir, oldName)); err != nil {
			t.Fatalf("保留期内的旧文件不应被删: %v", err)
		}
	})
}

func TestNewRotatingWriterConfigErrors(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(time.Date(2026, 10, 20, 12, 0, 0, 0, testTZ))
	base := testConfig(t, clk)
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "缺少 Clock", mutate: func(c *Config) { c.Clock = nil }},
		{name: "缺少 TZ", mutate: func(c *Config) { c.TZ = nil }},
		{name: "Dir 为空", mutate: func(c *Config) { c.Dir = "" }},
		{name: "prefix 含路径分隔符", mutate: func(c *Config) { c.FilePrefix = "../escape" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tt.mutate(&cfg)
			w, err := NewRotatingWriter(cfg)
			if err == nil {
				if closeErr := w.Close(); closeErr != nil {
					t.Errorf("Close() = %v", closeErr)
				}
				t.Fatal("期望 NewRotatingWriter 失败")
			}
		})
	}
}

func testConfig(t *testing.T, clk Clock) Config {
	t.Helper()
	return Config{
		Dir:        filepath.Join(t.TempDir(), "Logs"),
		FilePrefix: "DeepSeekSearch",
		TZ:         testTZ,
		MaxSizeMb:  50,
		DailyMaxMb: 500,
		MaxAgeDays: 7,
		MaxBackups: 50,
		Compress:   false,
		AlsoStderr: true,
		Level:      "info",
		Format:     "json",
		TsFormat:   "2006-01-02 15:04:05.000 -07:00",
		SlowMs:     3000,
		ConfigSha:  "sha-test-1234",
		Clock:      clk,
	}
}

func newWriter(t *testing.T, cfg Config) *RotatingWriter { //nolint:gocritic // 测试 helper
	t.Helper()
	w, err := NewRotatingWriter(cfg)
	if err != nil {
		t.Fatalf("NewRotatingWriter() = %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Cleanup Close() = %v", err)
		}
	})
	return w
}

func writeString(t *testing.T, w io.Writer, s string) {
	t.Helper()
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("Write() = %v", err)
	}
}

func waitMaintenance(t *testing.T, w *RotatingWriter) {
	t.Helper()
	select {
	case <-w.maintDone:
	case <-time.After(2 * time.Second):
		t.Fatal("每日维护未在 2s 内完成")
	}
}

func logFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() = %v", err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz") {
			names = append(names, name)
		}
	}
	return names
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll() = %v", err)
	}
}

func writeManualLog(t *testing.T, dir, prefix string, at time.Time, pid int, compressed bool, content string) string {
	t.Helper()
	mustMkdir(t, dir)
	name := fmt.Sprintf("%s_%s_%d.log", prefix, at.Format("20060102_150405"), pid)
	path := filepath.Join(dir, name)
	if compressed {
		path += ".gz"
		file, err := os.Create(path) //nolint:gosec // 测试临时目录
		if err != nil {
			t.Fatalf("Create() = %v", err)
		}
		zw := gzip.NewWriter(file)
		if _, err := zw.Write([]byte(content)); err != nil {
			t.Fatalf("gzip.Write() = %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("gzip.Close() = %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("file.Close() = %v", err)
		}
		return path
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() = %v", err)
	}
	return path
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // 测试临时目录
	if err != nil {
		t.Fatalf("ReadFile(%s) = %v", path, err)
	}
	return string(data)
}

func readGzip(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path) //nolint:gosec // 测试临时目录
	if err != nil {
		t.Fatalf("Open(%s) = %v", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close gzip file: %v", err)
		}
	}()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader() = %v", err)
	}
	defer func() {
		if err := zr.Close(); err != nil {
			t.Errorf("close gzip reader: %v", err)
		}
	}()
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("io.ReadAll() = %v", err)
	}
	return string(data)
}
