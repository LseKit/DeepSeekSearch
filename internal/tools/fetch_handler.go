// 文件名: fetch_handler.go
// 用途: ds_fetch 的完整编排：SSRF、熔断、限流、缓存碰撞复核、单飞、重试与快照。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
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
	defaultFetchTotalMs = 110000
	defaultFetchWaitMs  = 8000
	fetchToolName       = "ds_fetch"
)

type fetchHandler struct {
	deps     *Deps
	logger   *slog.Logger
	started  time.Time
	params   fetchParams
	rawURL   string
	cacheHit bool
}

// handleFetch 是 ds_fetch 的完整编排。
// 顺序：校验/SSRF → 熔断 → 限流 → 快照/缓存 → 单飞 → 上游 → 反转义 → 分节 → 预算 → 包裹。
func handleFetch(ctx context.Context, deps *Deps, req *mcp.CallToolRequest) *mcp.CallToolResult {
	if deps.Clock == nil {
		deps.Clock = resilience.SystemClock()
	}
	h := &fetchHandler{deps: deps, started: deps.Clock.Now()}
	h.logger = deps.Logger.With("call_id", newCallID(), "tool", fetchToolName)

	in, parseErr := parseFetchRequest(req)
	if in != nil {
		h.rawURL = derefString(in.URL)
	}
	h.logBegin(in)
	if parseErr != nil {
		return h.finish(errorResult(parseErr, fetchToolName), false, parseErr)
	}
	params, err := validateFetch(in, &deps.Cfg)
	if err != nil {
		if domain.CodeOf(err) == domain.CodeValSSRFBlocked {
			h.logSSRFBlocked(err)
		}
		return h.finish(errorResult(err, fetchToolName), false, err)
	}
	h.params = params
	h.rawURL = params.URL

	totalCtx, cancel := context.WithTimeout(ctx, h.totalTimeout())
	defer cancel()

	if deps.Circuit != nil {
		if allowErr := deps.Circuit.Allow("fetch"); allowErr != nil {
			return h.finish(errorResult(allowErr, fetchToolName), false, allowErr)
		}
	}
	if deps.Limiter != nil {
		if waitErr := deps.Limiter.Wait(totalCtx, "fetch"); waitErr != nil {
			mapped := h.mapInterrupted(ctx, totalCtx, waitErr)
			return h.finish(errorResult(mapped, fetchToolName), false, mapped)
		}
		defer deps.Limiter.Release("fetch")
	}

	if params.CacheID != "" {
		entry, lookupErr := lookupSnapshot(totalCtx, deps.Cache, params.CacheID)
		if lookupErr != nil {
			mapped := h.mapSnapshotError(params.CacheID, lookupErr)
			return h.finish(errorResult(mapped, fetchToolName), false, mapped)
		}
		return h.renderAndFinish(&entry, true)
	}

	normalized, normErr := domain.NormalizeUrl(params.URL, deps.Cfg.Dedupe.TrackingParams)
	if normErr != nil {
		normalized = params.URL
	}
	entryKey := fetchEntryKey(normalized)
	useSingleflight := true
	if h.cacheTTL() > 0 {
		entry, hit, collision, cacheErr := h.loadURLCache(totalCtx, entryKey, normalized)
		switch {
		case cacheErr != nil:
			h.logCacheDegraded("get", entryKey, cacheErr)
			useSingleflight = false
		case collision:
			useSingleflight = false
		case hit:
			return h.renderAndFinish(&entry, true)
		}
	}

	entry, shared, fetchErr := h.fetchEntry(totalCtx, entryKey, normalized, useSingleflight)
	if fetchErr != nil {
		mapped := h.mapInterrupted(ctx, totalCtx, fetchErr)
		return h.finish(errorResult(mapped, fetchToolName), false, mapped)
	}
	return h.renderAndFinish(&entry, shared)
}

func (h *fetchHandler) fetchEntry(totalCtx context.Context, entryKey, normalized string, useSingleflight bool) (cachedFetchEntry, bool, error) {
	rawURL := h.params.URL
	fn := func(ctx context.Context) ([]byte, error) {
		resp, err := h.upstream(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		now := h.deps.Clock.Now()
		entry := cachedFetchEntry{
			CacheID:    snapshotID(rawURL, now),
			URL:        normalized,
			URLSHA256:  sha256Hex(normalized),
			CapturedAt: now.Unix(),
			Title:      resp.Title,
			Content:    resp.Content,
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("marshal fetch entry: %w", err)
		}
		return raw, nil
	}
	commit := h.commitFunc(entryKey)
	var (
		raw    []byte
		shared bool
		err    error
	)
	if useSingleflight && h.deps.Singleflight != nil {
		raw, shared, err = h.deps.Singleflight.Do(totalCtx, entryKey, h.waitMs(), fn, commit)
	} else {
		raw, err = fn(totalCtx)
		if err == nil && commit != nil {
			if commitErr := commit(totalCtx, raw); commitErr != nil {
				h.logCacheCommitFailed(entryKey, commitErr)
			}
		}
	}
	if err != nil {
		return cachedFetchEntry{}, false, err
	}
	var entry cachedFetchEntry
	if unmarshalErr := json.Unmarshal(raw, &entry); unmarshalErr != nil {
		return cachedFetchEntry{}, false, fmt.Errorf("unmarshal fetch entry: %w", unmarshalErr)
	}
	return entry, shared, nil
}

func (h *fetchHandler) upstream(ctx context.Context, rawURL string) (domain.FetchResponse, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return domain.FetchResponse{}, h.totalTimeoutError()
		}
		h.logger.Info("call.upstream", "attempt", attempt, "url", h.redactedURL())
		attemptStart := h.deps.Clock.Now()
		resp, err := h.deps.Client.Fetch(domain.ContextWithAttempt(ctx, attempt), domain.FetchRequest{Url: rawURL})
		ms := h.deps.Clock.Now().Sub(attemptStart).Milliseconds()
		args := []any{"attempt", attempt, "ms", ms, "ok", err == nil, "url", h.redactedURL()}
		if err != nil {
			args = append(args, "code", domain.CodeOf(err))
		}
		h.logger.Info("call.upstream.done", args...)
		if err == nil {
			h.recordCircuit(nil)
			return resp, nil
		}
		// 同 search：ctx 是 fn 实际收到的 context，避免与上层 totalCtx 的 timer 竞争
		// 把总预算到期误判成单次尝试超时（2998）。
		if ctx.Err() != nil {
			h.recordCircuit(err)
			return domain.FetchResponse{}, h.totalTimeoutError()
		}
		lastErr = err
		retry, reason := h.shouldRetry(err, attempt)
		if reason == resilience.ReasonBudgetExhausted {
			h.recordCircuit(err)
			return domain.FetchResponse{}, fmt.Errorf("retry budget: %w",
				domain.NewLocalError(domain.CodeLocRetryBudget, "重试预算已耗尽", nil))
		}
		if !retry {
			h.recordCircuit(err)
			return domain.FetchResponse{}, lastErr
		}
		if !h.waitRetry(ctx, h.retryDelay(attempt)) {
			h.recordCircuit(err)
			return domain.FetchResponse{}, h.totalTimeoutError()
		}
	}
}

func (h *fetchHandler) shouldRetry(err error, attempt int) (bool, resilience.Reason) {
	if h.deps.Retry == nil {
		return false, resilience.ReasonDisabled
	}
	return h.deps.Retry.Should(err, attempt)
}

func (h *fetchHandler) retryDelay(attempt int) time.Duration {
	if h.deps.Retry == nil {
		return 0
	}
	return h.deps.Retry.Delay(attempt)
}

func (h *fetchHandler) waitRetry(ctx context.Context, delay time.Duration) bool {
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

func (h *fetchHandler) recordCircuit(err error) {
	if h.deps.Circuit != nil {
		h.deps.Circuit.Record("fetch", err)
	}
}

func (h *fetchHandler) loadURLCache(ctx context.Context, entryKey, normalized string) (cached cachedFetchEntry, hit, collision bool, err error) {
	raw, hit, err := h.deps.Cache.Get(ctx, entryKey)
	if err != nil {
		return cachedFetchEntry{}, false, false, fmt.Errorf("read fetch cache: %w", err)
	}
	if !hit {
		return cachedFetchEntry{}, false, false, nil
	}
	var decoded cachedFetchEntry
	if unmarshalErr := json.Unmarshal(raw, &decoded); unmarshalErr != nil {
		return cachedFetchEntry{}, false, true, nil //nolint:nilerr // 损坏条目按碰撞处理，绕过缓存重新抓取
	}
	if decoded.CacheID == "" {
		return cachedFetchEntry{}, false, true, nil
	}
	expected := sha256Hex(normalized)
	if decoded.URLSHA256 != expected {
		h.deps.Logger.Warn("warn.cache_key_collision",
			"key_hash", keyHash(entryKey), "url_sha256", expected)
		return cachedFetchEntry{}, false, true, nil
	}
	return decoded, true, false, nil
}

func (h *fetchHandler) commitFunc(entryKey string) func(context.Context, []byte) error {
	ttl := h.cacheTTL()
	if ttl <= 0 || h.deps.Cache == nil {
		return nil
	}
	return func(ctx context.Context, raw []byte) error {
		if err := h.deps.Cache.Put(ctx, entryKey, raw, ttl); err != nil {
			return fmt.Errorf("put fetch entry: %w", err)
		}
		var entry cachedFetchEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("unmarshal fetch entry for index: %w", err)
		}
		indexRaw, err := json.Marshal(snapshotIndex{EntryKey: entryKey, CacheID: entry.CacheID})
		if err != nil {
			return fmt.Errorf("marshal snapshot index: %w", err)
		}
		if err := h.deps.Cache.Put(ctx, snapshotIndexKey(entry.CacheID), indexRaw, ttl); err != nil {
			return fmt.Errorf("put snapshot index: %w", err)
		}
		return nil
	}
}

func (h *fetchHandler) renderAndFinish(entry *cachedFetchEntry, fromCache bool) *mcp.CallToolResult {
	h.cacheHit = fromCache
	res, mode, err := h.render(entry, fromCache)
	if err != nil {
		return h.finish(errorResult(err, fetchToolName), false, err)
	}
	billed := !fromCache && entry.Content != ""
	return h.finish(res, billed, nil, "mode", mode, "from_cache", fromCache)
}

func (h *fetchHandler) logBegin(in *fetchInput) {
	args := []any{}
	switch {
	case h.rawURL != "":
		args = append(args, "url", h.redactedURL())
	case in != nil && derefString(in.CacheID) != "":
		args = append(args, "cache_id", derefString(in.CacheID))
	}
	h.logger.Info("call.begin", args...)
}

func (h *fetchHandler) logSSRFBlocked(err error) {
	payload := domain.ToPayload(err, fetchToolName)
	var host string
	if value, ok := payload.Details["host"].(string); ok {
		host = value
	}
	h.logger.Warn("ssrf.blocked",
		"url", h.redactedURL(), "host", host,
		"matched_prefix", payload.Details["matched_prefix"])
}

func (h *fetchHandler) logCacheDegraded(stage, key string, err error) {
	h.deps.Logger.Warn("warn.cache_degraded",
		"stage", stage, "key_hash", keyHash(key), "err", err.Error())
}

func (h *fetchHandler) logCacheCommitFailed(key string, err error) {
	h.deps.Logger.Warn(evtCacheCommitFailedName,
		"key_hash", keyHash(key), "err", err.Error())
}

func (h *fetchHandler) mapSnapshotError(id string, err error) error {
	if domain.CodeOf(err) == domain.CodeLocSnapshotGone {
		return err
	}
	return fmt.Errorf("snapshot cache: %w",
		domain.NewLocalError(domain.CodeLocCacheDegraded, "缓存故障，无法读取快照",
			map[string]any{"cache_id": id}))
}

func (h *fetchHandler) mapInterrupted(parentCtx, totalCtx context.Context, err error) error {
	if totalCtx.Err() != nil || parentCtx.Err() != nil {
		return h.totalTimeoutError()
	}
	return err
}

func (h *fetchHandler) totalTimeout() time.Duration {
	ms := h.deps.Cfg.Timeout.Fetch.TotalTimeoutMs
	if ms <= 0 {
		ms = defaultFetchTotalMs
	}
	return time.Duration(ms) * time.Millisecond
}

func (h *fetchHandler) totalTimeoutError() error {
	elapsed := h.deps.Clock.Now().Sub(h.started).Milliseconds()
	return fmt.Errorf("total timeout: %w",
		domain.NewLocalError(domain.CodeLocTotalTimeout, "整次调用超过总时长上限",
			map[string]any{"elapsed_ms": elapsed}))
}

func (h *fetchHandler) finish(res *mcp.CallToolResult, billed bool, err error, extra ...any) *mcp.CallToolResult {
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
		h.deps.Usage.Record(fetchToolName, billed, h.cacheHit, err)
	}
	if slow := h.deps.Cfg.Log.SlowMs; slow > 0 && ms > int64(slow) {
		h.logger.Warn("warn.slow", "ms", ms)
	}
	return res
}

func (h *fetchHandler) cacheTTL() time.Duration {
	if h.deps.Cache == nil || !h.deps.Cfg.Cache.Enabled || h.deps.Cfg.Cache.TtlFetchS <= 0 {
		return 0
	}
	return time.Duration(h.deps.Cfg.Cache.TtlFetchS) * time.Second
}

func (h *fetchHandler) waitMs() int {
	if h.deps.Cfg.Cache.WaitMs > 0 {
		return h.deps.Cfg.Cache.WaitMs
	}
	return defaultFetchWaitMs
}

func (h *fetchHandler) redactedURL() string {
	return domain.RedactURL(h.rawURL, h.deps.Cfg.Log.RedactQueryKeys)
}

func fetchEntryKey(normalized string) string {
	return "fetch:" + fmt.Sprintf("%016x", domain.HashUrl(normalized))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func parseFetchRequest(req *mcp.CallToolRequest) (*fetchInput, error) {
	if req == nil || req.Params == nil {
		return nil, invalid("arguments", "缺少请求参数")
	}
	var in fetchInput
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return nil, invalid("arguments", "无法解析 JSON 参数")
		}
	}
	return &in, nil
}
