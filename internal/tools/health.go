// 文件名: health.go
// 用途: ds_health 本地诊断工具：8 项检查、build/limits 段与可选的不计费探活。
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

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

const healthDescription = `本地诊断工具。默认零网络、零计费；只有 probe_upstream=true 时用 GET 探活（405，不计费）。
遇到「搜不到结果 / 调用全部失败」时先调本工具自诊断，不要盲目重试。
status=ok 表示 8 项检查全部通过；degraded 表示至少一项异常，看 checks[].{name,ok,detail} 定位。`

const healthInputSchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "probe_upstream": {
      "type": "boolean", "default": false,
      "description": "是否探测上游连通性。只发 GET /v1/tools/search 并期望 405，不计费；默认 false。"
    }
  }
}`

type healthTool struct{}

// Name 返回 MCP 工具名。
func (t *healthTool) Name() string { return "ds_health" }

// Register 把 ds_health 的 schema、annotations 与 handler 注册到 server。
func (t *healthTool) Register(srv *mcp.Server, deps Deps) { //nolint:gocritic // 签名由 11 号 §五 固定
	srv.AddTool(healthToolDefinition(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleHealth(ctx, &deps, req), nil
	})
}

func healthToolDefinition() *mcp.Tool {
	return &mcp.Tool{
		Name:        "ds_health",
		Title:       "DeepSeek 健康诊断",
		Description: healthDescription,
		InputSchema: json.RawMessage(healthInputSchemaJSON),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}
}

type healthInput struct {
	ProbeUpstream *bool `json:"probe_upstream"`
}

type healthCheck struct {
	Name   string `json:"name"`
	Ok     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type healthOutput struct {
	Status string        `json:"status"`
	Checks []healthCheck `json:"checks"`
	Build  buildSummary  `json:"build"`
	Limits limitsSummary `json:"limits"`
}

type buildSummary struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	UptimeS   int64  `json:"uptime_s"`
}

type limitsSummary struct {
	SearchRPS   float64 `json:"search_rps"`
	FetchRPS    float64 `json:"fetch_rps"`
	MaxInflight int     `json:"max_inflight"`
	TTLSearchS  int     `json:"ttl_search_s"`
	TTLFetchS   int     `json:"ttl_fetch_s"`
}

type healthHandler struct {
	deps    *Deps
	logger  *slog.Logger
	started time.Time
}

// handleHealth 只做本地检查；唯一网络动作是 probe_upstream=true 时的 Client.Ping（405，不计费）。
func handleHealth(ctx context.Context, deps *Deps, req *mcp.CallToolRequest) *mcp.CallToolResult {
	if deps.Clock == nil {
		deps.Clock = resilience.SystemClock()
	}
	h := &healthHandler{deps: deps, started: deps.Clock.Now()}
	h.logger = deps.Logger.With("call_id", newCallID(), "tool", "ds_health")
	h.logger.Info("call.begin")

	in, err := parseHealthRequest(req)
	if err != nil {
		return h.finish(errorResult(err, "ds_health"), err)
	}
	checks := []healthCheck{
		h.checkAPIKey(),
		h.checkCacheWritable(),
		h.checkCacheStats(),
		h.checkClock(),
		h.checkLimiter(),
		h.checkCircuit(),
		h.checkEffectiveConfig(),
		h.checkUpstream(ctx, in.ProbeUpstream != nil && *in.ProbeUpstream),
	}
	return h.finish(h.render(checks), nil)
}

func (h *healthHandler) render(checks []healthCheck) *mcp.CallToolResult {
	failed := 0
	for _, check := range checks {
		if !check.Ok {
			failed++
		}
	}
	status := "ok"
	if failed > 0 {
		status = "degraded"
	}
	out := healthOutput{
		Status: status,
		Checks: checks,
		Build: buildSummary{
			Version:   h.deps.Build.Version,
			Commit:    h.deps.Build.Commit,
			BuildTime: h.deps.Build.BuildTime,
			GoVersion: h.deps.Build.GoVersion,
			UptimeS:   h.uptimeSeconds(),
		},
		Limits: limitsSummary{
			SearchRPS:   h.deps.Cfg.RateLimit.Search.Rps,
			FetchRPS:    h.deps.Cfg.RateLimit.Fetch.Rps,
			MaxInflight: h.deps.Cfg.RateLimit.MaxInflight,
			TTLSearchS:  h.deps.Cfg.Cache.TtlSearchS,
			TTLFetchS:   h.deps.Cfg.Cache.TtlFetchS,
		},
	}
	raw, err := json.Marshal(out)
	if err != nil {
		bad := domain.NewLocalError(domain.CodeLocInternal, "序列化诊断结果失败", nil)
		return errorResult(bad, "ds_health")
	}
	// 只有 8 项全部失败才把工具本身标成错误；单项失败仍要让模型读到完整诊断。
	return &mcp.CallToolResult{
		IsError: failed == len(checks) && len(checks) > 0,
		Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}},
	}
}

func (h *healthHandler) finish(res *mcp.CallToolResult, err error) *mcp.CallToolResult {
	ms := h.deps.Clock.Now().Sub(h.started).Milliseconds()
	args := []any{"ms", ms, "billed", false}
	if err != nil {
		args = append(args, "code", domain.CodeOf(err))
	}
	h.logger.Info("call.end", args...)
	if h.deps.Usage != nil {
		h.deps.Usage.Record("ds_health", false, false, err)
	}
	if slow := h.deps.Cfg.Log.SlowMs; slow > 0 && ms > int64(slow) {
		h.logger.Warn("warn.slow", "ms", ms)
	}
	return res
}

func (h *healthHandler) checkAPIKey() healthCheck {
	if h.deps.Cfg.ApiKey == "" {
		return healthCheck{Name: "api_key_present", Ok: false, Detail: "DEEPSEEK_API_KEY 未配置"}
	}
	return healthCheck{Name: "api_key_present", Ok: true, Detail: "已配置（值不展示）"}
}

type writableCache interface {
	CheckWritable() error
}

func (h *healthHandler) checkCacheWritable() healthCheck {
	if !h.deps.Cfg.Cache.Enabled {
		return healthCheck{Name: "cache_writable", Ok: true, Detail: "缓存已关闭（[cache].enabled=false）"}
	}
	if h.deps.Cache == nil {
		return healthCheck{Name: "cache_writable", Ok: false, Detail: "缓存已启用但未初始化"}
	}
	if cache, ok := h.deps.Cache.(writableCache); ok {
		if err := cache.CheckWritable(); err != nil {
			return healthCheck{Name: "cache_writable", Ok: false, Detail: err.Error()}
		}
		return healthCheck{Name: "cache_writable", Ok: true, Detail: h.deps.Cfg.Cache.DbPath + " 可写"}
	}
	return healthCheck{Name: "cache_writable", Ok: true, Detail: "缓存实现不支持写探测"}
}

func (h *healthHandler) checkCacheStats() healthCheck {
	if !h.deps.Cfg.Cache.Enabled {
		return healthCheck{Name: "cache_stats", Ok: true, Detail: "缓存已关闭（合法配置，不是故障）"}
	}
	if h.deps.Cache == nil {
		return healthCheck{Name: "cache_stats", Ok: false, Detail: "缓存已启用但未初始化"}
	}
	entries, bytes, degraded := h.deps.Cache.Stats()
	detail := fmt.Sprintf("entries=%d/%d bytes=%.1fMB/%dMB ttl=%ds/%ds degraded=%v",
		entries, h.deps.Cfg.Cache.MaxEntries,
		float64(bytes)/(1024*1024), h.deps.Cfg.Cache.MaxBytesMb,
		h.deps.Cfg.Cache.TtlSearchS, h.deps.Cfg.Cache.TtlFetchS, degraded)
	return healthCheck{Name: "cache_stats", Ok: !degraded, Detail: detail}
}

func (h *healthHandler) checkClock() healthCheck {
	zone := h.deps.Cfg.Log.Timezone
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return healthCheck{Name: "clock_tz", Ok: false, Detail: "时区无法加载: " + zone}
	}
	now := h.deps.Clock.Now().In(loc)
	year := now.Year()
	ok := year >= 2025 && year <= 2100
	_, offset := now.Zone()
	return healthCheck{
		Name: "clock_tz",
		Ok:   ok,
		Detail: fmt.Sprintf("tz=%s now=%s offset=%+ds year_ok=%v",
			zone, now.Format(time.RFC3339), offset, ok),
	}
}

type limiterDiagnoser interface {
	Diagnostics(string) resilience.LimiterDiagnostics
}

func (h *healthHandler) checkLimiter() healthCheck {
	if h.deps.Limiter == nil {
		return healthCheck{Name: "limiter_state", Ok: true, Detail: "限流器未启用"}
	}
	maxWait := int64(h.deps.Cfg.RateLimit.MaxWaitMs)
	if maxWait <= 0 {
		maxWait = 30000
	}
	diagnoser, hasDiag := h.deps.Limiter.(limiterDiagnoser)
	detail := ""
	ok := true
	for _, key := range []string{"search", "fetch"} {
		if hasDiag {
			diag := diagnoser.Diagnostics(key)
			keyOK := diag.Waiters == 0 || diag.EstimatedWaitMs < maxWait
			ok = ok && keyOK
			detail += fmt.Sprintf("%s: inflight=%d/%d waiters=%d est=%dms stage=%s; ",
				key, diag.Inflight, diag.Quota, diag.Waiters, diag.EstimatedWaitMs, diag.Stage)
			continue
		}
		depth := h.deps.Limiter.Depth(key)
		ok = ok && depth == 0
		detail += fmt.Sprintf("%s: waiters=%d (诊断接口不可用); ", key, depth)
	}
	return healthCheck{Name: "limiter_state", Ok: ok, Detail: detail}
}

type circuitDiagnoser interface {
	Diagnostics(string) resilience.CircuitDiagnostics
}

func (h *healthHandler) checkCircuit() healthCheck {
	if h.deps.Circuit == nil {
		return healthCheck{Name: "circuit_state", Ok: true, Detail: "熔断器未启用"}
	}
	diagnoser, hasDiag := h.deps.Circuit.(circuitDiagnoser)
	detail := ""
	ok := true
	for _, key := range []string{"search", "fetch"} {
		if hasDiag {
			diag := diagnoser.Diagnostics(key)
			keyOK := diag.State != "open"
			ok = ok && keyOK
			detail += fmt.Sprintf("%s: state=%s failures=%d/%d retry_after_ms=%d; ",
				key, diag.State, diag.Failures, diag.Threshold, diag.RetryAfterMs)
			continue
		}
		state := h.deps.Circuit.State(key)
		ok = ok && state != "open"
		detail += fmt.Sprintf("%s: state=%s fail_threshold=%d; ", key, state, h.deps.Cfg.Circuit.FailThreshold)
	}
	return healthCheck{Name: "circuit_state", Ok: ok, Detail: detail}
}

func (h *healthHandler) checkEffectiveConfig() healthCheck {
	redacted := h.deps.Cfg.Redacted()
	if _, err := json.Marshal(redacted); err != nil {
		return healthCheck{Name: "effective_config", Ok: false, Detail: "配置无法序列化"}
	}
	return healthCheck{
		Name:   "effective_config",
		Ok:     true,
		Detail: "已脱敏，config_sha256=" + configFingerprint(&h.deps.Cfg),
	}
}

func (h *healthHandler) checkUpstream(ctx context.Context, probe bool) healthCheck {
	if !probe {
		return healthCheck{Name: "upstream_reachable", Ok: true, Detail: "未探测"}
	}
	if h.deps.Client == nil {
		return healthCheck{Name: "upstream_reachable", Ok: false, Detail: "上游客户端未初始化"}
	}
	if err := h.deps.Client.Ping(ctx); err != nil {
		return healthCheck{
			Name:   "upstream_reachable",
			Ok:     false,
			Detail: pingFailureDetail(err),
		}
	}
	return healthCheck{
		Name:   "upstream_reachable",
		Ok:     true,
		Detail: "GET /v1/tools/search 返回 405，链路健康（不计费）",
	}
}

func pingFailureDetail(err error) string {
	switch domain.CodeOf(err) {
	case domain.CodeUpAuth:
		return "上游 401：API Key 失效（2401），检查 DEEPSEEK_API_KEY"
	case domain.CodeUpNotFound:
		return "上游 404：检查 [upstream].base_url 是否拼写正确并指向 /v1 路由"
	case domain.CodeUpNetwork:
		return "网络错误 2998 UP_NETWORK: " + err.Error()
	default:
		return fmt.Sprintf("探测失败（code=%d）: %v", domain.CodeOf(err), err)
	}
}

func (h *healthHandler) uptimeSeconds() int64 {
	if h.deps.StartedAt.IsZero() {
		return 0
	}
	seconds := int64(h.deps.Clock.Now().Sub(h.deps.StartedAt).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func configFingerprint(cfg *config.Config) string {
	raw, err := json.Marshal(cfg.Redacted())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func parseHealthRequest(req *mcp.CallToolRequest) (*healthInput, error) {
	if req == nil || req.Params == nil {
		return nil, invalid("arguments", "缺少请求参数")
	}
	var in healthInput
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return nil, invalid("arguments", "无法解析 JSON 参数")
		}
	}
	return &in, nil
}
