# Task-04 · 日志与轮转

> **执行方式**：2 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20
> **前置**：Task-03 已完成（`internal/config` 可 `Load` 并 `Validate`）

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | `log/slog` + 自研日轮转 writer。文件名 `DeepSeekSearch_20261020_130512_4821.log`（末段 `_4821` 是 **PID**，防多进程同秒启动撞名，见 A-01 §3 要求 2），保留 7 个自然日，单文件 50MB、单日总量 500MB 双上限 |
| 依赖 | Task-02（domain）、Task-03（config）、Task-06（`resilience.Clock`，若尚未落地则本任务自带一个最小 `Clock` 接口） |
| 产出 | 项目里第一个「有 IO」的包；`also_stderr=true` 时日志同时进 stderr（stdio 模式下这是唯一安全出口） |
| 必读 | `Contract/13-配置契约.md` 的 `[log]` 段与 §3.11；`Engineering/20-编码规范.md` §六；`Contract/18-仓库文件契约.md` §二（文件头要求） |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | RotatingWriter（日切 + 双上限 + 保留期） | 🔴 跨日切分与「保留含今天」的边界 |
| A-02 | Logger 构建与事件封装 | 🔴 stdio 下 MUST NOT 写 stdout |

> **本文件的节结构是 9 节模板的基准**：`0 前置 / 1 上下文 / 2 要创建的文件 / 3 实现要求 / 4 禁止事项 / 5 参考骨架 / 6 测试代码 / 7 验收命令与期望输出 / 8 完成定义（DoD）`。其它 Task MUST 与本文件一致。

---

## 原子任务 A-01 · RotatingWriter

### 0 前置

- Task-03 完成，`config.Load` 能返回含 `[log]` 段的 `Config`
- `resilience.Clock` 接口已存在；若 Task-06 尚未做，本任务在 `internal/logging` 内定义一个**最小等价接口**，并在 Task-06 落地后改为复用（见 §3 第 10 条）

### 1 上下文（自包含）

`lumberjack` 能按单文件大小轮转，但**做不到「按自然日总量封顶」**。我们需要两个上限同时生效：

| 上限 | 参数 | 行为 |
|---|---|---|
| 单文件 | `max_size_mb`（50MB） | 超了就换下一个文件（同一天内新的时间戳） |
| 单日总量 | `daily_max_mb`（500MB） | 超了就**停止写文件**；stderr 打一条 `warn.log_quota`（跨日重置），此后 stderr 分支转为限频采样；丢弃行数计入 `droppedLines`（细则见 §3 要求 5） |
| 保留期 | `max_age_days`（7） | 保留 7 个**自然日，含今天** |
| 绝对兜底 | `max_backups`（50） | 超过就删最老的（但**永不删今天**） |

为什么自研而不引第三方：功能面更窄（只做日志），代码约 140 行，可控。代价是必须有测试覆盖跨日切分 —— 这是本项目唯一会「悄悄不写日志」的地方。

### 2 要创建的文件

| 文件 | 内容 |
|---|---|
| `internal/logging/writer.go` | `RotatingWriter` + `NewRotatingWriter` + `Write`/`Close` |
| `internal/logging/rotate.go` | 跨日切分、文件命名、保留期清理、gzip |
| `internal/logging/writer_test.go` | 测试（见 §6） |
| `internal/logging/fake_clock_test.go` | 假 Clock（可 `Advance`） |

### 3 实现要求（逐条可验证）

| # | 要求 |
|:-:|---|
| 1 | `NewRotatingWriter(cfg Config) (*RotatingWriter, error)` 实现 `io.Writer` + `io.Closer`。`Config` 是 `internal/logging` 的窄配置（由 Task-12 从 `config.Config` 映射），其中 `Clock` MUST 非 nil —— 本包不内置 real clock，从根上避免直接 `time.Now()` |
| 2 | 🔴 文件名：`<dir>/<prefix>_YYYYMMDD_HHMMSS_<PID>.log`，`prefix` 默认 `DeepSeekSearch`，时间值用 `log.timezone`，PID 用 `os.Getpid()`（进程身份不是时间，G11 豁免，MUST 在构造时取一次缓存）。🔴 **同进程同秒创建多个 writer 时固定名必然撞名**，故 `O_EXCL` 创建失败时依次追加 `_1`、`_2` 序号（`<prefix>_YYYYMMDD_HHMMSS_<PID>_<n>.log`），HTTP 多进程同秒同理；`logNameRe` MUST 兼容这个可选序号，固定段仍是 `日期_时间_PID`。**为什么必须带 PID**：stdio 是本项目主推形态，一个客户端管理器同时拉起 2~3 个进程非常常见；秒级时间戳下同秒启动的进程**必然同名**，两个进程各自 `O_APPEND` 写同一个文件 —— 单行 <4KB 时 POSIX 追加本身是原子的，但轮转与配额统计（`curBytes`/`dayBytes`）是**各自进程内存里的**，双写会让两者都失真，且 `compress=true` 时一方可能 gzip 到对方正在写的文件。**文件名时间戳格式是固定的，MUST NOT 读 `log.timestamp_layout`**（裁决见下方 §3.1） |
| 3 | 跨自然日（按 `log.timezone`，默认 `Asia/Shanghai`）→ 关闭旧文件、开新文件、`dayBytes` 归零、**`quotaHit` 归零**（见要求 5） |
| 4 | 单文件超 `max_size_mb` → 同一天内滚到新的时间戳文件（`curBytes` 归零，`dayBytes` 累计） |
| 5 | 单日累计超 `daily_max_mb` → **停写文件**，往 stderr 打一条 `warn.log_quota`。🔴 三条修正：<br>① `quotaHit` 去重 MUST **随跨日重置** —— 否则第 2 天再超配额时静默丢日志、一条警告都没有（原设计「每进程只打一次」是 bug）；<br>② 配额 MUST 同样约束 `also_stderr` 分支：达标后 stderr 输出改为**限频采样**（每 100 行放行 1 行，另加跨日后的首条）—— `log.also_stderr` 默认 true，若 stderr 分支不受 500MB 限制，磁盘保住了但 stderr 管道的对端（客户端的日志收集器）会被灌爆；<br>③ 停写期间每丢弃一行 MUST 递增 `droppedLines` 计数（`Write` 对 slog 恒返回 `(len(p), nil)`，上层完全无感知，丢了多少必须可观测），并在 `log.rotate` 与 app.stop 事件里以 `dropped_lines` 字段上报 |
| 6 | 保留期清理：启动时一次 + 每天 00:05 一次，删除创建日期早于 `今天 - max_age_days + 1` 的文件。🔴 prune 前 MUST 先做**空闲自轮转**：若本进程当前文件的日期不是今天（说明进程长期没写日志），先轮转到今天的新文件并写一行 heartbeat（事件 `log.rotate`，字段 `reason="daily-idle"`）。**为什么**：轮转只在 `Write` 时触发，一个 8 天没写日志的常驻进程，其「当前文件」日期是 8 天前，会被**别的进程**的 prune 删掉 —— Linux 下 fd 悬空、日志继续写进不可见 inode（静默丢失），Windows 下删除失败或写入失败。heartbeat 让不变量「活进程的当前文件日期 = 今天」恒成立，配合要求 7「永不删当天文件」自然闭合。**否决的备选**：prune 前对候选文件做独占打开探测 —— POSIX 没有「被他人持有 fd 则打开失败」的原子语义，Windows 共享模式行为不同，`lsof`/`fuser` 是外部依赖（违反纯 Go、零外部工具原则），不可靠故否决 |
| 7 | `max_backups` 作为绝对上限兜底（超过就删最老），但**永不删当天文件** |
| 8 | `compress=true` → 轮转后的旧文件 gzip 成 `.log.gz`，**MUST NOT** 压缩当前正在写的文件；🔴 也 MUST NOT 压缩「日期 = 今天」的任何文件（同一天里另一个更晚启动的进程可能正在写它 —— 文件名带 PID 后同秒撞名已消除，但同日多文件仍合法） |
| 9 | 并发安全：`sync.Mutex` 包住全部写与切分 |
| 10 | 时间 MUST 来自注入的 `Clock`，**MUST NOT** 直接 `time.Now()`（见 `Engineering/20-编码规范.md` G11）。🔴 后台调度 goroutine 的「睡到明天 00:05」MUST 用注入的 `Clock.Sleep`/`Clock.Until` 等价能力实现，以便被 `FakeClock` 驱动 —— 否则跨日调度分支在测试里永远不可达 |
| 11 | 每个文件 MUST 有文件头 5 字段，`作者: 小逸`（见 `AGENTS.md` §5.5） |
| 12 | 目录不存在时 `os.MkdirAll`（只创建 `Logs/`，MUST NOT 创建别的目录） |
| 13 | 🔴 **调度 goroutine 生命周期**：`NewRotatingWriter` 至多拉起 **1 个**后台 goroutine（每天 00:05 做 prune + 空闲自轮转）。`Close()` 的语义 MUST = 「停止调度 goroutine（close done channel 或 ctx cancel）+ **等待其退出** + 刷盘关闭当前文件」。原设计 `Close()` 只关文件 ⇒ 测试里每个 `NewRotatingWriter` 泄漏一个 goroutine，`goleak`/`-race` 会挂；生产里优雅停机也没有可停的句柄。归属：goroutine 由 `RotatingWriter` 拥有、由 `Close()` 回收，写进 §8 DoD |
| 14 | `droppedLines` 用 `int64` 计数（要求 5③），`Stats()` 或直接字段暴露给 A-02 的事件封装层；`log.rotate` 与 app.stop 事件 MUST 带 `dropped_lines` |

### 3.1 🔴 裁决：日志文件名格式**不可配置**（E16 关联）

`13-配置契约` §二 里 `timestamp_layout` 的职责是「**文件名**里的时间戳格式」，注释还写着『改成 `"20060102"` 就只到天（同一天的日志会追加到同一个文件）』。这与本任务的两个硬约束**不可调和**：

1. `logNameRe` 与 prune/gzip 都依赖从文件名**解析出日期**；任意 Go layout（含下划线、纯日期、12 小时制）会让解析失效或歧义；
2. 「只精确到天」的格式下，同一天启动的多个进程**必然撞名**（PID 也救不了「同一进程同一天滚文件」与 `max_size_mb` 的切分语义 —— 追加到同一文件等于单文件上限失效）。

**裁决：文件名固定为 `<prefix>_YYYYMMDD_HHMMSS_<PID>.log`，MUST NOT 读取 `log.timestamp_layout`；`internal/logging/` 里该键名 MUST 零命中。** 日志**行内**时间格式是另一个键 `log.ts_format` 的职责（见 A-02 §3 要求 7），二者不得混用。

> ✅ 由此 `timestamp_layout` 成为孤儿配置（同 `respect_retry_after` 的先例，23 号 §6.3），**已在 `13-配置契约` 中整键移除**：§二 模板改为「文件名格式固定 + PID」的说明块，§3.11 表格该行改为删除记录。按 §1.1 第 10 条，用户配置里再写这个键会**拒绝启动**。

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 用 `lumberjack` 或任何第三方轮转库 |
| 2 | MUST NOT 在写入路径上做同步 fsync（性能） |
| 3 | MUST NOT 让清理逻辑删除「今天」的文件，即使它超过 `max_backups` |
| 4 | MUST NOT 在 `Write` 返回错误时影响调用方业务逻辑（日志写失败只打 stderr） |
| 5 | MUST NOT 用文件名排序推断日期 —— MUST 从文件名解析出的日期判断 |
| 6 | MUST NOT 在项目目录以外创建文件（只写 `Logs/`） |
| 7 | MUST NOT 用软链接/硬链接做轮转 |
| 8 | MUST NOT 让文件名读 `log.timestamp_layout`（格式固定，见 §3.1 裁决）；`internal/logging/` 里该键名 MUST 零命中 |
| 9 | MUST NOT 在调度 goroutine 里直接 `time.Sleep` —— MUST 用注入的 `Clock`（否则测试无法用 `FakeClock` 驱动跨日分支） |
| 10 | 配额达标后 MUST NOT 向 stderr 全量透传日志行（MUST 限频采样，见 §3 要求 5②） |

### 5 参考骨架

```go
// 文件名: writer.go
// 用途: 按自然日切分、带单文件与单日双上限的日志写入器。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package logging

import (
	"os"
	"sync"
)

// RotatingWriter 是按自然日切分、带双上限的日志写入器。
// 双上限缺一不可：单文件上限防「单个文件太大打不开」，
// 单日上限防「某个疯狂循环一夜写满磁盘」。
type RotatingWriter struct {
	mu       sync.Mutex
	cfg      Config
	clk      Clock
	file     *os.File
	pid      int    // 构造时 os.Getpid() 一次缓存；文件名的一部分（多进程同秒启动防撞名）
	curDate  string // "2026-10-20"，按配置时区
	curBytes int64
	dayBytes int64
	quotaHit bool  // 🔴 跨日 MUST 重置，否则第 2 天超配额时静默丢日志无警告
	droppedLines int64 // 配额丢弃计数；Write 对 slog 恒返回 nil，丢失量只能靠它可观测
	done      chan struct{} // Close() 关闭它以停止后台调度 goroutine
	schedDone chan struct{} // 调度 goroutine 退出时关闭；Close() 等待它（goleak 依赖此语义）
	closed    bool
}

// Write 实现 io.Writer。
// 它 MUST NEVER 向调用方返回「日志写失败」这种错误 —— 日志失败不该让业务失败。
// 内部错误一律打到 stderr，返回值恒为 len(p), nil。
// 配额达标后被丢弃的行 MUST 递增 droppedLines，并在 log.rotate 与 app.stop 事件上报。
func (w *RotatingWriter) Write(p []byte) (int, error)

// Close 刷盘并关闭当前文件，并 🔴 停止后台调度 goroutine：
//   close(done) → 等待 <-schedDone → 关文件。可重复调用（第二次返回 nil）。
// MUST NOT 只关文件不管 goroutine —— 测试里每个 NewRotatingWriter 都会拉起一个，
// 不回收则 goleak/-race 挂；生产里优雅停机也没有可停的句柄。
func (w *RotatingWriter) Close() error
```

```go
// 文件名: rotate.go
// 用途: 日志文件的跨日切分、命名、保留期清理与 gzip。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package logging

// logNameRe 从文件名里解析出日期、时间与 PID，用于保留期判断。
// 不用「按文件名排序推日期」——文件名前缀变了排序就失效，
// 必须真的把 YYYYMMDD 解析成日期。
// 🔴 文件名格式固定为 <prefix>_YYYYMMDD_HHMMSS_<PID>[_<n>].log[.gz]（见 §3 要求 2 与 §3.1 裁决），
//   可选 _<n> 只用于同秒同 PID 的撞名消歧；MUST NOT 由 log.timestamp_layout 配置。
var logNameRe = regexp.MustCompile(`^(.*)_(\d{8})_(\d{6})_(\d+)(?:_(\d+))?\.log(?:\.gz)?$`)

// pruneByAge 删除创建日期早于 today-(maxAge-1) 的文件。
// 「含今天」的意思是 maxAge=7 时保留今天与之前 6 天，共 7 个自然日。
// 调用前调度 goroutine MUST 先做「空闲自轮转」（§3 要求 6）：保证活进程的
// 当前文件日期 = 今天，从而永不会被别的进程的 prune 命中。
func pruneByAge(dir, prefix string, today time.Time, maxAge, maxBackups int) (removed int, err error)
```

### 6 测试代码

> 全部用假 Clock 推进日期，**MUST NOT** `time.Sleep`。测试文件同样需要文件头（`作者: 小逸`）。

| # | 用例 | 期望 |
|:-:|---|---|
| 1 | 同日写 3 次 | 同一个文件，`curBytes` 累加 |
| 2 | 跨日（假 Clock +24h） | 新文件名，旧文件已 `Close`（可 `Stat` 但不可写） |
| 3 | 单文件超 `max_size_mb` | 当天内产生第 2 个文件，`dayBytes` 继续累计 |
| 4 | 单日超 `daily_max_mb` | 后续写入被丢弃，stderr **只出现 1 次** `warn.log_quota`，`droppedLines` = 丢弃行数 |
| 5 | 保留 7 天含今天 | 造 8 天前的文件 → 被删；今天的文件 → 保留 |
| 6 | `max_backups=2` 造 5 个文件 | 只剩最新 2 个 + 当天文件 |
| 7 | `compress=true` | 旧文件有 `.gz` 后缀，当前文件**没有** |
| 8 | 并发 100 goroutine 写 | `-race` 无告警，总字节数精确等于写入之和 |
| 9 | `Close()` 连调两次 | 第二次返回 nil，不 panic |
| 10 | 文件名解析 | `DeepSeekSearch_20261020_130512_4821.log` → 日期 `2026-10-20`、PID `4821` |
| 11 | `Close()` 回收调度 goroutine | `Close()` 返回后 `schedDone` 已关闭（或用 `goleak.VerifyNone`）；连开 3 个 writer 全 Close 后零泄漏 |
| 12 | `quotaHit` 跨日重置 | 第 1 天超配额 → stderr 1 次 `warn.log_quota`；假 Clock 跨日、第 2 天再超配额 → **再出现 1 次**（合计 2 次） |
| 13 | 配额后 stderr 限频采样 | `also_stderr=true` 且配额达标后再写 1000 行 → stderr 收到的行数 ≤ 1000/100 + 常数（不是全量 1000 行） |
| 14 | 空闲自轮转（heartbeat） | 造一个「当前文件日期 = 3 天前」的 writer，假 Clock 触发 00:05 调度 → 当前文件换成今天的新文件，并写入 1 条 `log.rotate`（`reason="daily-idle"`）；旧文件不会被本进程 prune 删除（日期在保留期内） |

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc
$ go test ./internal/logging/ -race -v -run TestRotatingWriter
=== RUN   TestRotatingWriter
=== RUN   TestRotatingWriter/同日写3次同一文件
=== RUN   TestRotatingWriter/跨日切分
=== RUN   TestRotatingWriter/单文件上限
=== RUN   TestRotatingWriter/单日配额
=== RUN   TestRotatingWriter/保留7天含今天
=== RUN   TestRotatingWriter/并发100写
=== RUN   TestRotatingWriter/Close回收调度goroutine
=== RUN   TestRotatingWriter/quotaHit跨日重置
=== RUN   TestRotatingWriter/配额后stderr采样
=== RUN   TestRotatingWriter/空闲自轮转heartbeat
--- PASS: TestRotatingWriter (0.02s)
PASS
ok      github.com/LseKit/DeepSeekSearch/internal/logging

$ go test ./internal/logging/ -cover
coverage: 7x.x% of statements

$ gofumpt -l internal/logging/ && echo FMT_OK
FMT_OK
```

### 8 完成定义（DoD）

- [ ] §7 三条命令达到期望输出（**贴真实输出**）
- [ ] 14 个用例全绿，`-race` 干净
- [ ] `go test -cover` ≥ 75%
- [ ] 两个 `.go` 文件都有文件头且 `作者: 小逸`
- [ ] `grep -rn 'time.Now()' internal/logging/` 零命中
- [ ] `grep -rn 'timestamp_layout' internal/logging/` **零命中**（§3.1 裁决：文件名格式固定不可配）
- [ ] `grep -rn 'time.Sleep' internal/logging/` 零命中（调度循环只用注入的 `Clock`）
- [ ] 🔴 **调度 goroutine 归属**：由 `RotatingWriter` 拥有，`Close()` = 停调度 + 等退出 + 关文件；`goleak`（或等价的 `schedDone` 断言）用例通过 —— 这条写进 DoD 是因为它是本项目第一个「有后台 goroutine」的包，先例必须立对
- [ ] 文件名含 PID：同一秒 `New` 两个 writer（同目录同 prefix）得到**两个不同文件**
- [ ] `droppedLines` 在 `log.rotate` 与 app.stop 事件上报（联动 A-02）
- [ ] 提交：`feat(logging): 日轮转 writer 与双上限`

---

## 原子任务 A-02 · Logger 构建与事件封装

### 0 前置

- A-01 已完成并提交
- 已读 `Contract/13-配置契约.md` §3.11（三个必带字段）

### 1 上下文（自包含）

日志必须结构化（`slog`），事件名用小写点分，字段名全小写下划线。

🔴 **stdio 传输下 stdout 只能出协议帧** —— 任何 `fmt.Println` 都会让客户端 JSON-RPC 解析失败，表现为「客户端连上了但什么都不响应」，且**没有任何错误提示**。所以日志出口只有两个：文件（主）与 stderr（`also_stderr=true` 时）。

三个必带字段：

| 字段 | 为什么 |
|---|---|
| `call_id` | 把一次工具调用从 `call.begin` 到 `call.end` 串起来 |
| `config_sha256` | 诊断「我改了配置但好像没生效」—— 配置改没改，看这个值变没变 |
| `billed` | 对账：这次调用是否真的产生了上游计费 |

🔴 **「必带」的适用范围此前未定义，现由 §3.1.1「事件 × 必带字段表」逐事件规定**（`call_id` 只适用于调用上下文事件，app.* 等进程级事件 MUST NOT 带；`billed` 只在 call.end 出现）。缺失/多余由 §6 用例 8 的表驱动测试断言，不再靠调用点自觉。

### 2 要创建的文件

| 文件 | 内容 |
|---|---|
| `internal/logging/logger.go` | `New` + `ConfigSha256` + `toSlogLevel` + `Slow`（stderr 配额采样 `samplingWriter` 已在 A-01 的 `writer.go`，避免重复定义） |
| `internal/logging/events.go` | 🔴 28 个事件名常量（唯一真源） |
| ~~`internal/logging/redact.go`~~ | 🔴 **已移到 `internal/domain/redact.go`**（`Task-02` A-02 §3 第 12 条）：调用点在 `deepseek`/`tools`，而这两个包**不允许** import `logging`（10 号 §2.4/§2.6） |
| `internal/logging/logger_test.go` | 测试（见 §6） |

### 3 实现要求（逐条可验证）

| # | 要求 |
|:-:|---|
| 1 | `New(cfg Config) (*slog.Logger, io.Closer, error)`；`format=text` 用 `slog.NewTextHandler`，`json` 用 `slog.NewJSONHandler` |
| 2 | 输出目标：文件（`RotatingWriter`）+ 可选 stderr（`io.MultiWriter`） |
| 3 | 🔴 **MUST NOT 输出到 stdout**，代码里 `os.Stdout` 出现次数 = 0 |
| 4 | `ConfigSha256(cfg config.Config) string`：对 `Redacted()` 后的 JSON 求 sha256，取前 12 位。🔴 且 `New()` MUST 用 slog 的 `With("config_sha256", …)` **一次性全局注入**每条日志行（§3.1.1 裁决 1：全体事件必带，不依赖各调用点自觉）。指纹值由装配层（Task-12）调 `ConfigSha256(fullCfg)` 算好后，经 `logging.Config` 的 `ConfigSha` 字段传入 `New` |
| 5 | 事件常量表（`events.go`）定义下方 🔴 **28 个**事件名，MUST 用常量不用字面量 |
| 6 | `Slow` 判定：`ms > log.slow_ms` → 额外打 `warn.slow` |
| 7 | 🔴 **E16 更正**：日志**行内**时间格式用 `log.ts_format` 与 `log.timezone`（原文误写 `log.timestamp_layout` —— 该键在 `Contract/13-配置契约.md` §二 的职责是「**文件名**里的时间戳格式」，而文件名格式已由 A-01 §3.1 裁决为固定不可配，`internal/logging/` 里 `timestamp_layout` 键名 MUST 零命中）。slog 默认 Handler 不支持自定义时间版式，MUST 经 `HandlerOptions.ReplaceAttr` 把 `slog.TimeKey` 格式化为 `cfg.TsFormat`（先 `In(cfg.TZ)`）。🔴 时间值 MUST 取注入的 `cfg.Clock.Now()`，MUST NOT 用 slog 内部的真实时钟 —— 否则行内时间无法在测试里确定性断言（H8） |
| 8 | 日志级别从 `log.level` 映射到 `slog.Level`（`debug/info/warn/error`） |
| 9 | `New` 返回的 `io.Closer` MUST 是 `RotatingWriter`，调用方负责在退出前 `Close`（其语义含停调度 goroutine，见 A-01 要求 13） |
| 10 | 每个 `.go` 文件都有文件头 5 字段，`作者: 小逸` |
| 11 | `RedactURL` **不由本任务实现**，已移至 `internal/domain/redact.go`（`Task-02` A-02 §3 第 12 条）。本任务只负责在日志侧**使用**它：所有记录 URL 的字段 MUST 先过 `domain.RedactURL(raw, cfg.Log.RedactQueryKeys)` | 见第 12 条 |
| 12 | 所有记录 URL 的日志点（`ssrf.blocked` 的 `url`、`call.begin` 的目标 URL、`err` 的上下文字段等）MUST 先经 **`domain.RedactURL`** 再落日志。调用点在 Task-05/08/09 的日志语句里（各任务自查）；`log.redact_query_keys` 默认 7 项（13 号 §3.11） |

### 3.1 事件表（🔴 28 个，本节是**唯一真源**）

> `Contract/13-配置契约.md` §3.11 与 `Engineering/20-编码规范.md` §六 都指向本节，不另写一份。改事件名只改这里。

```
app.start   app.config   app.db   app.transport   app.ready   app.stop
call.begin  call.cache   call.limit.wait   call.upstream   call.upstream.done
call.retry  call.circuit  call.dedupe   call.section   call.end
err  warn.slow  warn.budget  warn.cache_degraded  warn.log_quota
ssrf.blocked  cache.prune  log.rotate
warn.cache_oversize  info.cache_recovered  warn.cache_commit_failed
warn.cache_key_collision
```

共 🔴 **28** 个（原 24 个；2026-10-21 实现缓存与工具时新增 warn.cache_oversize、info.cache_recovered、warn.cache_commit_failed 与 warn.cache_key_collision。原文与全库 5 处都写 23，但下面逐行数下来是 24 —— 枚举本身才是真源，数字是抄错的；`Scripts/CheckConsistency.py` 现已按枚举反查，写错会直接 FAIL）。新增事件 MUST 同时改本节与 `Contract/13-配置契约.md` §3.11 的引用说明（后者只引用，不重写）。

#### 3.1.1 🔴 事件 × 必带字段表（本节是**唯一真源**，测试断言以此为准）

此前「三个必带字段」的适用范围完全未定义：28 个事件里哪些必须带 `call_id`？app.* 带不带？`billed` 只在 call.end 出现吗？缺失时测试怎么断言？本表逐一裁决（表内事件名不加反引号是排版约定，避免与配置键混淆）：

| 事件 | `call_id` | `config_sha256` | `billed` | 其它必带 |
|---|:-:|:-:|:-:|---|
| app.start / app.config / app.db / app.transport / app.ready / app.stop | **MUST NOT** | ✅ | MUST NOT | app.stop 加 `dropped_lines`（A-01 要求 14） |
| call.begin | ✅ | ✅ | MUST NOT | `tool` |
| call.cache / call.limit.wait / call.upstream / call.upstream.done / call.retry / call.circuit / call.dedupe / call.section | ✅ | ✅ | MUST NOT | `tool`；call.upstream.done 加 `ms`；开启 `log.max_body_log_kb` 时另带 `req_body`/`resp_body`（按 KB 截断、URL 已脱敏） |
| call.end | ✅ | ✅ | **✅（唯一出现处）** | `tool`、`ms` |
| err（调用上下文） | ✅ | ✅ | MUST NOT | `code`、`code_name` |
| err（后台上下文：缓存/调度/清理） | **MUST NOT** | ✅ | MUST NOT | `code`、`code_name` |
| warn.slow / warn.budget | ✅ | ✅ | MUST NOT | `tool`、`ms` |
| warn.cache_degraded / warn.log_quota | MUST NOT | ✅ | MUST NOT | warn.log_quota 加 `dropped_lines` |
| warn.cache_oversize | MUST NOT | ✅ | MUST NOT | `key_hash`、`bytes`、`limit_kb`（A-01 要求 5） |
| info.cache_recovered | MUST NOT | ✅ | MUST NOT | —（缓存连续失败后首次成功复位时打，A-01 要求 12.1） |
| warn.cache_commit_failed | MUST NOT | ✅ | MUST NOT | `key_hash`、`err`（singleflight commit 失败但不再让调用失败，A-02 要求 9.1） |
| warn.cache_key_collision | MUST NOT | ✅ | MUST NOT | `key_hash`、`url_sha256`（FNV-1a 64 命中但 sha256 复核失败，Task-09 A-02 2.1） |
| ssrf.blocked | ✅ | ✅ | MUST NOT | 脱敏后的 `url`（§3 要求 12） |
| cache.prune / log.rotate | MUST NOT | ✅ | MUST NOT | log.rotate 加 `reason`、`dropped_lines` |

**裁决理由**：

1. `config_sha256` **全体事件必带** —— 它是进程级属性，由 `New()` 用 slog 的 `With` **一次性全局注入**（见 §3 要求 4），不依赖每个调用点自觉，因此可以做到 24/24 无遗漏。
2. `call_id` **只在调用上下文必带** —— 语义是「串起一次工具调用」；app.* / cache.prune / log.rotate / warn.cache_degraded / warn.log_quota 发生在调用之外，**没有**可串的调用，带一个假值（`""` 或 `"-"`）比不带更糟（grep `call_id` 排查时会混入噪音）。
3. `billed` **只在 call.end 必带** —— call.end 是一次调用唯一的对账汇聚点。一次调用可能含多次上游请求（重试），中间事件（call.upstream / call.retry）各自标 billed 会产生「一次调用多条 billed=true」的歧义账目；重试是否额外计费由 call.upstream.done 的 `status` 推断即可。
4. 缺失时的测试断言：§6 用例 8 **表驱动**逐事件构造日志行，按本表断言字段**在场/不在场**（`TestEventRequiredFields`）。

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 用 `log.Printf` / `fmt.Println`（`golangci-lint` 会拦） |
| 2 | MUST NOT 在任何日志里打 `ApiKey` / `HttpToken` 原文 |
| 3 | MUST NOT 打完整 query 正文。报文级日志由 `log.max_body_log_kb` 控制（默认 0 = 关，2026-10-21 恢复，见 DEBT-009）：开启后 `call.upstream.done` 行带 `req_body`/`resp_body`，按该值截断，`ds_fetch` 的 URL 先过 `domain.RedactURL` |
| 4 | MUST NOT 让 logger 成为包级全局变量 —— 它 MUST 通过 `Deps.Logger` 注入 |
| 5 | MUST NOT 在 `events.go` 里定义与本节不一致的事件名 |
| 6 | MUST NOT 把 URL 原文写进任何日志 —— query 值 MUST 先经 `domain.RedactURL` 按 `log.redact_query_keys` 脱敏（§3 要求 11/12） |
| 7 | MUST NOT 给 app.* / cache.prune / log.rotate / warn.cache_degraded / warn.log_quota 等后台事件带 `call_id`，也 MUST NOT 给 call.end 以外的事件带 `billed`（§3.1.1 表） |

### 5 参考骨架

```go
// 文件名: logger.go
// 用途: 构造结构化日志器，并计算配置指纹供排查使用。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package logging

// New 构造日志器。返回值里的 closer MUST 在进程退出前调用，
// 否则缓冲的最后几行会丢在内存里，且后台调度 goroutine 不会被回收（A-01 要求 13）。
//
// 绝不写 os.Stdout：stdio 传输下那是协议通道，混一个字节客户端就断开。
func New(cfg Config) (*slog.Logger, io.Closer, error) {
	w, err := NewRotatingWriter(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: %w", err)
	}
	var out io.Writer = w
	if cfg.AlsoStderr {
		// stdio 传输下这是唯一能被人看见的出口（stdout 被协议占用）。
		// 🔴 stderr 分支 MUST 与文件分支共享配额状态：配额达标后经
		//    samplingWriter 限频采样（每 100 行放行 1 行 + 跨日首条），
		//    否则 500MB 的闸只保护磁盘，stderr 管道对端（客户端的日志
		//    收集器）会被灌爆（A-01 要求 5②）。
		out = io.MultiWriter(w, &samplingWriter{dst: os.Stderr, quota: w.quotaExceeded, every: 100})
	}
	opts := &slog.HandlerOptions{
		Level: toSlogLevel(cfg.Level),
		// 🔴 E16：行内时间格式是 log.ts_format 的职责（log.timestamp_layout
		//    在 13 号里是「文件名」时间戳格式，且文件名已裁决为固定不可配）。
		//    slog 默认 Handler 不支持自定义时间版式，经 ReplaceAttr 输出。
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
				a.Value = slog.StringValue(a.Value.Time().In(cfg.TZ).Format(cfg.TsFormat))
			}
			return a
		},
	}
	var h slog.Handler
	if cfg.Format == "json" {
		h = slog.NewJSONHandler(out, opts)
	} else {
		h = slog.NewTextHandler(out, opts)
	}
	// config_sha256 全局注入一次（§3.1.1 裁决 1：全体事件必带）。
	// 指纹值由装配层（Task-12）用 ConfigSha256(fullCfg) 算好后传入 cfg。
	return slog.New(h).With("config_sha256", cfg.ConfigSha), w, nil
}
```

```go
// 文件名: events.go
// 用途: 定义全部日志事件名常量，是全项目事件名的唯一真源。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package logging

const (
	EvAppStart     = "app.start"
	EvCallBegin    = "call.begin"
	// ... 共 28 个
)
```

> 🔴 `RedactURL` 的实现骨架已随函数迁到 `Task-02` A-02 §5（`internal/domain/redact.go`），本节不再重复 —— 两处各放一份骨架必然漂移。

### 6 测试代码

| # | 用例 | 期望 |
|:-:|---|---|
| 1 | `TestNew`：`format=json` | 输出可被 `json.Unmarshal` 解析 |
| 2 | `TestNew_NoStdout`：重定向 `os.Stdout` 后调 logger | 捕获内容为空 |
| 3 | `TestConfigSha256`：同配置两次调用 | 结果相同 |
| 4 | `TestConfigSha256`：改任一字段 | 结果不同 |
| 5 | `TestConfigSha256`：只改 `ApiKey` | **结果相同**（因为走 `Redacted()`） |
| 6 | `TestEventConstants` | 全部匹配 `^[a-z][a-z0-9_.]*$`，数量 = **28** |
| 7 | `TestEventUniqueness` | **28** 个常量值互不相同 |
| 8 | `TestEventRequiredFields`：**表驱动**，按 §3.1.1 逐事件构造日志行 | 每个事件的 `call_id`/`config_sha256`/`billed` 在场/不在场与表一致；每条输出都含 `config_sha256`（全局注入生效）；call.end 之外的任何事件都不含 `billed` |
| 9 | `TestNew_LineTimeFormat`：设 `ts_format` 为可识别的自定义版式 | 行内 `time` 字段按 `ts_format` + `timezone` 输出（E16 回归：与文件名时间戳无关） |

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc
$ go test ./internal/logging/ -v -run 'TestNew|TestConfigSha256|TestEvent'
--- PASS: TestNew (0.00s)
--- PASS: TestNew_NoStdout (0.00s)
--- PASS: TestNew_LineTimeFormat (0.00s)
--- PASS: TestConfigSha256 (0.00s)
--- PASS: TestEventConstants (0.00s)
--- PASS: TestEventUniqueness (0.00s)
--- PASS: TestEventRequiredFields (0.00s)
PASS

$ grep -rn 'os.Stdout' internal/logging/*.go | grep -v '_test.go' ; echo "生产代码命中数=$(grep -rn 'os.Stdout' internal/logging/*.go | grep -v '_test.go' | wc -l)"
生产代码命中数=0

$ grep -rn 'timestamp_layout' internal/logging/ ; echo "命中数=$(grep -rn 'timestamp_layout' internal/logging/ | wc -l)"
命中数=0

$ grep -c 'Ev[A-Z]' internal/logging/events.go
28
```

> ⚠️ `os.Stdout` 的零命中只约束**生产代码**：`TestNew_NoStdout` 必须引用 `os.Stdout` 才能把它重定向到管道，测试文件里的出现是这条断言的实现手段，不是违规。

### 8 完成定义（DoD）

- [ ] §7 四条命令达到期望输出（**贴真实输出**）
- [ ] `os.Stdout` 在生产代码中零命中、`timestamp_layout` 在整个 `internal/logging/` 零命中（E16）
- [ ] 🔴 28 个事件常量全部定义且唯一
- [ ] §3.1.1「事件 × 必带字段表」有表驱动测试覆盖（用例 8），28 个事件逐一断言
- [ ] 本任务**不实现** `RedactURL`（已归 `Task-02` A-02）；但 MUST 在 review 时确认 Task-05/08/09 的 URL 日志点都调用了 `domain.RedactURL`
- [ ] 全部 `.go` 文件都有文件头且 `作者: 小逸`
- [ ] 提交：`feat(logging): 结构化 logger 与事件常量`
