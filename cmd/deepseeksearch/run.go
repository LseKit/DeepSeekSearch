// 文件名: run.go
// 用途: 19 步装配、-version/-check、优雅关闭与三处版本号注入。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"time"

	// 副作用导入：Windows 与 distroless 镜像没有系统 tzdata，
	// 缺它则 LoadLocation("Asia/Shanghai") 必然失败、发布物启动即 exit 1。
	_ "time/tzdata"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/deepseek"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/logging"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
	"github.com/LseKit/DeepSeekSearch/internal/tools"
	"github.com/LseKit/DeepSeekSearch/internal/transport"
)

type clientFactory func(deepseek.Config) deepseek.Client

type flagOptions struct {
	showVersion bool
	checkOnly   bool
	configPath  string
	envPath     string
	transport   string
}

// run 是 main 的全部逻辑，返回退出码；拆出来让测试不必真的 os.Exit。
func run(args []string, stdout, stderr io.Writer, stop <-chan struct{}) int {
	return runWith(args, stdout, stderr, stop, deepseek.New)
}

func runWith(args []string, stdout, stderr io.Writer, stop <-chan struct{}, newClient clientFactory) (code int) {
	defer func() {
		if rec := recover(); rec != nil {
			writef(stderr, "panic: %v\n", rec)
			code = 1
		}
	}()

	opts, parseCode := parseFlags(args, stderr)
	if parseCode != 0 {
		return parseCode
	}
	// -version 必须在任何 IO 之前返回：配置损坏、目录不存在也要能查版本。
	if opts.showVersion {
		printVersion(stdout, EffectiveVersion(version, ""))
		return 0
	}

	tz, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		writef(stderr, "%s\n", "时区库加载失败（正常情况下不可能：已嵌入 time/tzdata，请检查构建是否误加了 -tags timetzdata_exclude）")
		return 1
	}

	cfg, warnings, err := loadConfig(opts)
	if err != nil {
		writef(stderr, "配置错误: %v\n", err)
		return 1
	}
	for _, warning := range warnings {
		writef(stderr, "warning: %s\n", warning)
	}
	if opts.checkOnly {
		printCheckSummary(stdout, &cfg)
		return 0
	}
	return serve(&cfg, tz, stop, newClient, stderr)
}

func parseFlags(args []string, stderr io.Writer) (opts flagOptions, code int) {
	fs := flag.NewFlagSet("DeepSeekSearch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&opts.showVersion, "version", false, "打印版本后立即退出，不读任何配置")
	fs.BoolVar(&opts.checkOnly, "check", false, "只校验配置并打印摘要，不建目录、不起服务")
	fs.StringVar(&opts.configPath, "config", "Config/config.toml", "配置文件路径")
	fs.StringVar(&opts.envPath, "env", "Config/.env", "密钥文件路径")
	fs.StringVar(&opts.transport, "transport", "", "覆盖传输方式：stdio | streamable-http")
	if err := fs.Parse(args); err != nil {
		return opts, 2
	}
	return opts, 0
}

// loadConfig 加载并校验配置；-transport 覆盖后 MUST 再校验一次，
// 否则 --transport stdio 会绕开“stdio 下日志不能写 stdout”的跨字段检查。
func loadConfig(opts flagOptions) (config.Config, []string, error) {
	cfg, err := config.Load(opts.configPath, opts.envPath)
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("加载配置: %w", err)
	}
	if opts.transport != "" {
		cfg.Server.Transport = opts.transport
	}
	warnings, err := cfg.Validate()
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("校验配置: %w", err)
	}
	return cfg, warnings, nil
}

func printVersion(w io.Writer, effective string) {
	writef(w, "DeepSeekSearch %s (commit %s, built %s, %s)\n",
		effective, commit, buildTime, runtime.Version())
}

// writef 写 stdout/stderr；这些流是进程的最终落点，写失败后无处再报，直接放弃。
func writef(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return
	}
}

func printCheckSummary(w io.Writer, cfg *config.Config) {
	backend := "off"
	if cfg.Cache.Enabled {
		backend = cfg.Cache.Backend
	}
	writef(w, "配置校验通过（transport=%s, host=%s, port=%d, cache=%s）\n",
		cfg.Server.Transport, cfg.Server.Http.Host, cfg.Server.Http.Port, backend)
}

func currentBuildInfo(cfg *config.Config) domain.BuildInfo {
	return domain.BuildInfo{
		Version:   EffectiveVersion(version, cfg.App.Version),
		Commit:    commit,
		BuildTime: buildTime,
		GoVersion: runtime.Version(),
	}
}

func serve(cfg *config.Config, tz *time.Location, stop <-chan struct{}, newClient clientFactory, stderr io.Writer) int {
	clk := resilience.SystemClock()
	build := currentBuildInfo(cfg)
	logger, closer, err := newLogger(cfg, tz, clk)
	if err != nil {
		writef(stderr, "日志初始化失败: %v\n", err)
		return 1
	}
	writer, ok := closer.(*logging.RotatingWriter)
	if !ok {
		writer = nil
	}

	deps, closeCache, err := buildDeps(cfg, tz, clk, logger, build, newClient)
	if err != nil {
		return failRuntime(logger, closer, closeCache, stderr, "build_deps", err)
	}

	srv := newMCPServer(build, logger)
	if regErr := tools.RegisterAll(srv, deps); regErr != nil {
		return failRuntime(logger, closer, closeCache, stderr, "register_tools", regErr)
	}
	handler, err := transport.BuildWithCache(cfg.Server.Transport, *cfg, build, logger, deps.Cache)
	if err != nil {
		return failRuntime(logger, closer, closeCache, stderr, "build_transport", err)
	}

	logger.Info(domain.EvAppStart, "version", build.Version, "commit", build.Commit,
		"build", build.BuildTime, "go", build.GoVersion, "pid", os.Getpid())

	ctx, cancel := context.WithCancel(context.Background())
	go watchStop(ctx, stop, cancel)
	serveErr := handler.Serve(ctx, srv)
	cancel()
	return shutdown(logger, closer, writer, deps.Singleflight, closeCache, serveErr)
}

func watchStop(ctx context.Context, stop <-chan struct{}, cancel context.CancelFunc) {
	select {
	case <-stop:
		cancel()
	case <-ctx.Done():
	}
}

// failRuntime 处理装配失败：记日志、按顺序关缓存与日志，返回退出码 1。
func failRuntime(logger *slog.Logger, closer io.Closer, closeCache func() error,
	stderr io.Writer, stage string, cause error) int {
	logger.Error(domain.EvErr, "stage", stage, "error", cause)
	if closeCache != nil {
		if closeErr := closeCache(); closeErr != nil {
			logger.Error(domain.EvErr, "stage", "close_cache_after_"+stage, "error", closeErr)
		}
	}
	if closeErr := closer.Close(); closeErr != nil {
		writef(stderr, "关闭日志失败: %v\n", closeErr)
	}
	return 1
}

// shutdown 固定顺序：ReleaseAll → cache.Close → logger.Close。
// ReleaseAll 必须在 cache.Close 之前，否则 SQLite 连接已关、租约只能等 TTL。
func shutdown(logger *slog.Logger, closer io.Closer, writer *logging.RotatingWriter,
	sf resilience.Singleflight, closeCache func() error, serveErr error) int {
	code := 0
	if serveErr != nil {
		logger.Error(domain.EvErr, "stage", "serve", "error", serveErr)
		code = 1
	}
	dropped := int64(0)
	if writer != nil {
		dropped = writer.DroppedLines()
	}
	logger.Info(domain.EvAppStop, "dropped_lines", dropped, "exit_code", code)

	relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := sf.ReleaseAll(relCtx, ""); err != nil {
		logger.Error(domain.EvErr, "stage", "release_leases", "error", err)
		code = 1
	}
	cancel()
	if err := closeCache(); err != nil {
		logger.Error(domain.EvErr, "stage", "close_cache", "error", err)
		code = 1
	}
	if err := closer.Close(); err != nil {
		writef(os.Stderr, "关闭日志失败: %v\n", err)
		code = 1
	}
	return code
}

func newLogger(cfg *config.Config, tz *time.Location, clk resilience.Clock) (*slog.Logger, io.Closer, error) {
	logger, closer, err := logging.New(logging.Config{
		Dir:        cfg.Log.Dir,
		FilePrefix: cfg.Log.FilePrefix,
		TZ:         tz,
		MaxSizeMb:  cfg.Log.MaxSizeMb,
		DailyMaxMb: cfg.Log.DailyMaxMb,
		MaxAgeDays: cfg.Log.MaxAgeDays,
		MaxBackups: cfg.Log.MaxBackups,
		Compress:   cfg.Log.Compress,
		AlsoStderr: cfg.Log.AlsoStderr,
		Level:      cfg.Log.Level,
		Format:     cfg.Log.Format,
		TsFormat:   cfg.Log.TsFormat,
		SlowMs:     cfg.Log.SlowMs,
		ConfigSha:  logging.ConfigSha256(*cfg),
		Clock:      clk,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("初始化日志: %w", err)
	}
	return logger, closer, nil
}
