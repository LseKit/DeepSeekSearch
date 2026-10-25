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
	"github.com/LseKit/DeepSeekSearch/internal/health"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
	"github.com/LseKit/DeepSeekSearch/internal/resilience/budget"
	"github.com/LseKit/DeepSeekSearch/internal/resilience/cache"
	"github.com/LseKit/DeepSeekSearch/internal/resilience/circuit"
	"github.com/LseKit/DeepSeekSearch/internal/resilience/ratelimit"
	"github.com/LseKit/DeepSeekSearch/internal/resilience/retry"
	"github.com/LseKit/DeepSeekSearch/internal/tools"
)

func buildDeps(cfg *config.Config, tz *time.Location, clk resilience.Clock, logger *slog.Logger,
	build domain.BuildInfo, newClient clientFactory,
) (tools.Deps, func() error, error) {
	rc := resilienceConfig(cfg, tz, clk, logger)
	cacheStore, err := openCache(cfg, &rc, logger)
	if err != nil {
		return tools.Deps{}, nil, err
	}
	closeCache := func() error {
		if cacheStore == nil {
			return nil
		}
		return cacheStore.Close()
	}

	limiter, err := ratelimit.NewLimiter(limiterSpecs(cfg), cfg.RateLimit.MaxInflight,
		cfg.RateLimit.SearchShare, time.Duration(cfg.RateLimit.MaxWaitMs)*time.Millisecond, clk)
	if err != nil {
		if closeErr := closeCache(); closeErr != nil {
			logger.Error(domain.EvErr, "stage", "close_cache_after_limiter_error", "error", closeErr)
		}
		return tools.Deps{}, nil, fmt.Errorf("构造限流器: %w", err)
	}

	searchRC := rc
	searchRC.MaxAttempts = cfg.Timeout.Search.MaxAttempts
	fetchRC := rc
	fetchRC.MaxAttempts = cfg.Timeout.Fetch.MaxAttempts
	searchRetry := retry.NewRetry(searchRC, clk, nil)
	fetchRetry := retry.NewRetry(fetchRC, clk, nil)
	searchClient := newClient(deepseekConfig(cfg, build, cfg.Timeout.Search.PerAttemptMs))
	fetchClient := newClient(deepseekConfig(cfg, build, cfg.Timeout.Fetch.PerAttemptMs))

	deps := tools.Deps{
		Cfg:          *cfg,
		Client:       searchClient,
		SearchClient: searchClient,
		FetchClient:  fetchClient,
		Limiter:      limiter,
		Retry:        searchRetry,
		SearchRetry:  searchRetry,
		FetchRetry:   fetchRetry,
		Budget:       budget.NewBudget(rc, clk),
		Cache:        cacheStore,
		Singleflight: cache.NewSingleflight(cacheStore, clk),
		Circuit:      circuit.NewCircuit(rc, clk),
		Clock:        clk,
		StartedAt:    clk.Now(),
		Usage:        health.NewUsageTracker(clk),
		Build:        build,
		Logger:       logger,
	}
	return deps, closeCache, nil
}

func limiterSpecs(cfg *config.Config) map[string]ratelimit.BucketSpec {
	return map[string]ratelimit.BucketSpec{
		"search": {Rps: cfg.RateLimit.Search.Rps, Burst: cfg.RateLimit.Search.Burst},
		"fetch":  {Rps: cfg.RateLimit.Fetch.Rps, Burst: cfg.RateLimit.Fetch.Burst},
	}
}

func resilienceConfig(cfg *config.Config, tz *time.Location, clk resilience.Clock, logger *slog.Logger) resilience.Config {
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
		MaxAttempts:           cfg.Timeout.Search.MaxAttempts,
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

func deepseekConfig(cfg *config.Config, build domain.BuildInfo, perAttemptMs int) deepseek.Config {
	return deepseek.Config{
		BaseUrl:          cfg.Upstream.BaseURL,
		ApiKey:           cfg.ApiKey,
		UserAgent:        cfg.Upstream.UserAgent,
		PerAttempt:       time.Duration(perAttemptMs) * time.Millisecond,
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
	cacheStore, err := cache.NewSqliteCache(*rc)
	if err == nil {
		return cacheStore, nil
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
