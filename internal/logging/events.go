// 文件名: events.go
// 用途: 定义全部 26 个日志事件名常量，是全项目事件名的唯一真源。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package logging

// 事件名唯一真源是 Task-04 A-02 §3.1；本文件不重复写说明注释，
// 否则一处事件表会分叉成两份会漂移的真源。
const (
	EvAppStart     = "app.start"
	EvAppConfig    = "app.config"
	EvAppDB        = "app.db"
	EvAppTransport = "app.transport"
	EvAppReady     = "app.ready"
	EvAppStop      = "app.stop"

	EvCallBegin        = "call.begin"
	EvCallCache        = "call.cache"
	EvCallLimitWait    = "call.limit.wait"
	EvCallUpstream     = "call.upstream"
	EvCallUpstreamDone = "call.upstream.done"
	EvCallRetry        = "call.retry"
	EvCallCircuit      = "call.circuit"
	EvCallDedupe       = "call.dedupe"
	EvCallSection      = "call.section"
	EvCallEnd          = "call.end"

	EvErr                = "err"
	EvWarnSlow           = "warn.slow"
	EvWarnBudget         = "warn.budget"
	EvWarnCacheDegraded  = "warn.cache_degraded"
	EvWarnCacheOversize  = "warn.cache_oversize"
	EvInfoCacheRecovered = "info.cache_recovered"
	EvWarnLogQuota       = "warn.log_quota"
	EvSSRFBlocked        = "ssrf.blocked"
	EvCachePrune         = "cache.prune"
	EvLogRotate          = "log.rotate"
)
