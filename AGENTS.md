# AGENTS.md

> **这份文件是给编码 agent 的，不是给人看的。** 人类入口是 [`README.md`](README.md)。
> 详细设计在 [`docs/`](docs/)，**按需读，不要通读**（见 §11.1）。本文件里的命令你会被要求真的执行，输出会被核对。
> 仓库：[`LseKit/DeepSeekSearch`](https://github.com/LseKit/DeepSeekSearch)　许可：MIT（见 §1.2）

---

## 1. 这个项目是什么

**DeepSeekSearch** 是一个 [MCP](https://modelcontextprotocol.io) 服务器，把 DeepSeek 的
`POST /v1/tools/search` 与 `POST /v1/tools/fetch` 两个 HTTP 接口包成五个 MCP 工具：

| 工具 | 作用 |
|---|---|
| `ds_search` | 网页搜索，返回已按 `rerank_score` 降序的结果 |
| `ds_fetch` | 网页正文抓取；大文档按标题分节，避免上下文爆炸 |
| `ds_health` | 本地诊断：Key / 上游连通 / 缓存 / 生效配置 |
| `ds_research` | 组合研究：1 次搜索 + 并发抓取前 N 条来源，返回带编号引用的材料 |
| `ds_extract` | 批量抓取：1~10 个 URL 并发，单条失败只影响该条 |

对外两种传输：`stdio` 与**无状态 `Streamable HTTP`**（MCP 2026-07-28）。

### 1.1 不是做什么（别自己加）

| 不做 | 原因 |
|---|---|
| 结果重排序 / 加权打分 | 上游已按 `rerank_score` 单调降序，重排是用更少信息覆盖更多信息 |
| 固定阈值过滤（`min_rerank`） | 实测同一阈值在不同 query 上保留率 0%~100%，会让整批查询静默变空 |
| 答案合成 | 调用方本身就是 LLM 且持有完整上下文；合成会销毁引用、隐藏 prompt injection |
| SSE 传输 | 2026-07-28 只定义 `stdio` 与 `Streamable HTTP` |
| Key 池 / 多租户 | QPS 是账号级，同账号多 Key 无收益 |
| 阶段二能力 | 见 `docs/Decisions/16-技术债登记表.md`；`ds_research` / `ds_extract` / `no_cache` 已于 2026-10-21 实现 |

### 1.2 许可

**MIT**：可商用、可修改、可闭源分发、可再许可，唯一义务是保留版权声明与许可原文（选型见 ADR-012）。

---

## 2. 十条硬规则

违反任一条的提交会被拒绝，不允许讨论。

| # | 规则 | 为什么 |
|:-:|---|---|
| **H1** | **代码为准**：运行时行为以代码为唯一真源；改行为 MUST 同步 `docs/Contract/` | 文档无法执行；同步由 `make drift` 校验 |
| **H2** | **提交粒度 = 一个原子任务一次 commit**，MUST NOT 揉进别的任务 | 粒度粗了无法逐条核对，出问题也不能只回滚那一个任务 |
| **H3** | **MUST NOT 改测试让它变绿** | 规模/架构/契约测试是护栏，改它等于拆护栏 |
| **H4** | **MUST NOT 自己发明**字段名、错误码、配置键、工具名 | 三处文档与代码有 diff 检查，名字不一致直接红 |
| **H5** | **MUST NOT 读整个 `docs/`**，只读任务点名的章节 | 通读会淹掉重点，并在细节里编出不存在的字段 |
| **H6** | **"测试通过"必须贴真实命令输出** | 没有输出的"通过"等于没测 |
| **H7** | **`internal/domain` MUST NOT import 本项目其它包** | 它是全项目词汇表，被所有人 import，不能依赖任何人 |
| **H8** | **所有时间 MUST 从注入的 `Clock` 取**，MUST NOT 直接 `time.Now()` | 否则 TTL / 跨日切分 / 熔断窗口无法确定性测试 |
| **H9** | **`stdio` 传输下 `os.Stdout` 只能出协议帧** | 混一个 `fmt.Println` 客户端就解析失败，且没有错误提示 |
| **H10** | **缓存只存上游原文**，MUST NOT 存反转义后的内容 | 反转义策略一变就要清库，且无法回溯原文 |

> 源文件文件头格式见 `docs/Engineering/20-编码规范.md` §4.4（`make repo-files` 会校验）。

---

## 3. 环境与命令

### 3.1 环境要求

```bash
go version                       # 必须 ≥ go1.27（go.mod 写 go 1.27，toolchain 行钉补丁版本）
go tool golangci-lint version    # v2.x；版本由 go.mod 的 tool 指令锁定，MUST NOT 用全局安装版
```

### 3.2 命令表（MUST 用 `make`，不要自己拼 `go` 命令）

| 命令 | 作用 | 什么时候跑 |
|---|---|---|
| `make build` | 编译到 `../Bin/DeepSeekSearch`，注入版本 | 改完代码想验证能编 |
| `make test` | `go test -count=1 ./...` | 每次提交前 |
| `make test-race` | 带竞态检测 | **提交前 MUST 跑** |
| `make test-integration` | 真实调用上游（**会计费**） | 只在你明确被要求时 |
| `make lint` | `go tool golangci-lint run ./...` | 提交前 MUST 跑 |
| `make cover-check` | 🔴 **逐包**覆盖率门槛，不达标退出非 0 | 提交前 MUST 跑 |
| `make size` | F-S1 / F-S3 / F-S5（F-S2 / F-S4 由 `arch_test.go` 用 `go/ast` 强制） | 提交前 |
| `make drift` | 文档漂移 8 项检查 | 提交前 |
| `make repo-files` | A 级发布文件齐备 | 提交前 |
| `make check` | 上面 7 项的总和（除 integration） | **收尾只需跑这一条** |

> `cover` 只打印一行总覆盖率，**不能**当门槛；判门槛的是 `cover-check`。

### 3.3 跑单测

```bash
go test ./internal/domain/ -run TestNormalizeUrl -v   # 单个测试
go test ./... -count=1                                # 全量（MUST 带 -count=1，否则显示 ok (cached)）
```

---

## 4. 代码结构地图

🔴 **git 仓库根就是 `GitHubSrc/`**（`.git` 在这一层）。所有 `git` 命令 MUST 在 `GitHubSrc/` 内执行；
`docs/` 是仓库内唯一文档根；部署根的 `Scripts/` 不在仓库里。

```
GitHubSrc/
├── cmd/deepseeksearch/     装配。唯一 os.Exit 的地方
├── internal/domain/        纯类型 + 42 个错误码 + 纯函数。零内部依赖
├── internal/config/        toml + dotenv + env 合并，校验
├── internal/logging/       slog + 自研日轮转
├── internal/deepseek/      上游 HTTP 客户端（一次往返 + 解析，不重试不缓存）
├── internal/health/        ds_health 诊断 + 用量统计（只读依赖，独立成包守住行数上限）
├── internal/resilience/    接口根：Clock / Config / 五类能力接口 / Reason
│   ├── ratelimit/          限流（令牌桶 + 在飞配额）
│   ├── retry/              重试（退避 + 预算）
│   ├── budget/             预算（字符 / 窗口 / 日累计）
│   ├── circuit/            熔断
│   └── cache/              SQLite 缓存 + 进程内/跨进程 singleflight
├── internal/tools/         MCP 工具：校验 + 编排 + 渲染
├── internal/transport/     stdio + Streamable HTTP + 中间件链
├── internal/toolschema/    工具 JSON Schema 字面量（纯数据叶子包）
├── internal/mcputil/       入参/结果薄封装（叶子包）
├── internal/toolvalidate/  工具层纯校验函数（叶子包）
├── internal/arch_test.go   架构守护测试
├── Scripts/                CheckSize / CheckDocDrift / CheckRepoFiles / Release
└── docs/                   唯一文档根（入库，直接维护）
```

### 4.1 改哪里（决策表）

| 你要做的事 | 改哪个文件 | 不要碰 |
|---|---|---|
| 加一个参数校验 | `internal/tools/*_validate.go` | `domain`（除非是纯函数边界） |
| 改错误码文案 | `internal/domain/codes.go` + `docs/Contract/14-错误契约.md` | 别的地方 |
| 加一个配置项 | `internal/config/config.go` + `docs/Contract/13-配置契约.md` §二/§三 + `config.example.toml` | 三处必须同改 |
| 新增 MCP 工具 | 新文件 `internal/tools/xxx.go` + `registry.go` 加 1 行 | `transport` |
| 新增传输 | 新文件 `internal/transport/xxx.go` + `Build()` 加 case | `tools` `deepseek` |
| 改上游请求体 | `internal/deepseek/request.go` + `docs/Contract/12-数据契约.md` §一 | 别在 `tools` 里拼 JSON |
| 改日志事件名 | `internal/domain/events.go` + `docs/Tasks/Task-04` A-02 | 代码是定义处，文档只做说明 |

### 4.2 依赖方向（单向）

```
cmd → transport, tools, resilience, deepseek, config, logging
tools → domain, config, deepseek, resilience, health, mcputil, toolvalidate
transport → domain, config, logging
resilience/{ratelimit,retry,budget,circuit,cache} → domain, resilience
deepseek | logging | config → domain
domain → （标准库，无本项目依赖）
```

违规会被 `internal/arch_test.go` 拦住（白名单在 `docs/Contract/10-包依赖矩阵.md` §二）。

---

## 5. Go 编码标准

### 5.1 官方规则（Go Code Review Comments + Effective Go）

| # | 规则 |
|:-:|---|
| G1 | 命名用 **MixedCaps**，不用下划线 |
| G2 | 首字母缩写**全大写或全小写**：`urlPath` ✅ / `UrlPath` ❌ |
| G3 | receiver 短且一致，同一类型所有方法同名 |
| G4 | `ctx context.Context` 是**第一个参数**，MUST NOT 存进 struct |
| G5 | 错误字符串**小写、无标点结尾** |
| G6 | 正常错误 `return error`，**不用 panic** |
| G7 | 错误必须 wrap，`%w` **只能一个**；多个用 `errors.Join` |
| G8 | 判断错误用 `errors.Is` / `errors.As`，**不匹配字符串** |
| G9 | **不写 `init()`**；依赖注入在 `cmd` 里显式做 |
| G10 | 接口定义在**使用方**包；accept interfaces, return structs |
| G11 | 导出符号必须有 doc 注释，以标识符名开头、句号结尾 |
| G12 | 每个包必须有包注释（写在 `doc.go`） |
| G13 | 提前 return（卫语句），嵌套 ≤3 层 |
| G14 | 零值应可用（`var buf bytes.Buffer` 不需要 `New`） |
| G15 | `defer` 里的错误显式处理 |
| G16 | goroutine 必须有**退出路径**（ctx 或 close） |
| G17 | 切片预分配 `make([]T, 0, n)` |
| G18 | 不用 `fmt.Sprint` 拼日志，用结构化字段 |

### 5.2 本项目追加

| # | 规则 | 为什么 |
|:-:|---|---|
| P1 | 时间一律从 `deps.Clock` 取 | H8 |
| P2 | 文本长度按 **rune**，不按 byte | 中文 1 字 3 字节，按 byte 切片会切出 `�` |
| P3 | 协议数据 MUST NOT 用 `map[string]any` | 上游**静默忽略**未知字段，拼错键名不报错，只返回错结果 |
| P4 | 包装 `http.ResponseWriter` MUST 实现 `Flush()` **和** `Unwrap()` | 少了 Flush 流式响应静默卡死 3 分钟，零错误日志 |
| P5 | 上游错误 MUST 转成 `*domain.UpstreamError` | 上层只按错误码决策，不解析字符串 |
| P6 | `internal/config` MUST NOT 创建目录 / 打开日志 / 发请求 | 它是纯加载 + 校验，副作用留给 `cmd` |
| P7 | `internal/deepseek` MUST NOT import `mcp` | 上游契约与协议解耦，换协议不动 HTTP 层 |
| P8 | 缓存 key 用 `domain.NormalizeUrl`，**发给上游的仍是原始 URL** | 归一化会改语义；归一只是防 `?utm_source=x` 击穿缓存 |
| P9 | 日志 MUST NOT 出现 API Key / Bearer token | 先 `Redacted()` 再打印 |
| P10 | 新增 `exported` 符号 MUST 同时加 `Test<Name>` | 测试规范要求 |

### 5.3 接口定义位置的两条例外

| 接口 | 定义处 | 为什么例外 |
|---|---|---|
| `deepseek.Client` | `internal/deepseek` | 与 HTTP 实现同生命周期；拆出去会引入第三层包 |
| `resilience.Clock` | `internal/resilience` | 被 `logging` 用；放谁都成环，靠参数注入打破 |

### 5.4 注释规范（中文，MUST）

```go
// ✅ 写「为什么」—— 代码看不出来的东西
// 上游把 403/404/500/域名不存在/内网 IP/二进制内容全部塌缩成同一个 404，
// 所以这里无法按状态码分类，只能把 7 种成因写进 hint 交给调用方判断。

// ❌ 写「做什么」—— 读者看代码就知道
// 遍历 results，对每个结果取 url 字段
```

**只有四类内容值得写注释**：为什么这样做 / 踩过的坑 / 实测数据 / 反直觉的取舍。
**禁止**：复述代码、`TODO` 不带日期与人（格式 `// TODO(2026-11-01, 名字): ...`）、注释掉的死代码、英文注释、表情符号。

---

## 6. 测试标准

### 6.1 三层

| 层 | 位置 | 依赖 | 触发 |
|---|---|---|---|
| **L1 单元** | `*_test.go` | 零外部依赖 | `make test` |
| **L2 契约黄金** | `*_test.go` + 包内 `testdata/` | 固定输入输出文件 | `make test` |
| **L3a 协议/端到端** | `*_test.go`（进程内 server + `httptest` 假上游 + stdio 管道），**MUST NOT 打 build tag** | 无 Key、零计费 | `make test` |
| **L3b 真实上游** | `*_integration_test.go`，首行 MUST 打 `//go:build integration` | 真实上游 + 真 Key（**计费**） | `make test-integration` |

> 🔴 L3a 若被误打 `integration` tag ⇒ 日常 `make test` **静默不跑**，断言形同失效；不打 tag 却连真实上游 ⇒ 违反 T1，每次 `go test` 都花钱。

### 6.2 铁律

| # | 规则 |
|:-:|---|
| T1 | `make test` MUST **不产生任何计费调用** |
| T2 | L1 MUST NOT 访问网络、MUST NOT 写项目目录外的文件 |
| T3 | 测试 MUST 能单独 `go test -run TestXxx` 通过 |
| T4 | 时间相关 MUST 用假 `Clock`，**MUST NOT 用 `time.Sleep`** |
| T5 | 每个 `exported` 函数 MUST 有 ≥1 个 `Test<函数名>` |
| T6 | ≥3 个用例 MUST 用表驱动 + `t.Run` |
| T7 | 无共享状态的 L1 MUST 加 `t.Parallel()` |
| T8 | 断言用标准库 + `github.com/google/go-cmp/cmp`（仅测试依赖）；**MUST NOT 引入 testify** |
| T9 | 黄金文件 MUST 由真实抓取得到，**MUST NOT 手写假 JSON** |
| T10 | 断言 MUST 检查返回值内容，不能只有 `if err != nil { t.Fatal }` |

### 6.3 覆盖率门槛

| 包 | 门槛 |
|---|:-:|
| `domain` `config` `resilience`（根与五个子包） | **≥ 80%** |
| `deepseek` `tools` | ≥ 70% |
| `transport` | ≥ 60% |
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
存量违规已清零（DEBT-010）；MUST NOT 重新引入基线文件或往豁免里塞批量存量。

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
| X11 | 在仓库外维护第二份文档副本 | 两份副本必然漂移；`docs/` 是唯一文档根 |
| X12 | 提交二进制到 git | `Bin/` 在 `.gitignore` 里；二进制只进 Release 附件 |
| X13 | 用软链接 / 硬链接 | 项目铁律 |
| X14 | 在项目目录外写文件 | 项目铁律（只写 `Data/` `Logs/`） |

---

## 9. 文档契约与单一真源

**同一个事实只写在一个地方**，其它文档只引用。运行时行为以代码为准。

| 事实 | 文档位置（代码实现为准） |
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
| `build` | 构建 / Makefile / CI |
| `chore` | 杂项（依赖、豁免登记） |

**scope** 用包名：`domain` `config` `logging` `deepseek` `resilience` `tools` `transport` `cmd` `repo`。

```
feat(domain): 时间/URL/切片/反转义纯函数
fix(transport): statusWriter 补 Flush 与 Unwrap，修复流式响应卡死
docs(contract): 配置契约新增 [limits.budget] 四项
```

**MUST NOT** 写 `update` / `fix bug` / `改进` 这种没有信息量的。

### 10.2 PR 检查清单

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

**按需读，不要通读。** 每个任务的「必读」列了它真正需要的 1~3 个章节。

| 你要做什么 | 只读这些 |
|---|---|
| 建目录、写 Makefile | `docs/00-项目宪章.md` §三、§五；`docs/01-架构总览.md` §一 |
| 写任何 Go 代码 | 本文件 §5 |
| 写任何测试 | 本文件 §6 |
| 定义数据结构/字段 | `docs/Contract/12-数据契约.md` 对应小节（逐字抄） |
| 定义函数签名 | `docs/Contract/11-接口契约.md`（逐字抄） |
| 定义错误码 | `docs/Contract/14-错误契约.md` §二 §三 |
| 写配置项 | `docs/Contract/13-配置契约.md` §二 → §三 |
| 写传输层 | `docs/Contract/15-协议契约.md` §四 §六 §七之二 §七之三 §十 |
| 想知道「为什么不用 XX」 | `docs/Decisions/ADR.md` |
| 想知道「这个功能什么时候做」 | `docs/Decisions/16-技术债登记表.md` |

### 11.2 开新任务时先答三个问题

```
Q1 本任务要创建/修改哪几个文件？
Q2 哪一条约束最容易被写错？
Q3 验收命令有几条？
```

答错说明读错文档了。此时纠正成本是「重读一节」，而写完再纠正是「重写一个包」。

### 11.3 执行纪律

```
答三问 → 实现 → 跑验收 → 贴 DoD → commit → 切下一个原子任务（先重读它的要求）
```

1. **每完成一个原子任务就立刻 commit**，MUST NOT 攒到文档末尾 —— 窗口中途挂掉时必须能精确回到最后一个已验收的状态。
2. **切换原子任务前 MUST 重新读它的要求**（前置 / 上下文 / 要创建的文件 / 实现要求 / 禁止事项 / 参考骨架 / 测试代码 / 验收命令），MUST NOT 凭上一个任务的记忆写。
3. **MUST NOT 在一个窗口里做第二份 Task 文档**：跨文档意味着换了整套接口与契约。

### 11.4 收尾动作（每次 MUST 做）

```bash
make check                 # 🔴 7 项全绿
git status --short         # 确认没多出文件
```

然后逐条回报：① `make check` 的真实输出；② DoD 逐条 `[x]` 或 `[ ]`（`[ ]` 必须写原因）；③ 建议的 commit message。

---

## 12. 卡住了怎么办

按顺序试，**不要跳过**：

| 步 | 动作 |
|---|---|
| 1 | **读报错原文**，不要猜 |
| 2 | 查本文件 §8「禁止清单」与 `docs/Handover/90-Qoder执行手册.md` §六「8 个常见失败模式」 |
| 3 | 查 `docs/Contract/14-错误契约.md`（错误码在说什么） |
| 4 | 查 `docs/Decisions/ADR.md`（是不是「本来就这么设计的」） |
| 5 | **停下来**，把报错原文 + 已尝试的动作列出来问 |

**MUST NOT** 自己发明一套方案绕过 —— 绕过的那套会在下一个原子任务里撞墙。

### 12.1 六个最容易踩的坑

| 现象 | 原因 | 处理 |
|---|---|---|
| 客户端连上了但什么都不响应 | 中间件包装 `ResponseWriter` 没实现 `Flush`+`Unwrap` | P4，逐字抄 `docs/Contract/11` §6.1 |
| 日期差一天 | 直接用 UTC 取日期 | 先 `In(cst8)` 再取 `YYYY-MM-DD` |
| 中文末尾出现 `�` | 按 byte 切片 | `domain.SliceByRune` |
| 代码块里的 `\n` 变真换行 | 反转义时删了所有反斜杠 | 只反转义实测全集 **32** 个字符（ADR-013）；且 MUST 单趟替换，多趟会把嵌套转义 `\\\.` 过度还原 |
| 改配置不生效 | 优先级顺序反了 / `toml` tag 拼错 | `Config.Source(key)` 打印来源 |
| 反代 / 直连局域网后全部 403 | 两层各查各的：`hostGuard` 查 **Host 头**（`3093`）；SDK 的 DNS 重绑定防护查「**本连接本地地址是回环** + Host 非回环」 | 反代或 `host="0.0.0.0"`：`allow_non_local_host=true`（是否鉴权由 `auth_enabled` 单独决定，ADR-018）；反代到回环监听且转发原始 Host：再加 `disable_localhost_protection=true` |

---

## 13. 术语表

| 术语 | 含义 |
|---|---|
| **原子任务** | 一次会话能完成的最小工作单元，对应 `docs/Tasks/` 里的 `A-NN` |
| **契约** | `docs/Contract/` 下的冻结文档。改它 MUST 走 ADR |
| **单一真源** | 同一事实只在一处定义，其它地方只引用（见 §9） |
| **漂移** | 真源改了但引用没改。`make drift` 检测 |
| **规模红线** | F-S1~F-S6。`make size` 检测 |
| **降级** | 缓存挂了按未命中继续，不让工具变砖 |
| **计费调用** | 打到 `/v1/tools/*` 的请求。¥5/1000 次，只有 200 且非空才计费 |
| **快照** | 一次具体抓取的正文（`cache_id`）。TTL 过期后 MUST 报 `3080`，MUST NOT 用新内容替代 |
| **无状态 Streamable HTTP** | MCP 2026-07-28 的传输形态。`Stateless` 恒为 `true`，不提供开关 |
