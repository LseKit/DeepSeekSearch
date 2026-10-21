// 文件名: deps.go
// 用途: 把 config.Config 映射成 resilience/deepseek/logging 的窄配置，并组装 tools.Deps。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/deepseek"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
	"github.com/LseKit/DeepSeekSearch/internal/tools"
)

func buildDeps(cfg *config.Config, tz *time.Location, clk resilience.Clock, logger *slog.Logger,
	build domain.BuildInfo, newClient clientFactory) (tools.Deps, func() error, error) {
	rc := resilienceConfig(cfg, tz, clk, logger)
	cache, err := openCache(cfg, &rc, logger)
	if err != nil {
		return tools.Deps{}, nil, err
	}
	closeCache := func() error {
		if cache == nil {
			return nil
		}
		return cache.Close()
	}

	limiter, err := resilience.NewLimiter(limiterSpecs(cfg), cfg.RateLimit.MaxInflight,
		cfg.RateLimit.SearchShare, time.Duration(cfg.RateLimit.MaxWaitMs)*time.Millisecond, clk)
	if err != nil {
		if closeErr := closeCache(); closeErr != nil {
			logger.Error(domain.EvErr, "stage", "close_cache_after_limiter_error", "error", closeErr)
		}
		return tools.Deps{}, nil, fmt.Errorf("构造限流器: %w", err)
	}

	deps := tools.Deps{
		Cfg:          *cfg,
		Client:       newClient(deepseekConfig(cfg, build)),
		Limiter:      limiter,
		Retry:        resilience.NewRetry(rc, clk, nil),
		Budget:       resilience.NewBudget(rc, clk),
		Cache:        cache,
		Singleflight: resilience.NewSingleflight(cache, clk),
		Circuit:      resilience.NewCircuit(rc, clk),
		Clock:        clk,
		StartedAt:    clk.Now(),
		Usage:        tools.NewUsageTracker(clk),
		Build:        build,
		Logger:       logger,
	}
	return deps, closeCache, nil
}

func limiterSpecs(cfg *config.Config) map[string]resilience.BucketSpec {
	return map[string]resilience.BucketSpec{
		"search": {Rps: cfg.RateLimit.Search.Rps, Burst: cfg.RateLimit.Search.Burst},
		"fetch":  {Rps: cfg.RateLimit.Fetch.Rps, Burst: cfg.RateLimit.Fetch.Burst},
	}
}

func resilienceConfig(cfg *config.Config, tz *time.Location, clk resilience.Clock, logger *slog.Logger) resilience.Config {
	// Retry/Client 各只有一个 MaxAttempts/PerAttempt，而配置按工具分开：
	// 取两者的 max。search 的总预算（67s）会先于 45s 的单次上限生效，
	// 因此取 max 不会让 search 超出自己的 total_timeout。
	maxAttempts := max(cfg.Timeout.Search.MaxAttempts, cfg.Timeout.Fetch.MaxAttempts)
	threshold := cfg.Circuit.FailThreshold
	if !cfg.Circuit.Enabled {
		threshold = 1 << 30 // 不可达阈值 = 关闭；Resilience 配置没有单独的 Enabled 字段
	}
	maxWindow, maxDay := 0, 0
	if cfg.Limits.Budget.Enabled {
		maxWindow, maxDay = cfg.Limits.Budget.MaxCallsPerWindow, cfg.Limits.Budget.MaxCallsPerDay
	}
	return resilience.Config{
		RetryEnabled:          cfg.Retry.Enabled,
		MaxAttempts:           maxAttempts,
		JitterMode:            cfg.Retry.JitterMode,
		FixedMs:               cfg.Retry.FixedMs,
		SpreadMs:              cfg.Retry.SpreadMs,
		RetryBudgetEnabled:    cfg.Retry.Budget.Enabled,
		RetryBudgetRatio:      cfg.Retry.Budget.Ratio,
		RetryBudgetWindowS:    cfg.Retry.Budget.WindowS,
		MaxCallsPerWindow:     maxWindow,
		WindowS:               cfg.Limits.Budget.WindowS,
		MaxCallsPerDay:        maxDay,
		MaxTotalChars:         cfg.Limits.MaxTotalChars,
		TZ:                    tz,
		CircuitFailThreshold:  threshold,
		CircuitOpenMs:         cfg.Circuit.OpenMs,
		CircuitHalfOpenProbes: cfg.Circuit.HalfOpenProbes,
		CacheDbPath:           cfg.Cache.DbPath,
		CacheMaxEntries:       cfg.Cache.MaxEntries,
		CacheMaxBytesMB:       cfg.Cache.MaxBytesMb,
		CacheMaxEntryKB:       cfg.Cache.MaxEntryKb,
		CachePruneIntervalS:   cfg.Cache.PruneIntervalS,
		Clock:                 clk,
		Logger:                logger,
		TotalTimeoutSearchMs:  cfg.Timeout.Search.TotalTimeoutMs,
		TotalTimeoutFetchMs:   cfg.Timeout.Fetch.TotalTimeoutMs,
	}
}

func deepseekConfig(cfg *config.Config, build domain.BuildInfo) deepseek.Config {
	perAttempt := max(cfg.Timeout.Search.PerAttemptMs, cfg.Timeout.Fetch.PerAttemptMs)
	return deepseek.Config{
		BaseUrl:          cfg.Upstream.BaseURL,
		ApiKey:           cfg.ApiKey,
		UserAgent:        cfg.Upstream.UserAgent,
		PerAttempt:       time.Duration(perAttempt) * time.Millisecond,
		Dial:             time.Duration(cfg.Timeout.DialMs) * time.Millisecond,
		TLSHandshake:     time.Duration(cfg.Timeout.TLSHandshakeMs) * time.Millisecond,
		ResponseHeader:   time.Duration(cfg.Timeout.ResponseHeaderMs) * time.Millisecond,
		MaxResponseBytes: cfg.Upstream.MaxResponseBytes,
		BuildInfo:        build,
	}
}

func openCache(cfg *config.Config, rc *resilience.Config, logger *slog.Logger) (resilience.Cache, error) {
	if !cfg.Cache.Enabled {
		return nil, nil
	}
	cache, err := resilience.NewSqliteCache(*rc)
	if err == nil {
		return cache, nil
	}
	if !cfg.Cache.DegradedOk {
		return nil, fmt.Errorf("缓存打开失败: %w", err)
	}
	logger.Warn(domain.EvWarnCacheDegraded, "error", err)
	return nil, nil
}

func newMCPServer(build domain.BuildInfo, logger *slog.Logger) *mcp.Server {
	return mcp.NewServer(
		&mcp.Implementation{Name: "DeepSeekSearch", Version: build.Version},
		&mcp.ServerOptions{
			Logger: logger,
			// SetCacheable 是服务器级单回调，必须按 URI 分派，MUST NOT 返回同一组值。
			SetCacheable: func(_ context.Context, r mcp.Request, c *mcp.Cacheable) {
				ttl, scope := tools.CacheableForRequest(r)
				c.TTLMs, c.CacheScope = int(ttl), scope
			},
		},
	)
}
