// 文件名: codes.go
// 用途: 定义 41 个错误码常量与 CodeRegistry 静态元信息表。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

// CodeInfo 是错误码的静态元信息。
// Name 是稳定契约，MUST NOT 改名（客户端可能按它做分支）。
type CodeInfo struct {
	Name      string // 稳定英文名，UPPER_SNAKE
	Message   string // 中文，一句话说清发生了什么
	Hint      string // 中文，告诉调用方现在能做什么
	Retryable bool   // 重试器唯一依据
}

// 1xxx 是我方入参校验错误，从 1000 起、步长 10。
const (
	CodeValInvalidParam    = 1000
	CodeValQueryTooLong    = 1010
	CodeValCountRange      = 1020
	CodeValDateFormat      = 1030
	CodeValURLScheme       = 1040
	CodeValURLCredentials  = 1050
	CodeValURLTooLong      = 1060
	CodeValSSRFBlocked     = 1070
	CodeValSyntaxConflict  = 1080
	CodeValSectionNotFound = 1090
	CodeValSynthDisabled   = 1095
)

// 2xxx 是上游返回错误，码值 MUST 等于 2000 + HTTP 状态码；
// 2000、2998、2999 是无对应状态码的哨兵，故不在该公式内。
const (
	CodeUpValidation     = 2400
	CodeUpAuth           = 2401
	CodeUpBalance        = 2402
	CodeUpForbidden      = 2403
	CodeUpNotFound       = 2404
	CodeUpTimeout        = 2408
	CodeUpTooLarge       = 2413
	CodeUpRateLimited    = 2429
	CodeUpServer         = 2500
	CodeUpUnavailable    = 2503
	CodeUpGatewayTimeout = 2504
	CodeUpUnexpected     = 2000
	CodeUpNetwork        = 2998
	CodeUpParse          = 2999
)

// 3xxx 是本地资源/状态错误，从 3000 起、步长 10；3099 是兜底哨兵。
const (
	CodeLocQueueTimeout        = 3000
	CodeLocRetryBudget         = 3010
	CodeLocTotalTimeout        = 3020
	CodeLocCircuitOpen         = 3030
	CodeLocNoAPIKey            = 3040
	CodeLocHTTPTokenMissing    = 3041
	CodeLocContentTooShort     = 3050
	CodeLocCacheDegraded       = 3060
	CodeLocBudgetExceeded      = 3070
	CodeLocSingleflightTimeout = 3075
	CodeLocSnapshotGone        = 3080
	CodeLocConfigInvalid       = 3090
	CodeLocHTTPUnauthorized    = 3091
	CodeLocHTTPExposedNoAuth   = 3092
	CodeLocHTTPHostRejected    = 3093
	CodeLocInternal            = 3099
)

// CodeRegistry 是全部 41 个错误码的静态元信息表，ds://error-codes 由它生成。
//
// 1080 是软错误（只写 warnings[]，永不作为 code 返回），但它仍是 warnings 文案的
// 唯一来源，所以 MUST 留在表内；否则 Resource 会少一行、常量数也会与表长度不等。
var CodeRegistry = map[int]CodeInfo{
	1000: {
		Name:      "VAL_INVALID_PARAM",
		Message:   "参数不合法",
		Hint:      "按工具 schema 修正参数后重试，本次调用未发往上游。",
		Retryable: false,
	},
	1010: {
		Name:      "VAL_QUERY_TOO_LONG",
		Message:   "拼装后的查询超过 128 个字符",
		Hint:      "缩短 query，或减少 site、lang、日期等结构化条件后重试。",
		Retryable: false,
	},
	1020: {
		Name:      "VAL_COUNT_RANGE",
		Message:   "count 必须在 1~50 之间",
		Hint:      "把 count 调整到 1~50；默认 10 通常已够用。",
		Retryable: false,
	},
	1030: {
		Name:      "VAL_DATE_FORMAT",
		Message:   "日期格式不合法",
		Hint:      "after/before 用 YYYY 或 YYYY-MM-DD，例如 2025-01-01。",
		Retryable: false,
	},
	1040: {
		Name:      "VAL_URL_SCHEME",
		Message:   "URL 必须是 http 或 https 且包含主机名",
		Hint:      "检查 URL 的 scheme 与 host，例如 https://example.com/page。",
		Retryable: false,
	},
	1050: {
		Name:      "VAL_URL_CREDENTIALS",
		Message:   "URL 不能包含用户名或密码",
		Hint:      "移除 URL 中 user:pass@ 的部分后重试。",
		Retryable: false,
	},
	1060: {
		Name:      "VAL_URL_TOO_LONG",
		Message:   "URL 超过 8192 个字符",
		Hint:      "换用更短的链接，或先缩短跳转地址再抓取。",
		Retryable: false,
	},
	1070: {
		Name:      "VAL_SSRF_BLOCKED",
		Message:   "该 URL 指向内网地址，已被本地安全策略拦截",
		Hint:      "本工具只允许抓取公网 http/https 地址；确需内网资源请改用其他工具。",
		Retryable: false,
	},
	1080: {
		Name:      "VAL_SYNTAX_CONFLICT",
		Message:   "query 已含同名操作符，结构化字段未追加",
		Hint:      "删除 query 中重复的操作符，或改用 raw_query=true 原样发送。",
		Retryable: false,
	},
	1090: {
		Name:      "VAL_SECTION_NOT_FOUND",
		Message:   "指定的节 ID 不在该快照的目录中",
		Hint:      "先不带 section 调用 fetch 取 outline，再用返回的 sN/cN 重试。",
		Retryable: false,
	},
	1095: {
		Name:      "VAL_SYNTH_DISABLED",
		Message:   "答案合成是阶段二能力，当前未启用",
		Hint:      "移除 synthesize 参数，直接阅读返回的搜索结果。",
		Retryable: false,
	},
	2400: {
		Name:      "UP_VALIDATION",
		Message:   "上游判定请求不合法",
		Hint:      "检查请求体字段与 JSON 格式后重试；原样重试不会成功。",
		Retryable: false,
	},
	2401: {
		Name:      "UP_AUTH",
		Message:   "API Key 缺失或无效",
		Hint:      "检查 DEEPSEEK_API_KEY 是否正确、是否过期，更新后重启服务。",
		Retryable: false,
	},
	2402: {
		Name:      "UP_BALANCE",
		Message:   "账户余额不足",
		Hint:      "充值后再调用；重试不会成功，也不要反复换查询。",
		Retryable: false,
	},
	2403: {
		Name:      "UP_FORBIDDEN",
		Message:   "上游安全策略拒绝了该请求",
		Hint:      "调整查询或目标 URL 后重试；原样重试可能仍被拒绝。",
		Retryable: false,
	},
	2404: {
		Name:      "UP_NOT_FOUND",
		Message:   "上游未能取到该网页的可用正文",
		Hint:      "上游取不到该页正文。可能是站点拒绝抓取（维基百科/知乎/Reddit/HN 实测抓不到）、页面不存在、目标站返回错误、内容类型不支持、或无可提取正文 —— 上游不区分。建议换来源，勿对同一 URL 重试。",
		Retryable: false,
	},
	2408: {
		Name:      "UP_TIMEOUT",
		Message:   "上游读取请求体超时",
		Hint:      "稍后重试；持续出现时减小请求体并检查网络。",
		Retryable: true,
	},
	2413: {
		Name:      "UP_TOO_LARGE",
		Message:   "请求体超过上游大小限制",
		Hint:      "缩短 query 或降低 count 后重试。",
		Retryable: false,
	},
	2429: {
		Name:      "UP_RATE_LIMITED",
		Message:   "触发上游 QPS 限流",
		Hint:      "按退避等待后重试；长期出现请降低 [ratelimit] 中的 rps。",
		Retryable: true,
	},
	2500: {
		Name:      "UP_SERVER",
		Message:   "上游服务器内部错误",
		Hint:      "稍后重试；持续出现说明上游故障，可查看上游状态页。",
		Retryable: true,
	},
	2503: {
		Name:      "UP_UNAVAILABLE",
		Message:   "上游服务暂不可用",
		Hint:      "稍后重试；持续不可用请检查上游状态与 [upstream].base_url。",
		Retryable: true,
	},
	2504: {
		Name:      "UP_GATEWAY_TIMEOUT",
		Message:   "上游网关超时",
		Hint:      "稍后重试或换更快的来源；慢站抓取常返回此码。",
		Retryable: true,
	},
	2000: {
		Name:      "UP_UNEXPECTED",
		Message:   "上游返回了未预期的状态码",
		Hint:      "记录 upstream_http 并检查请求；不要原样重试。",
		Retryable: false,
	},
	2998: {
		Name:      "UP_NETWORK",
		Message:   "未能从上游取得 HTTP 响应",
		Hint:      "多为 DNS、TCP、TLS 或单次超时，重试即可；持续出现请检查网络与 base_url。",
		Retryable: true,
	},
	2999: {
		Name:      "UP_PARSE",
		Message:   "上游返回 200 但响应体不符合契约",
		Hint:      "记录 request_id 后重试；持续出现说明上游接口可能已变更。",
		Retryable: false,
	},
	3000: {
		Name:      "LOC_QUEUE_TIMEOUT",
		Message:   "请求排队超过等待上限仍未获得执行许可",
		Hint:      "调高 [ratelimit.search].rps 或降低并发后重试。",
		Retryable: true,
	},
	3010: {
		Name:      "LOC_RETRY_BUDGET",
		Message:   "重试预算已耗尽",
		Hint:      "等待预算窗口滑过后再试；频繁出现请降低调用频率。",
		Retryable: false,
	},
	3020: {
		Name:      "LOC_TOTAL_TIMEOUT",
		Message:   "整次调用超过总时长上限",
		Hint:      "稍后重试；持续超时请调大 [timeout] 的 total_ms。",
		Retryable: false,
	},
	3030: {
		Name:      "LOC_CIRCUIT_OPEN",
		Message:   "熔断器处于打开状态",
		Hint:      "按 retry_after_ms 等待后重试；持续打开请检查上游健康。",
		Retryable: true,
	},
	3040: {
		Name:      "LOC_NO_API_KEY",
		Message:   "未配置 DEEPSEEK_API_KEY",
		Hint:      "设置环境变量 DEEPSEEK_API_KEY 或写入 .env 后重启。",
		Retryable: false,
	},
	3041: {
		Name:      "LOC_HTTP_TOKEN_MISSING",
		Message:   "HTTP 鉴权已启用但 token 未设置或过短",
		Hint:      "设置至少 16 字节的 DEEPSEEKSEARCH_HTTP_TOKEN 后重启。",
		Retryable: false,
	},
	3050: {
		Name:      "LOC_CONTENT_TOO_SHORT",
		Message:   "抓到的正文过短，疑似导航页或空页",
		Hint:      "换一个来源或 URL；确属正常页面可调低 min_content_chars。",
		Retryable: false,
	},
	3060: {
		Name:      "LOC_CACHE_DEGRADED",
		Message:   "缓存故障，已降级为直连",
		Hint:      "调用可继续；检查缓存数据库与磁盘空间以恢复缓存。",
		Retryable: false,
	},
	3070: {
		Name:      "LOC_BUDGET_EXCEEDED",
		Message:   "时间窗或日累计调用次数超限",
		Hint:      "等待窗口重置或调整 [limits.budget]；不要继续重试。",
		Retryable: false,
	},
	3075: {
		Name:      "LOC_SINGLEFLIGHT_TIMEOUT",
		Message:   "等待他人填充缓存超时",
		Hint:      "稍后重试；持续出现请调大 [cache].wait_ms。",
		Retryable: true,
	},
	3080: {
		Name:      "LOC_SNAPSHOT_GONE",
		Message:   "该 cache_id 对应的快照已过期或不存在",
		Hint:      "重新 fetch 该 URL 获取新 cache_id 后再按节读取。",
		Retryable: false,
	},
	3090: {
		Name:      "LOC_CONFIG_INVALID",
		Message:   "配置校验失败",
		Hint:      "按错误中的字段路径修正 config.toml 或环境变量后重启。",
		Retryable: false,
	},
	3091: {
		Name:      "LOC_HTTP_UNAUTHORIZED",
		Message:   "HTTP 请求缺少或携带了错误的 Bearer token",
		Hint:      "在 Authorization 头带上正确的 Bearer token 后重试。",
		Retryable: false,
	},
	3092: {
		Name:      "LOC_HTTP_EXPOSED_NO_AUTH",
		Message:   "服务对外暴露但未启用鉴权，已拒绝启动",
		Hint:      "设置 auth_enabled=true 并提供 token，或只监听回环地址。",
		Retryable: false,
	},
	3093: {
		Name:      "LOC_HTTP_HOST_REJECTED",
		Message:   "请求 Host 不是回环地址，已拒绝",
		Hint:      "改用回环地址访问，或设置 allow_non_local_host=true 并启用鉴权。",
		Retryable: false,
	},
	3099: {
		Name:      "LOC_INTERNAL",
		Message:   "内部错误，属于本工具自身缺陷",
		Hint:      "带上 call_id 与复现步骤提交 issue；不要原样重试。",
		Retryable: false,
	},
}
