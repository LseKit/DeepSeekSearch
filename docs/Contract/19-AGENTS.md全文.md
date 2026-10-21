# 19 · `AGENTS.md` 全文（唯一真源）

> **文件路径**：`/mcp/DeepSeekSearch/Doc/Contract/19-AGENTS.md全文.md`
> **用途**：`GitHubSrc/AGENTS.md` 的**唯一真源**。Task-13 A-01 逐字复制；`Scripts/CheckRepoFiles.sh` 用 `diff` 校验两者字节相同。
> **创建日期**：2026-10-20
> **依据**：AGENTS.md 开放格式（LF Projects，60k+ 项目在用）；Go 官方 `Effective Go` / `Go Code Review Comments`；Google Go Style Guide；Uber Go Style Guide；本项目 `Engineering/20~22` 与 `Contract/10~17`
> **状态**：已冻结（v0.1.0）
> **修改规则**：改这里 → 同步 `GitHubSrc/AGENTS.md` → 跑 `make drift`。MUST NOT 只改其中一处。

---

## 使用说明

下面由两个 HTML 注释标记界定的区间内是**逐字内容**，不是示例。复制时取标记之间的部分（标记本身不进文件）。

提取与校验由 `Scripts/CheckRepoFiles.sh` 与 `Scripts/CheckDocDrift.sh` 实现（两处逻辑一致：按标记切片、去掉前导空行、与 `GitHubSrc/AGENTS.md` 逐字节 `diff`）。

> ⚠️ **本文件里 MUST NOT 再出现那两个标记的任何复制品**（包括示例命令里的字面量）。标记只能各出现一次，否则切片会从错误的行开始 —— 这个坑已经踩过一次。

<!-- BEGIN AGENTS.md -->
# AGENTS.md

> **这份文件是给编码 agent 的，不是给人看的。** 人类入口是 [`README.md`](README.md)。
> 你（agent）在这里能拿到：怎么构建、怎么测、代码长什么样、哪些线不能踩、卡住了怎么办。
> 详细设计在 [`docs/`](docs/)，**按需读，不要通读**（见 §11.1）。

读完本文件后再动手。本文件里的命令你会被要求真的执行，输出会被核对。

---

## 1. 这个项目是什么

**DeepSeekSearch** 是一个 [MCP](https://modelcontextprotocol.io) 服务器，把 DeepSeek 的
`POST /v1/tools/search` 与 `POST /v1/tools/fetch` 两个 HTTP 接口包成三个 MCP 工具：

| 工具 | 作用 |
|---|---|
| `ds_search` | 网页搜索。返回已按 `rerank_score` 降序的结果 |
| `ds_fetch` | 网页正文抓取。大文档按标题分节，避免上下文爆炸 |
| `ds_health` | 本地诊断。检查 Key / 上游连通 / 缓存 / 生效配置 |

对外提供两种传输：`stdio` 与**无状态 `Streamable HTTP`**（MCP 2026-07-28）。

### 1.1 不是做什么（别自己加）

| 不做 | 原因 |
|---|---|
| 结果重排序 / 加权打分 | 上游返回时已按 `rerank_score` 单调降序，重排是用更少信息覆盖更多信息 |
| 固定阈值过滤（`min_rerank`） | 实测同一阈值在不同 query 上保留率 0%~100%，会让整批查询静默变空 |
| 答案合成 | 调用方本身就是 LLM 且有用户完整上下文；合成会销毁引用、隐藏 prompt injection |
| SSE 传输 | 2026-07-28 规范只定义 `stdio` 与 `Streamable HTTP` |
| Key 池 / 多租户 | QPS 是账号级，同账号多 Key 无收益 |
| `ds_research` / `ds_extract` / `no_cache` | 已登记为阶段二技术债 |

### 1.2 许可

本项目以 **MIT** 发布 —— 开源界公认最宽松的主流许可：可商用、可修改、可闭源分发、可再许可，唯一义务是保留版权声明与许可原文。选型理由见 `docs/Decisions/ADR.md` 的 ADR-012。

---

## 2. 十一条硬规则

违反任一条的提交会被拒绝，不允许讨论。

| # | 规则 | 为什么 |
|:-:|---|---|
| **H1** | **文档优先**：改行为 MUST 先改 `docs/Contract/`，再改代码 | 契约是唯一真源。代码先行必然漂移，`make drift` 会红 |
| **H2** | **提交粒度 = 一个原子任务一次 commit**，MUST NOT 把多个原子任务揉进一个 commit，MUST NOT 顺手做别的原子任务 | 粒度粗了就无法用 `docs/Handover/91-验收总表.md` 逐条核对，出了问题也不能只回滚那一个任务 |
| **H3** | **MUST NOT 改测试让它变绿** | 规模/架构/契约测试是护栏。改它等于拆护栏 |
| **H4** | **MUST NOT 自己发明**字段名、错误码、配置键、工具名 | 三处文档与代码有 diff 检查，名字不一致直接红 |
| **H5** | **MUST NOT 读整个 `docs/`**，只读任务点名的章节 | 通读会淹掉重点，你会在细节里编出不存在的字段 |
| **H6** | **"测试通过"必须贴真实命令输出** | 没有输出的"通过"等于没测 |
| **H7** | **`internal/domain` MUST NOT import 本项目其它包** | 它是全项目词汇表，被所有人 import，不能依赖任何人 |
| **H8** | **所有时间 MUST 从注入的 `Clock` 取**，MUST NOT 直接 `time.Now()` | 直接调会让 TTL / 跨日切分 / 熔断窗口无法确定性测试 |
| **H9** | **`stdio` 传输下 `os.Stdout` 只能出协议帧** | 混一个 `fmt.Println` 客户端就解析失败，且没有错误提示 |
| **H10** | **缓存只存上游原文**，MUST NOT 存反转义后的内容 | 反转义策略一变就要清库，且无法回溯原文 |
| **H11** | **每个源文件 MUST 有文件头，`作者` 恒为 `小逸`**；MUST NOT 出现 AI / 助手 / 生成工具署名 | 这是法务声明，也是给下一个 agent 的第一句话。写法见 §5.5 |

---

## 3. 环境与命令

### 3.1 环境要求

```bash
go version                 # 必须 ≥ go1.27（go.mod 写 go 1.27）
go tool golangci-lint version   # v2.x；版本由 go.mod 的 tool 指令 + go.sum 锁定（MUST NOT 用全局安装的版本）
```

### 3.2 命令表（MUST 用 `make`，不要自己拼 `go` 命令）

| 命令 | 作用 | 什么时候跑 |
|---|---|---|
| `make build` | 编译到 `../Bin/DeepSeekSearch`，注入版本 | 改完代码想验证能编 |
| `make test` | `go test -count=1 ./...` | 每次提交前 |
| `make test-race` | 带竞态检测 | **提交前 MUST 跑** |
| `make test-integration` | 真实调用上游（**会计费**） | 只在你明确被要求时 |
| `make lint` | `go tool golangci-lint run ./...`（版本由 `go.mod` 的 `tool` 指令锁定，与 CI 同源） | 提交前 MUST 跑 |
| `make cover` | 覆盖率汇总（只打印一行总覆盖率，**不能**当门槛） | 想看总覆盖率时 |
| `make cover-check` | 🔴 **逐包**覆盖率门槛判定，不达标退出非 0（`21-测试规范` §3.2） | 提交前 MUST 跑 |
| `make size` | 🔴 规模红线 **F-S1 / F-S3 / F-S5**（文件行数 / 包文件数 / 包行数）。**不含 F-S2 / F-S4** —— 函数长度与嵌套深度由 `internal/arch_test.go` 的 `TestFunctionLength` / `TestNestingDepth` 用 `go/ast` 强制，随 `make test` 跑（分工见 `Engineering/22-架构守护规则.md` §三） | 提交前 |
| `make drift` | 文档漂移 🔴 **8** 项检查 | 提交前 |
| `make repo-files` | A/B 级发布文件齐备 + 源文件署名 | 提交前 |
| `make check` | 🔴 **上面 7 个的总和**（`build` `test-race` `lint` `cover-check` `size` `drift` `repo-files`；除 integration） | **收尾时只需跑这一条** |

### 3.3 提交前的最小集合

```bash
make check
```

它等价于 `build + test-race + lint + cover-check + size + drift + repo-files`（7 项）。任何一项红，MUST 修完再提交。

> 🔴 是 `cover-check` 而不是 `cover`：后者只打印一行总覆盖率，无法判定 §6.3 的**逐包**门槛。

### 3.4 跑单测

```bash
go test ./internal/domain/ -run TestNormalizeUrl -v   # 单个测试
go test ./internal/resilience/ -race -cover           # 单包 + 覆盖率
go test ./... -count=1                                # 全量（禁缓存）
```

**MUST 带 `-count=1`**：Go 会缓存测试结果，改了代码仍显示 `ok (cached)`。

---

## 4. 代码结构地图

🔴 **git 仓库根就是下面这个 `GitHubSrc/`**（`.git` 在这一层）。所有 `git` 命令 MUST 在 `GitHubSrc/` 内执行；
`Doc/` 与部署根的 `Scripts/` **不在仓库里**（`Doc/` 由 `SyncDocs.sh` 单向同步到 `docs/`）。

```
GitHubSrc/
├── cmd/deepseeksearch/     装配。唯一 os.Exit 的地方
├── internal/domain/        纯类型 + 41 个错误码 + 纯函数。零内部依赖
├── internal/config/        toml + dotenv + env 合并，校验
├── internal/logging/       slog + 自研日轮转
├── internal/deepseek/      上游 HTTP 客户端（一次往返 + 解析，不重试不缓存）
├── internal/resilience/    限流 / 重试 / 预算 / 熔断 / 缓存 / singleflight
├── internal/tools/         MCP 工具：校验 + 编排 + 渲染
├── internal/transport/     stdio + Streamable HTTP + 中间件链
├── internal/arch_test.go   架构守护测试
├── Scripts/                CheckSize / CheckDocDrift / SyncDocs / Release
└── docs/                   从 ../Doc 单向同步，MUST NOT 手写
```

> 测试夹具在**包内** `internal/<pkg>/testdata/`，仓库根**没有** `testdata/`：`config` 8 份 `.toml`、`deepseek` 2 组 `.json` + 同名 `.meta`、`tools` 的 `search_input_schema.json`。清单与更新规则见 `docs/Engineering/21-测试规范.md` §五。

### 4.1 改哪里（决策表）

| 你要做的事 | 改哪个文件 | 不要碰 |
|---|---|---|
| 加一个参数校验 | `internal/tools/*_validate.go` | `domain`（除非是纯函数边界） |
| 改错误码文案 | `internal/domain/codes.go` + `docs/Contract/14-错误契约.md` | 别的地方 |
| 加一个配置项 | `internal/config/config.go` + `docs/Contract/13-配置契约.md` §二 与 §三 + `config.example.toml` | 三处必须同改 |
| 新增 MCP 工具 | 新文件 `internal/tools/xxx.go` + `registry.go` 加 1 行 | `transport` |
| 新增传输 | 新文件 `internal/transport/xxx.go` + `Build()` 加 case | `tools` `deepseek` |
| 改上游请求体 | `internal/deepseek/request.go` + `docs/Contract/12-数据契约.md` §一 | 别在 `tools` 里拼 JSON |
| 改日志事件名 | `internal/logging/events.go` + `docs/Tasks/Task-04` A-02 | 三处文档只引用不重写 |

### 4.2 依赖方向（单向，MUST NOT 反向）

```
cmd → transport, tools, resilience, deepseek, config, logging
tools → domain, config, deepseek, resilience
transport → domain, config, logging
resilience → domain, config
deepseek → domain, config
logging → domain, config
config → domain
domain → （标准库，无本项目依赖）
```

违规会被 `internal/arch_test.go` 拦住（白名单在 `docs/Contract/10-包依赖矩阵.md` §二）。

---

## 5. Go 编码标准

### 5.1 官方规则（Go Code Review Comments + Effective Go）

| # | 规则 | 反例 → 正例 |
|:-:|---|---|
| G1 | 命名用 **MixedCaps**，不用下划线 | `api_key` → `apiKey`；`MAX_RETRY` → `maxRetry` |
| G2 | 首字母缩写**全大写或全小写**，不混写 | `UrlPath` ❌ / `urlPath` ✅ / `URLPath` ✅（导出）/ `httpUrl` → `httpURL` |
| G3 | receiver 短且一致，同一类型所有方法同名 | `func (this *Client)` → `func (c *Client)` |
| G4 | `ctx context.Context` 是**第一个参数**，MUST NOT 存进 struct | `Search(req, ctx)` ❌ |
| G5 | 错误字符串**小写、无标点结尾** | `"Failed to fetch URL."` ❌ → `"failed to fetch url"` ✅ |
| G6 | 正常错误 `return error`，**不用 panic** | `panic("bad config")` ❌ |
| G7 | 错误必须 wrap，`%w` **只能一个**；多个用 `errors.Join` | `fmt.Errorf("%w: %w", a, b)` ❌ |
| G8 | 判断错误用 `errors.Is` / `errors.As`，**不匹配字符串** | `strings.Contains(err.Error(), "timeout")` ❌ |
| G9 | **不写 `init()`** | 依赖注入在 `cmd` 里显式做 |
| G10 | 接口定义在**使用方**包；"accept interfaces, return structs" | 例外见 §5.3 |
| G11 | 导出符号必须有 doc 注释，**以标识符名开头、句号结尾** | `// Search 调用上游搜索接口并返回解析结果。` ✅ |
| G12 | 每个包必须有包注释 | 写在 `doc.go` 或首个文件顶部 |
| G13 | 提前 return（卫语句），嵌套 ≤3 层 | `if ok { if ok2 { ... } }` → 反转条件早退 |
| G14 | 零值应可用 | `var buf bytes.Buffer` 不需要 `New` |
| G15 | `defer` 里的错误显式处理 | 命名返回值 + `defer func(){ err = errors.Join(err, f.Close()) }()` |
| G16 | goroutine 必须有**退出路径**（ctx 或 close） | 裸 `go func(){}()` 会泄漏 |
| G17 | 切片预分配 | `make([]T, 0, n)` |
| G18 | 不用 `fmt.Sprint` 拼日志，用结构化字段 | `logger.Info("call.end", "ms", 812)` ✅ |

### 5.2 本项目追加的规则

| # | 规则 | 为什么 |
|:-:|---|---|
| P1 | 时间一律从 `deps.Clock` 取 | H8 |
| P2 | 文本长度按 **rune**，不按 byte | 中文 1 字 3 字节，`s[0:8000]` 会切出 `�` |
| P3 | 协议数据 MUST NOT 用 `map[string]any` | 上游**静默忽略**未知字段，拼错键名不报错，只会静默返回错结果 |
| P4 | 包装 `http.ResponseWriter` MUST 实现 `Flush()` **和** `Unwrap()` | 少了 Flush 流式响应静默卡死 3 分钟，零错误日志 |
| P5 | 上游错误 MUST 转成 `*domain.UpstreamError`，MUST NOT 返回裸 `url.Error` | 上层只按错误码决策，不解析字符串 |
| P6 | `internal/config` MUST NOT 创建目录 / 打开日志 / 发请求 | 它是纯加载 + 校验，副作用留给 `cmd` |
| P7 | `internal/deepseek` MUST NOT import `mcp` | 上游契约与协议解耦，换协议不动 HTTP 层 |
| P8 | 缓存 key 用 `domain.NormalizeUrl` 的结果，但**发给上游的仍是原始 URL** | 归一化会改语义；归一只是为了让 `?utm_source=x` 这类参数不击穿缓存 |
| P9 | 日志 MUST NOT 出现 API Key / Bearer token | 先 `Redacted()` 再打印 |
| P10 | 新增 `exported` 符号 MUST 同时加 `Test<Name>` | 测试规范要求 |

### 5.3 接口定义位置的两条例外

| 接口 | 定义处 | 为什么例外 |
|---|---|---|
| `deepseek.Client` | `internal/deepseek` | `tools` 依赖它，但它与 HTTP 实现同生命周期；拆出去会引入第三层包 |
| `resilience.Clock` | `internal/resilience` | 被 `logging` 用；放谁都成环，靠**参数注入**打破 |

### 5.4 注释规范（中文，MUST）

```go
// ✅ 写「为什么」—— 代码看不出来的东西
// 上游把 403/404/500/域名不存在/内网 IP/二进制内容全部塌缩成同一个 404，
// 所以这里无法按状态码分类，只能把 7 种成因写进 hint 交给调用方判断。
// 成因清单见 docs/Contract/14-错误契约 §2.3 与 §2.3.1。

// ❌ 写「做什么」—— 读者看代码就知道
// 遍历 results，对每个结果取 url 字段
for _, r := range results {
```

**只有四类内容值得写注释**：为什么这样做 / 踩过的坑 / 实测数据 / 反直觉的取舍。

**禁止**：复述代码、`TODO` 不带日期与人（格式 `// TODO(2026-11-01, 名字): ...`）、注释掉的死代码、英文注释、表情符号。

### 5.5 文件头与作者署名

每个源文件（`.go` / `.sh`）MUST 以**文件头注释**开头，字段与顺序固定：

```go
// 文件名: dedupe.go
// 用途: 搜索结果三层去重（URL hash / 转载标题 / 域名配额），输出保持 rerank_score 降序。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package domain
```

| 字段 | 规定 |
|---|---|
| `文件名` | 与磁盘上的实际文件名一致（含扩展名） |
| `用途` | 一句话说清**这个文件负责什么**。MUST NOT 写「这是 xxx 模块」这种同义反复 |
| `创建日期` | `YYYY-MM-DD`。新建时定下，**之后修改不改它** |
| `作者` | 恒为 **`小逸`**。MUST NOT 出现 AI / 助手 / 模型 / 生成工具的名字 |
| `项目` | 恒为 `DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>` |

**MUST NOT 出现的署名**：`Co-authored-by: AI`、`Generated by ...`、`AI 协助`、`Hermes`、`Claude`、`GPT`、`Qoder`、任何第二作者。**这个项目只有一个作者：小逸。**

`.sh` 文件的头注释写在 shebang **之后**：

```bash
#!/usr/bin/env bash
# 文件名: CheckSize.sh
# 用途: 检查 F-S1 / F-S3 / F-S5 规模红线，违规时非零退出（F-S2/F-S4 归 arch_test.go）。
# 创建日期: 2026-10-20
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
set -euo pipefail
```

**包注释只写在 `doc.go`**（每个包恰好一个），其它文件只写文件头 —— 否则每个文件都会变成一份重复的包文档：

```go
// 文件名: doc.go
// 用途: 定义全项目共用的类型、错误码与纯函数，并提供包级文档。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package domain 定义全项目共用的类型、错误码与纯函数。
//
// 本包 MUST NOT 导入本项目其它包，也 MUST NOT 做任何 IO。
package domain
```

> 为什么要有文件头：这个仓库长期由 agent 维护，`用途` 那一行是**给下一个 agent 的第一句话**，比翻 git log 直接。作者字段是法务声明，不是致谢栏。

---

## 6. 测试标准

### 6.1 三层

| 层 | 位置 | 依赖 | 触发 |
|---|---|---|---|
| **L1 单元** | `*_test.go` | 零外部依赖（无网络、无落盘） | `make test` |
| **L2 契约黄金** | `*_test.go` + 包内 `testdata/`（`.json` + 同名 `.meta`、`.toml`） | 固定输入输出文件 | `make test` |
| **L3a 协议/端到端** | `*_test.go`（进程内起 server + `httptest` 假上游 + stdio 管道），**MUST NOT 打 build tag** | 本地假上游，**无 API Key、零计费** | `make test` |
| **L3b 真实上游集成** | `*_integration_test.go`，**首行 MUST 打** `//go:build integration` | 真实上游 + 真 Key（**计费**） | `make test-integration` |

> 🔴 原来的「L3 = 真实上游 + 真 Key」与「协议测试 P1~P26 属 L3（`httptest`）」自相矛盾：`httptest` 是本地假上游，不需要 Key、不计费。这个归属混乱只有两种结局，都是事故 —— 协议测试被打上 `integration` tag ⇒ 日常 `make test` **静默不跑**，P 断言形同失效且无人发现；或不打 tag 却连真实上游 ⇒ **违反 T1「不计费」铁律**，每次 `go test` 都花真钱。故拆成 L3a / L3b，判据见 `Engineering/21-测试规范.md` §一 与铁律 T7。

### 6.2 铁律

| # | 规则 |
|:-:|---|
| T1 | `make test` MUST **不产生任何计费调用** |
| T2 | L1 MUST NOT 访问网络、MUST NOT 写项目目录外的文件 |
| T3 | 测试 MUST 能单独 `go test -run TestXxx` 通过（不依赖执行顺序） |
| T4 | 时间相关 MUST 用假 `Clock`，**MUST NOT 用 `time.Sleep` 等真实时间** |
| T5 | 每个 `exported` 函数 MUST 有 ≥1 个 `Test<函数名>` |
| T6 | ≥3 个用例 MUST 用表驱动 + `t.Run` |
| T7 | 无共享状态的 L1 MUST 加 `t.Parallel()` |
| T8 | 断言用标准库 + `github.com/google/go-cmp/cmp`（🔴 **仅测试**依赖，只能出现在 `_test.go`；「4 个第三方」是运行时口径，见 `Contract/10-包依赖矩阵.md` §2.9.1）。**MUST NOT 引入 testify** |
| T9 | 黄金文件 MUST 由真实抓取得到，**MUST NOT 手写假 JSON** |
| T10 | 断言 MUST 检查返回值内容，不能只有 `if err != nil { t.Fatal }` |

### 6.3 覆盖率门槛

| 包 | 门槛 |
|---|:-:|
| `internal/domain` `internal/config` `internal/resilience` | **≥ 80%** |
| `internal/deepseek` `internal/tools` | ≥ 70% |
| `internal/transport` | ≥ 60% |
| 项目整体 | **≥ 65%**（低于此禁止发版） |

### 6.4 标准表驱动骨架

```go
func TestBuildQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   domain.SearchRequest
		want string
	}{
		{name: "只传 query 时原样透传", in: domain.SearchRequest{Query: "生酮饮食"}, want: "生酮饮食"},
		{name: "site 多个用 OR 连接", in: domain.SearchRequest{Query: "go", Site: []string{"a.com", "b.com"}}, want: "go site:a.com OR site:b.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, _ := domain.BuildQuery(tt.in); got != tt.want {
				t.Fatalf("BuildQuery() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

---

## 7. 规模红线（F-S1 ~ F-S6）

| 编号 | 红线 | 统计口径 |
|:-:|---|---|
| **F-S1** | 单文件 ≤ **600** 行 | 含空行与注释 |
| **F-S2** | 单函数 ≤ **50** 行 | **不含**注释与空行 |
| **F-S3** | 圈复杂度 ≤ **10** | `if`/`for`/`case`/`&&`/`\|\|` 各 +1 |
| **F-S4** | 嵌套深度 ≤ **3** | `if/for/switch/func literal` |
| **F-S5** | 单包 ≤ 3000 行 / ≤10 文件 / ≤20 导出符号 | 非测试文件 |
| **F-S6** | 豁免 MUST 登记在 `docs/Engineering/豁免台账.md` | 未登记的超限 = 违规 |

**MUST NOT** 用删注释、压行、加 `//nolint` 过关。撞线就拆 —— 拆不动才申请豁免，且要说清「为什么不拆」。

**存量违规**以 `Scripts/size-baseline.json` 冻结（ADR-017），只减不增；新增代码 MUST 从第一天起达标。

---

## 8. 禁止清单（review 逐条对照）

| # | 禁止 | 为什么 |
|:-:|---|---|
| X1 | `panic` 用于正常错误路径 | 一个 panic 带走整个进程，stdio 下客户端直接断连 |
| X2 | 包级可变 `var`（除哨兵错误、正则、常量表） | 破坏可测试性，多 goroutine 下是数据竞争 |
| X3 | `init()` 做 IO | 执行时机不可控，测试里无法替换 |
| X4 | 无 `ctx` 的 `http.NewRequest` | 调用无法取消 |
| X5 | `defer resp.Body.Close()` 前不检查 `err` | nil panic |
| X6 | 包装 `ResponseWriter` 缺 `Flush`/`Unwrap` | P4 |
| X7 | 用 `strings.Contains(err.Error(), ...)` 判错误类型 | 用 `errors.Is`/`errors.As` |
| X8 | 把 API Key 拼进任何 `fmt.Errorf` | 会进日志 |
| X9 | 在 `ds_fetch` 里对正文做 HTML 清洗 | 上游给的已是 Markdown，清洗会毁代码块与表格 |
| X10 | 按 byte 处理正文长度 | P2 |
| X11 | 手写 `docs/` 下的任何文件 | 那是 `Scripts/SyncDocs.sh` 的产物 |
| X12 | 提交二进制到 git | `Bin/` 在 `.gitignore` 里；二进制只进 Release 附件 |
| X13 | 用软链接 / 硬链接 | 项目铁律 |
| X14 | 在项目目录外写文件 | 项目铁律（只写 `Data/` `Logs/`） |

---

## 9. 文档契约与单一真源

**同一个事实只写在一个地方**，其它文档只引用。

| 事实 | 唯一真源 |
|---|---|
| Go interface 签名 | `docs/Contract/11-接口契约.md` |
| 错误码 / 消息 / hint | `docs/Contract/14-错误契约.md` §二 |
| 配置键与默认值 | `docs/Contract/13-配置契约.md` §二（模板） |
| 日志事件名 | `docs/Tasks/Task-04-日志与轮转.md` A-02 |
| 包白名单 | `docs/Contract/10-包依赖矩阵.md` §二 |
| 工具名与参数 | `docs/Contract/12-数据契约.md` §四 |
| 仓库文件清单 | `docs/Contract/18-仓库文件契约.md` |
| AGENTS.md 正文 | `docs/Contract/19-AGENTS.md全文.md` |
| 版本号 | `Makefile` 的 `VERSION` |
| 阶段二欠账 | `docs/Decisions/16-技术债登记表.md` |

**改了真源没改引用 = 漂移**，`make drift` 会红。改一处 MUST 同时改全部引用它的地方。

---

## 10. 提交与 PR

### 10.1 提交信息

格式：`<type>(<scope>): <中文短句>`

| type | 用途 |
|---|---|
| `feat` | 新功能 |
| `fix` | 修 bug |
| `docs` | 只改文档 |
| `refactor` | 不改行为 |
| `test` | 只改测试 |
| `build` | 构建/Makefile/CI |
| `chore` | 杂项（依赖、豁免登记） |

**scope** 用包名：`domain` `config` `logging` `deepseek` `resilience` `tools` `transport` `cmd` `repo`。

示例：

```
feat(domain): 时间/URL/切片/反转义纯函数
fix(transport): statusWriter 补 Flush 与 Unwrap，修复流式响应卡死
docs(contract): 配置契约新增 [limits.budget] 四项
```

**MUST NOT** 写 `update` / `fix bug` / `改进` 这种没有信息量的。

### 10.2 PR 检查清单（模板会自动带出来，逐条勾）

- [ ] 对应 Task 与原子任务已填（如 `Task-02 / A-02`）
- [ ] `make check` 全绿（贴输出）
- [ ] 新 `exported` 符号都有测试
- [ ] 契约如有变更，`docs/Contract/` 已同步
- [ ] 没有提交二进制、`.env`、`Data/`、`Logs/`

### 10.3 分支

`main` 常驻可发布。功能分支 `feat/<slug>`，修复 `fix/<slug>`。只开 **Squash and merge**。

---

## 11. 你的工作流（agent 专用）

### 11.1 读文档的正确姿势

**按需读，不要通读。** 每个原子任务文档的「必读」列了它真正需要的 1~3 个章节。

| 你要做什么 | 只读这些 |
|---|---|
| 建目录、写 Makefile | `docs/00-项目宪章.md` §三 规模红线、§五 依赖方向；`docs/01-架构总览.md` §一 目录结构图 |
| 写任何 Go 代码 | 本文件 §5 |
| 写任何测试 | 本文件 §6 |
| 定义数据结构/字段 | `docs/Contract/12-数据契约.md` 对应小节（逐字抄，不许改写字段名） |
| 定义函数签名 | `docs/Contract/11-接口契约.md`（逐字抄） |
| 定义错误码 | `docs/Contract/14-错误契约.md` §二 §三 |
| 写配置项 | `docs/Contract/13-配置契约.md` §二 → §三 |
| 写传输层 | `docs/Contract/15-协议契约.md` §四 §六 §七之二 §七之三 §十 |
| 想知道「为什么不用 XX」 | `docs/Decisions/ADR.md` |
| 想知道「这个功能什么时候做」 | `docs/Decisions/16-技术债登记表.md` |

### 11.2 开新任务时先答三个问题

在写任何代码之前，用三行回答：

```
Q1 本任务要创建/修改哪几个文件？
Q2 哪一条约束最容易被写错？
Q3 验收命令有几条？
```

答错说明读错文档了。此时纠正成本是「重读一节」，而写完再纠正是「重写一个包」。

### 11.3 一个 Task 文档一个窗口

一个窗口做完整份 Task 文档里的**全部原子任务**，但提交粒度仍是一个原子任务一次：

```
答三问 → 实现 → 跑验收 → 贴 DoD → commit → 切下一个原子任务（先重读它的八节）
```

三条纪律，缺一不可：

1. **每完成一个原子任务就立刻 commit**，MUST NOT 攒到文档末尾一起提交 —— 窗口中途挂掉时，必须能精确回到最后一个已验收的状态。
2. **切换原子任务前 MUST 重新读它的八节**（0 前置 / 1 上下文 / 2 要创建的文件 / 3 实现要求 / 4 禁止事项 / 5 参考骨架 / 6 测试代码 / 7 验收命令），MUST NOT 凭上一个任务的记忆写。同一文档里各任务的入参、返回体、错误码、测试断言各不相同，凭记忆写就会编出不存在的字段。
3. **MUST NOT 在一个窗口里做第二份 Task 文档**：跨文档意味着换了整套接口与契约，上下文噪音才会真正失控。

> 原规则是「一个原子任务一个窗口」。放宽到文档级后，上面第 2 条就是替代「换窗口」的隔离手段 —— 少了它，这个放宽就是拿幻觉换省事。

### 11.4 收尾动作（每次 MUST 做）

```bash
make check                 # 🔴 7 项全绿（build/test-race/lint/cover-check/size/drift/repo-files）
git status --short         # 确认没多出文件
```

然后逐条回报：

1. `make check` 的真实输出
2. 本任务的 DoD 清单，逐条 `[x]` 或 `[ ]`（`[ ]` 必须写原因）
3. 建议的 commit message

---

## 12. 卡住了怎么办

按顺序试，**不要跳过**：

| 步 | 动作 |
|:-:|---|
| 1 | **读报错原文**，不要猜 |
| 2 | 查本文件 §8「禁止清单」与 `docs/Handover/90-Qoder执行手册.md` §六「8 个常见失败模式」 |
| 3 | 查 `docs/Contract/14-错误契约.md`（错误码在说什么） |
| 4 | 查 `docs/Decisions/ADR.md`（是不是「本来就这么设计的」） |
| 5 | **停下来**，把报错原文 + 已尝试的动作列出来问 |

**MUST NOT** 自己发明一套方案绕过。绕过的那套会在下一个原子任务里撞墙。

### 12.1 六个最容易踩的坑

| 现象 | 原因 | 处理 |
|---|---|---|
| 客户端连上了但什么都不响应 | 中间件包装 `ResponseWriter` 没实现 `Flush`+`Unwrap` | P4，逐字抄 `docs/Contract/11` §6.1 |
| 日期差一天 | 直接用 UTC 取日期 | `docs/Contract/12` §二：先 `In(cst8)` 再取 `YYYY-MM-DD` |
| 中文末尾出现 `�` | 按 byte 切片 | `domain.SliceByRune` |
| 代码块里的 `\n` 变真换行 | 反转义时删了所有反斜杠 | 只反转义实测全集 **32** 个字符（原 11 字符表漏了 21 种，ADR-013）；且 MUST 单趟替换，多趟会把嵌套转义 `\\\.` 过度还原 |
| 改配置不生效 | 优先级顺序反了 / `toml` tag 拼错 | `Config.Source(key)` 打印来源 |
| 反代 / 直连局域网后全部 403 | 两层各查各的：`hostGuard` 查 **Host 头**（`3093`）；SDK 的 DNS 重绑定防护查「**本连接本地地址是回环** + Host 非回环」 | 反代或 `host="0.0.0.0"`：`allow_non_local_host=true` **且** `auth_enabled=true`；反代到回环监听且转发原始 Host：再加 `disable_localhost_protection=true` |

---

## 13. 术语表

| 术语 | 含义 |
|---|---|
| **原子任务** | 一次 IDE 会话能完成的最小工作单元，对应 `docs/Tasks/` 里的 `A-NN` |
| **契约** | `docs/Contract/` 下的冻结文档。改它 MUST 走 ADR |
| **单一真源** | 同一事实只在一处定义，其它地方只引用（见 §9） |
| **漂移** | 真源改了但引用没改。`make drift` 检测 |
| **规模红线** | F-S1~F-S6。`make size` 检测 |
| **降级** | 缓存挂了按未命中继续，不让工具变砖 |
| **计费调用** | 打到 `/v1/tools/*` 的请求。¥5/1000 次，只有 200 且非空才计费 |
| **快照** | 一次具体抓取的正文（`cache_id`）。TTL 过期后 MUST 报 `3080`，MUST NOT 用新内容替代 |
| **无状态 Streamable HTTP** | MCP 2026-07-28 的传输形态。`Stateless` 恒为 `true`，不提供开关 |
<!-- END AGENTS.md -->

---

## 与其它文档的关系

| 文档 | 关系 |
|---|---|
| `Contract/18-仓库文件契约.md` | AGENTS.md 是 A3 级文件，验收 = 与本文档 `diff` 无差异 |
| `Engineering/20~22` | AGENTS.md §5~§7 是它们的**摘要**（面向 agent 的压缩版）；细节以 Engineering 为准 |
| `Handover/90-Qoder执行手册.md` | 手册是「操作流程」，AGENTS.md 是「宪法」。手册引用本文件，不重复 |
| `README.md` | 面向人类。AGENTS.md MUST NOT 重复 README 的快速开始 |
