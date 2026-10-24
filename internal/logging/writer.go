// 文件名: writer.go
// 用途: 按自然日切分、带单文件与单日双上限的日志写入器，并拥有唯一的每日调度 goroutine。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

const (
	dateLayout     = "2006-01-02"
	nameTimeLayout = "20060102_150405"
	evLogRotate    = domain.EvLogRotate
	evWarnLogQuota = domain.EvWarnLogQuota
)

// Clock 是日志包需要的最小时间接口，由装配层注入。
// 本包 MUST NOT 直接调用 time.Now：否则跨日切分与 00:05 调度无法用假 Clock 测试。
type Clock interface {
	// Now 返回当前时间。
	Now() time.Time
	// After 在 d 之后向返回的 channel 发送一次时间。
	After(d time.Duration) <-chan time.Time
}

// Config 是 logging 的窄配置，由装配层从 config.Config 映射。
// Clock 与 TZ MUST 非 nil；MaxSizeMb / DailyMaxMb 为 0 表示不设该上限。
type Config struct {
	Dir        string
	FilePrefix string
	TZ         *time.Location
	MaxSizeMb  int
	DailyMaxMb int
	MaxAgeDays int
	MaxBackups int
	Compress   bool
	AlsoStderr bool
	Level      string
	Format     string
	TsFormat   string
	SlowMs     int
	ConfigSha  string
	Clock      Clock
}

// RotatingWriter 是按自然日切分、带双上限的日志写入器。
// 双上限缺一不可：单文件上限防「单个文件太大打不开」，
// 单日上限防「某个疯狂循环一夜写满磁盘」。
type RotatingWriter struct {
	mu            sync.Mutex
	cfg           Config
	clk           Clock
	file          *os.File
	pid           int
	curName       string
	curDate       string
	curBytes      int64
	dayBytes      int64
	maxFileBytes  int64
	dailyMaxBytes int64
	quotaHit      bool
	droppedLines  int64
	stderr        io.Writer
	done          chan struct{}
	schedDone     chan struct{}
	maintDone     chan struct{}
	closed        bool
}

// NewRotatingWriter 创建日志目录、打开今天的日志文件、执行一次启动清理，并拉起调度 goroutine。
// Close 负责回收该 goroutine；调用方 MUST 在进程退出前调用 Close。
func NewRotatingWriter(cfg Config) (*RotatingWriter, error) { //nolint:gocritic // 签名与日志轮转器构造保持一致
	if err := prepareWriterConfig(&cfg); err != nil {
		return nil, err
	}
	w := &RotatingWriter{
		cfg:           cfg,
		clk:           cfg.Clock,
		pid:           os.Getpid(),
		maxFileBytes:  int64(cfg.MaxSizeMb) * 1024 * 1024,
		dailyMaxBytes: int64(cfg.DailyMaxMb) * 1024 * 1024,
		stderr:        os.Stderr,
		done:          make(chan struct{}),
		schedDone:     make(chan struct{}),
		maintDone:     make(chan struct{}, 1),
	}
	now := cfg.Clock.Now().In(cfg.TZ)
	if err := w.openLocked(now); err != nil {
		return nil, fmt.Errorf("logging: 打开日志文件失败: %w", err)
	}
	w.curDate = now.Format(dateLayout)
	w.startupCleanup(now)
	go w.schedule()
	return w, nil
}

// prepareWriterConfig 校验必填项、补默认值并创建目录。
func prepareWriterConfig(cfg *Config) error {
	if cfg.Clock == nil {
		return fmt.Errorf("logging: Clock 未注入")
	}
	if cfg.TZ == nil {
		return fmt.Errorf("logging: TZ 未注入")
	}
	if cfg.Dir == "" {
		return fmt.Errorf("logging: Dir 不能为空")
	}
	if cfg.FilePrefix == "" {
		cfg.FilePrefix = "DeepSeekSearch"
	}
	if strings.ContainsAny(cfg.FilePrefix, `/\`) || cfg.FilePrefix == "." || cfg.FilePrefix == ".." {
		return fmt.Errorf("logging: FilePrefix 不能包含路径分隔符")
	}
	if cfg.TsFormat == "" {
		cfg.TsFormat = time.RFC3339
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return fmt.Errorf("logging: 创建日志目录失败: %w", err)
	}
	return nil
}

// startupCleanup 启动时清一次旧文件；失败只写 stderr，不阻塞启动。
func (w *RotatingWriter) startupCleanup(now time.Time) {
	removed, err := pruneByAge(w.cfg.Dir, w.cfg.FilePrefix, now, w.cfg.MaxAgeDays, w.cfg.MaxBackups, w.cfg.Compress)
	if err != nil {
		w.emitStderr(fmt.Sprintf("logging: 启动清理失败: %v\n", err))
		return
	}
	if removed > 0 {
		w.emitStderr(fmt.Sprintf("logging: 启动清理删除 %d 个旧日志文件\n", removed))
	}
}

// Write 实现 io.Writer。它 MUST NEVER 向调用方返回日志写失败 —— 日志失败不该让业务失败。
// 内部错误一律打到 stderr，返回值恒为 len(p), nil；配额丢弃计入 droppedLines。
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || len(p) == 0 {
		return len(p), nil
	}
	now := w.clk.Now().In(w.cfg.TZ)
	if !w.rotateIfNeeded(now, len(p)) {
		return len(p), nil
	}
	w.writeRawLocked(p)
	return len(p), nil
}

// rotateIfNeeded 按跨日 / 日上限 / 单文件上限决定是否轮转；返回 false 表示本行被丢弃。
func (w *RotatingWriter) rotateIfNeeded(now time.Time, n int) bool {
	if w.curDate != now.Format(dateLayout) {
		return w.rotateOrDrop("跨日", func() error { return w.rotateDailyLocked(now, "daily") })
	}
	if w.dailyMaxBytes > 0 && w.dayBytes >= w.dailyMaxBytes {
		w.dropLocked()
		return false
	}
	if w.maxFileBytes > 0 && w.curBytes > 0 && w.curBytes+int64(n) > w.maxFileBytes {
		return w.rotateOrDrop("单文件", func() error { return w.rotateSizeLocked(now) })
	}
	return true
}

// rotateOrDrop 执行轮转；失败只打 stderr 并计入 droppedLines，绝不阻塞写日志。
func (w *RotatingWriter) rotateOrDrop(reason string, rotate func() error) bool {
	if err := rotate(); err != nil {
		w.emitStderr(fmt.Sprintf("logging: %s切分失败: %v\n", reason, err))
		w.droppedLines++
		return false
	}
	return true
}

// Close 停止调度 goroutine、等待其退出并关闭当前文件；可重复调用（第二次返回 nil）。
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()

	close(w.done)
	<-w.schedDone

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	if err != nil {
		return fmt.Errorf("logging: 关闭日志文件失败: %w", err)
	}
	return nil
}

// DroppedLines 返回因单日配额被丢弃的行数，供 app.stop / log.rotate 事件上报。
func (w *RotatingWriter) DroppedLines() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.droppedLines
}

func (w *RotatingWriter) quotaExceeded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.quotaHit
}

func (w *RotatingWriter) dropLocked() {
	w.droppedLines++
	if w.quotaHit {
		return
	}
	w.quotaHit = true
	w.emitStderr(string(w.eventLineLocked("WARN", evWarnLogQuota, "")))
}

// openLocked 用 O_EXCL 打开一个新文件；同名时追加 _1、_2 序号（同进程同秒多 writer）。
// 调用方 MUST 持有 w.mu。
func (w *RotatingWriter) openLocked(now time.Time) error {
	base := fmt.Sprintf("%s_%s_%d", w.cfg.FilePrefix, now.Format(nameTimeLayout), w.pid)
	for seq := 0; seq < 1000; seq++ {
		name := base + ".log"
		if seq > 0 {
			name = fmt.Sprintf("%s_%d.log", base, seq)
		}
		path := filepath.Join(w.cfg.Dir, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // 路径由已校验的 Dir+Prefix 构造
		if err == nil {
			w.file = file
			w.curName = name
			w.curBytes = 0
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("创建 %s 失败: %w", path, err)
		}
	}
	return fmt.Errorf("同名日志文件超过 1000 个")
}

func (w *RotatingWriter) rotateDailyLocked(now time.Time, reason string) error {
	if w.file != nil {
		oldPath := w.file.Name()
		if err := w.file.Close(); err != nil {
			w.emitStderr(fmt.Sprintf("logging: 关闭旧日志失败: %v\n", err))
		}
		w.file = nil
		if w.cfg.Compress {
			if err := gzipFile(oldPath); err != nil {
				w.emitStderr(fmt.Sprintf("logging: 压缩旧日志失败: %v\n", err))
			}
		}
	}
	if err := w.openLocked(now); err != nil {
		return err
	}
	w.curDate = now.Format(dateLayout)
	w.dayBytes = 0
	w.quotaHit = false
	w.writeEventLocked("INFO", evLogRotate, reason)
	return nil
}

func (w *RotatingWriter) rotateSizeLocked(now time.Time) error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			w.emitStderr(fmt.Sprintf("logging: 关闭旧日志失败: %v\n", err))
		}
		w.file = nil
	}
	if err := w.openLocked(now); err != nil {
		return err
	}
	w.writeEventLocked("INFO", evLogRotate, "size")
	return nil
}

func (w *RotatingWriter) writeRawLocked(p []byte) {
	if w.file == nil {
		w.droppedLines++
		return
	}
	n, err := w.file.Write(p)
	w.curBytes += int64(n)
	w.dayBytes += int64(n)
	if err != nil {
		w.emitStderr(fmt.Sprintf("logging: 写日志失败: %v\n", err))
	}
}

func (w *RotatingWriter) writeEventLocked(level, event, reason string) {
	if line := w.eventLineLocked(level, event, reason); line != nil {
		w.writeRawLocked(line)
	}
}

type logEventRecord struct {
	Time         string `json:"time"`
	Level        string `json:"level"`
	Msg          string `json:"msg"`
	Reason       string `json:"reason,omitempty"`
	DroppedLines int64  `json:"dropped_lines"`
	ConfigSha    string `json:"config_sha256"`
}

func (w *RotatingWriter) eventLineLocked(level, event, reason string) []byte {
	now := w.clk.Now().In(w.cfg.TZ).Format(w.cfg.TsFormat)
	if w.cfg.Format == "text" {
		line := fmt.Sprintf("time=%q level=%s msg=%s", now, level, event)
		if reason != "" {
			line += fmt.Sprintf(" reason=%q", reason)
		}
		line += fmt.Sprintf(" dropped_lines=%d config_sha256=%s\n", w.droppedLines, w.cfg.ConfigSha)
		return []byte(line)
	}
	data, err := json.Marshal(logEventRecord{
		Time:         now,
		Level:        level,
		Msg:          event,
		Reason:       reason,
		DroppedLines: w.droppedLines,
		ConfigSha:    w.cfg.ConfigSha,
	})
	if err != nil {
		return nil
	}
	return append(data, '\n')
}

// emitStderr 只写 stderr；调用方决定是否持有 w.mu。写失败无处可报，忽略。
func (w *RotatingWriter) emitStderr(line string) {
	if w.stderr == nil || line == "" {
		return
	}
	//nolint:errcheck // stderr 自身失败时没有更可靠的出口
	_, _ = io.WriteString(w.stderr, line)
}

func (w *RotatingWriter) schedule() {
	defer close(w.schedDone)
	for {
		now := w.clk.Now().In(w.cfg.TZ)
		next := time.Date(now.Year(), now.Month(), now.Day(), 0, 5, 0, 0, w.cfg.TZ)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		select {
		case <-w.done:
			return
		case <-w.clk.After(next.Sub(now)):
			w.runDailyMaintenance()
		}
	}
}

func (w *RotatingWriter) runDailyMaintenance() {
	w.mu.Lock()
	if !w.closed {
		now := w.clk.Now().In(w.cfg.TZ)
		if w.curDate != now.Format(dateLayout) {
			if err := w.rotateDailyLocked(now, "daily-idle"); err != nil {
				w.emitStderr(fmt.Sprintf("logging: 每日空闲切分失败: %v\n", err))
			}
		}
		removed, err := pruneByAge(w.cfg.Dir, w.cfg.FilePrefix, now, w.cfg.MaxAgeDays, w.cfg.MaxBackups, w.cfg.Compress)
		if err != nil {
			w.emitStderr(fmt.Sprintf("logging: 每日清理失败: %v\n", err))
		} else if removed > 0 {
			w.emitStderr(fmt.Sprintf("logging: 每日清理删除 %d 个旧日志文件\n", removed))
		}
	}
	w.mu.Unlock()

	select {
	case w.maintDone <- struct{}{}:
	default:
	}
}

// samplingWriter 在单日配额达标后把 stderr 输出限频到每 every 行 1 行，
// 外加配额切换后的首条。配额未达标时原样透传。
type samplingWriter struct {
	dst      io.Writer
	quota    func() bool
	every    int
	mu       sync.Mutex
	wasQuota bool
	n        int
}

func (s *samplingWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dst == nil {
		return len(p), nil
	}
	every := s.every
	if every <= 0 {
		every = 100
	}
	if s.quota == nil || !s.quota() {
		s.wasQuota = false
		s.n = 0
		return s.writeDst(p)
	}
	if !s.wasQuota {
		s.wasQuota = true
		s.n = 0
		return s.writeDst(p)
	}
	s.n++
	if s.n%every == 0 {
		return s.writeDst(p)
	}
	return len(p), nil
}

func (s *samplingWriter) writeDst(p []byte) (int, error) {
	n, err := s.dst.Write(p)
	if err != nil {
		return n, fmt.Errorf("logging: 写 stderr 失败: %w", err)
	}
	return n, nil
}
