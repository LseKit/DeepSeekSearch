# Task-09 · Tools：`ds_fetch`

> **执行方式**：2 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 抓取网页正文，含 SSRF 黑名单、快照缓存、按标题分节、Markdown 反转义、静默截断标记 |
| 依赖 | Task-02 ~ Task-08 |
| 产出 | `internal/tools` 覆盖率 ≥ 70%；568K 字符的页面不会一次塞进上下文 |
| 必读 | `Contract/12-数据契约` §1.3、§1.5、§4.2、§五、§六、§七；`Contract/14-错误契约` §2.3 + §2.3.1（2404 的 7 种成因） |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | schema、SSRF 校验、快照 ID | `cache_id` MUST 绑快照不绑 URL |
| A-02 | handler 编排（分节 / 反转义 / 截断标记 / 包裹） | 按 byte 切出乱码；反转义毁掉代码块 |

---

## 原子任务 A-01 · schema、SSRF 与快照 ID
### 0 前置

- Task-02 ~ Task-08 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

`ds_fetch` 有四个反直觉的实测约束：

| 约束 | 事实 |
|---|---|
| 无 `offset` | 上游只收 `url`，没有分页/续读参数。分节是我们自己做的 |
| 🔴 **上游不截断，而是 504** | 旧文档称「上游静默截断，同一 URL 5 并发返回 5 种长度（168K~429K）」—— **已被 23 号 §4.6/§4.7 推翻**。实测并发冷抓取 go.dev：5 个请求中 3 个 200 且**内容 sha256 完全相同（105,162 runes）**、2 个 **504 精确在 30.126s/30.131s**。真正的失败模式是**上游 30s 网关超时**，不是内容长度不确定 |
| 🔴 **正文可以非常大** | RFC 9110 实测 **437,738 runes / 439,285 bytes**（完整、末尾 `Germany`，未截断）；HTML spec 281,905；pkg.go.dev/net/http 139,685。⇒ 分节与预算是**我方**的责任，不是「上游截断后我方兜底」 |
| 转义 Markdown | **32 种**字符被加反斜杠（ADR-013，23 号 §4.5）：`/ # . , : { } ( ) - ! * \| = _ ; [ ] < > " % ? & @ ` + "`" + ` ~ $ ^ \\`。旧文档的 11 字符表**缺 21 种** |
| 🔴 **只有 fetch 转义** | search 侧的 content/snippet/summary/title **不转义**（23 号 §4.5.2）。对 search 结果调 `Unescape` 会把代码块里的字面量 `\n` 变成 `n` |

`cache_id` 必须**绑定一次具体抓取**，不能绑 URL。否则 TTL 过期后重新抓，同一个 `cache_id` 会返回完全不同的内容，而模型以为读的是同一份文档。

### 2 要创建的文件

`internal/tools/fetch_validate.go`（调用 toolvalidate）、`internal/tools/fetch_handler.go`、`internal/tools/fetch_render.go`、`internal/toolvalidate/ssrf.go`、`internal/toolschema/schema.go`、`internal/tools/fetch_validate_test.go`

### 3 实现要求

#### 3.1 schema

| # | 要求 |
|:-:|---|---|
| 1 | 🔴 **参数恰好 7 个**：`url` `cache_id` `section` `sections` `max_chars` `unescape` `no_cache`（`no_cache` 与 `cache_id` 同传 MUST 拒绝） |
| 2 | `no_cache` 已按 DEBT-003 落地（2026-10-21）：`true` 时跳过读缓存但写回；与 `cache_id` 同传 MUST 拒绝（`cache_id` 本身就是读缓存） |
| 3 | `url` 与 `cache_id` 二选一（`anyOf` 禁用 → 用两个可选字段 + 运行时校验） |
| 4 | `max_chars` 1~200000，默认 32000（0 = 用配置默认值） |
| 5 | `unescape` 默认 **true** |
| 6 | `Title` = `"DeepSeek 网页抓取"` |
| 7 | 三项 annotations 同 `ds_search` |
| 8 | description 必须说明三种模式（inline/outline/section）与 `cache_id` 的过期语义 |

#### 3.2 SSRF 校验（写在 `internal/toolvalidate/ssrf.go` 内）

| # | 要求 |
|:-:|---|---|
| 1 | 拒绝非 http/https scheme |
| 2 | 拒绝含 `user:pass@` 的 URL |
| 3 | 拒绝 URL > 8192 字符 |
| 4 | 拒绝 host 命中黑名单（🔴 **12 个网段 + 3 个域名后缀**，与 `toolvalidate/ssrf.go` 的 `blockedNets` 逐项相同）：`0.0.0.0/8` `10.0.0.0/8` `127.0.0.0/8` `100.64.0.0/10`（CGNAT）`169.254.0.0/16` `172.16.0.0/12` `192.0.0.0/24` `192.168.0.0/16` `198.18.0.0/15` `::1/128` `fc00::/7` `fe80::/10` |
| 4.1 | 🔴 **MUST 先 `Unmap()` 再判 `Contains`**：`netip.MustParseAddr("::ffff:127.0.0.1").Is4In6()` 为真，而它对 `127.0.0.0/8` 的 `Contains` **返回 false** ⇒ IPv4-mapped IPv6 直接绕过整张黑名单。URL 里也可写成 `http://[::ffff:7f00:1]/` |
| 5 | 🔴 拒绝 IP 混淆写法，**MUST 有明确算法**（不能只靠 `netip.ParseAddr`，它对这些写法直接解析失败 ⇒ 被当成普通域名放过）：逐 label 按 8/10/16 进制解析，若整体 ≤ `0xFFFFFFFF` 则转成 `netip.Addr` 再过黑名单。测试 MUST 覆盖 ≥6 种变体（见 §6） |
| 6 | 拒绝 `*.internal` `*.local` `*.localhost`，以及 host 就是 `localhost` |
| 7 | 🔴 **威胁模型的正确表述**：抓取由 **DeepSeek 上游执行，我方进程从不直连目标 URL**（`12-数据契约` §一 的 `fetchRequest{url}` 就是明证；实测上游对 `127.0.0.1`/`10.0.0.1`/`169.254.169.254`/十进制 IP 一律返回 404）。所以这张表是**纵深防御**：防止本服务被当作攻击放大器去探测上游侧或自建代理侧的内网，**不是**「保护我方进程的出网」。旧文档 §3.2 第 7 条与 §5 骨架注释的表述都是错的 |
| 7.1 | `base_url` 被改成自建代理时，本黑名单**尤其重要** —— 那时代理就在我方可控网络里，纵深防御变成第一道防线 |
| 8 | 命中一律返回 `1070`，`hint` 说明原因 |
| 8.1 | 🔴 命中时 MUST 记一条 `ssrf.blocked` 日志，但 **`url` 字段 MUST 先过 `domain.RedactURL(raw, cfg.Log.RedactQueryKeys)`**（函数与用例由 `Task-04` §3 要求 11/12 提供，本任务只是调用点）。<br>**为什么这条是安全项而不是风格项**：ds_fetch 抓的 URL 常带凭据 —— OAuth 回调 `?code=`、签名下载链 `?sig=`/`?token=`、临时分享链。原样落盘等于把**别人的**凭据写进保留 7 天的日志；而 `ssrf.blocked` 恰恰是最容易带这类 URL 的事件（攻击者探测时用的就是带 token 的内网链接）。<br>`log.redact_query_keys` 默认 7 项：`token/key/secret/password/code/sig/auth`（`13-配置契约` §3.11） |
| 8.2 | 🔴 同一规则适用于 A-02 里**所有**记目标 URL 的日志点（`call.begin` 的 `url`、`call.upstream` 的 `url`、错误上下文字段）。MUST NOT 只在 `ssrf.blocked` 一处脱敏 —— 漏一处等于没脱敏，而漏掉的那处往往正是成功路径（日志量最大、最容易被翻到） |
| 9 | 🔴 **实测上游已自带校验**（23 号 §4.2）：`file://`/`ftp://` → 400 `'url' must be an HTTP or HTTPS URL with a host`；`user:pass@` → 400 `'url' must not contain credentials`；>8192 → 400。我方仍 MUST 自己校验（第 1~3 条）—— 在上游之前就拒绝，省一次往返、且错误码是我方的 1040/1050/1060 而非笼统的 2400 |

#### 3.3 快照 ID

| # | 要求 |
|:-:|---|---|
| 1 | `cache_id` 格式 `snap_<16 hex>`，内容 = `sha256(上游原始 URL + 抓取时间戳秒)` 前 16 字符 |
| 2 | 同一 `cache_id` 在整个 TTL 内返回**同一份快照** |
| 3 | 快照过期/不存在 → `3080 LOC_SNAPSHOT_GONE` + `hint`「该快照已过期，请重新 fetch」 |
| 4 | 🚫 MUST NOT 静默用新内容替代旧快照 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用「先 `net.LookupIP` 再连接」的模式做 SSRF 判定 —— 解析与连接之间 DNS 可变（TOCTOU）。我方只对**字面量 host** 判断 + 依赖上游侧防护 |
| 1.1 | 🔴 **但 `SECURITY.md` MUST NOT 把「DNS 重绑定绕过私网黑名单」列为受理的漏洞**（`Task-13` §SECURITY.md 骨架原文列了它）。设计明确不做 DNS 解析，安全政策却承诺防它 ⇒ 必然招来无法闭环的报告。二选一：**① 维持不解析，把 DNS 重绑定移入「哪些不算安全问题」并说明「抓取由上游执行，我方不直连」；② 改用 `net.Dialer.Control` 在实际连接 IP 上二次校验**（这才是无 TOCTOU 的正解 —— 原禁令把「先 Lookup 再连接」这个弱方案当成唯一方案否定了）。裁决：**取 ①**，理由见 `Task-13` |
| 2 | MUST NOT 允许 `file://` / `gopher://` / `data:` |
| 3 | MUST NOT 让 `cache_id` = hash(url) |
| 4 | MUST NOT 加 `offset` 参数（上游不支持，见 `16-技术债` S2） |
| 5 | `no_cache=true` MUST NOT 读缓存、MUST NOT 参与 singleflight，且结果 MUST 写回缓存 |
| 6 | 🔴 MUST NOT 把 URL 原文写进任何日志字段 —— query 值 MUST 先经 `domain.RedactURL` 按 `log.redact_query_keys` 脱敏（§3.2 第 8.1/8.2 条）。这条对 `ssrf.blocked`、`call.begin`、`call.upstream` 与错误上下文**一律适用**，不存在「只在被拦的时候脱敏」这种半套做法 |

### 5 参考骨架

```go
// blockedNets 是 SSRF 黑名单。🔴 定位是**纵深防御**，不是唯一防线：
// 抓取由 DeepSeek 上游执行，我方进程从不直连目标 URL（实测上游对 127.0.0.1/
// 10.0.0.1/169.254.169.254/十进制 IP 一律 404，见 23 号 §4.2）。
// 这张表存在的意义是：① base_url 被改成自建代理时成为第一道防线；
// ② 不让本服务沦为攻击放大器；③ 用我方错误码（1070）而非笼统的 2400 回应。
var blockedNets = []netip.Prefix{
    netip.MustParsePrefix("0.0.0.0/8"),      // 「本网络」，含 0.0.0.0 自身
    netip.MustParsePrefix("10.0.0.0/8"),
    netip.MustParsePrefix("127.0.0.0/8"),
    netip.MustParsePrefix("100.64.0.0/10"),  // CGNAT（RFC 6598），云厂商内网常用
    netip.MustParsePrefix("169.254.0.0/16"), // 链路本地 + 云元数据 169.254.169.254
    netip.MustParsePrefix("172.16.0.0/12"),
    netip.MustParsePrefix("192.0.0.0/24"),   // IANA 保留
    netip.MustParsePrefix("192.168.0.0/16"),
    netip.MustParsePrefix("198.18.0.0/15"),  // 基准测试保留
    netip.MustParsePrefix("::1/128"),
    netip.MustParsePrefix("fc00::/7"),       // IPv6 ULA
    netip.MustParsePrefix("fe80::/10"),      // IPv6 链路本地
}

// 🔴 addrIsBlocked MUST 先 Unmap()：::ffff:127.0.0.1 对 127.0.0.0/8 的
// Contains 返回 **false**，不 Unmap 就等于给整张黑名单开了一个后门。
func addrIsBlocked(a netip.Addr) bool {
    a = a.Unmap()
    for _, n := range blockedNets {
        if n.Contains(a) {
            return true
        }
    }
    return false
}

// 🔴 parseObfuscatedIP 处理「十进制/八进制/十六进制 IP」。
// netip.ParseAddr("2130706433") 会**解析失败**，若就此放过，它会被当成普通域名
// 交给上游 —— 实测上游返回 404，但我方 MUST NOT 依赖上游的行为。
// 算法：逐 label 按 8/10/16 进制解析，label 数 1~4，整体 ≤ 0xFFFFFFFF 则转 netip.Addr。
func parseObfuscatedIP(host string) (netip.Addr, bool)

// snapshotID 绑定「一次具体抓取」而不是「一个 URL」。
// 绑 URL 的话，TTL 过期后重新抓会得到同一个 id 却是完全不同的内容，
// 而模型会以为读的还是同一份文档 —— 这是最危险的一种静默错误。
func snapshotID(rawURL string, capturedAt time.Time) string
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| `http://localhost/x` | `1070` |
| `http://127.0.0.1/x` | `1070` |
| `http://169.254.169.254/latest/meta-data/` | `1070` |
| 🔴 `http://2130706433/`（十进制 = 127.0.0.1） | `1070` |
| 🔴 `http://0x7f000001/`（十六进制） | `1070` |
| 🔴 `http://017700000001/`（八进制） | `1070` |
| 🔴 `http://127.1/`（省略写法） | `1070` |
| 🔴 `http://0x7f.0.0.01/`（混合进制） | `1070` |
| 🔴 `http://0177.0.0.1/`（八进制 label） | `1070` |
| 🔴 `http://[::ffff:127.0.0.1]/`（IPv4-mapped IPv6） | `1070` —— **专治 §3.2 第 4.1 条**，不 `Unmap()` 必然漏 |
| 🔴 `http://[::ffff:7f00:1]/`（同上十六进制写法） | `1070` |
| 🔴 `http://[::1]/` | `1070` |
| 🔴 `http://0.0.0.0/` | `1070` |
| 🔴 `http://100.64.0.1/`（CGNAT） | `1070` |
| `http://192.168.1.1/` | `1070` |
| `http://example.internal/` | `1070` |
| 🔴 `http://8.8.8.8/`（公网 IP，**不该**被拦） | 放行 —— 黑名单测试 MUST 有反例，否则「全拦」也能过 |
| `ftp://example.com/` | `1040` |
| `https://u:p@example.com/` | `1050` |
| `url` 与 `cache_id` 都不给 | `1000` |
| 两个都给 | `1000` |
| `cache_id="snap_不存在"` | `3080` |
| `max_chars=0` 且配置 32000 | 生效 32000 |

### 7 验收命令与期望输出

```bash
$ go test ./internal/tools/ -v -run 'TestSSRF|TestFetchValidate|TestSnapshotID'
--- PASS: TestSSRF (0.00s)
    --- PASS: TestSSRF/十进制IP混淆
    --- PASS: TestSSRF/云元数据地址
    ...
```

### 8 完成定义（DoD）

- [ ] 🔴 23 个用例全绿（原写 13，与 §6 表的 23 行不符；数字 MUST 等于 §6 表的数据行数）
- [ ] 黑名单覆盖 🔴 **12** 个网段（与 `toolvalidate/ssrf.go` 的 `blockedNets` 12 项一致）+ 3 个域名后缀 + **6 种** IP 混淆写法 + 2 种 IPv4-mapped IPv6 + **1 个公网 IP 反例**（无反例则「全拦」也能过）
- [ ] 提交：`feat(tools): ds_fetch schema 与 SSRF 防护`

---

## 原子任务 A-02 · handler 编排
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

三件事必须做对：

1. **分节**：正文 > `inline_max_chars`（8000 **rune**）时返回目录而非全文。目录让模型「按需取」，而不是被 437,738 rune 的 RFC 塞爆。
2. **反转义**：上游把 **32 种** Markdown 字符加了反斜杠（ADR-013）。`unescape=true`（默认）时调 `domain.Unescape`；它 MUST NOT 动代码块里的 `\n` `\d` —— 因为字符类里没有 `n` 和 `d`，只脱反斜杠、不解释转义序列。
3. 🔴 **预算**：三种模式（inline / outline / section）**都**受 `max_chars` 约束。旧设计只让 inline 受限，section 与 outline 完全无上限 —— 而 `ds_search` 有 32,000 rune 硬顶。这正是本项目反复强调要防的「568K 字符塞爆上下文」，只是换了个入口。
4. 🔴 **不再有「上游截断」启发式**：旧设计的立论（并发返回 5 种长度）已被 23 号 §4.6 推翻 —— 实测并发成功响应**内容 sha256 完全相同**，真正的失败模式是 **504（30s 网关超时）**，而 504 是**独立的可重试错误**（`2504`），不需要猜。

### 2 要创建/修改的文件（在 A-01 基础上追加）

`internal/tools/fetch_handler.go`、`internal/tools/fetch_handler_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 顺序：校验 → SSRF → 熔断 → 限流 → 缓存（5 并发 singleflight）→ 上游（重试）→ 反转义 → 分节 → 截断 → 包裹 |
| 2 | 缓存 key = `NormalizeUrl(url, cfg.Dedupe.TrackingParams)` 的 FNV-1a 64 hash + `"fetch"` |
| 2.1 | 🔴 **entry 里 MUST 同时存归一化 URL 的 sha256，命中时复核**。只用 64-bit FNV-1a 当 key 意味着**碰撞即静默返回另一个 URL 的正文** —— 对 LLM 是最恶劣的错误形态：内容看起来完全正常，模型无从察觉。复核失败按未命中处理并记 `warn.cache_key_collision` |
| 3 | singleflight 生效：5 并发同 URL 只打 1 次上游 |
| 4 | 🔴 三种模式自动判定：`utf8.RuneCountInString(content) ≤ inline_max_chars` → `inline`；否则 `outline`；指定 `section` → `section` |
| 4.1 | 🔴 **MUST 用 rune 数，不能用 `len(content)`（byte）**：这与本任务 §4 禁止事项 2、`12-数据契约` §六「一律 rune 计」冲突。实测官方文档页 **6,405 runes / 9,355 bytes**、CSDN 首页 1,913/4,505（2.35×）⇒ 用 byte 判定会把 6,405 rune 的页面误推入 outline 模式（多一次往返 + 体验降级） |
| 5 | `outline` 返回：`cache_id` `total_chars` `mode="outline"` `outline_kind` `sections[]`（id/level/title/chars）+ 每节前 200 rune 预览 |
| 5.1 | 🔴 **outline 自身也 MUST 有上限**：节数 > `[limits].outline_max_sections`（默认 **200**）时只返回前 200 节并置 `warnings[]`「目录已折叠，共 N 节」。理由：pkg.go.dev/net/http 实测 139,685 rune，标题极多的文档可产生数百上千节，**outline 本身就能到 100K+ 字符**，等于用一个「目录」绕过了所有预算 |
| 5.2 | outline 的总 rune 数同样受 `max_chars` 约束，超出填 `truncated_by="budget"` |
| 6 | `section` 模式：按 id 取节全文；id 不存在 → `1090 VAL_SECTION_NOT_FOUND` |
| 7 | `sections[]` 多节合并，中间插 `\n\n---\n\n` 分隔 |
| 7.1 | 🔴 **section / sections 模式同样 MUST 应用 `max_chars`**，超出用 `domain.SliceByRune` 截断并填 `truncated_by="budget"`。旧设计（`12-数据契约` §六「`max_chars` 仅内联模式生效」）让标题稀疏的页面单节可达数十万 rune，多节合并更无上限 |
| 8 | `unescape=true` 用 `domain.Unescape`（**32 个字符**，ADR-013）；`false` 原样 |
| 9 | 正文 < `min_content_chars`（120）→ `3050 LOC_CONTENT_TOO_SHORT` |
| 10 | 🔴 `truncated_by` **只有两种取值**：我方按 `max_chars` 截断 → `"budget"`；未截断 → **不填该字段**。**删除「upstream」这一档** |
| 10.1 | 🔴 **为什么删**：旧判据是「`total_chars ∈ [100000,600000]` 且末尾不在句号/换行/闭合标签」。实测 **RFC 9110 = 437,738 runes、末尾正是 `Germany`**（完整、未截断）⇒ 落在窗口内且末尾不匹配 ⇒ **必然假阳性**。一个会稳定误报的标记比没有标记更糟：模型会据此反复重试或换来源，白花计费 |
| 10.2 | 上游真的抓不完时返回的是 **504**（23 号 §4.6：30.126s / 30.131s），映射为 `2504 UP_GATEWAY_TIMEOUT`，**可重试**。这才是「上游没给全」的确定信号 |
| 11 | `title` 为空时返回 `""`（不编造） |
| 12 | 全部正文经 `WrapUntrusted` 包裹（nonce 定界符，见 `Task-02` §3 第 10 条） |
| 13 | `cache_id` 写进响应，供后续按节取 |
| 14 | 🔴 **2404 的 `hint` MUST 含实测失败站点清单**：15 个主流站点实测 **9 成功 / 6 失败（40%）** —— `en.wikipedia.org`、`zh.wikipedia.org`、`zhihu.com`、`news.ycombinator.com`、`reddit.com`、`raw.githubusercontent.com` 全部塌缩为 404（23 号 §4.3）。文案逐字取自 `14-错误契约` §2.3.1，且 MUST 与 `ds_search` 的 2404 hint 不同（那是 `base_url` 配错） |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 对正文做 HTML 清洗（上游给的已是 Markdown，自己清洗会破坏代码块与表格） |
| 2 | MUST NOT 按 byte 切（必须 `domain.SliceByRune`） |
| 3 | 🔴 MUST NOT 产出 `truncated_by="upstream"` —— 该档已删除（§3 第 10 条）。任何基于「字符数窗口 + 末尾字符」的截断启发式都 MUST NOT 实现，因为已实测到稳定假阳性（RFC 9110） |
| 3.1 | MUST NOT 用 `len(content)`（byte）判 `inline_max_chars` 或 `max_chars`（§3 第 4.1 条） |
| 3.2 | MUST NOT 让 section / outline 模式绕过 `max_chars`（§3 第 5.1 / 7.1 条） |
| 4 | MUST NOT 在 `unescape=false` 时仍做任何字符替换 |
| 5 | MUST NOT 让缓存里存反转义后的内容（缓存永远存上游原文） |
| 6 | MUST NOT 分节时修改原文（不许 trim / 合并空白） |

### 5 参考骨架

```go
// handleFetch 是 ds_fetch 的完整编排。
// 反转义与分节的先后顺序是：先反转义再分节 ——
// 因为反斜杠会出现在标题里（`\#\# Title`），不先反转义就识别不出标题。
func handleFetch(ctx context.Context, deps Deps, in fetchInput) *mcp.CallToolResult

// 🔴 isProbablyTruncated **已删除**，MUST NOT 实现。
//
// 它的立论是「并发请求同一 URL 返回不同长度（实测 168293/231368/322742/428900/428901）」。
// 23 号 §4.6 复测推翻了这一点：并发冷抓取 go.dev/doc/effective_go，5 个请求中
// 3 个 200 且**内容 105,162 runes、sha256 完全相同**，另 2 个是 **504 精确在 30.126s/30.131s**
// —— 长度分歧来自超时失败，不是内容不确定。间隔 30s 重抓仍字节相同。
//
// 而它的判据（total_chars ∈ [100000,600000] 且末尾不在句号/换行/闭合标签）
// 在 RFC 9110 上**必然假阳性**：实测 437,738 runes、末尾 "Germany"、内容完整。
//
// 结论：上游「没给全」的唯一确定信号是 504 → 2504（可重试）。截断标记只保留
// truncated_by="budget"（我方按 max_chars 主动截断，是确定事实）。

// budgetCut 是三种模式共用的预算闸门。inline / outline / section 都 MUST 过它。
// 返回 (文本, 是否被截断)；被截断时调用方填 truncated_by="budget"。
func budgetCut(content string, maxChars int) (string, bool) {
    if utf8.RuneCountInString(content) <= maxChars { // 🔴 rune，不是 len()
        return content, false
    }
    out, _ := domain.SliceByRune(content, 0, maxChars)
    return out, true
}
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 3000 字符正文 | `mode="inline"`，全文返回 |
| 20000 字符 + 标题 | `mode="outline"`，`outline_kind="headings"`，≥2 节 |
| 20000 字符无标题 | `mode="outline"`，`outline_kind="fixed"` |
| 用返回的 `cache_id` + `section="s1"` | 返回该节全文 |
| `section="s99"` | `1090` |
| `unescape=true` 含 `\#\#` | 变成 `##` |
| `unescape=false` | 原样保留反斜杠 |
| 正文含 `\n`（字面反斜杠 n） | `unescape=true` 后仍是 `\n`（不转成换行） |
| 100 字符正文 | `3050` |
| 🔴 437,738 rune 正文（RFC 9110 实测规模）、末尾 `Germany`、`max_chars=200000` | `truncated_by="budget"`。**MUST NOT** 出现 `"upstream"` —— 这条专治已删除的启发式（旧实现在此必然假阳性） |
| 🔴 150,000 rune 正文、`max_chars=200000`（不触发预算） | **不含** `truncated_by` 字段（未截断就不填） |
| 我方 `max_chars=4000` 截断 | `truncated_by="budget"` |
| 🔴 6,405 rune / 9,355 byte 的正文（官方文档页实测比例），`inline_max_chars=8000` | `mode="inline"` —— **按 byte 判会得到 `outline`**，这条专治 §3 第 4.1 条 |
| 🔴 `section="s1"` 且该节 50,000 rune、`max_chars=32000` | 返回 32,000 rune + `truncated_by="budget"`（section 模式不豁免预算） |
| 🔴 `sections=["s1","s2","s3"]` 合并后超 `max_chars` | 截断 + `truncated_by="budget"` |
| 🔴 outline 有 350 节、`outline_max_sections=200` | `sections` 长度 == 200，`warnings` 含「共 350 节」 |
| 🔴 上游返回 504 | `2504`，`retryable=true`，**不含** `truncated_by` |
| 🔴 两个不同 URL 构造出同一个 FNV-1a 64 hash（测试里用桩） | sha256 复核失败 → 按未命中处理 + `warn.cache_key_collision`，MUST NOT 返回另一个 URL 的正文 |
| 🔴 `unescape=true` 对 `a\\_b` | 得到 `a_b`（32 字符集里的 `\_`，ADR-013 高频缺失项） |
| 5 并发同 URL | `fakeClient.fetchCalls == 1` |

### 7 验收命令与期望输出

```bash
$ go test ./internal/tools/ -race -v -run TestHandleFetch
--- PASS: TestHandleFetch (0.03s)
    --- PASS: TestHandleFetch/outline_模式
    --- PASS: TestHandleFetch/按节取正文
    --- PASS: TestHandleFetch/反转义不动代码块
    --- PASS: TestHandleFetch/budget_截断
    --- PASS: TestHandleFetch/不产出_upstream_截断标记
```

### 8 完成定义（DoD）

- [ ] 🔴 20 个用例全绿（原写 21，§6 表实为 20 行），`-race` 干净
- [ ] 🔴 `grep -rn 'isProbablyTruncated\|truncated_by.*upstream' internal/tools/` **零命中**（已删除的启发式不得复活）
- [ ] 🔴 `grep -rn 'len(content)' internal/tools/fetch*.go` **零命中**（一律用 `utf8.RuneCountInString`）
- [ ] 🔴 SSRF 测试含 ≥6 种 IP 混淆变体 + 2 种 IPv4-mapped IPv6 + 1 个公网 IP 反例
- [ ] `internal/tools` 整体覆盖率 ≥ 70%
- [ ] 提交：`feat(tools): ds_fetch handler 编排`
