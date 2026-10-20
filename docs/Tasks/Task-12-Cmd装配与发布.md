# Task-12 · Cmd 装配、集成测试与发布

> **执行方式**：3 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 把 6 个包装配成可执行程序；跑真实上游的集成测试；产出四平台发布物并打 tag |
| 依赖 | Task-01 ~ Task-11 |
| 产出 | `v0.1.0` 可发布 |
| 必读 | `Contract/11-接口契约` §七（装配顺序 19 步）；`Contract/17-版本与发布契约` §七（发布检查单 **21** 项）；`Engineering/21-测试规范` §一 |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | main 装配与优雅关闭 | 装配顺序错会导致启动即崩 |
| A-02 | 集成测试 L3b | 会真实计费，必须有 `-tags integration` 隔离 |
| A-03 | 发布物、文档同步与打 tag | 版本三处不一致 |

---

## 原子任务 A-01 · main 装配与优雅关闭
### 0 前置

- Task-01 ~ Task-11 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

`Cmd` 是唯一知道「谁依赖谁」的地方。装配顺序在图上有严格的先后（`11-接口契约` §七）：先配置、再日志、再资源、最后网络。**MUST NOT 在装配阶段发起任何上游请求** —— 启动必须能在离线环境完成。

四个命令行参数：`-config` `-env` `-transport` `-version`（+ `-check`）。

### 2 要创建的文件

`cmd/deepseeksearch/main.go`（改写）、`cmd/deepseeksearch/run.go`、`cmd/deepseeksearch/main_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 严格按 19 步顺序装配（见 `11-接口契约` §七） |
| 2 | `-version` 在**任何 IO 之前**处理并 `os.Exit(0)` |
| 3 | `-check` 校验配置后打印脱敏摘要并 `os.Exit(0)`（不建目录、不起服务） |
| 4 | `-transport` 覆盖 `config.toml` 的 `[server] transport` |
| 4.1 | 🔴 **五个 flag 的默认值 MUST 逐字如下**（`Ops/30-部署运维手册` §二 与 §四 systemd 的 `ExecStart` 都按这个口径写命令，改动这里等于改动全手册）：<br>`-config` 默认 `Config/config.toml`；`-env` 默认 `Config/.env`；`-transport` 默认 `""`（空 = 不覆盖，用 `config.toml` 的值）；`-version` / `-check` 是 bool，默认 `false`。<br>**两个相对路径都按「进程当前工作目录」解析**，MUST NOT 按可执行文件所在目录解析 —— 后者需要 `os.Executable()` + 处理符号链接/`argv[0]` 被改写的情形，而 systemd 与手动 `cd` 两种启动方式的行为会不一致。<br>⇒ 因此 30 号的所有命令都先 `cd /mcp/DeepSeekSearch` 再执行，且**不带** `-env`（默认值就是 `Config/.env`，会被自动发现）。<br>`-env` 指向的文件**不存在时 MUST 报错退出**（3040），不能静默跳过：`.env` 是 API Key 的唯一来源，静默跳过只会把错误推迟到第一次上游调用变成 2401，排查成本高得多 |
| 5 | 🔴 **`cmd/deepseeksearch/main.go` MUST 有一行 `import _ "time/tzdata"`**（把 IANA 时区库编进二进制，约 +450KB）。<br>**为什么这条是 P0**：发布矩阵含 **windows/amd64**（见本任务「发布」节与 `Task-14` 的 release.yml），而 **Windows 没有系统 tzdata**；配合 `CGO_ENABLED=0` 的纯静态二进制，`time.LoadLocation("Asia/Shanghai")` 在 Windows 上**必然失败** ⇒ README 宣传的 Windows 产物**开箱即死**（启动就 `exit 1`）。精简容器镜像（`FROM scratch`、`distroless`）同理。<br>加了这一行后，第 5.1 条的失败分支降为**不可达的防御性兜底**，`13-配置契约` §1.1 第 12 项检查与 `豁免台账` 中 `domain/time.go` 的 G6 panic 例外均可关闭 |
| 5.1 | `time.LoadLocation("Asia/Shanghai")` 失败 → 打印「时区库加载失败（正常情况下不可能：已嵌入 `time/tzdata`，请检查构建是否误加了 `-tags timetzdata_exclude`）」+ `os.Exit(1)` |
| 6 | 配置校验失败 → 打印**全部** error 与 warning（`Validate()` 返回 `(warnings []string, err error)`）+ `os.Exit(1)` |
| 7 | 缓存打开失败且 `cache.degraded_ok=false` → `os.Exit(1)`；否则降级 + `warn.cache_degraded` |
| 8 | 🔴 **关闭序列 MUST 严格按此顺序**（顺序错了会丢租约或写坏 SQLite）：<br>`收到 SIGINT/SIGTERM` → `cancel()` → `handler.Serve` 返回 → **`sflight.ReleaseAll(ctx, "")`** → `cache.Close()` → `logger.Close()` → `os.Exit(0)`<br>**为什么 `ReleaseAll` 必须在 `cache.Close()` 之前**：跨进程 singleflight 的租约存在 SQLite 的 `inflight` 表里（见 `Task-07`）；`cache.Close()` 之后连接已关，租约就删不掉了，只能等 TTL 过期，期间其它进程会一直空等到 3075。<br>🔴 **owner 传空串**：owner 由 `NewSingleflight` 内部按 `hostname:pid:startupNano` 生成，不暴露给装配层；`ReleaseAll(ctx, "")` 按本实例释放（`Contract/11-接口契约.md` §四 已写明）。原文写 `sflight.ReleaseAll(owner)` 但装配层拿不到 owner —— 复制一份生成算法必然漂移 |
| 9 | **第二次收到 SIGINT/SIGTERM → 立即 `os.Exit(1)`**（不再走关闭序列）。关闭卡住时用户的本能是再按一次 Ctrl-C；没有这条，进程会看起来「按了没反应」 |
| 10 | 优雅关闭超时 15s，超时强制退出（`os.Exit(1)`） |
| 11 | 启动日志打 `app.start` 与 `app.ready`（含 `config_sha256`、监听地址、`version`） |
| 11.1 | 🔴 **`config_sha256` 的值 MUST 由装配层算好、经 `logging.Config` 的 `ConfigSha` 字段传进 `logging.New`**：`cfg.Logging.ConfigSha = logging.ConfigSha256(fullCfg)`（`Task-04` A-02 §3 要求 4）。<br>**为什么必须在装配层算、不能在 logging 包内部算**：`logging` 不 import `config`（`10-包依赖矩阵` §2.3），拿不到完整配置；而 `ConfigSha256` 是对 `Redacted()` 后的 JSON 求 sha256 取前 12 位，需要**完整**配置对象。<br>**为什么它是全局注入而不是各调用点自己带**：`New()` 用 slog 的 `With("config_sha256", …)` 一次性注入 ⇒ 每条日志行都有。靠各调用点自觉必然漏（28 个事件里漏一个，排查时就会怀疑「这条日志到底是哪次启动打的」） |
| 11.2 | 🔴 **`app.stop` MUST 带 `dropped_lines` 字段**，值取自 logging 层的 `droppedLines` 计数（`Task-04` A-01 §3 要求 5③/要求 14）。<br>**为什么这个字段不可省**：单日配额（`log.daily_max_mb`）达标后 writer 停写文件，而 `Write` 对 slog **恒返回 `(len(p), nil)`** ⇒ 上层完全无感知。丢了 3 万行还是 0 行，只有这个计数能说明。缺了它，「日志里怎么少了一大段」会变成一个无法回答的问题<br>装配层 MUST 在关闭序列里、`logger.Close()` **之前**取到这个值（Close 之后计数通道已关） |
| 12 | 🔴 **三处版本号的 `v` 前缀口径 MUST 一致**：`-version` 输出、`serverInfo.version`、`/healthz` 的 `version` **一律带 `v`**，与 Makefile 的 `VERSION := v0.1.0` 同源。⚠️ `15-协议契约` 的 `/healthz` 示例此前写的是 `"0.1.0"`（无 `v`），已修正 —— 否则 `Release.sh` 的三项「版本一致性」检查实测必然打架，而那是本项目自己认定的「发版第一号事故源」 |
| 13 | 🔴 **发布只推 tag**：`git push origin "${VERSION}"`。原写法 `git push origin main --follow-tags` 与 `main` 分支保护冲突（Scorecard 与社区规范通常要求禁直推 `main`），见 `16-技术债登记表` T7。若仓库确实未开分支保护，`git push origin main` 可作为**额外一步**手工执行 |
| 14 | 🔴 **发布命令里的版本变量 MUST 写 `${VERSION}`，MUST NOT 写 `$(VERSION)`**。后者在 bash 里是**命令替换**（「执行名为 VERSION 的命令」），展开为空串 ⇒ `grep -F ""` 匹配一切 ⇒ 版本检查永远假绿。见 `17-版本与发布契约` §七 的「变量语法铁律」 |
| 15 | 🔴 **`--version` 的单元测试 MUST NOT 硬断言 `v0.1.0`**。`go test ./cmd/...` 不注入 `-ldflags`，`version` 变量是编译期默认值（`v0.0.0-dev`）。正确写法：`main_test.go` 用 `package main`，测试里直接给 `version` 变量赋值，断言「输出包含**被赋的那个值**」 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用 `init()` |
| 2 | 🔴 MUST NOT 在装配阶段发上游请求（除 `self_test_on_start=true` 的 5s Ping） |
| 3 | MUST NOT 在 `-version` / `-check` 路径上创建目录 |
| 4 | MUST NOT 用 `log.Fatal`（用 `os.Exit` + 显式日志，`log.Fatal` 不 flush 我们的 writer） |
| 5 | MUST NOT 让 panic 逃出 `main`（defer recover 打日志 + `os.Exit(1)`） |

### 5 参考骨架

```go
// run 是 main 的全部逻辑，返回退出码。
// 拆出来是为了让测试能调它而不真的 os.Exit。
func run(args []string, stdout, stderr io.Writer) int {
    fs := flag.NewFlagSet("DeepSeekSearch", flag.ContinueOnError)
    fs.SetOutput(stderr)
    showVersion := fs.Bool("version", false, "打印版本并退出")
    checkOnly   := fs.Bool("check", false, "只校验配置并退出")
    configPath  := fs.String("config", "Config/config.toml", "配置文件路径")
    envPath     := fs.String("env", "Config/.env", "密钥文件路径")
    transport   := fs.String("transport", "", "覆盖传输方式：stdio | streamable-http")
    // ... 解析失败 return 2

    // -version 必须在任何 IO 之前返回：配置损坏、目录不存在也要能查版本。
    if *showVersion {
        fmt.Fprintf(stdout, "DeepSeekSearch %s (commit %s, built %s, %s)\n",
            version, commit, buildTime, runtime.Version())
        return 0
    }
    // ... 时区 → 配置 → check → 日志 → 资源 → 客户端 → server → tools → transport
}
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| `run(["--version"])` | 返回 0，stdout 单行含 `v0.1.0` |
| `run(["--version"])` 且配置目录不存在 | 仍返回 0 |
| `run(["-check","-config","坏配置"])` | 返回 1，stderr 含字段路径 |
| `run(["-transport","sse"])` | 返回 1，提示合法值 |
| 未知 flag | 返回 2 |
| `run` 装配成功（用临时配置） | 返回 0，且 `fakeClient` 零调用（未设 self_test） |

### 7 验收命令与期望输出

```bash
$ go build -o ../Bin/DeepSeekSearch ./cmd/deepseeksearch && echo BUILD_OK
BUILD_OK

$ ../Bin/DeepSeekSearch --version
DeepSeekSearch v0.1.0 (commit ..., built ..., go1.27.1)

$ ../Bin/DeepSeekSearch -check -config ../Config/config.toml
配置校验通过（transport=streamable-http, host=127.0.0.1, port=8765, cache=sqlite）

$ go test ./cmd/... -v
--- PASS: TestRun (0.01s)
```

### 8 完成定义（DoD）

- [ ] 6 个用例全绿
- [ ] `os.Exit` ONLY in `cmd/deepseeksearch/main.go` 的 `main()`（含其内联的第二次信号闭包）；`run()` MUST NOT 调用 `os.Exit`。正常退出 1 处 + 第二次信号立即退出 1 处 = 文件内共 2 处，均属 main() 的职责
- [ ] 提交：`feat(cmd): 装配与优雅关闭`

---

## 原子任务 A-02 · 集成测试 L3b
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

L3b 是**唯一会真实计费**的层。所以：

- 全部文件首行 `//go:build integration`
- `make test`（默认）**不会**跑到
- 必须有真实 `DEEPSEEK_API_KEY`
- 总调用数必须控制（≤ 10 次，约 ¥0.05）

L3b 验证的是「真实世界能不能用」：上游的字段名、错误码、日期格式、截断行为、协议协商 —— 这些 mock 测不出来。

> 🔴 **本原子任务原名「集成测试 L3」，已随 `Engineering/21-测试规范.md` §一 的分层裁决整体改名为 L3b**，用例编号 `L3-1`~`L3-6` 同步改为 `L3b-1`~`L3b-6`。
> 改名不是文字洁癖：原来的「L3 = 真实上游 + 真 Key」与「协议测试 P1~P26 属 L3（`httptest`）」自相矛盾 —— `httptest` 是**本地假上游**，不需要 Key、不计费。归属不清只有两种结局，都是事故：协议测试被打上 `integration` tag ⇒ 日常 `make test` **静默不跑**，P 断言形同失效且无人发现；或不打 tag 却连真实上游 ⇒ 每次 `go test` 都花真钱（违反铁律 T1）。
> ⇒ **L3a**（协议/端到端，`httptest` 假上游，**不打 tag**，随 `make test` 跑）归 `Task-11`；**L3b**（真实上游，**首行 MUST 打** `//go:build integration`）才是本任务。判据见 21 号铁律 **T7**。

### 2 要创建的文件

`internal/integration/e2e_integration_test.go`、`internal/integration/protocol_integration_test.go`、`internal/integration/README.md`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 两个文件都有 `//go:build integration` |
| 2 | 缺 `DEEPSEEK_API_KEY` 时 `t.Skip`（带明确原因） |
| 3 | L3b-1：真实 `ds_search` 一次，`count=3`，断言 ≥1 条结果且 `rerank_score` 降序 |
| 4 | L3b-2：真实 `ds_fetch` 一次（`https://example.com/`），断言正文非空且 `cache_id` 非空 |
| 5 | L3b-3：快照复用 —— 用 L3b-2 的 `cache_id` 取 `section`，断言不从上游重新抓 |
| 6 | L3b-4：stdio 端到端 —— 用 SDK client 连 `DeepSeekSearch -transport stdio`，跑一次 `tools/list` + `tools/call`，断言 stdout 无污染 |
| 7 | L3b-5：协议 P17/P18 —— stdio 与 HTTP 两条路各一次真实调用。🔴 **两条的判据不同，MUST 分开写**：<br>**P17（stdio）**：跑一次 `tools/list` + `tools/call`，断言 ① 调用成功 ② **stdout 只有 JSON-RPC 帧、无任何日志行**（日志一律走 stderr/文件；一行日志混进 stdout 就会让客户端的协议解析直接失败，而这个失败在日志里看不出来 —— 见 `Task-11` A-01 的 fd 加固）。<br>**P18（HTTP + SSE）**：🔴 **MUST 以 `server.http.json_response = false` 起一个实例再测**。默认值是 `true`，那时端点返回普通 JSON、**根本没有 SSE 流** ⇒ 「首包时间」这个断言永远拿不到流、也就永远不会失败 —— 这是本项目最典型的**假绿**（`15-协议契约` §七 与 `Task-11` A-02 §6.1 都记了这条）。<br>断言 MUST 是**首包 < 1s**，不是总耗时：`Flush` 缺失时总耗时可能仍然正常（响应最终会一次性发出），只有首包时间能暴露「中间件的 `statusWriter` 没提升 `Flush` 方法」那个坑（`15-协议契约` §6.3）。<br>MUST 另有一条**负向验证**：故意把中间件的 `statusWriter` 换成只嵌 `http.ResponseWriter` 接口的版本，确认 P18 真的会红 —— 不红的断言等于没写 |
| 8 | L3b-6：`/healthz` 的 `version` 与 `serverInfo.version` 与 `--version` 三者一致（P26） |
| 9 | 总上游调用次数 ≤ 10（用包级 `atomic.Int64` 计数，超了 `t.Fatal`） |
| 10 | 每个用例必须能单独 `go test -run TestXxx -tags integration` 通过 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 让 L3b 文件被默认 `go test ./...` 编译进去（漏 build tag = 每次跑测试都花钱） |
| 2 | MUST NOT 在 L3b 里做压力测试（那是手动任务，不是 CI） |
| 3 | MUST NOT 断言 `request_id` 的字面值 |
| 4 | MUST NOT 断言具体 URL（上游结果会变）；只断言结构与排序 |
| 5 | MUST NOT 把测出的真实响应写进 `testdata/`（除非按 C2 规则加采集注释） |

### 5 参考骨架

```go
//go:build integration

package integration

// L3b 集成测试会真实调用 DeepSeek API 并计费（¥5 / 1000 次）。
// 一律放在 build tag 后面，默认 `go test ./...` 不会编译到这里。
// 跑法：DEEPSEEK_API_KEY=sk-xxx make test-integration

var upstreamCalls atomic.Int64

func TestSearch_Real(t *testing.T) {
    key := os.Getenv("DEEPSEEK_API_KEY")
    if key == "" {
        t.Skip("未设置 DEEPSEEK_API_KEY，跳过真实调用测试")
    }
    // ... count=3，一次调用
}
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 无 Key 时跑 | 全部 `SKIP`，退出码 0 |
| 有 Key 时跑 | 6 个用例 PASS，总上游调用 ≤ 10 |
| 故意去掉 build tag | `make test` 报错（编译期就该拦住） |

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc
$ go test ./... -tags integration 2>&1 | head -3
# 没有该 tag 的文件不参与，编译正常

$ DEEPSEEK_API_KEY=$(grep DEEPSEEK_API_KEY ../Config/.env | cut -d= -f2) \
  go test ./internal/integration/ -tags integration -v -timeout 300s
=== RUN   TestSearch_Real
--- PASS: TestSearch_Real (3.42s)
=== RUN   TestFetch_Real
--- PASS: TestFetch_Real (1.18s)
=== RUN   TestFetch_SnapshotReuse
--- PASS: TestFetch_SnapshotReuse (0.02s)
=== RUN   TestStdioE2E
--- PASS: TestStdioE2E (2.10s)
=== RUN   TestProtocol_P17_P18
--- PASS: TestProtocol_P17_P18 (1.55s)
=== RUN   TestVersionConsistency_P26
--- PASS: TestVersionConsistency_P26 (0.31s)
PASS
ok      github.com/LseKit/DeepSeekSearch/internal/integration
```

### 8 完成定义（DoD）

- [ ] 6 个用例 PASS（贴真实输出）
- [ ] 上游调用计数 ≤ 10
- [ ] `go test ./...`（无 tag）不受影响
- [ ] 提交：`test(integration): 真实上游端到端`

---

## 原子任务 A-03 · 发布物、文档同步与打 tag
### 0 前置

- A-02 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

发版是机械流程，所以写成 `Scripts/Release.sh`，按 `Contract/17-版本与发布契约` §七 的 16 项逐条执行。任何一项不通过就 `exit 1`。

同时要把 `Doc/` 单向同步到 `GitHubSrc/docs/`（**不用软链接** —— 项目铁律）。

### 2 要创建的文件

`GitHubSrc/Scripts/Release.sh`、`GitHubSrc/Makefile`（追加 `release` / `dist` 目标）、`Doc/Handover/91-验收总表.md` 的勾选更新

### 3 实现要求

| # | 要求 |
|---|---:|
| 1 | `release` 目标交叉编译四平台：`linux/amd64` `linux/arm64` `darwin/arm64` `windows/amd64` |
| 2 | 产物命名 `DeepSeekSearch_v0.1.0_<os>_<arch>[.exe]` |
| 3 | 生成 `Bin/SHA256SUMS`（`sha256sum` 全部产物） |
| 4 | `Release.sh` 顺序执行 **21** 项检查（项数 MUST 由脚本自己数 17 号 §七 的表行数得出，MUST NOT 硬编码），任一项失败立即退出并打印项号 |
| 5 | 第 9/10/11 项必须**实测**版本三处一致 |
| 6 | 第 13 项 `SyncDocs.sh --check` 无 diff |
| 7 | 打**附注** tag：`git tag -a v0.1.0 -m "..."` |
| 8 | `git push origin main --follow-tags` |
| 9 | 打 tag 前 MUST 二次确认 `git status` 干净 |

### 4 禁止事项

| # | 禁止 |
|---|---:|
| 1 | MUST NOT 用轻量 tag（`git tag v0.1.0` 不带 `-a`） |
| 2 | MUST NOT 在 tag 已存在时强推（`--force`） |
| 3 | MUST NOT 跳过第 9/10/11 项（版本一致性是发版第一号事故源） |
| 4 | MUST NOT 用软链接做 `docs/` 同步 |
| 5 | MUST NOT 在 `Bin/` 里留旧版本文件（`make dist` 先清空） |

### 5 参考骨架

```bash
#!/usr/bin/env bash
# 文件名: Release.sh
# 用途: 按 17-版本与发布契约 §七 的 16 项检查顺序发版，任一项失败立即退出。
set -euo pipefail

step() { printf '\n[%02d/16] %s\n' "$1" "$2"; }
die()  { printf 'FAILED: %s\n' "$1" >&2; exit 1; }

step 1 "工作区干净"
[ -z "$(git status --porcelain)" ] || die "工作区有未提交改动"

step 2 "在 main 分支"
[ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || die "不在 main"

# ... 3~16
```

### 6 测试代码

手工验证（不进测试套件）：

| 场景 | 期望 |
|---|---|
| 工作区有改动时跑 | 第 1 项失败退出 |
| `VERSION` 已被 tag 占用 | 第 3 项失败退出 |
| 故意把 `--version` 注入值改错 | 第 9 项失败退出 |
| 正常跑完 | 打出 `v0.1.0` tag，`Bin/` 有 4 个产物 + SHA256SUMS |

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc && make release
$ ls -1 ../Bin/
DeepSeekSearch_v0.1.0_darwin_arm64
DeepSeekSearch_v0.1.0_linux_amd64
DeepSeekSearch_v0.1.0_linux_arm64
DeepSeekSearch_v0.1.0_windows_amd64.exe
SHA256SUMS

$ bash Scripts/Release.sh --dry-run
[01/16] 工作区干净 ................ OK
...
[16/16] 推送 ...................... (dry-run 跳过)

$ git tag -l
v0.1.0

$ git describe --tags
v0.1.0
```

### 8 完成定义（DoD）

- [ ] `Bin/` 有 4 个产物 + `SHA256SUMS`
- [ ] `git tag -l` 显示 `v0.1.0`
- [ ] `git status` 干净
- [ ] 提交：`release: v0.1.0`

---

## Task-12 完成后的整体验收

```bash
$ cd /mcp/DeepSeekSearch/GitHubSrc
$ make lint && make test-race && make cover && bash Scripts/CheckSize.sh . \
  && bash Scripts/CheckDocDrift.sh . && make release
$ git log --oneline | head -5
$ git tag -l
v0.1.0
```

到此 `v0.1.0` 可发布。下一阶段（DEBT-001~004）另开 ADR。
