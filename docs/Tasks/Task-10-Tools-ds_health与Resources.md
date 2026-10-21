# Task-10 · Tools：`ds_health` 与 MCP Resources

> **执行方式**：2 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 一个本地诊断工具 + 三个 Resource。**MUST NOT 发起任何计费请求** |
| 依赖 | Task-02 ~ Task-09 |
| 产出 | 模型能自己回答「为什么搜不到」；错误码表不进 description 省 92% token |
| 必读 | `Contract/14-错误契约` §五（Resource 的 2026-07-28 合规要求）；`Contract/15-协议契约` §七之二 |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | `ds_health` 工具 | 不能因为某个检查失败就让整个工具失败 |
| A-02 | 三个 Resource | Resource URI 必须稳定，`ttlMs` 必须设 |

---

## 原子任务 A-01 · `ds_health` 工具
### 0 前置

- Task-02 ~ Task-09 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

`ds_health` 存在的理由：用户/模型遇到「搜不到东西」时，第一反应是重试或换 query。但真实原因可能是 Key 无效、缓存目录不可写、或者服务根本连不上上游。让模型能**自己诊断**，比让人翻日志快得多。

**核心约束：MUST NOT 发起任何计费请求（`POST /v1/tools/*`）。** Key 有效性与链路可达性只在用户显式传 `probe_upstream=true` 时才探测，用的是 `Client.Ping`：发 `GET {base_url}/v1/tools/search`，上游实测返回 **405 空 body**（23 号 §二）。🔴 **计费口径（已裁决，统一为「不计费」）**：按 README 的「HTTP 200 且拿到内容才计费」，405 ≠ 200 且响应体为空 ⇒ **不计费**。本文档 MUST NOT 再出现「probe_upstream 会计费」的说法（依据：`Decisions/ADR.md` ADR-015）。MUST NOT 用真实 `POST /v1/tools/search`（`count=1`）做探测 —— 那才会计费。

### 2 要创建的文件

`internal/tools/health.go`、`internal/tools/health_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 参数 1 个：`probe_upstream bool = false` |
| 2 | 检查项 8 条，每条返回 `{name, ok, detail}` |
| 3 | 任何一条失败 → 整体 `status="degraded"`，但**工具本身不返回 `IsError`**（除非全部失败） |
| 4 | 检查项：`api_key_present`、`cache_writable`、`cache_stats`、`clock_tz`、`limiter_state`、`circuit_state`、`effective_config`、`upstream_reachable` |
| 5 | 🔴 **工具元数据（Task-11 的前置依赖，MUST 在本任务补齐）**：`Name` = `"ds_health"`；`Title` = `"DeepSeek 健康诊断"`（中文人类可读名，客户端 UI 直接显示，口径同 `Contract/15-协议契约.md` §七之二）；`description` MUST 说清三点 —— ①只做本地诊断，默认零网络、**零计费**；②遇到「搜不到结果 / 调用全部失败」时先调本工具自诊断，不要盲目重试；③`status` 的语义（`ok` / `degraded`）与逐条 `{name, ok, detail}` 怎么读。`Annotations` 三项 `ReadOnlyHint=true`、`IdempotentHint=true`、`OpenWorldHint=true`。🔴 **go-sdk v1.8.0 的类型不同**：`ReadOnlyHint` / `IdempotentHint` 是 **`bool`**，`OpenWorldHint` 是 **`*bool`**（只有它需要 `boolPtr`）；原文「三个指针都 MUST 非 nil」是错的，按 bool 写「非 nil」根本编译不过。Task-11 的 `TestToolAnnotations` 会断言**三个工具**的 `ReadOnlyHint==true`、`IdempotentHint==true`、`OpenWorldHint != nil && *OpenWorldHint==true`，本任务不补，按「一个原子任务一个窗口」执行到 Task-11 时必然返工。`OpenWorldHint=true` 的理由：`probe_upstream=true` 时确实访问外部网络；annotation 是静态元数据，MUST NOT 随入参变化 |
| 6 | `upstream_reachable` 只在 `probe_upstream=true` 时真做（用 `Client.Ping`，**不计费**，见 §1 计费口径），否则返回 `ok=true, detail="未探测"`。判定标准照 ADR-015 的诊断表逐字实现：**405** → `ok=true`（链路健康、路由正确）；**401** → `ok=false`，detail 指向 Key 失效（`2401`/`3040`）；**404** → `ok=false`，detail 明确提示检查 `[upstream].base_url`（base_url 或路径配错，23 号 §二）；其它 4xx/5xx → `ok=false`，带上游状态码；**网络错误/超时 → `ok=false`，带 `2998 UP_NETWORK`**（🔴 MUST NOT 用 `2999`：2999 的唯一成因是「上游 200 但响应体不合契约」，「上游不可达」是 2998，见 `Contract/14-错误契约.md` §1.2/§1.3、ADR-016） |
| 7 | `clock_tz` / `limiter_state` / `circuit_state` / `cache_stats` 四项的判定标准 MUST 按 §3.1 逐字实现（无判据则 `degraded` 语义不稳，模型无法靠它自诊断） |
| 8 | `effective_config` 用 `Config.Redacted()`，Key/Token 显示 `***` |
| 9 | 返回 `build` 段：`version` `commit` `build_time` `go_version` `uptime_s` |
| 10 | 返回 `limits` 段：当前 `rps`、`max_inflight`、各 `ttl` |
| 11 | 无网络、无副作用、耗时 < 50ms（`probe_upstream=false` 时） |

#### 3.1 🔴 四个检查项的判定标准（本任务裁决，逐字实现）

这四个检查项此前只有名字没有判据 ⇒ 实现者只能拍脑袋，`degraded` 语义随之不稳，而模型要靠它自诊断。裁决如下（每条附理由）：

| 检查项 | `ok=true` 判据 | `detail` 内容 | 理由 |
|---|---|---|---|
| `clock_tz` | ① `time.LoadLocation` 能加载 `[log].timezone`（默认 `Asia/Shanghai`）；② 系统墙钟年份 ∈ [2025, 2100] | 时区名、当前本地时间、UTC 偏移 | 时钟「偏差多少」在本地**不可测**（对比真实时间需要网络，违反第 11 条的零网络约束），故退化为两条可判定的健全性检查：①精简容器可能缺 tzdata，`LoadLocation` 失败会让 `date`/`age_days` 全错 —— date 铁律依赖 Asia/Shanghai 换算（12 号 §二；23 号 §3.3 实测 `date` 恒为 16:00 UTC）；②时钟回拨（如回到 1970）会让 TLS 证书校验与 `age_days` 全崩，年份粗检能兜住最常见的 NTP 故障 |
| `limiter_state` | 排队等待者数为 0；或预计等待 < `[ratelimit].max_wait_ms` | `rps`、`burst`、🔴 **per-key** 的当前 inflight/该 key 的配额（不是 `max_inflight` 总数）、等待者数、预计等待 ms、`details.stage` 会卡在哪一段 | 🔴 **令牌数为 0 本身不算异常**：令牌桶按 rps 持续回填，瞬时为 0 是高负载常态、毫秒级即恢复；真正危险的信号是「排队即将超时」（会触发 `3000 LOC_QUEUE_TIMEOUT`），所以判据落在预计等待时长而不是令牌余量。🔴 **detail MUST 按 key 分列**：在飞配额是 per-key 的（`Task-06` §A-01 3.2），只报「当前 inflight/8」会把「search 满了」与「fetch 满了」混成一个数 —— 而这两者要调的旋钮不同（`search_share` vs `max_inflight`），运维看不出该动哪个 |
| `circuit_state` | 熔断器处于 Closed 或 HalfOpen | 状态、连续失败数/`[circuit].fail_threshold`、Open 时剩余 `retry_after_ms`（来自 `[circuit].open_ms`） | 🔴 **Open 判 `ok=false`**：Open 期间所有搜索/抓取调用直接失败 `3030 LOC_CIRCUIT_OPEN`，正是 ds_health 要诊断的「为什么搜不到」的头号本地成因。HalfOpen 判 `ok=true`：它在放行探测流量、属于恢复中状态（`[circuit].half_open_probes` ≥1，见 `Task-06` §A-03），报 degraded 反而制造噪音。🔴 另需注意：连续失败计数**只统计 `14-错误契约` §6.1 白名单里的码**，所以「用户参数错了很多次」不会让熔断器跳闸 —— detail 里的失败数因此可能远小于日志里的错误总数，这是设计而非丢数据 |
| `cache_stats` | ① `[cache].enabled=false` → `ok=true`（合法配置，不是故障）；② enabled 且缓存 DB 可打开、可查询条目数、且未处于降级直连 | entries/`[cache].max_entries`、bytes_mb/`[cache].max_bytes_mb`、`ttl_search_s`/`ttl_fetch_s`、是否降级 | 判 `ok=false` 的只有「已触发 `3060 LOC_CACHE_DEGRADED` 降级直连」或「DB 探测失败」—— 这两种意味着缓存层实际不可用。**容量打满不算故障**：`[cache].prune_interval_s` 的后台清理是设计内行为，写进 detail 供参考即可，否则大缓存机器会永久 degraded |

> 裁决边界：以上判据只决定 `ok` 与 `degraded`，**不改变**第 3 条「单项失败 ≠ 工具 `IsError`」的既有规则；也 MUST NOT 为任何一项新造错误码（码表已冻结 **41** 条）。

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 以 `POST /v1/tools/search` 或 `POST /v1/tools/fetch` 做「Key 有效性验证」（POST 拿到 200 + 内容即计费）。注意与 `Client.Ping` 区分：Ping 发的是 `GET {base_url}/v1/tools/search`，实测返回 **405 空 body**、不计费，是允许且推荐的探活方式（23 号 §二、ADR-015） |
| 2 | MUST NOT 返回未脱敏的配置 |
| 3 | MUST NOT 因单项失败就 `IsError: true`（诊断工具本身不能也不可用） |
| 4 | MUST NOT 让 `probe_upstream=true` 默认开启 |
| 5 | MUST NOT 探测文件系统其它位置（只看 `Data/` 是否可写：写一个 0 字节临时文件再删） |

### 5 参考骨架

```go
// healthCheck 是单项检查结果。
// ok=false 不代表工具失败 —— 诊断工具的价值就在于能报告「哪里不对」。
type healthCheck struct {
    Name   string `json:"name"`
    Ok     bool   `json:"ok"`
    Detail string `json:"detail"`
}

// handleHealth 只做本地检查，唯一可能的网络动作是 probe_upstream=true 时的
// Client.Ping：发 GET {base_url}/v1/tools/search，上游实测返回 405 空 body
// （23 号 §二）。405 ≠ 200 且无内容 ⇒ 按 README 计费口径不计费（ADR-015）。
// 注册时 MUST 带 Title 与三个非 nil 的 hint（§3 第 5 条），否则 Task-11 的
// TestToolAnnotations 会红。
func handleHealth(ctx context.Context, deps Deps, in healthInput) *mcp.CallToolResult
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 默认调用 | 8 条检查，耗时 < 50ms |
| `cache` 用只读目录 | `cache_writable.ok=false`，整体 `degraded`，工具不 `IsError` |
| `ApiKey` 空（人为构造） | `api_key_present.ok=false` |
| `effective_config` 内容 | 不含真实 Key 字符 |
| `probe_upstream=true` | 调用 `fakeClient.Ping`，`pingCalls==1` |
| `probe_upstream=false` | `pingCalls==0` |
| 🔴 `Annotations` 三项 + `Title` | 三个 hint 指针非 nil 且 true；`Title="DeepSeek 健康诊断"`（与 Task-11 `TestToolAnnotations` 同口径） |
| 🔴 Ping 返回 404 | `upstream_reachable.ok=false`，detail 含 `base_url`（ADR-015 诊断表） |
| 🔴 Ping 网络错误 | `ok=false`，detail 含 `2998`，MUST NOT 含 `2999` |
| 🔴 熔断置 Open（人为构造） | `circuit_state.ok=false` + 整体 `degraded`，工具不 `IsError` |
| 🔴 令牌数为 0 但无排队（人为构造） | `limiter_state.ok=true`（§3.1：瞬时 0 令牌不是异常） |
| 🔴 `[cache].enabled=false` | `cache_stats.ok=true`，detail 写明「缓存已关闭」（合法配置非故障） |
| 🔴 时区不可加载（人为构造坏 `[log].timezone`） | `clock_tz.ok=false` |
| 8 条全失败（人为构造） | `IsError=true` |

### 7 验收命令与期望输出

```bash
$ go test ./internal/tools/ -v -run TestHealth
--- PASS: TestHealth (0.00s)
    --- PASS: TestHealth/默认不做网络探测
    --- PASS: TestHealth/缓存不可写时降级
```

### 8 完成定义（DoD）

- [ ] 14 个用例全绿
- [ ] `probe_upstream=false` 时 `fakeClient` 零调用
- [ ] `Annotations`：`ReadOnlyHint` / `IdempotentHint` 为 `true`（bool），`OpenWorldHint` 非 nil 且指向 `true`（*bool）—— SDK v1.8.0 的类型口径见 `15-协议契约` §七之二
- [ ] 提交：`feat(tools): ds_health 诊断工具`

---

## 原子任务 A-02 · 三个 MCP Resource
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

为什么错误码表放 Resource 不放 tool description：42 个码 + hint 约 1,500 token，而 `tools/list` 每次调用都会返回 description。放进 Resource 后 description 只需 3 条行为规则（约 120 token），**省 92%**。而且模型只在真正出错时才需要读码表。

三个 Resource（🔴 URI / MIME / `ttlMs` / `cacheScope` 的**唯一真源是 `Contract/14-错误契约.md` §五**，本表只是摘要，改动 MUST 先改那边）：

| URI | 内容 | 大小 | `ttlMs` | `cacheScope` |
|---|---|:-:|:-:|:-:|
| `ds://effective-config` | 脱敏后的生效配置 + 每个值的来源层级 + `config_sha256` | ~3KB | `300000` | `private` |
| `ds://error-codes` | 42 个错误码全表（code/name/message/hint/retryable） | ~4KB | `300000` | `public` |
| `ds://usage` | 本次进程生命周期内的调用统计（按工具、按小时） | ~1KB | `1000` | `private` |

### 2 要创建的文件

`internal/tools/resources.go`（含 usage 统计，不单独建 usage.go）、`internal/tools/resources_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 三个 Resource 用 `mcp.Resource` + handler 注册到 `srv` |
| 2 | `MimeType = "application/json"`（**三个都是**。裁决见 `14-错误契约` §5.0 第 2 条：`error-codes` 此前在 14 号写的是 `text/markdown`，但 markdown 就得把 14 号正文嵌进二进制 = 第二份手写真源，且无法断言「条目数 == 41」） |
| 3 | `Name` 与 `Title` 都是中文，`Description` 说清「什么时候该读它」 |
| 4 | 🔴 **`ttlMs` / `cacheScope` MUST 按 URI 逐个设，MUST NOT 三个都用同一组值**：`error-codes` 与 `effective-config` 用 `300000`（两者在单进程生命周期内都恒定 —— 配置启动时加载一次不 reload、码表编译进二进制），`usage` 用 **`1000`**。<br>**为什么 `usage` 不用 `0`**：`15-协议契约` §6.2 实测「`SetCacheable` 回调没接上时 `ttlMs` 就是 0」⇒ 0 同时表示「我故意不缓存」与「回调忘了接」，后者正是那个「客户端缓存毫无收益」的静默故障。用 `1000` 表示不缓存，0 就重新变成**只可能是 bug** 的信号（`14-错误契约` §5.0 第 3 条）。<br>**为什么不能全局一个值**：`SetCacheable` 是**服务器级**的单个回调，MUST 按 URI 分派；若对所有请求都返回 300000，`usage` 会被客户端缓存 5 分钟、统计数字直接失去意义 |
| 4.1 | 🔴 `effective-config` 与 `usage` 的 `cacheScope` MUST 是 `"private"`（含脱敏配置、`config_sha256`、调用量，不得进共享缓存）；只有 `error-codes` 是 `"public"`（无敏感内容，多客户端可共用）。原文写的 `"global"` 不是 `15-协议契约` §6.2 里的取值 |
| 5 | `error-codes` 内容由 `domain.CodeRegistry` 生成，**MUST NOT 手写 JSON** |
| 6 | `effective-config` 用 `Config.Redacted()` + 逐项 `Source(key)` |
| 7 | `usage` 统计：`tool` → `{calls, billed, cache_hits, errors, last_call_at}`；按小时分桶保留最近 24 桶 |
| 8 | `usage` 计数 MUST 在每次工具调用结束时更新（`atomic`） |
| 9 | 三个 handler 都 MUST 尊重 `ctx.Done()` |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 在 Resource 里返回 Key/Token 原文 |
| 2 | MUST NOT 让 `usage` 落盘（进程内存即可，重启清零；跨会话持久统计是阶段二） |
| 3 | MUST NOT 手写 `error-codes` 的 JSON 字符串（必须从 `CodeRegistry` 生成，否则码表改了这里忘了改） |
| 4 | MUST NOT 用 `ds://errors` 之类不稳定 URI（一旦发布即为契约） |
| 5 | 🔴 MUST NOT 让任何 Resource 的 `ttlMs` 为 **0**（0 与「`SetCacheable` 回调没接上」在协议上不可区分，见 §3 第 4 条）。易变的 `usage` 用 `1000` |
| 6 | 🔴 MUST NOT 对所有 Resource 返回同一组 `ttlMs`/`cacheScope`（`usage` 会被缓存 5 分钟，统计失去意义） |
| 7 | 🔴 MUST NOT 用 `"global"` 之类自造的 `cacheScope` 取值，只允许 `"public"` / `"private"`（`15-协议契约` §6.2） |
| 8 | MUST NOT 把 `14-错误契约` 的正文嵌进二进制来生成 `error-codes`（那是第二份手写真源；MUST 由 `domain.CodeRegistry` 生成，见第 3 条） |

### 5 参考骨架

```go
// registerResources 注册三个只读 Resource。
// 错误码表放这里而不是 tool description：42 个码约 1500 token，
// 而 tools/list 每次都要付；放 Resource 后 description 只要 120 token。
func registerResources(srv *mcp.Server, deps Deps) error

// resourceCache 是三个 Resource 的 ttlMs/cacheScope 唯一取值处。
// 🔴 表驱动而不是 if-else 链：`14-错误契约` §五 是唯一真源，
//    这张表 MUST 与它逐字一致，§6 有一条用例直接断言三组值互不相同。
var resourceCache = map[string]struct {
    TTLMs  int64
    Scope  string
}{
    "ds://effective-config": {300000, "private"}, // 含脱敏配置与 config_sha256，不得进共享缓存
    "ds://error-codes":      {300000, "public"},  // 无敏感内容，多客户端可共用
    "ds://usage":            {1000, "private"},   // 🔴 不用 0：0 与「回调没接上」不可区分（§3 第 4 条）
}

// SetCacheable 是**服务器级**的单个回调（签名见 `15-协议契约` §6.2），
// 由 Task-11 在 NewServer 时挂上，MUST 按 URI 查上表分派。
// 查不到的 URI 返回 (1000,"private") 而不是 0 —— 宁可少缓存，
// 也不要让 0 这个「回调没接上」的信号被合法值污染。
// go-sdk v1.8.0 的 mcp.Request 没有 URI()，故另给一个从
// resources/read Params.URI 取键的入口，Task-11 直接用 CacheableForRequest(r)。
func CacheableFor(uri string) (ttlMs int64, scope string)
func CacheableForRequest(req mcp.Request) (ttlMs int64, scope string)

// usageTracker 记录本进程的调用统计。
// 🔴 并发口径（二者 MUST 同时成立，此前注释与结构体自相矛盾）：
//   ① stats map 在 newUsageTracker() 时按 3 个固定工具名一次建好，此后**只读**
//      （永不新增/删除键 ⇒ 读 map 无需加锁）；
//   ② 计数器全部用 atomic —— 热路径上每 ms 都可能有调用，MUST NOT 争锁。
type usageTracker struct {
    stats map[string]*toolStat // 构造后只读：键 = {ds_search, ds_fetch, ds_health}
}

// toolStat 的计数全是原子操作，last_call_at 用 atomic.Value 存 time.Time。
type toolStat struct {
    calls, billed, cacheHits, errors atomic.Int64
    lastCallAt                       atomic.Value
}
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 读 `ds://error-codes` | 合法 JSON，条目数 == `len(domain.CodeRegistry)` == 41 |
| 内容里无手写痕迹 | 改 `CodeRegistry` 一项 → 输出跟着变（测试里临时改再改回） |
| 读 `ds://effective-config` | 无真实 Key 字符；含 `"source"` 字段 |
| 读 `ds://usage` | 初始全 0；调用一次 `ds_search` 后 `calls==1` |
| `usage` 的 `billed` | 缓存命中时不计 |
| URI 常量 | 三个字符串与文档一致 |
| 🔴 `SetCacheable` 按 URI 分派 | 对三个 URI 分别调用回调，得到 `(300000,"public")` / `(300000,"private")` / `(1000,"private")` —— **三者互不相同**才算过；若实现只设了一个全局值，这条会立刻失败 |
| 🔴 任何 Resource 的 `ttlMs` 都 > 0 | 三个都断言 `> 0`（§3 第 4 条：0 与「回调没接上」不可区分） |
| 🔴 `cacheScope` 只取 `public` / `private` | 断言不含 `"global"` 等自造取值（§3 第 4.1 条） |
| 🔴 `resources/list` 顺序确定 | 连读 20 次，URI 序列恒为字典序 `effective-config, error-codes, usage`（`14-错误契约` §5.1） |

### 7 验收命令与期望输出

```bash
$ go test ./internal/tools/ -v -run TestResources
--- PASS: TestResources (0.01s)
    --- PASS: TestResources/error_codes_与注册表一致
    --- PASS: TestResources/effective_config_已脱敏
```

> 🔴 **本任务不提供端到端 curl 验收命令。** 此前这里有一条 `curl -X POST localhost:8765/mcp -H 'Mcp-Method: resources/read' ...`：它缺 JSON-RPC body、缺 `Mcp-Name` 头、缺 body 内 `_meta["io.modelcontextprotocol/protocolVersion"]`，照抄必得 400（`Mcp-Method` 缺失/不匹配 → `-32020`，缺 `_meta.protocolVersion` → `-32602`，见 `Contract/15-协议契约.md` §四），**无法当验收证据**，已删除。HTTP 端到端验收统一归 Task-12 A-02 的 L3（唯一真实起服务的层，其 P17/P18 覆盖 stdio 与 HTTP 两条路）。「`ds://error-codes` 条目数 == 41」的断言由本任务单测直接读 Resource handler 的输出计数保证，不依赖网络层。

### 8 完成定义（DoD）

- [ ] 🔴 10 个用例全绿（原写 6，§6 表已扩到 10 行；数字 MUST 等于 §6 表的数据行数）
- [ ] `ds://error-codes` 条目数 41
- [ ] 🔴 三个 Resource 的 `ttlMs` 全 > 0，且 `cacheScope` 只取 `public`/`private` —— 与 `Contract/14-错误契约.md` §五 逐字一致
- [ ] 🔴 `SetCacheable` 是按 URI 分派的，不是一个全局常量（§6 那条「三者互不相同」的用例必须真实存在并通过）
- [ ] 提交：`feat(tools): ds_health、Resources 与用量统计`
