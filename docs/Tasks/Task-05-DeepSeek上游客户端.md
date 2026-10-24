# Task-05 · DeepSeek 上游客户端

> **执行方式**：2 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 一次 HTTP 往返 + 严格解析。**不做重试、不做限流、不碰缓存** |
| 依赖 | Task-02（domain）、Task-03（config）、Task-04（logging） |
| 产出 | `internal/deepseek` 覆盖率 ≥ 70%；**14 个 2xxx 状态映射 + 2998/2999 两个兜底**全部正确 |
| 必读 | `Contract/23-上游能力实测报告.md` §二 §五 §六（上游事实唯一真源）；`Contract/12-数据契约` §一（请求/响应/错误信封）；`Contract/14-错误契约` §1.2 §1.3 §2.2 §2.3 §2.5；`Contract/11-接口契约` §一 §三；`Decisions/ADR.md` ADR-015 ADR-016 |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | HTTP 客户端与请求构造 | `ori_question=""` → 400；`localization=""` → 200 规则相反；**网络错误若不当场包成 2998，会裸传到 `ToPayload` 变成不可重试** |
| A-02 | 响应解析与错误解析 | 上游错误信封**没有 `param` 字段**；`request_id` 必须提取；**`error.code` 恒为 `invalid_request_error`，MUST NOT 用它判类型** |

---

## 原子任务 A-01 · HTTP 客户端与请求构造
### 0 前置

- Task-02（domain）、Task-03（config）、Task-04（logging） 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

两个端点：`POST /v1/tools/search`、`POST /v1/tools/fetch`。请求体必须用**严格 struct** 序列化，MUST NOT 用 `map[string]any` —— 因为上游会**静默忽略未知字段**（实测），拼错键名不会报错，只会静默返回错误结果。

三个实测反直觉的序列化规则：

| 字段 | 规则 |
|---|---|
| `ori_question` | 只允许 `null` 或省略。传 `""` → 400 `must be between 1 and 128` |
| `localization` | 传 `""` → **200**（与上条相反） |
| `count` | 显式传 `null` → 400 `expected u32` |

### 2 要创建的文件

`internal/deepseek/client.go`、`internal/deepseek/request.go`、`internal/deepseek/transport.go`、`internal/deepseek/client_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 实现 `Client` 接口的三个方法（`Search` / `Fetch` / `Ping`） |
| 2 | `http.Transport` 分层超时：`DialContext` 用 `cfg.Dial`、`TLSHandshakeTimeout` 用 `cfg.TLSHandshake`、`ResponseHeaderTimeout` 用 `cfg.ResponseHeader`（字段定义见 `11-接口契约` §三） |
| 2.1 | 🔴 **单次尝试的总闸门由本包执行**：`do()` 第一件事 `ctx, cancel = context.WithTimeout(ctx, cfg.PerAttempt)`。Resilience 只管「试几次、隔多久」，MUST NOT 管单次多久 |
| 2.2 | 🔴 为什么 2.1 不能省：只有分层超时时，`ResponseHeaderTimeout` 触发后上游**慢速滴流 body** 完全无保护。实测上游有 30s 网关硬超时（23 号 §4.6），但那是上游的行为，不是我方的保证 |
| 3 | `MaxResponseBytes` 用 `io.LimitReader(body, cfg.MaxResponseBytes+1)`；读满 `max+1` 字节 ⇒ **判定为溢出**，返回 `2999`，`Detail` 写明「响应体超过 N 字节上限，已截断」 |
| 3.1 | 🔴 为什么是 `max+1` 不是 `max`：`LimitReader(r, max)` 在「恰好 max 字节的合法 JSON」与「被截断的 max 字节」两种情况下返回**完全相同的数据**，无法区分。多读 1 字节即可判定 |
| 4 | 请求头：`Authorization: Bearer <key>`、`Content-Type: application/json`、`User-Agent`（`cfg.UserAgent` 为空时用 `BuildInfo` 拼 `DeepSeekSearch/<version>`；**版本号 MUST NOT 写进配置文件**，否则违反 Task-01 的「三处同源」铁律） |
| 5 | `Search` 请求体用 `searchRequest` struct，`count`/`need_content`/`ori_question` 用指针 |
| 6 | `ori_question` 为空串时 MUST 置 nil（omitempty 生效），MUST NOT 发 `""` |
| 7 | `localization` 为空串时 MUST 省略 |
| 8 | 🔴 **`Ping` 发 `GET {base_url}/v1/tools/search`，把 HTTP 405 判定为健康**（ADR-015）。MUST NOT 用 `GET /` —— 它返回 404 空体，无法区分「网络通但 base_url 配错」，而 base_url 配错恰是最常见的部署事故 |
| 8.1 | `Ping` 判定表：**405** → nil（健康）；**401** → error 指向 Key 失效（3040/2401）；**404** → error 明确提示「检查 `[upstream].base_url` 是否以 `/v1` 结尾且拼写正确」；其它 4xx/5xx → error 带上游状态码；网络错误 → error 带 2998 |
| 8.2 | `Ping` **不计费**：405 的响应体为空，不满足「HTTP 200 且拿到内容」的计费条件（23 号 §2.2 实测）。MUST NOT 改用 `GET /v1/models` —— 它虽免费但属 chat 侧端点，依赖 Key 有 chat 权限，受限 Key 会**误报不可达** |
| 9 | API Key MUST NOT 出现在任何 error 字符串里（`Ping` 之外的请求失败要先 scrub） |
| 9.1 | 🔴 **上游 message 会回显 Key 后 4 位**（实测 401：`Your api key: ****0000 is invalid`）。`upstream_msg` 在返回与落日志前 MUST 按 `14-错误契约` §2.5.1 二次脱敏：`(?i)(api[_ ]?key|token|secret|authorization)\s*[:=]?\s*\S*` → `[REDACTED]`，独立的 `sk-[A-Za-z0-9]{8,}` → `sk-***` |
| 10 | 响应体先 `io.ReadAll` 再 `json.Unmarshal`，MUST NOT 直接 Decode（要能记录原始 body 长度） |
| 11 | 🔴 **`do()` MUST 把网络层失败当场包成 2998**：`net.Error` / `context.DeadlineExceeded` / `io.ErrUnexpectedEOF` / `io.EOF` → `domain.NewNetworkError(cause, attempt, err)`，原始错误放 `cause error` 保留 |
| 11.1 | `cause` 取值（进 `details.cause`，见 14 号 §2.2 表注）：DNS 解析失败 → `dns`；连接被拒/不可达 → `tcp_connect`；TLS 失败 → `tls_handshake`；`ctx` 因 `PerAttempt` 到期 → `attempt_timeout`；已收到响应头但读体中断 → `response_reset` |
| 11.2 | 🔴 为什么 11 是 P0：不包的话裸 `net.Error` 会穿到 `ToPayload` 落进兜底码，被标成**不可重试** —— 于是 `14-错误契约` §六 承诺的「网络错误 ✅ 可重试」永不生效，一次 DNS 抖动就直接失败给用户。见 ADR-016 |
| 12 | `attempt` 序号由 Resilience 通过 `ctx` 传入：`domain.AttemptFromContext(ctx)`（缺省 1），配套 `domain.ContextWithAttempt(ctx, n)`。本包 MUST NOT 自己维护计数器 —— 那是有状态的，违反 §4 禁止事项 1 |
| 12.1 | 🔴 **这对函数 MUST 放 `domain`，不能放 `resilience`**：`10-包依赖矩阵` §2.4 明确禁止 `deepseek` import `resilience`（否则 deepseek↔resilience 互相依赖，Resilience 包裹 Client 就成环）。`domain` 是二者共同的叶子依赖，是唯一合法落点 |
| 13 | **本包 MUST NOT 持有 Logger**（`10-包依赖矩阵` 禁止 `deepseek → logging`）。报文日志通过 ctx 感知的 `ExchangeHook` / `WithExchangeHook` 实现，由 Tools 层关联 `call_id`；`DEBT-009` 已于 2026-10-21 关闭 |
| 13.1 | `log.max_body_log_kb` 控制报文日志开关与截断（默认 `0` = 关）；开启后 `req_body`/`resp_body` 进 `call.upstream.done`，`ds_fetch` 的 URL MUST 先经 `domain.RedactURL` |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 在本包做重试（resilience 层的职责） |
| 2 | MUST NOT 在本包读写缓存 |
| 3 | MUST NOT 用 `map[string]any` 构造请求体 |
| 4 | MUST NOT 让 `http.Client` 用默认 `Timeout` 替代 `PerAttempt`（默认 0 = 永不超时）。分层超时与 `PerAttempt` **两者都要**，职责不同 |
| 5 | MUST NOT 在错误消息里拼 `cfg.ApiKey` |
| 6 | MUST NOT 在本包做「多次尝试」——`attempt` 只从 `ctx` 读，MUST NOT 有循环或计数器字段 |
| 7 | MUST NOT 把裸 `net.Error` / `context.DeadlineExceeded` 直接 return（必须包成 2998，见 §3 第 11 条） |

### 5 参考骨架

```go
// client 是 Client 接口的实现。它只负责「一次 HTTP 往返 + 解析」，
// 重试/限流/缓存全部在外层，所以这里的代码必须保持无状态。
type client struct {
    cfg  Config
    hc   *http.Client
    base *url.URL
}

// Search 调用 POST /v1/tools/search。
// 上游对 query 长度按 rune 计 1~128；超长会 400 且不计费，但我们仍在
// Tools 层先校验一次，因为少一次 12 秒的往返比省一次校验值钱得多。
func (c *client) Search(ctx context.Context, req domain.SearchRequest) (domain.SearchResponse, error) {
    body := toSearchBody(req)
    raw, status, err := c.do(ctx, "/v1/tools/search", body)
    if err != nil {
        return domain.SearchResponse{}, err
    }
    if status != http.StatusOK {
        return domain.SearchResponse{}, parseUpstreamError(status, raw)
    }
    var resp domain.SearchResponse
    if err := json.Unmarshal(raw, &resp); err != nil {
        return domain.SearchResponse{}, domain.NewUpstreamParseError(len(raw), err)
    }
    return resp, nil
}

// toSearchBody 把对外请求转成上游请求体。
// 三个指针字段的规则各不相同（实测）：ori_question 空串必须 nil、
// localization 空串直接省略、count 为 0 表示不传。
func toSearchBody(req domain.SearchRequest) searchRequest
```

### 6 测试代码（用 `httptest.Server`）

| # | 用例 | 期望 |
|:-:|---|---|
| 1 | 正常 200 | 解析出 10 条 |
| 2 | `ori_question=""` | 请求体里**没有** `ori_question` 键 |
| 3 | `count=0` | 请求体里**没有** `count` 键 |
| 4 | `localization=""` | 请求体里没有 `localization` 键 |
| 5 | 上游返回 500 | 返回 `*domain.UpstreamError`，`Code == 2500` |
| 6 | 响应体非 JSON | 返回 `2999` |
| 7 | 响应体**恰好** `MaxResponseBytes` 字节的合法 JSON | 解析成功（**不是** 2999）—— 这条专治 `LimitReader(max)` 与 `LimitReader(max+1)` 的差异 |
| 8 | 响应体 = `MaxResponseBytes + 1` | 返回 `2999`，`Detail` 含「已截断」，不 OOM |
| 9 | 请求头 | `Authorization` 与 `User-Agent` 存在且格式正确 |
| 10 | `cfg.UserAgent == ""` | 发出的 UA = `DeepSeekSearch/<BuildInfo.Version>` |
| 11 | 🔴 DNS 失败（host 指向不存在的域名） | `Code == 2998`，`IsRetryable == true`，`details.cause == "dns"` |
| 12 | 🔴 连接被拒（`httptest` server 已 Close） | `Code == 2998`，`details.cause == "tcp_connect"` |
| 13 | 🔴 `PerAttempt` 到期（handler `time.Sleep` 超过它） | `Code == 2998`，`details.cause == "attempt_timeout"`，**MUST NOT** 是 3020（3020 是整链预算，归 Tools 层） |
| 14 | 🔴 响应头已到但 body 读一半断开（handler 写 `Content-Length: 100` 后只写 10 字节就 Hijack 关闭） | `Code == 2998`，`details.cause == "response_reset"` |
| 15 | 🔴 `Ping` 得到 **405** | 返回 nil（健康） |
| 16 | 🔴 `Ping` 得到 **404** | 返回 error，文本含 `base_url` |
| 17 | 🔴 `Ping` 得到 **401** | 返回 error，文本**不含** Key 的任何片段 |
| 18 | 🔴 401 的 message 含 `Your api key: ****0000 is invalid` | `upstream_msg` 里该片段已替换为 `[REDACTED]`；断言输出中 `0000` **不出现** |
| 19 | `attempt` 从 ctx 传入 3 | `details.attempt == 3`；ctx 无值时 == 1 |

> 用例 11~14 是 **ADR-016 的落地证据**：它们 MUST 断言 `IsRetryable(err) == true`。若某条断言失败，说明网络错误又落回兜底码了 —— 这正是「§六 承诺的可重试永不生效」那个 bug 的复现方式。

### 7 验收命令与期望输出

```bash
$ go test ./internal/deepseek/ -v -run TestClient
--- PASS: TestClient (0.01s)
    --- PASS: TestClient/正常解析10条
    --- PASS: TestClient/ori_question_空串不发
    --- PASS: TestClient/count_为零不发
    --- PASS: TestClient/500_映射为_2500
```

### 8 完成定义（DoD）

- [ ] 19 个用例全绿（`go test ./internal/deepseek/ -run TestClient -v`）
- [ ] 🔴 用例 11~14 的 `IsRetryable == true` 断言全绿（ADR-016 落地证据）
- [ ] 请求体里零 `map[string]any`（`grep -c 'map\[string\]any' internal/deepseek/*.go` = 0，测试文件除外）
- [ ] `grep -c 'time/tzdata' internal/deepseek/*.go` = 0（本包不碰时区，`tzdata` 只在 `cmd/` 导入）
- [ ] `go test ./internal/deepseek/ -race -count=1` 全绿
- [ ] 提交：`feat(deepseek): 上游客户端与请求构造`

---

## 原子任务 A-02 · 响应解析与错误解析
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

上游错误信封实测形状（**与官方文档有差异**，23 号 §五）：

```json
{"error":{"message":"'query' must not be empty (request_id: c7f28c86-...)","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}
```

三处与官方文档不同：

| 字段 | 官方文档 | 实测 | 我方处理 |
|---|---|---|---|
| `param` | 有，指示出错字段 | **恒为 `null`** | struct 里 MUST 是 `*string` 且能缺省 |
| `code` | 未明确 | 401/404/429 上**一律是 `invalid_request_error`** | 🔴 **MUST NOT 用它判类型**，只按 HTTP status 映射 |
| `request_id` | 独立字段 | **不是字段**，只在 `message` 文本尾部 `(request_id: <uuid>)` | 正则提取，是唯一对账凭据（上游账单只有 request_id） |

实测 `type`/`code` 全表（见 `14-错误契约` §2.5）：400→`invalid_request_error`/同；401→`authentication_error`/**`invalid_request_error`**；404→`not_found_error`/**`invalid_request_error`**；429→`rate_limit_error`/**`invalid_request_error`**；504→`gateway_timeout_error`/同。

🔴 **Fetch 的 404 是语义重载的（7 种成因）**：403 / 404 / 500 / 域名不存在 / 内网 IP / 二进制内容 全部塌缩成同一个 `404 failed to fetch URL`；实测另有两类：① **主流站点约 40% 抓不到**（`en/zh.wikipedia.org`、`zhihu.com`、`news.ycombinator.com`、`reddit.com`、`raw.githubusercontent.com`，23 号 §4.3）；② **`base_url` 配错时 `ds_search` 也返回 404**，语义与 fetch 完全不同。

⇒ `hint` MUST **按工具分流**：`parseUpstreamError` 需要知道出错的是 search 还是 fetch（由调用方传入 `tool string` 参数），两套文案见 `14-错误契约` §2.3.1。

### 2 要创建的文件

`internal/deepseek/parse.go`、`internal/deepseek/error.go`、`internal/deepseek/parse_test.go`、`internal/deepseek/testdata/*.json`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | `parseUpstreamError(status int, raw []byte, tool string) *domain.UpstreamError`（`tool` ∈ `ds_search`/`ds_fetch`，用于 2404 的 hint 分流） |
| 2 | 🔴 映射规则**四条**（完整理由见 `14-错误契约` §2.2 表注）：① 已枚举的 11 个状态 → `Code = 2000 + status`；② 未枚举的 **5xx** → `2500`（✅ 可重试）；③ 未枚举的 4xx **与一切 1xx/3xx** → `2000`（❌ 不可重试 —— 3xx 走到这里说明重定向已耗尽，重试必然拿到同一个重定向）；④ **`status == 0`（拿不到响应）→ `2998`**。真实状态码一律由 `upstream_http` 带出，不因兜底而丢失 |
| 2.1 | 🔴 MUST NOT 读 `error.code` 或 `error.type` 做分支 —— 401/404/429 的 `code` 实测全是 `invalid_request_error`（见 §1）。二者的值只原样记入日志字段 `upstream_type` 供人工排查（`error.type` 存入 `domain.UpstreamError.UpstreamType`，调用方用 `domain.UpstreamTypeOf` / `domain.AppendUpstreamType` 读取与拼字段） |
| 3 | `request_id` 正则：`\(request_id:\s*([0-9a-fA-F-]{8,64})\)`。提取失败时 `RequestId` 留空，MUST NOT 报错 |
| 4 | `Error()` 输出格式固定：`upstream: <code> <NAME> (http <status>, request_id=<id>)`，便于日志解析。**MUST NOT** 含 Key 的任何片段 |
| 5 | 2404 的 `Hint` MUST 按 `tool` 分流：`ds_fetch` 列 7 种成因（含实测抓不到的站点清单）；`ds_search` 指向 `base_url` 配错。两套文案逐字取自 `14-错误契约` §2.3.1 |
| 6 | 2402 的 `Hint` MUST 明确「余额不足，重试无用」 |
| 7 | 响应体为空但 status=200 → `2999` |
| 7.1 | 🔴 `2999` 的成因已收窄为**唯一一种**：HTTP 200 且 `json.Unmarshal`/字段校验失败。「未知错误」一律归 `3099 LOC_INTERNAL`（那是 `domain.ToPayload` 的职责，本包不产生 3099）。见 ADR-016 |
| 8 | `results` 为 `[]` 时 MUST 返回空切片 + nil error（不是错误）。实测零结果就是 `200 {"results":[]}` |
| 9 | `date` 为 null、`authority` 为 -1、`title` 为空串、`site_name` 为 null（实测 18% 概率）→ 都必须能解析不崩 |
| 9.1 | 🔴 `date` 实测 **20% 为 null**（10/50 样本），非 null 时时间部分**恒为 `16:00:00+00:00`**（40/40）。本包只负责「解析成 `*string` 原样交给 domain」，时区转换是 `domain.ParseDate` 的职责，MUST NOT 在这里做 |
| 10 | 🔴 黄金文件放 `testdata/`，**MUST 是合法 JSON**。采集元数据（时间、来源 URL、脱敏说明）写在**同名 `.meta` sidecar 文件**里，MUST NOT 写进 JSON 首行注释 |
| 10.1 | 为什么：JSON **没有注释语法**。首行加 `# captured: ...` 会让文件变成非法 JSON，`os.ReadFile` + `json.Unmarshal` 直接失败 —— 于是实现者要么手写剥离逻辑（每个测试都重复一遍），要么改用 `//go:embed` 后再 `strings.TrimPrefix`（更糟）。sidecar 是唯一干净解 |
| 10.2 | 命名约定：`testdata/search_ok_20261020.json` + `testdata/search_ok_20261020.meta`。`.meta` 是纯文本 `key: value` 行 |
| 11 | 上游 `error.message` MUST 原样保留（脱敏后）到 `UpstreamError.UpstreamMsg`，截断到 512 rune（不是 byte，中文会被切半） |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 在解析时对 `results` 做任何过滤/排序（那是 domain 的事） |
| 2 | MUST NOT 假设 `error.param` 存在 |
| 3 | MUST NOT 用 `strings.Contains(errMsg, "rate limit")` 判断错误类型 —— MUST 按 status 映射 |
| 4 | MUST NOT 手写假 JSON 当黄金文件（必须真实抓取） |
| 5 | MUST NOT 在黄金文件里保留真实 `request_id`（替换成全零 UUID） |
| 6 | MUST NOT 在黄金 `.json` 里写注释行 —— 采集信息放 `.meta` sidecar（见 §3 第 10 条） |
| 7 | MUST NOT 用 `error.code` / `error.type` 做错误分类（见 §3 第 2.1 条） |
| 8 | MUST NOT 在本包做 Markdown 反转义 —— 那是 `domain.Unescape` 的职责，且**只对 fetch 的 content 生效**（search 侧不转义，见 12 号 §6.3） |

### 5 参考骨架

```go
var requestIDRe = regexp.MustCompile(`\(request_id:\s*([0-9a-fA-F-]{8,64})\)`)

// upstreamError 是上游错误信封。Param 必须可缺省 ——
// 官方文档写了这个字段，但实测 100% 不存在（6 次抓取全部没有）。
type upstreamError struct {
    Error struct {
        Message string  `json:"message"`
        Type    string  `json:"type"`
        Param   *string `json:"param"`
    } `json:"error"`
}

// codeForStatus 把上游 HTTP 状态映射到我方错误码。
// 🔴 规则不是单纯的「2000 + status」—— ADR-007 的公式只对**已枚举**的状态成立，
//    未枚举的必须由兜底吸收，否则会算出「2000 + 该状态」这种不在冻结码表（42 条）里的码。
//    四条分支的完整理由见 14-错误契约 §2.2 的表注，此处只写结论：
//      status == 0        → 2998（根本没拿到响应，不是上游故障也不是状态码问题）
//      已枚举的 11 个     → 2000 + status
//      其他 5xx           → 2500（✅ 可重试：5xx 是上游故障，重试有意义）
//      其他一切（4xx/3xx/1xx）→ 2000（❌ 不可重试）
// 🔴 3xx MUST 归 2000 而不是 2500：走到这里说明重定向已耗尽，重试必然拿到同一个
//    重定向，归 2500 只会白烧 max_attempts 次调用与退避时间。
// 🔴 真实状态码不丢：一律由 UpstreamError.UpstreamHttp 带出去（14 号 §三），
//    code 只承担「该怎么处置」的职责，502 与 500 依然可区分。
func codeForStatus(status int) int {
    if status == 0 {
        return domain.CodeUpNetwork // 2998
    }
    switch status {
    case 400, 401, 402, 403, 404, 408, 413, 429, 500, 503, 504:
        return 2000 + status
    }
    if status >= 500 && status <= 599 {
        return domain.CodeUpServer // 2500：未枚举 5xx 的兜底
    }
    return domain.CodeUpUnexpected // 2000：未枚举 4xx 与一切 1xx/3xx
}
```

```go
// do 是唯一发请求的地方。它的错误处理顺序就是 14 号 §1.3 的判定顺序在本包的落地：
// 「拿不到响应」当场包成 2998，MUST NOT 让裸 net.Error 逃出去。
func (c *client) do(ctx context.Context, path string, body any) (raw []byte, status int, err error) {
    ctx, cancel := context.WithTimeout(ctx, c.cfg.PerAttempt) // §3 第 2.1 条
    defer cancel()

    attempt := domain.AttemptFromContext(ctx) // §3 第 12 条，缺省 1（MUST NOT 用 resilience.*，见 12.1）
    resp, err := c.hc.Do(req)
    if err != nil {
        // 🔴 这一整个分支是 ADR-016 的核心。漏掉它 = 「网络错误可重试」成为空话。
        return nil, 0, domain.NewNetworkError(classifyNetErr(ctx, err), attempt, err)
    }
    defer resp.Body.Close()

    raw, err = io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxResponseBytes+1)) // §3 第 3 条
    if err != nil {
        return nil, resp.StatusCode, domain.NewNetworkError("response_reset", attempt, err)
    }
    if int64(len(raw)) > c.cfg.MaxResponseBytes {
        return nil, resp.StatusCode, domain.NewUpstreamParseError(len(raw), errResponseTruncated)
    }
    return raw, resp.StatusCode, nil
}

// classifyNetErr 把 net.Error 归到 5 个 cause 之一（§3 第 11.1 条）。
// ctx 已到期 ⇒ attempt_timeout（我方主动取消），否则按错误类型分。
func classifyNetErr(ctx context.Context, err error) string
```

### 6 测试代码

| # | 用例 | 期望 |
|:-:|---|---|
| 1 | 400 + `must not be empty` | `Code=2400`，`RequestId` 已提取 |
| 2 | 402 | `Code=2402`，`Retryable=false` |
| 3 | 404 + `tool=ds_fetch` | `Code=2404`，`Hint` 含 **7 种成因**且含 `维基百科`（实测失败站点之一） |
| 4 | 🔴 404 + `tool=ds_search` | `Code=2404`，`Hint` 含 `base_url`，**不含** fetch 的站点清单 —— 两条 hint MUST 不同，否则分流没生效 |
| 5 | 429 | `Code=2429`，`Retryable=true` |
| 6 | 504 | `Code=2504`，`Retryable=true` |
| 7 | 418（未枚举 4xx） | `Code=2000` |
| 8 | 502（未枚举 5xx） | `Code=2500`、`Retryable=true`、`UpstreamHttp=502` |
| 8.1 | 🔴 501 / 507（未枚举 5xx） | 同样 `Code=2500`，且 `UpstreamHttp` 保留真值（502 与 500 MUST 可区分） |
| 8.2 | 🔴 **302（重定向耗尽）** | `Code=2000`、`Retryable=**false**` —— 归 2500 会让重试白烧 `max_attempts` 次拿到同一个重定向（§A-02 第 2 条 ③） |
| 8.3 | 🔴 100（1xx 异常） | `Code=2000`、`Retryable=false`（与 3xx 同一分支） |
| 9 | 🔴 `status=0`（`codeForStatus` 直调） | `Code=2998`，`Retryable=true` |
| 10 | 错误体无 `error` 键 | `Code=2999` |
| 11 | 🔴 错误体的 `code` 是 `invalid_request_error` 但 status 是 429 | `Code=2429`（**按 status，不按 code**）—— 这条专治 §3 第 2.1 条 |
| 12 | `results: []` | 空切片 + nil error |
| 13 | `date: null` / `authority: -1` / `title: ""` / `site_name: null` | 解析成功，零 panic |
| 14 | 错误消息里含真实 Key 形态串 | 输出里被替换为 `[REDACTED]`；断言原文 **不出现** |
| 15 | `error.message` 长 800 rune | `UpstreamMsg` 截到 512 **rune**，且末字符不是半个汉字（用 `utf8.ValidString` 断言） |
| 16 | 🔴 `testdata/*.json` 全部可被 `json.Unmarshal` | 遍历目录断言 —— 这条防止有人再把 `# captured:` 写回 JSON 首行 |
| 17 | 每个 `.json` 都有同名 `.meta` | 遍历目录断言 |

### 7 验收命令与期望输出

```bash
$ go test ./internal/deepseek/ -cover
coverage: 7x.x% of statements

$ go test ./internal/deepseek/ -v -run TestParse
--- PASS: TestParseUpstreamError (0.00s)
    --- PASS: TestParseUpstreamError/404_fetch_含7种成因
    --- PASS: TestParseUpstreamError/404_search_指向base_url
    ...
--- PASS: TestParseSearchResponse (0.00s)
```

### 8 完成定义（DoD）

- [ ] 覆盖率 ≥ 70%（`go test ./internal/deepseek/ -cover`）
- [ ] 🔴 20 个用例全绿（原写 17；本轮补了 501/507、302、100 三条映射用例，数字 MUST 等于 §6 表的数据行数）
- [ ] 🔴 `testdata/` 下每个 `.json` 都能 `json.Unmarshal`（用例 16），且都有同名 `.meta`（用例 17）
- [ ] 🔴 用例 4 断言「search 与 fetch 的 2404 hint 不同」通过 —— 这是 §3 第 5 条分流的唯一自动化证据
- [ ] `grep -rn 'error.code\|\.Code ==' internal/deepseek/parse.go` 无按上游 `code` 字段分支的代码
- [ ] `go test ./internal/deepseek/ -race -count=1` 全绿
- [ ] 提交：`feat(deepseek): 响应解析与错误映射`
