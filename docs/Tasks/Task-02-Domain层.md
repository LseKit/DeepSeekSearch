# Task-02 · Domain 层（类型、错误码、纯函数）

> **执行方式**：3 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 建起全项目的地基：类型、41 个错误码、全部纯函数。**零 IO、零第三方依赖** |
| 依赖 | Task-01 |
| 产出 | `internal/domain` 覆盖率 ≥ 80%，`gocyclo -over 10` 零命中 |
| 必读 | `Contract/12-数据契约` 全文；`Contract/14-错误契约` §二 §三；`Contract/11-接口契约` §一；`Engineering/20-编码规范` §三 §四 |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | 错误码与错误类型 | 41 个码一个都不能少，`name` 是稳定契约 |
| A-02 | 时间 / URL / 切片 / 反转义 | 时区差一天、按 byte 切出乱码 |
| A-03 | 语法拼装 / 分节 / 去重 / 包裹 | 拼错操作符上游静默返回无关结果 |

---

## 原子任务 A-01 · 错误码与错误类型

### 0 前置

- Task-01 完成
- 已通读 `Contract/14-错误契约` §二（41 个码的完整表）

### 1 上下文（自包含）

MCP 工具失败时不能只返回一句错误消息 —— 调用方（一个 LLM）需要知道「这是我的错还是上游的错」「能不能重试」「要不要换个参数」。所以错误必须结构化：数字码 + 稳定英文名 + 中文消息 + 中文建议 + 可重试标志 + 上游 request_id。

三段位设计：`1xxx` 我方入参、`2xxx` 上游（编号 = `2000 + http_status`）、`3xxx` 本地资源。

### 2 要创建的文件

| 文件 | 内容 |
|---|---|
| `internal/domain/errors.go` | 三个错误类型 + 构造函数 + `Error()` 实现 |
| `internal/domain/codes.go` | 41 个 `Code*` 常量 + `CodeRegistry` 映射 |
| `internal/domain/errors_test.go` | 测试 |
| `internal/domain/codes_test.go` | 测试（条数、唯一性、命名格式） |

### 3 实现要求

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | 常量**恰好 41 个**，取值与 `14-错误契约` §二 完全一致 | `grep -c $'^\tCode' codes.go` = 41（🔴 MUST 用 `$'\t'`：BRE 不解析 `\t`，字面写法恒返回 0） |
| 2 | 常量命名 `Code<Name>`，`Name` 与错误码表 `name` 列相同（如 `CodeUpRateLimited`） | `codes_test.go` 断言 |
| 3 | `CodeRegistry` 是 `map[int]CodeInfo`，长度 **41**（= 常量数，**含软错误 1080**） | 测试断言 `len(CodeRegistry) == 41` |
| 4 | `CodeInfo` 含 4 字段：`Name`(string)、`Message`(string)、`Hint`(string)、`Retryable`(bool) | 结构体定义 |
| 5 | `Message` 与 `Hint` 全中文，MUST NOT 为空 | 测试遍历断言非空 |
| 6 | `Name` 全 UPPER_SNAKE，正则 `^[A-Z][A-Z0-9_]*$` | 测试用 regexp 断言 |
| 7 | 三个错误类型都实现 `error` 接口 | `var _ error = (*UpstreamError)(nil)` 三行 |
| 8 | `ErrorPayload` **9 字段**，字段名/json tag/**声明顺序**三者都与 `14-错误契约` §三 一致（`encoding/json` 按声明序输出，顺序即契约） | 结构体定义比对 + 一条黄金 JSON 快照测试 |
| 9 | `ToPayload(err error, tool string) ErrorPayload` 按 **14 号 §1.3 的四步顺序**归类，MUST NOT 一律把未知错误归 `2999`。`tool` 只影响 **2404 的 hint**（`14 号 §2.3.1`）：`ds_search` 走「base_url 配错」那套，其余走「取不到正文」那套；空串按 `ds_fetch` | 测试逐条覆盖 4 个分支 + 2404 的两种 `tool` |
| 10 | `IsRetryable(err error) bool` 只查 `CodeRegistry[code].Retryable`，MUST NOT 用字符串匹配 | 测试 |

#### 3.1 🔴 `ToPayload` 的判定顺序（照抄，MUST NOT 自行发挥）

```
1. err 实现 domain.Coded（有 Code()）        → 用该码
2. errors.As 得 *UpstreamError               → codeForStatus(status)；status==0 时用 2998
3. net.Error / context.DeadlineExceeded
   / io.ErrUnexpectedEOF / io.EOF            → 2998 UP_NETWORK（可重试）
4. 其余一切                                  → 3099 LOC_INTERNAL（不可重试）
```

**为什么第 3 步不能省**：省了以后网络层错误会落到第 4 步，被归成「不可重试」，于是 `14-错误契约` §六 承诺的「网络错误 ✅ 可重试」永不生效 —— 一次 DNS 抖动就直接失败给用户。详见 `ADR-016`。

**为什么第 4 步是 3099 不是 2999**：`2999 UP_PARSE` 的语义已收窄为「HTTP 200 但响应体不合契约」（外部问题）。把我方 bug 也塞进 2999，会让运维去查上游 API 变更，排查方向被系统性带偏。

#### 3.2 🔴 1080 `VAL_SYNTAX_CONFLICT` 也 MUST 进 `CodeRegistry`

1080 是**软错误**（只写 `warnings[]`，永不作为 `code` 返回）。但它的 `Name`/`Message`/`Hint` 仍是 `warnings[]` 文案的来源，且 `ds://error-codes` Resource 承诺「41 条全表」。

⇒ `len(CodeRegistry) == len(常量) == 41`，**MUST NOT** 为了「它不会返回」而排除它 —— 那会造成 `CodeRegistry` 与常量数不等（历史上此处曾写 38 vs 39，两个数字都错且互相矛盾），并让 `ds://error-codes` 少一行。

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 用 `errors.New` 预定义 41 个哨兵错误（`var ErrXxx = errors.New(...)`）—— 我们只需要码，不需要哨兵值 |
| 2 | MUST NOT 在 `Message` 里加英文。`Name` 是给机器看的，`Message`/`Hint` 是给模型看的中文 |
| 3 | MUST NOT 让 `Error()` 返回含 API Key 的字符串 |
| 4 | MUST NOT 用 `iota` 生成码值 —— 码值是契约，必须字面量写死 |
| 5 | MUST NOT 让 `2xxx` 偏离 `2000 + http_status` 公式 |
| 6 | MUST NOT 在 `domain` 包 import 除标准库外的任何东西 |

### 5 参考骨架

```go
// CodeInfo 是错误码的静态元信息。
// Name 是稳定契约，MUST NOT 改名（客户端可能按它做分支）。
type CodeInfo struct {
    Name      string // "UP_RATE_LIMITED"
    Message   string // 中文，一句话说清发生了什么
    Hint      string // 中文，告诉调用方现在能做什么
    Retryable bool   // 重试器唯一依据
}

// ErrorPayload 是所有工具失败时的统一返回体。
// 🔴 9 字段：5 个必带（含 false/零值，无 omitempty）+ 4 个条件带（有则带，无则省略）。
//    字段名 / json tag / **声明顺序**三者 MUST 与 `Contract/14-错误契约.md` §三 逐字一致 ——
//    14 号 §三 是唯一真源，本节只是抄录；顺序错会让快照测试与文档对不上。
type ErrorPayload struct {
    // —— 5 个必带 ——
    Code      int    `json:"code"`      // 见 §二
    Name      string `json:"name"`      // UPPER_SNAKE，稳定可依赖
    Message   string `json:"message"`   // 中文，说清「发生了什么」
    Hint      string `json:"hint"`      // 中文，说清「怎么办」
    Retryable bool   `json:"retryable"` // MUST 显式序列化，false 也要出现

    // —— 4 个条件带 ——
    UpstreamHttp int            `json:"upstream_http,omitempty"`
    UpstreamMsg  string         `json:"upstream_msg,omitempty"`
    RequestId    string         `json:"request_id,omitempty"`
    Details      map[string]any `json:"details,omitempty"`
}

// UpstreamError 是上游返回的错误。它保留原始 HTTP 状态与上游消息，
// 因为「上游说了什么」是排查的唯一线索。
type UpstreamError struct {
    Code       int    // 例如 2429
    HttpStatus int    // 例如 429
    RequestId  string
    UpstreamMsg string
    cause      error  // 底层网络错误，可为 nil
}

func (e *UpstreamError) Error() string {
    // MUST 不含任何 Key。格式固定便于日志解析：
    // "upstream: 2429 UP_RATE_LIMITED (http 429, request_id=...)"
}

func (e *UpstreamError) Unwrap() error { return e.cause }

// ValidationError 是我方入参校验失败。
type ValidationError struct {
    Code   int
    Field  string // 参数名，如 "count"
    Detail string // 中文细节，如 "必须在 1~50 之间，实际 100"
}

// LocalError 是本地资源/状态问题（排队超时、熔断、缓存降级、配置非法）。
type LocalError struct {
    Code   int
    Detail string
    Extra  map[string]any // 附加字段，如 {"retry_after_ms": 15000}
}
```

### 6 测试代码

```go
func TestCodeRegistry(t *testing.T) {
    t.Parallel()

    if got, want := len(CodeRegistry), 41; got != want {
        t.Fatalf("CodeRegistry 长度 = %d, want %d（不许增删而不改文档）", got, want)
    }

    nameRe := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
    for code, info := range CodeRegistry {
        t.Run(fmt.Sprint(code), func(t *testing.T) {
            if !nameRe.MatchString(info.Name) {
                t.Errorf("code %d 的 Name %q 不是 UPPER_SNAKE", code, info.Name)
            }
            if info.Message == "" || info.Hint == "" {
                t.Errorf("code %d 的 Message/Hint 为空", code)
            }
            // 2xxx 必须是 2000 + http_status
            if code >= 2000 && code < 3000 && code != 2999 && code != 2000 {
                status := code - 2000
                if status < 400 || status > 599 {
                    t.Errorf("code %d 推出的 http_status %d 不合法", code, status)
                }
            }
        })
    }
}

func TestIsRetryable(t *testing.T) {
    t.Parallel()
    tests := []struct {
        name string
        code int
        want bool
    }{
        {"429 可重试", CodeUpRateLimited, true},
        {"504 可重试", CodeUpGatewayTimeout, true},
        {"404 不可重试（实测 403 也塌缩成 404，重试只会付费）", CodeUpNotFound, false},
        {"402 余额不足绝不可重试", CodeUpBalance, false},
    }
    // ...表驱动
}
```

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc
$ go test ./internal/domain/ -run 'TestCode|TestIsRetryable|TestToPayload' -v
--- PASS: TestCodeRegistry (0.00s)
--- PASS: TestIsRetryable (0.00s)
--- PASS: TestToPayload (0.00s)
PASS
ok      github.com/LseKit/DeepSeekSearch/internal/domain

$ go build ./... && echo BUILD_OK
BUILD_OK

$ grep -c $'^\tCode' internal/domain/codes.go   # $'\t' 由 bash 先展开成真制表符
41
```

### 8 完成定义（DoD）

- [ ] 🔴 常量 **41** 个，且 `CodeRegistry` 长度 **41**（两者必须相等；原写「长度 39」与同一行的 41 自相矛盾，已修）
- [ ] `domain` 包 import 只有标准库
- [ ] 提交：`feat(domain): 错误码体系与错误类型`

---

## 原子任务 A-02 · 时间 / URL / 切片 / 反转义

### 0 前置

- A-01 完成
- 已通读 `Contract/12-数据契约` §二（时区）、§三（URL 规范化）、§5.5（rune 切片）、§六（反转义）

### 1 上下文（自包含）

这四个纯函数各自对应**一个实测踩过的坑**：

| 函数 | 坑 |
|---|---|
| `ParseDate` | 上游 `date` 恒为 `T16:00:00+00:00`（北京日期零点序列化成 UTC）。直接按 UTC 取日期会**差一天** |
| `NormalizeUrl` | 上游缓存被 `?utm_source=x` 击穿，`http:` vs `https:` 也击穿。缓存 key 必须自己归一 |
| `SliceByRune` | 中文 1 字 3 字节，`s[0:8000]` 会切出 `�` |
| `Unescape` | 上游把 **32 种** Markdown 字符加反斜杠（ADR-013，23 号 §4.5 实测 7 个页面样本）；一份 437,738 rune 的 RFC 9110 正文里有数万个反斜杠。**只有 fetch 的 content 被转义，search 侧不转义**（23 号 §4.5.2） |

### 2 要创建的文件

| 文件 | 内容 |
|---|---|
| `internal/domain/time.go` | `ParseDate`、`cst8` 变量 |
| `internal/domain/url.go` | `NormalizeUrl`、`HashUrl` |
| `internal/domain/redact.go` | 🔴 `RedactURL`（日志里的 URL query 脱敏） |
| `internal/domain/text.go` | `SliceByRune`、`Unescape`、`NormalizeTitle`、`WrapUntrusted` |
| `internal/domain/time_test.go` / `url_test.go` / `redact_test.go` / `text_test.go` | 测试 |

### 3 实现要求

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | `cst8` 用 `time.LoadLocation("Asia/Shanghai")`，MUST NOT 用 `time.FixedZone` | `grep -c 'LoadLocation' time.go` = 1 且 `grep -c 'FixedZone' time.go` = 0 |
| 2 | `ParseDate(s *string, now time.Time) PublishedDate`：null → 三字段全 null；有效值 → 先 `In(cst8)` 再取 `YYYY-MM-DD`。🔴 `now` MUST 由调用方从注入的 `Clock` 传入，MUST NOT 在 domain 里调 `time.Now()`（H8/T4） | 测试：`ParseDate(ptr("2026-04-26T16:00:00+00:00"), fixedNow)` → `"2026-04-27"` |
| 3 | `AgeDays` 用 `now.In(cst8)` 的当天 00:00 相减，不是 `time.Since(...).Hours()/24`；跨零点按时区切分 | 测试：`now` 固定为某天 23:59 与次日 00:01，同一日期分别得 `0` 与 `1` |
| 4 | `NormalizeUrl` 实现 `12-数据契约` §3.2 的 10 步，顺序 MUST 一致 | 测试 21 个变体 |
| 5 | 追踪参数**由调用方注入**，不在本包硬编码：`NormalizeUrl(raw string, trackingParams []string) (string, error)`。键名大小写不敏感、精确匹配 | 测试：`?UTM_SOURCE=x` 被剥离；`trackingParams=nil` 时一个都不剥 |
| 5.1 | 🔴 **为什么必须参数化**：该表是 `[dedupe].tracking_params` 配置项（`13-配置契约`），默认 **26 个**（清单逐字见 `12-数据契约` §三 第 7 步）。在 domain 里硬编码就等于「配置项是假的」，且历史上 Task-02 写 27、两处契约写 26 —— 三个数字打架的根源正是硬编码 |
| 5.2 | 🔴 **MUST NOT 断言「恰好 26 个」**：测试 MUST 断言「与注入的 `trackingParams` 集合相等」，而不是硬编码数字。表是可配置的，写死数字的测试会在用户改配置后变红，进而诱导执行窗口去改配置凑绿 |
| 6 | Path 为 `/` 时归一为 `""`；非根路径**保留尾斜杠** | 测试：`/a/` 与 `/a` 是不同 key |
| 7 | `HashUrl` 用 FNV-1a 64，返回 `uint64` | 同输入同输出 |
| 8 | `SliceByRune(s, offset, limit)` 返回 `(string, bool)`，bool = 是否被截断 | 测试 emoji/中文/越界 |
| 9 | 🔴 `Unescape` 的字符类**恰好 32 个字符**（ADR-013）：`[/#.,:{}()\-!*\|=_;\[\]<>"%?&@`+"`"+`~$^\\]`。**MUST 单趟 `ReplaceAllString`**，MUST NOT 多趟逐个替换 | 测试见 §6 的 32 字符全表用例 |
| 9.1 | 🔴 **为什么必须单趟**：实测存在 `\\\.` 这类**嵌套转义**（23 号 §4.5.1，26 处）。多趟替换时第一趟把 `\\\.` 变成 `\\.`、第二趟再变成 `\.` —— 过度反转义，输出与原文不符 |
| 9.2 | 🔴 **只用于 fetch 的 content**：search 侧的 content/snippet/summary/title **不转义**（23 号 §4.5.2）。对其调用 `Unescape` 会把代码块里的字面量 `\n` 变成 `n`。调用方归属见 `Task-09` |
| 9.3 | 漏掉的高频字符及后果（实测频次）：`\|`480 破坏 Markdown 表格、`\*`382、`\=`338、`\;`223、`\'`197、`\+`167、`\_`140 破坏标识符（`user\_name` → 与 `user_name` 不等）。完整表见 `12-数据契约` §6.1.1 |
| 10 | 🔴 `WrapUntrusted(source, body)` MUST 用**每次调用随机生成的 nonce 定界符**：`<untrusted_content id="a8f3k2" source="...">…</untrusted_content id="a8f3k2">`。body 内出现**同一个 nonce 串**的地方一律转义 | 测试：逃逸被阻断（含大小写与空白变体） |
| 10.1 | 🔴 **为什么原方案不安全**：原设计只替换精确小写的 `</untrusted_content>`。恶意网页写 `</UNTRUSTED_CONTENT>` 或 `</untrusted_content >` 就不命中，而 **LLM 对标签大小写与尾随空白普遍不敏感** ⇒ 攻击者可伪造闭合标签，在其后注入「系统指令」。这是本项目自定义的核心安全控制（`14-错误契约` §4.1 的 description 第 3 条承诺了包裹有效性），承诺了就必须真的有效 |
| 10.2 | nonce MUST 用 `crypto/rand` 生成（6 字节 hex），**MUST NOT** 用 `math/rand/v2` —— 后者可预测，攻击者能提前猜到定界符。这是 `10-包依赖矩阵` §2.1 给 domain 加 `crypto/rand` 的唯一理由 |
| 10.3 | 退化方案（若判定 nonce 过重）：至少做到「大小写不敏感替换 + 剥离 body 内所有裸 `<`」，并在 `12-数据契约` §七 写明该方案的残余风险。二者选一，MUST NOT 停留在「只替换精确小写」 |
| 11 | `NormalizeTitle` = 去首尾空白 + 全角转半角 + 小写 + 连续空白压一个 | 测试：`"Ａ  B"` → `"a b"` |
| 12 | 🔴 实现 `RedactURL(raw string, keys []string) string`：对 URL 的 **query 参数**，凡键名（大小写不敏感）命中 `keys` 者，值替换为 `***`；`url.Parse` 失败或无 query 时原样返回（MUST NOT panic） | 测试：`?token=abc&Token=def&keep=1` → `token=***&Token=***&keep=1` |
| 12.1 | 🔴 **为什么放 `domain` 而不是 `internal/logging`**：调用点在 `deepseek`（Task-05 的请求体日志）、`tools`（Task-08/09 的 URL 日志）、`transport` 三处，而 `10-包依赖矩阵` §2.4/§2.6 明确**不允许** `deepseek`/`tools` → `logging`。放到 logging 会让这三个包要么违反白名单，要么把纯函数改成注入式接口。它是**纯字符串变换、零 IO**，正合 domain 的定位（与 `NormalizeUrl` 同类）。`logging` 只负责「怎么落盘」，不负责「脱敏规则」 |
| 12.2 | 键表由调用方注入（`cfg.Log.RedactQueryKeys`），MUST NOT 在 domain 硬编码 —— 同 §3 第 5.1 条的理由：它在 `13-配置契约` 里是一个可配置项（默认 7 项） |

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 在 `NormalizeUrl` 里对**发给上游的 URL** 做归一 —— 归一结果只用于缓存 key 与去重 |
| 2 | MUST NOT 输出 `00:00:00` 时刻（伪造数据） |
| 3 | MUST NOT 用 `strings.TrimSuffix(path, "/")` 处理根路径（会把 `"/"` 变成 `""` 又把 `/a/` 变 `/a`） |
| 4 | MUST NOT 在 `Unescape` 里用 `strings.ReplaceAll(s, "\\", "")`（会毁掉代码块） |
| 4.1 | MUST NOT 对 `Unescape` 做多趟替换（见 §3 第 9.1 条：嵌套转义会被过度反转义） |
| 4.2 | MUST NOT 在 domain 里硬编码追踪参数表（见 §3 第 5.1 条） |
| 4.3 | MUST NOT 对 search 侧结果调用 `Unescape`（见 §3 第 9.2 条） |
| 5 | MUST NOT 让 `SliceByRune` 用 byte 索引 |
| 6 | MUST NOT 用 `time.FixedZone` |

### 5 参考骨架

```go
// cst8 是 Asia/Shanghai。MUST NOT 用 time.FixedZone("CST", 8*3600)，
// 因为后者不带时区名，日志里显示为 "+0800" 不可读，且不参与历史偏移计算。
var cst8 = mustLoadCST8()

func mustLoadCST8() *time.Location {
    loc, err := time.LoadLocation("Asia/Shanghai")
    if err != nil {
        // 这是启动期致命错误：容器缺 tzdata 时会发生。
        // 用 panic 是可以的 —— 它发生在包初始化，不是用户请求路径。
        panic("domain: 无法加载 Asia/Shanghai 时区，请安装 tzdata: " + err.Error())
    }
    return loc
}
```

> **关于 `panic`**：`20-编码规范` G6 禁止「正常错误用 panic」。这里是**包初始化期的不可恢复故障**，且必须在任何请求前暴露，属于允许的例外。MUST 在 `22-架构守护规则` 的豁免台账里登记。

### 6 测试代码（必查用例）

| 用例 | 期望 |
|---|---|
| `ParseDate(nil, fixedNow)` | `Date=nil, AgeDays=nil, DatePrecision="day"` |
| `ParseDate("2026-04-26T16:00:00+00:00", fixedNow)` | `Date="2026-04-27"`（**不是 04-26**） |
| `ParseDate("2026-04-27T09:53:15+00:00", fixedNow)` | `Date="2026-04-27"`（当天 17:53 北京，仍 27 日） |
| `NormalizeUrl("https://example.com/")` | `"https://example.com"` |
| `NormalizeUrl("https://EXAMPLE.com:443/x?utm_source=a&b=2&a=1")` | `"https://example.com/x?a=1&b=2"` |
| `NormalizeUrl("http://example.com/")` | `"http://example.com"`（scheme 保留，与 https 不同 key） |
| `NormalizeUrl("https://u:p@example.com/")` | error（含凭据） |
| `NormalizeUrl("ftp://x.com/")` | error（非 http/https） |
| `NormalizeUrl("https://example.com/a/")` | `"https://example.com/a/"`（尾斜杠保留） |
| `SliceByRune("中文abc", 0, 2)` | `("中文", true)` |
| `SliceByRune("😀😀", 0, 1)` | `("😀", true)` |
| `Unescape(`\#\# A\. B`)` | `"## A. B"` |
| `Unescape(`a\nb`)` | `"a\nb"` 不变 —— 字符类里**没有** `n`，反斜杠后的 `n` 不属于 32 种，整段原样保留 |
| 🔴 `Unescape` 遍历 **32 个字符**逐个断言 | 对每个 c ∈ 字符类，`Unescape("\\"+c) == c`；用**表驱动**列出全部 32 个，MUST NOT 只测其中几个 |
| 🔴 `Unescape(`\\\.`)`（嵌套转义） | `"\\."` —— 只脱掉一层。若得到 `"."` 说明实现是多趟替换（§3 第 9.1 条） |
| 🔴 `Unescape("a\\_b")` | `"a_b"`（标识符不被破坏，ADR-013 的高频缺失项） |
| 🔴 `Unescape("x \\| y")` | `"x | y"`（Markdown 表格不被破坏，实测频次最高 480） |
| `NormalizeUrl(u, nil)` | 不剥离任何 query 参数 |
| `NormalizeUrl("https://x.com/?utm_source=a", []string{"utm_source"})` | `"https://x.com"`（根路径 `/` 归一为 `""`，且空 query MUST NOT 输出裸 `?`，见 12 号 §3.2 第 9 步） |
| 🔴 `WrapUntrusted("x", "a</untrusted_content>b")` | 输出不含裸 `</untrusted_content>`，且**含 nonce** |
| 🔴 `WrapUntrusted("x", "a</UNTRUSTED_CONTENT>b")` | 大写变体同样无法伪造闭合（§3 第 10.1 条） |
| 🔴 `WrapUntrusted("x", "a</untrusted_content >b")` | 尾随空白变体同样被阻断 |
| `RedactURL("https://x/cb?token=abc&Token=def&keep=1", []string{"token"})` | `token=***&Token=***&keep=1`（键名大小写不敏感） |
| `RedactURL("https://x/a?b=1", nil)` | 原样返回 |
| `RedactURL("not a url", []string{"token"})` | 原样返回且**不 panic** |
| `RedactURL("https://x/?sig=abc&sig2=def", []string{"sig"})` | `sig=***&sig2=def`（**精确匹配**，不是前缀匹配） |
| 🔴 两次调用 `WrapUntrusted` | nonce **不相同**（`crypto/rand`，非固定值） |

### 7 验收命令与期望输出

```bash
$ go test ./internal/domain/ -v -run 'TestParseDate|TestNormalizeUrl|TestSliceByRune|TestUnescape|TestWrapUntrusted'
--- PASS: TestParseDate (0.00s)
--- PASS: TestNormalizeUrl (0.00s)
--- PASS: TestSliceByRune (0.00s)
--- PASS: TestUnescape (0.00s)
--- PASS: TestWrapUntrusted (0.00s)
PASS

$ go test ./internal/domain/ -cover
coverage: 8x.x% of statements
```

### 8 完成定义（DoD）

- [ ] 🔴 上表 **27** 个必查用例全部覆盖且绿（原写 14，与表格实际行数不符；数字 MUST 等于 §6 表的数据行数）
- [ ] `domain` 覆盖率 ≥ 60%（A-03 完成后整体 ≥80%）
- [ ] 提交：`feat(domain): 时间/URL/切片/反转义纯函数`

---

## 原子任务 A-03 · 语法拼装 / 分节 / 去重 / 包裹

### 0 前置

- A-02 完成
- 已通读 `Contract/12-数据契约` §4.1（语法拼装）、§五（分节）、§八（去重）

### 1 上下文（自包含）

四个函数，全部是「上游不做、必须我们做」的事：

| 函数 | 为什么必须有 |
|---|---|
| `BuildQuery` | 上游对语法**零校验**：`site:不存在的域名` 返回 200 + 无关结果。我们必须在本地拼对，并回报 `query_effective` |
| `SplitSections` | Fetch 一次能返回 568K 字符（≈200k token），不分节直接塞进上下文会爆 |
| `Dedupe` | 上游一次 `count=50` 可能返回 50 条全来自同一个域名 |
| `WrapUntrusted`（A-02 已做） | 上游正文无任何清洗，是 prompt injection 的直接入口 |

### 2 要创建的文件

| 文件 | 内容 |
|---|---|
| `internal/domain/query.go` | `BuildQuery` |
| `internal/domain/section.go` | `SplitSections`、`Section`、`Outline` |
| `internal/domain/dedupe.go` | `Dedupe` |
| `internal/domain/{query,section,dedupe}_test.go` | 测试 |

### 3 实现要求

#### 3.1 `BuildQuery`

```go
// BuildQuery 按 12-数据契约 §4.1 的优先级把结构化字段拼成上游 query。
// rawQuery 为 true 时原样返回 req.Query，MUST NOT 追加任何操作符。
func BuildQuery(req SearchRequest) (effective string, warnings []string)
```

| # | 要求 |
|:-:|---|
| 1 | 拼接顺序固定：`query` → `site[]` → `exclude_sites[]` → `lang[]` → `after` → `before` → `exact_phrase` → `exclude_terms[]` |
| 2 | `site[]` 多于一个时用 `" OR "` 连接（`site:a.com OR site:b.com`） |
| 3 | `query` 内已含同名操作符（如已写 `site:`）时**不再追加**，追加一条 warning |
| 4 | `exclude_terms[]` 里含空格时加双引号（`-"foo bar"`） |
| 5 | 拼装后 > 128 **rune** 时返回 `warnings` 里一条「超长」提示（由 Tools 层转成 🔴 **`1010` `VAL_QUERY_TOO_LONG`** 拒绝 —— 原写 `1090`，那是 `VAL_SECTION_NOT_FOUND`（`section` ID 不在快照 TOC 内），属**跨工具语义撞车**：ds_search 根本没有 section 参数，用户拿到的 hint 会指向一个不存在的入参） |
| 6 | 返回值 `effective` 必须是**实际要发出去的完整串** |

#### 3.2 `SplitSections`

```go
// SplitSections 把正文切成节。判定顺序：标题 → 固定分块 → 单节兜底。
func SplitSections(content string, fixedChunk int) Outline
```

| # | 要求 |
|---|---|
| 1 | 只认 ATX 标题（`^#{1,6} `），MUST NOT 认 Setext（`===` 下划） |
| 2 | `` ``` `` 围栏内的 `#` MUST NOT 当标题 |
| 3 | `#hashtag`（`#` 后无空格）MUST NOT 当标题 |
| 4 | ≥2 个标题 → `OutlineKind="headings"`，id 为 `s0/s1/...` |
| 5 | 否则 → `OutlineKind="fixed"`，按 `fixedChunk` **rune** 均分，id 为 `c0/c1/...` |
| 6 | 永不返回空 `Sections`；单节也要有一条 |
| 7 | 一级标题缺失时，第一个标题前的内容合成一个 `Level=0` 的 `(前言)` 节 |
| 8 | `Chars` 字段是 **rune** 数 |

#### 3.3 `Dedupe`

```go
// Dedupe 按三层（URL hash / 转载 / 域名配额）去重，保持 rerank_score 降序。
func Dedupe(items []SearchResult, maxPerDomain int, trackingParams []string) (kept []SearchResult, stats DedupeStats)
// 🔴 trackingParams MUST 由调用方注入（来自 [dedupe].tracking_params）：L1 用 NormalizeUrl 算 hash，
//    传入 nil 时 utm_* 等参数不会被剥离，同页多链会被当成不同条目。
//    MUST NOT 在 domain 里硬编码那份表 —— 理由同 A-02 §3 第 5.1 条。
```

| # | 要求 |
|:-:|---|
| 1 | L1：`NormalizeUrl` 后 FNV-1a 64 hash 相同 → 丢 |
| 2 | L2：`NormalizeTitle(title)` + `site_name` 均相同 → 只留 `rerank_score` 最高 |
| 3 | L3：同 host 超过 `maxPerDomain` → 丢低分 |
| 4 | 输出 MUST 按 `rerank_score` 降序，MUST NOT 打乱 |
| 5 | `DedupeStats` 记录三层各丢多少条 |
| 6 | `maxPerDomain <= 0` 视为不限制 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 在 `BuildQuery` 里对 `query` 做任何转义/编码 —— 原样透传 |
| 2 | MUST NOT 用第三方 Markdown 解析库。我们自己只认 ATX，30 行代码够 |
| 3 | MUST NOT 在 `Dedupe` 里用 `sort.Slice` 之外的排序（`sort.Slice` 不稳定，但我们的 key 唯一） |
| 4 | MUST NOT 让 `SplitSections` 修改原文（不许 trim、不许合并空白） |
| 5 | MUST NOT 在 `domain` 引入任何第三方依赖 |

### 5 参考骨架

```go
// sectionIDPrefix 区分两种切分来源：headings 用 s，fixed 用 c。
// 用字母前缀而不是纯数字，是为了让模型一眼看出「这节的边界是标题还是固定长度」。
const (
    prefixHeading = "s"
    prefixFixed   = "c"
)

// fenceRe 匹配 ``` 围栏的开关行。用它来跟踪「当前是否在代码块内」，
// 因为代码块里的 # 是 Shell/Python 注释，不是 Markdown 标题。
var fenceRe = regexp.MustCompile("^\\s*```")
```

### 6 测试代码（必查用例）

| 用例 | 期望 |
|---|---|
| `BuildQuery{Query:"go"}` | `("go", nil)` |
| `BuildQuery{Query:"go", Site:["a.com","b.com"]}` | `"go site:a.com OR site:b.com"` |
| `BuildQuery{Query:"go site:x.com", Site:["a.com"]}` | `"go site:x.com"` + 1 warning |
| `BuildQuery{Query:"go", RawQuery:true, Site:["a.com"]}` | `"go"` + 0 warning |
| `BuildQuery{Query:"go", ExcludeTerms:["foo bar"]}` | `go -"foo bar"` |
| `SplitSections("## A\nx\n## B\ny", 8000)` | `headings`，2 节，id `s0 s1` |
| `SplitSections("# 嵌套\n## 子\n", 8000)` | 2 节，level 1 与 2 |
| `SplitSections("```\n# 不是标题\n```\n", 8000)` | `fixed`，1 节 |
| `SplitSections("#hashtag 内容", 8000)` | `fixed` |
| `SplitSections("", 8000)` | `fixed`，1 节，`Chars=0` |
| `Dedupe` 两条同 URL 不同 utm | 留 1 条 |
| `Dedupe` 两条同标题同站点 | 留分高的 |
| `Dedupe` 同域名 5 条 `maxPerDomain=3` | 留 3 条 |

### 7 验收命令与期望输出

```bash
$ go test ./internal/domain/ -v
--- PASS: TestBuildQuery (0.00s)
--- PASS: TestSplitSections (0.00s)
--- PASS: TestDedupe (0.00s)
...
PASS
ok      github.com/LseKit/DeepSeekSearch/internal/domain

$ go test ./internal/domain/ -cover
coverage: 9x.x% of statements

# 🔴 圈复杂度只看**生产代码**：测试函数天然复杂（表驱动 + 多分支），算进来会让每个任务都红。
# 🔴 MUST NOT 写成 `gocyclo ... || echo "无超限函数"` —— `|| echo` 会把「命令不存在」
#    与「有超限」两种失败都变成成功输出，判据恒假绿（本项目已实测踩中）。
$ go tool gocyclo -over 10 -ignore '_test\.go' ./internal/domain/
（无输出 = 通过；任何输出 = 失败）

# F-S1 逐文件 ≤600 行（手工检查）。
# 🔴 CheckSize.sh 是 **Task-11** 的交付物，本任务阶段还不存在 —— 此处用手工等价检查，
#    权威判定等 Task-11 之后再跑。
$ find internal/domain -name '*.go' ! -name '*_test.go' -exec wc -l {} + | sort -rn | head -3
（最大文件 < 600）
```

### 8 完成定义（DoD）

- [ ] `domain` 覆盖率 **≥ 80%**（`go test -cover` 实测）
- [ ] 🔴 **生产代码**圈复杂度零超限：`go tool gocyclo -over 10 -ignore '_test\.go' ./internal/domain/` 无输出（MUST NOT 用 `|| echo`；`CheckSize.sh` 属 Task-11，本轮用手工 F-S1 检查替代）
- [ ] 提交：`feat(domain): 语法拼装/分节/去重`
