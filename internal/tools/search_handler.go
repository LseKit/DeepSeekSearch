// 文件名: search_handler.go
// 用途: ds_search 的完整编排：熔断、限流、缓存、单飞、重试、渲染与错误体。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

const (
	defaultSearchTotalMs = 67000
	defaultSearchWaitMs  = 8000
	searchToolName       = "ds_search"
)

type searchHandler struct {
	deps     *Deps
	logger   *slog.Logger
	started  time.Time
	params   searchParams
	cacheHit bool
}

// handleSearch 是 ds_search 的完整编排。
// 顺序不能改：校验在任何 IO 之前；限流在缓存查询之前（缓存命中也过限流）。
func handleSearch(ctx context.Context, deps *Deps, req *mcp.CallToolRequest) *mcp.CallToolResult {
	if deps.Clock == nil {
		deps.Clock = resilience.SystemClock()
	}
	h := &searchHandler{deps: deps, started: deps.Clock.Now()}
	h.logger = deps.Logger.With("call_id", newCallID(), "tool", searchToolName)
	h.logger.Info("call.begin")

	in, err := parseSearchRequest(req)
	if err != nil {
		return h.finish(errorResult(err, searchToolName), false, err)
	}
	params, err := validateSearch(in, &deps.Cfg)
	if err != nil {
		return h.finish(errorResult(err, searchToolName), false, err)
	}
	h.params = params

	totalCtx, cancel := context.WithTimeout(ctx, h.totalTimeout())
	defer cancel()

	if deps.Circuit != nil {
		if allowErr := deps.Circuit.Allow("search"); allowErr != nil {
			return h.finish(errorResult(allowErr, searchToolName), false, allowErr)
		}
	}
	if deps.Limiter != nil {
		if waitErr := deps.Limiter.Wait(totalCtx, "search"); waitErr != nil {
			mapped := h.mapInterrupted(ctx, totalCtx, waitErr)
			return h.finish(errorResult(mapped, searchToolName), false, mapped)
		}
		defer deps.Limiter.Release("search")
	}

	key := searchCacheKey(&params)
	ttl := h.cacheTTL()
	if ttl > 0 {
		raw, hit, getErr := deps.Cache.Get(totalCtx, key)
		if getErr != nil {
			h.logCacheDegraded("get", key, getErr)
		}
		if getErr == nil && hit {
			env, ok := decodeSearchEnvelope(raw)
			if ok {
				return h.renderAndFinish(env, true)
			}
			h.logCacheDegraded("decode", key, fmt.Errorf("缓存内容不是合法的搜索结果信封"))
			return h.runUpstream(totalCtx, ctx, key, ttl, false)
		}
	}
	return h.runUpstream(totalCtx, ctx, key, ttl, true)
}

func (h *searchHandler) runUpstream(totalCtx, parentCtx context.Context, key string, ttl time.Duration, useSingleflight bool) *mcp.CallToolResult {
	upstreamReq := domain.SearchRequest{
		Query:        h.params.EffectiveQuery,
		Count:        &h.params.Count,
		NeedContent:  &h.params.NeedContent,
		OriQuestion:  h.params.OriQuestion,
		Localization: h.params.Localization,
	}
	fn := func(ctx context.Context) ([]byte, error) {
		resp, err := h.upstream(ctx, &upstreamReq)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(cachedSearchEnvelope{
			CachedAt: h.deps.Clock.Now().Unix(),
			Response: resp,
		})
		if err != nil {
			return nil, domain.NewLocalError(domain.CodeLocInternal, "序列化缓存信封失败", nil)
		}
		return raw, nil
	}
	commit := h.commitFunc(key, ttl)
	var (
		raw     []byte
		shared  bool
		callErr error
	)
	if useSingleflight && h.deps.Singleflight != nil {
		raw, shared, callErr = h.deps.Singleflight.Do(totalCtx, key, h.waitMs(), fn, commit)
	} else {
		raw, callErr = fn(totalCtx)
		if callErr == nil && commit != nil {
			if commitErr := commit(totalCtx, raw); commitErr != nil {
				h.logCacheCommitFailed(key, commitErr)
			}
		}
	}
	if callErr != nil {
		mapped := h.mapInterrupted(parentCtx, totalCtx, callErr)
		return h.finish(errorResult(mapped, searchToolName), false, mapped)
	}
	env, ok := decodeSearchEnvelope(raw)
	if !ok {
		bad := domain.NewLocalError(domain.CodeLocInternal, "缓存内容无法解析", nil)
		return h.finish(errorResult(bad, searchToolName), false, bad)
	}
	return h.renderAndFinish(env, shared)
}

func (h *searchHandler) renderAndFinish(env cachedSearchEnvelope, fromCache bool) *mcp.CallToolResult {
	h.cacheHit = fromCache
	res, total, results := h.render(env, fromCache)
	billed := !fromCache && len(env.Response.Results) > 0
	return h.finish(res, billed, nil,
		"total", total, "results", results, "from_cache", fromCache)
}

func (h *searchHandler) upstream(ctx context.Context, req *domain.SearchRequest) (domain.SearchResponse, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return domain.SearchResponse{}, h.totalTimeoutError()
		}
		h.logger.Info("call.upstream", "attempt", attempt)
		attemptStart := h.deps.Clock.Now()
		resp, err := h.deps.Client.Search(domain.ContextWithAttempt(ctx, attempt), *req)
		ms := h.deps.Clock.Now().Sub(attemptStart).Milliseconds()
		args := []any{"attempt", attempt, "ms", ms, "ok", err == nil}
		if err != nil {
			args = append(args, "code", domain.CodeOf(err))
		}
		h.logger.Info("call.upstream.done", args...)
		if err == nil {
			h.recordCircuit(nil)
			return resp, nil
		}
		lastErr = err
		retry, reason := h.shouldRetry(err, attempt)
		if reason == resilience.ReasonBudgetExhausted {
			h.recordCircuit(err)
			return domain.SearchResponse{}, fmt.Errorf("retry budget: %w",
				domain.NewLocalError(domain.CodeLocRetryBudget, "重试预算已耗尽", nil))
		}
		if !retry {
			h.recordCircuit(err)
			return domain.SearchResponse{}, lastErr
		}
		if !h.waitRetry(ctx, h.retryDelay(attempt)) {
			h.recordCircuit(err)
			return domain.SearchResponse{}, h.totalTimeoutError()
		}
	}
}

func (h *searchHandler) shouldRetry(err error, attempt int) (bool, resilience.Reason) {
	if h.deps.Retry == nil {
		return false, resilience.ReasonDisabled
	}
	return h.deps.Retry.Should(err, attempt)
}

func (h *searchHandler) retryDelay(attempt int) time.Duration {
	if h.deps.Retry == nil {
		return 0
	}
	return h.deps.Retry.Delay(attempt)
}

func (h *searchHandler) waitRetry(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-h.deps.Clock.After(delay):
		return true
	}
}

func (h *searchHandler) recordCircuit(err error) {
	if h.deps.Circuit != nil {
		h.deps.Circuit.Record("search", err)
	}
}

func (h *searchHandler) totalTimeout() time.Duration {
	ms := h.deps.Cfg.Timeout.Search.TotalTimeoutMs
	if ms <= 0 {
		ms = defaultSearchTotalMs
	}
	return time.Duration(ms) * time.Millisecond
}

func (h *searchHandler) totalTimeoutError() error {
	elapsed := h.deps.Clock.Now().Sub(h.started).Milliseconds()
	return fmt.Errorf("total timeout: %w",
		domain.NewLocalError(domain.CodeLocTotalTimeout, "整次调用超过总时长上限",
			map[string]any{"elapsed_ms": elapsed}))
}

func (h *searchHandler) mapInterrupted(parentCtx, totalCtx context.Context, err error) error {
	if totalCtx.Err() != nil || parentCtx.Err() != nil {
		return h.totalTimeoutError()
	}
	return err
}

func (h *searchHandler) finish(res *mcp.CallToolResult, billed bool, err error, extra ...any) *mcp.CallToolResult {
	ms := h.deps.Clock.Now().Sub(h.started).Milliseconds()
	args := make([]any, 0, 6+len(extra))
	args = append(args, "ms", ms, "billed", billed)
	if err != nil {
		code := domain.CodeOf(err)
		args = append(args, "code", code)
		if info, ok := domain.CodeRegistry[code]; ok {
			args = append(args, "code_name", info.Name)
		}
	}
	args = append(args, extra...)
	h.logger.Info("call.end", args...)
	if h.deps.Usage != nil {
		h.deps.Usage.Record(searchToolName, billed, h.cacheHit, err)
	}
	if slow := h.deps.Cfg.Log.SlowMs; slow > 0 && ms > int64(slow) {
		h.logger.Warn("warn.slow", "ms", ms)
	}
	return res
}

func (h *searchHandler) cacheTTL() time.Duration {
	if h.deps.Cache == nil || !h.deps.Cfg.Cache.Enabled || h.deps.Cfg.Cache.TtlSearchS <= 0 {
		return 0
	}
	return time.Duration(h.deps.Cfg.Cache.TtlSearchS) * time.Second
}

func (h *searchHandler) waitMs() int {
	if h.deps.Cfg.Cache.WaitMs > 0 {
		return h.deps.Cfg.Cache.WaitMs
	}
	return defaultSearchWaitMs
}

func (h *searchHandler) commitFunc(key string, ttl time.Duration) func(context.Context, []byte) error {
	if ttl <= 0 || h.deps.Cache == nil {
		return nil
	}
	return func(ctx context.Context, val []byte) error {
		return h.deps.Cache.Put(ctx, key, val, ttl)
	}
}

func (h *searchHandler) logCacheDegraded(stage, key string, err error) {
	h.deps.Logger.Warn("warn.cache_degraded",
		"stage", stage, "key_hash", keyHash(key), "err", err.Error())
}

func (h *searchHandler) logCacheCommitFailed(key string, err error) {
	h.deps.Logger.Warn(evtCacheCommitFailedName,
		"key_hash", keyHash(key), "err", err.Error())
}

const evtCacheCommitFailedName = "warn.cache_commit_failed"

type searchCacheFields struct {
	Query        string  `json:"query"`
	Count        int     `json:"count"`
	NeedContent  bool    `json:"need_content"`
	OriQuestion  *string `json:"ori_question,omitempty"`
	Localization string  `json:"localization,omitempty"`
}

func searchCacheKey(params *searchParams) string {
	raw, err := json.Marshal(searchCacheFields{
		Query:        params.EffectiveQuery,
		Count:        params.Count,
		NeedContent:  params.NeedContent,
		OriQuestion:  params.OriQuestion,
		Localization: params.Localization,
	})
	if err != nil {
		return "search:marshal-error"
	}
	sum := sha256.Sum256(raw)
	return "search:" + hex.EncodeToString(sum[:])
}

func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:6])
}

func newCallID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

func parseSearchRequest(req *mcp.CallToolRequest) (*searchInput, error) {
	if req == nil || req.Params == nil {
		return nil, invalid("arguments", "缺少请求参数")
	}
	var in searchInput
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return nil, invalid("arguments", "无法解析 JSON 参数")
		}
	}
	return &in, nil
}
