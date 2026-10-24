# Task-07 · 缓存与跨进程 singleflight

> **执行方式**：2 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20
> **2026-10-21 包布局变更（DEBT-010）**：接口仍在 `internal/resilience` 根包，缓存与 singleflight 实现已迁入 `internal/resilience/cache`。下列路径与 `-run` 命令保留交付时口径，当前运行用 `go test ./internal/resilience/...`。

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | SQLite 持久化缓存 + 跨进程 singleflight。**缓存是「省钱」不是「功能」，任何缓存错误都不得让工具失败** |
| 依赖 | Task-02、Task-03、Task-06（Clock） |
| 产出 | `internal/resilience` 覆盖率 ≥ 80%；5 并发同 URL 只打 1 次上游 |
| 必读 | `ADR-006`（为什么 SQLite 不用内存）；`Contract/12-数据契约` §1.5.1（5 并发 = 5 次计费的实测） |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | SQLite Cache（schema / TTL / LRU / Prune） | 写爆磁盘；`:memory:` 测试隔离 |
| A-02 | Singleflight（进程内 + 跨进程租约） | 租约泄漏导致永久等待 |

---

## 原子任务 A-01 · SQLite Cache
### 0 前置

- Task-02、Task-03、Task-06（Clock） 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

**决定性理由**：`stdio` 模式下一个客户端一个进程。选内存缓存等于在最主流的部署形态下缓存完全失效（3 个客户端 = 3 份隔离缓存）。

另有实测支撑：同一 URL **并发**冷请求 5 次 → **上游不合并并发请求**，5 并发 = 5 次计费。这需要 `inflight` 表做跨进程单飞（A-02）。

> 🔴 **本段原有的证据已被推翻，MUST NOT 再引用**：原文写「5 次返回 5 种不同长度（168K/231K/322K/428K/429K）」，据此推出「上游返回内容不确定」。`23-上游能力实测报告` §4.6 复测：并发冷抓取 `go.dev/doc/effective_go`，5 个请求中 **3 个 200 且内容 105,162 runes、sha256 完全相同**，另 2 个是 **504 且精确出现在 30.126s / 30.131s**（上游网关 30s 硬超时）。间隔 30s 重抓仍字节相同。
>
> ⇒ 长度分歧来自**超时失败**，不是内容不确定。**单飞的必要性依然成立**，但正确的理由是两条：① 5 并发 = 5 次计费（真金白银）；② 并发冷抓取会挤占上游配额、更容易撞上 30s 网关超时（`16-技术债登记表` X6 记录了 fetch 对主流站点约 40% 的失败率，重试雪上加霜）。

### 2 要创建的文件

`internal/resilience/cache/cache.go`、`internal/resilience/cache/cache_sqlite.go`、`internal/resilience/cache/cache_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 驱动 `modernc.org/sqlite`（纯 Go，无 CGO） |
| 2 | 建表 SQL 与 `ADR-006` §表结构**逐字一致**（`cache` + `inflight` 两表 + 2 索引） |
| 3 | 打开后立即执行 4 条 PRAGMA：`journal_mode=WAL` `synchronous=NORMAL` `busy_timeout=5000` `temp_store=MEMORY` |
| 4 | `Get`：命中且未过期 → 返回值；过期 → 删除并返回未命中 |
| 4.1 | 🔴 **命中统计 MUST NOT 每次读都 UPDATE**（原设计「命中即写 `hits`/`last_hit_at`」是读路径上的写放大）：SQLite 的 WAL 仍是**单写者**，多进程共享同一个 `Data/Cache.db` 时，命中 UPDATE 会与租约的 INSERT/DELETE、100ms 轮询、淘汰扫描叠加，在 `busy_timeout=5000` 下会造成 5 秒级卡顿。改为：`hits` 在**内存里累加**、`last_hit_at` 降频更新（每 key 最多每 `prune_interval_s` 写一次，或在 `Prune` 里批量落盘）。`Stats()` 的 `hits` 因此是「本进程视角 + 上次落盘值」，MUST 在文档里写明它不是全局精确值 |
| 5 | `Put`：`len(val) > max_entry_kb*1024` → **不写**，返回 nil（不是错误），但 MUST 记一条 `warn.cache_oversize`（含 key 的 hash、实际字节数、上限） |
| 5.1 | 🔴 **这一条与 A-02 的租约时序强耦合，MUST 一起看**：owner 成功拿到内容但因超限**永不写值**时，所有跨进程等待者必然「租约消失且缓存无值」⇒ 要么空等到 `3075`、要么抢占租约**再打一次上游**。所以超限不能只是「静默不写」，MUST 让 owner 写一个**短 TTL 的 oversize 标记**（见 A-02 §3 第 9.2 条），等待者据此立即返回而不是傻等 |
| 5.2 | 🔴 `max_entry_kb` 的默认值已由 1024 提到 **2048**（`13-配置契约`），因为实测 RFC 9110 正文就有 439,285 字节、且中文页 byte ≈ rune×2.2~2.4（`23` 号 §4.3/§4.4）。判超限 MUST 按 **byte** 比（缓存存的是字节），但 `max_chars` 一类的预算 MUST 按 **rune** 比 —— 两者口径不同，MUST NOT 混用 |
| 6 | 淘汰 MUST NOT 在每次 `Put` 时做全表聚合（`SELECT COUNT(*), SUM(nbytes)` + 排序删除）。改为**借 `Prune` 周期摊销**：`Put` 只维护内存里的近似计数，超过阈值时置一个 `needEvict` 标志，由 prune 调度器统一执行 |
| 6.1 | 🔴 为什么：多进程并发 `Put` 时，每个进程都独立跑一遍全表淘汰 ⇒ **过度淘汰**（N 个进程各删一批，实际删掉 N 倍），且全表聚合在 500 条 ×2MB 的量级上不便宜。摊销到 prune 周期后，淘汰由「谁先跑到谁做」变成低频事件，配合 `busy_timeout` 才不会互相踩 |
| 7 | `Stats()` 用一次 `SELECT COUNT(*), SUM(nbytes)`；DB 故障时返回 `degraded=true` |
| 8 | `Prune()` 删除 `expires_at < now` 的行，返回删除条数 |
| 9 | 目录不存在时 `MkdirAll`（`Data/`） |
| 10 | `dbPath == ":memory:"` 时用内存库（测试专用） |
| 11 | 全部方法带 `ctx`，遵守 `ctx.Done()` |
| 12 | **任何 DB 错误 MUST NOT 变成工具失败**：调用方（Tools 层）拿到 error 后记 `warn.cache_degraded` 并按未命中继续 |
| 12.1 | 🔴 **`degraded` MUST 有复位路径**（原设计是 `atomic.Bool` 只置位、永不复位）：一次瞬时磁盘满就会让进程**永久降级**，之后每一次调用都真金白银计费，而运维只看到启动时那一条 warn。改为：**连续 N 次（默认 3）失败才置位**，且**任意一次成功操作后复位**并记 `info.cache_recovered` |
| 13 | 🔴 `Cache` MUST 有 `Close() error`：关闭 `*sql.DB` 并**停止 prune 调度 goroutine**（见第 14 条）。`cmd` 的关闭序列里它排在 `sflight.ReleaseAll` **之后**、`logger.Close` **之前**（见 `Task-12` §3 第 8 条） |
| 14 | 🔴 **prune 调度器 MUST 有明确的归属与生命周期**：`prune_interval_s`（默认 300）此前是个**没有任何实现要求**的孤儿配置键。裁决：调度 goroutine 由 `resilience` 在 `NewCache` 时启动，用注入的 `Clock.Sleep` 驱动（可被 `FakeClock` 加速），`Close()` 时通过 ctx cancel 停止**并等待其退出**（否则 `goleak`/`-race` 会挂）。⚠️ `11-接口契约` 曾写「每 10 分钟」，与配置默认 300s（5 分钟）冲突 —— **以配置项为准**（E15） |
| 15 | 🔴 **缓存 key MUST 带 schema 版本前缀**：`v1:<tool>:<hash>`。否则升级后字段语义变了（例如本轮把 `truncated_by` 的取值从 3 种减到 1 种），旧缓存条目会被当成新格式返回给模型，而它看不出来 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用 `CGO` 版 `mattn/go-sqlite3` |
| 2 | MUST NOT 缓存**反转义后**的正文 —— 缓存里永远存上游原文（反转义只在输出时做） |
| 3 | MUST NOT 存 API Key / 认证信息进 `body` |
| 4 | MUST NOT 在 `Get`/`Put` 里 `panic` |
| 5 | MUST NOT 忘记 `defer rows.Close()` 与 `rows.Err()` 检查（`rowerrcheck`/`sqlclosecheck` 会拦） |
| 6 | MUST NOT 用字符串拼接 SQL（必须参数化） |

### 5 参考骨架

```go
// sqliteCache 是 Cache 接口的 SQLite 实现。
// 选它而不是内存的唯一决定性原因是 stdio 模式一客户端一进程 —— 
// 内存缓存在最主流的部署形态下命中率为 0。
type sqliteCache struct {
    db       *sql.DB
    cfg      Config
    clk      Clock
    degraded atomic.Bool
}

// Get 命中返回 (值, true, nil)。过期视为未命中并顺手删除。
// 返回 error 时调用方 MUST 按「未命中」处理并记 warn.cache_degraded，
// 因为缓存挂了不该让工具变砖。
func (c *sqliteCache) Get(ctx context.Context, key string) ([]byte, bool, error)
```

### 6 测试代码（全部用 `:memory:`）

| 用例 | 期望 |
|---|---|
| Put 后 Get | 值相同，未过期 |
| TTL 到期（假 Clock） | 未命中，行被删 |
| `ttl <= 0` | Put 后立即未命中 |
| 超 `max_entry_kb` | Put 返回 nil 但不写入 |
| 超 `max_entries` | 淘汰最久未访问的 |
| `hits` / `last_hit_at` | Get 两次后 `hits == 2` |
| `Prune` | 返回删除条数 |
| `Stats` | `entries`/`bytes` 准确；关闭 DB 后 `degraded=true` |
| 并发 100 Get/Put | `-race` 干净 |
| 目录不存在 | 自动创建 `Data/`（用临时目录测） |

### 7 验收命令与期望输出

```bash
$ go test ./internal/resilience/ -race -v -run TestSqliteCache
--- PASS: TestSqliteCache (0.05s)
    --- PASS: TestSqliteCache/TTL_到期视为未命中
    --- PASS: TestSqliteCache/超单条上限不写入
    ...

# 🔴 文件库 MUST 落在 t.TempDir() 下，MUST NOT 写 /tmp/dstest 这类固定路径：
#    21-测试规范 T2 要求 L1 不写项目目录外的文件；固定路径还会让并发跑测试互相污染。
#    测试内断言三点：文件已创建、PRAGMA journal_mode = wal、关闭重开后数据仍在。
$ go test ./internal/resilience/ -race -v -run TestSqliteCache
（「落盘」子用例覆盖上述三点；t.TempDir() 由 go test 自动清理）
```

### 8 完成定义（DoD）

- [ ] 10 个用例全绿，`-race` 干净
- [ ] `:memory:` 之外无测试落盘到项目目录
- [ ] 提交：`feat(resilience): SQLite 缓存实现`

---

## 原子任务 A-02 · Singleflight（进程内 + 跨进程）
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

🔴 **这不是优化，是必需。** 实测：同一 URL 5 个并发冷请求 → **5 次计费**，且并发冷抓取更容易撞上上游 30s 网关超时（`23` 号 §4.6：5 个请求里 2 个 504 于 30.126s/30.131s）。

> 🔴 原文的「上游返回 5 种不同长度」已被推翻（见 A-01 §1 的说明）：3 个成功响应的 sha256 **完全相同**。单飞的理由是**省钱 + 降低撞超时的概率**，不是「内容不确定所以必须共享」。

两层实现：

| 层 | 作用域 | 实现 |
|---|---|---|
| 进程内 | 单进程内的 goroutine | `map[string]*call` + mutex（标准 `singleflight` 模式） |
| 跨进程 | 多个 stdio 子进程 / 多实例 | SQLite `inflight` 表做**租约**：先到者写一行 `(k, owner, expires_at)`，后到者轮询缓存，租约到期可抢占 |

租约的关键是**不能泄漏**：owner 进程崩溃会让租约一直存在，所以必须有 `expires_at`，且它必须 > owner 可能占用的最长时间。

🔴 **租约时长的公式（E12 裁决，原文「默认 60s」与公式自相矛盾）**：

```
leaseS = max(60, ceil(2 × timeout.<tool>.total_timeout_ms / 1000))
```

按默认配置 `timeout.fetch.total_timeout_ms = 110000` ⇒ **实为 220s，不是 60s**。「60」只是下限兜底。为什么是 2 倍：owner 最坏情况是「排队等到 `max_wait_ms` + 全部重试耗尽」，而等待者必须在 owner 还活着的时候**一直看到租约存在** —— 租约比 owner 的实际耗时短，就会发生「owner 还在跑，等待者却抢占了租约再打一次上游」。

### 2 要创建的文件

`internal/resilience/cache/singleflight.go`、`internal/resilience/cache/singleflight_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | `Do(ctx, key, waitMs, fn)` 返回 `(val, shared, err)` |
| 2 | 进程内：同 key 并发只执行 1 次 `fn`，其它等待并共享结果 |
| 3 | 跨进程：`INSERT OR IGNORE INTO inflight(k, owner, expires_at)` → 若 `affected == 1` 说明我是 owner，执行 `fn` |
| 3.1 | 🔴 **`Do` 的签名 MUST 带 `commit` 回调**：`Do(ctx, key, waitMs, fn, commit)`，其中 `commit func(context.Context, []byte) error` 由 Tools 层提供、内部就是 `cache.Put`。**owner 的顺序 MUST 是「`fn` 成功 → `commit(val)` 成功 → 才 `DELETE` 租约」**。理由见下方 §3.1 的时序证明 |
| 4 | 非 owner → 轮询 `cache.Get(key)`，间隔 100ms，直到 ① 拿到值 ② `waitMs` 超时 ③ 读到 oversize 标记（第 9.2 条）④ 租约消失 |
| 4.1 | 🔴 **分支 ④「租约消失且缓存无值」MUST 有明确定义的行为**（原设计列了这个条件却没说怎么办）：裁决为 **重新尝试一次 `INSERT OR IGNORE` 抢租约**；抢到就成为新 owner 执行 `fn`，抢不到就继续轮询。MUST NOT 直接返回 `3075`（那会让等待者在 owner 只是慢了一点时白白失败），也 MUST NOT 无条件自己打上游（那就没单飞了）。配合第 3.1 条，这个分支在正常情况下**几乎不可达** —— 只在 owner 进程被 kill 时触发 |
| 5 | 租约 `expires_at = now + leaseS`，`leaseS = max(60, ceil(2 × timeout.<tool>.total_timeout_ms / 1000))`。**默认配置下 fetch 实为 220s**（见 §1，E12） |
| 6 | 租约过期 → 允许抢占（`UPDATE inflight SET owner=?, expires_at=? WHERE k=? AND expires_at < now`）。MUST 检查 `RowsAffected == 1` 才算抢占成功，否则说明别人先抢到了 |
| 7 | 🔴 owner 执行 `fn` 返回 error → **先把错误码写进 `inflight.last_error_code` 再删租约**（见第 7.1 条），然后把 error 返回给进程内等待者 |
| 7.1 | 🔴 **跨进程的错误传播此前完全未定义**：第 7 条的「error 原样返回给等待者」只在**进程内**成立（靠 `map[string]*call` 共享）。跨进程等待者只能观察到「租约消失了」，无从知道 owner 是成功还是失败 ⇒ 会以为「值马上就要来了」而一直轮询到 `waitMs` 超时。裁决：`inflight` 表加一列 `last_error_code INTEGER`，owner 失败时 `UPDATE inflight SET last_error_code=? WHERE k=? AND owner=?` **再** DELETE；等待者读到非 0 ⇒ 立即返回 **`3075`（retryable）**，`details.owner_error_code` 带 owner 的真实错误码。🔴 **`hint` MUST 用 `CodeRegistry` 里 3075 的统一文案，MUST NOT 为同一个码写第二套 hint** —— 一个码一套文案是单一真源的基本约束；「owner 刚失败」与「owner 只是慢」的区分由 `owner_error_code`（非 0 = 刚失败）承担，不需要、也不允许改 hint |
| 8 | `waitMs` 超时 → 返回 `*domain.LocalError`（`3075`）带 `key` 与 `waited_ms`（进 `details`，见 `14-错误契约` §3.0） |
| 9 | 🔴 **`Do` MUST 自己调用 `commit` 写缓存**，MUST NOT 让 Tools 层在 `Do` 返回之后再写（原第 9 条「由调用方 Tools 层写，`Do` 只负责租约」正是竞态的来源）。「职责分离」这个理由不成立 —— 租约的生命周期**就是**「从开始取，到值可被他人读到」，把写值切出去等于把租约语义截断了一半 |
| 9.1 | `commit` 返回 error（例如 DB 故障）时：记 `warn.cache_commit_failed`、**仍然删租约**、把 `fn` 的结果正常返回给调用方。理由：owner 自己已经拿到内容了，不该因为「没能存进缓存」而让用户这次调用失败；等待者则走第 4.1 条重新抢租约 |
| 9.2 | 🔴 **值超过 `max_entry_kb` 时（`cache.Put` 会静默不写）owner MUST 写一个短 TTL 的 oversize 标记**：`cache.Put(key, oversizeSentinel, ttl=60s)`，sentinel 是个可识别的小对象（`oversize:true` + 实际字节数）。等待者读到它就**立即返回**而不是傻等到 `waitMs` 超时。没有这一条，「超限静默不写」会让全部跨进程等待者必然空等或重复计费（见 A-01 §3 第 5.1 条） |
| 10 | 进程退出前 MUST 清理自己的全部租约（`ReleaseAll(owner)`）。🔴 它在 `cmd` 关闭序列里的位置是 `cancel → Serve 返回 → **ReleaseAll** → cache.Close → logger.Close` —— MUST 在 `cache.Close()` **之前**，因为租约就存在那个 SQLite 库里（见 `Task-12` §3 第 8 条） |
| 11 | `cache == nil` 时退化为纯进程内单飞（`Do` 仍工作，`commit` 可为 nil） |
| 12 | 🔴 **`owner` 标识的生成规则 MUST 写死**：`owner = fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), startupUnixNano)`。`ReleaseAll(owner)` 的正确性**完全依赖 owner 的唯一性** —— 只用 PID 的话，容器里 PID 常常恒为 1，两个容器共享同一个 `Data/Cache.db`（NFS/挂载卷）时会互删对方的租约 |
| 13 | 🔴 **`fn` panic 时 MUST 清除进程内 `map[string]*call` 的条目**：若只在正常返回路径清，panic 会让该 key **永久卡死**（后续所有同 key 调用都在等一个永远不会被 fulfill 的 call）。MUST 用 `defer` + `recover`，清除后**再删租约**，然后把 panic 转成 `3099 LOC_INTERNAL` 返回（见 `ADR-016`） |
| 14 | 🔴 **owner 的 ctx 被客户端取消时的语义 MUST 定死**：裁决为「**等待者接管执行**」而不是「跟着一起取消」。理由：stdio 模式下 owner 的 ctx 来自某一个客户端连接，它断开不代表其它进程不需要这个值；若共享取消，一个客户端取消会让所有等待者一起失败、租约也已释放 ⇒ 下一个调用者又要重新打上游。实现：`fn` 收到的 ctx MUST 用 `context.WithoutCancel` 去掉 owner 的取消信号，只保留 `PerAttempt`/`total_timeout` 的截止时间 |
| 15 | 🔴 **等待者共享的 `val []byte` MUST 约定为只读**：`Do` 返回给 N 个等待者的是**同一个底层数组**，任何一方原地改字节（例如就地做反转义）都会污染其它人。约定写进 `Do` 的 doc comment；Tools 层的反转义天然安全（`domain.Unescape` 返回新串） |

#### 3.1 🔴 为什么「先删租约、后写缓存」必然导致双重计费

原设计的时序（`Do` 只管租约、Tools 层在 `Do` 返回后才 `cache.Put`）：

```
进程 A（owner）                       进程 B（等待者，100ms 轮询）
──────────────────────────────        ────────────────────────────
fn() 成功，拿到 439KB 正文
Do: DELETE FROM inflight WHERE k=?
Do: return val  ─────────────┐
                             │        轮询：cache.Get(k) → 未命中
Tools: 反转义 / 分节 / 包裹   │        轮询：租约还在吗？→ **不在了**
（这几步要几毫秒到几十毫秒）  │        ⇒ 落入「租约消失且缓存无值」分支
Tools: cache.Put(k, val) ◄───┘        ⇒ 原设计**未定义行为**
```

在那个窗口里，进程 B 只有两种可能的实现，**两种都错**：

| B 的行为 | 后果 |
|---|---|
| 继续轮询直到 `waitMs` 超时 | 返回 `3075`，用户这次调用失败 —— 而 A 其实已经成功拿到了内容 |
| 抢占租约自己执行 `fn` | **再打一次上游 = 双重计费** —— 恰好是这套设计要消灭的场景 |

窗口看起来只有几毫秒，但它包含了 Tools 层的**反转义 + 分节 + `WrapUntrusted` 包裹**。实测 RFC 9110 正文 437,738 runes，32 字符集的单趟正则替换 + 按标题分节在这个量级上**不是几毫秒**。而 stdio 模式下多进程并发抓同一个热门 URL 是常态。

**加重因素**：A-01 §3 第 5 条规定超 `max_entry_kb` 的值**静默不写缓存**。这种情况下 owner 永远走不到 `cache.Put`，窗口不是「几毫秒」而是**无限长** ⇒ 所有跨进程等待者必然空等或重复计费。

⇒ 所以第 3.1 条把 `commit` 纳入 `Do`：**Put 成功之后才 DELETE 租约**。这样「租约消失」与「值可读」之间的窗口被关闭，第 4.1 条那个分支退化为「只在 owner 进程被 kill 时可达」的防御性兜底。

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 让等待者**无条件**自己去打上游（那就没单飞了）。第 4.1 条的「重新抢租约」是**有条件的**：抢到才执行，抢不到继续等 |
| 2 | MUST NOT 让租约没有 `expires_at` |
| 3 | MUST NOT 用 `time.Sleep` 轮询（用 Clock 或 `select` + `time.After`，并在测试里可加速） |
| 4 | MUST NOT 吞掉 owner 的 error（进程内等待者拿到同一个 error；跨进程等待者按第 7.1 条拿 `3075` + `owner_error_code`） |
| 5 | 🔴 **MUST NOT 让 Tools 层在 `Do` 返回之后才写缓存** —— 原第 5 条「`Do` 不写缓存、职责分离」**已作废**，理由见 §3.1。写缓存 MUST 通过 `commit` 回调发生在租约释放**之前** |
| 6 | MUST NOT 用 `os.Getpid()` 单独当 owner 标识（容器里恒为 1，见第 12 条） |
| 7 | MUST NOT 在 `fn` panic 后把 `map[string]*call` 的条目留着（该 key 会永久卡死，见第 13 条） |
| 8 | MUST NOT 原地修改 `Do` 返回的 `val []byte`（多个等待者共享同一底层数组，见第 15 条） |

### 5 参考骨架

```go
// Do 保证「同一 key 在同一时刻只有一次上游请求」，跨 goroutine 与跨进程同时生效。
// 返回 shared=true 表示本次没有真正执行 fn（结果来自他人）。
//
// 为什么这是必需而不是优化：实测同一 URL 5 个并发冷请求会被上游计费 5 次，
// 且并发冷抓取更容易撞上上游 30s 网关超时（23 号 §4.6：5 个里 2 个 504）。
// 🔴 原文的「返回 5 种不同截断长度」已被推翻 —— 3 个成功响应的 sha256 完全相同。
//
// 🔴 commit 是租约生命周期的一部分，MUST NOT 挪到 Do 之外：
//    owner 的顺序必须是 fn 成功 → commit(val) 成功 → 才 DELETE 租约。
//    否则「租约已消失但值还不可读」的窗口里，跨进程等待者要么空等到 3075、
//    要么抢占租约再打一次上游（= 双重计费）。详见 §3.1 的时序证明。
//
// 🔴 返回的 val 是**共享的底层数组**，所有等待者只读，MUST NOT 原地修改。
func (s *singleflight) Do(
    ctx context.Context,
    key string,
    waitMs int,
    fn func(context.Context) ([]byte, error),
    commit func(context.Context, []byte) error, // 可为 nil（cache == nil 时）
) (val []byte, shared bool, err error)

// ownerID 的唯一性是 ReleaseAll 正确性的前提。
// 🔴 MUST NOT 只用 os.Getpid()：容器里 PID 常常恒为 1，
//    两个容器共享同一个 Data/Cache.db 时会互删对方的租约。
func ownerID() string // hostname:pid:startupUnixNano
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 进程内 5 goroutine 同 key | `fn` 只执行 1 次（`atomic.Int64` 断言），5 个都拿到值 |
| 5 个不同 key | `fn` 执行 5 次 |
| 跨进程：两个 `singleflight` 共享同一个**文件**库 | 见下方说明 |
| owner 返回 error（进程内等待者） | 拿到**同一个** error 对象 |
| 🔴 owner 返回 error（**跨进程**等待者） | 拿到 `3075` + `details.owner_error_code` == owner 的错误码。**MUST NOT** 一直轮询到 `waitMs` 超时（第 7.1 条） |
| 🔴 **时序：`commit` 先于 DELETE 租约** | 用桩 cache 记录调用顺序，断言 `Put` 的时间戳/序号 **早于** 租约消失。这条是 §3.1 竞态修复的唯一自动化证据 |
| 🔴 **窗口内到达的等待者不会重复计费** | owner 在 `fn` 返回后、`commit` 完成前，让第二个进程开始轮询 ⇒ 断言 `fn` 总执行次数仍为 **1** |
| 🔴 值超 `max_entry_kb` | owner 写了 oversize 标记；等待者**立即**返回而不是等到 `waitMs` 超时（第 9.2 条） |
| 🔴 `fn` panic | ① `map[string]*call` 里该 key 的条目已清除（后续同 key 调用不会卡死）② 租约已删 ③ 返回 `3099`（第 13 条） |
| 🔴 owner 的 ctx 被取消，另有等待者 | 等待者**接管执行**并拿到值，MUST NOT 跟着一起失败（第 14 条） |
| 🔴 等待者修改返回的 `val` | 用 `-race` 跑「两个等待者各读一遍」+ 一条「原地写必须被 review 拦住」的注释断言（第 15 条） |
| `waitMs=100` 且无人写缓存、租约一直在 | 返回 `3075` |
| 🔴 「租约消失且缓存无值」 | 等待者重新抢租约并成为新 owner，`fn` 总次数 == 2（第一次是原 owner 被 kill 前跑的）。用桩模拟原 owner 消失（第 4.1 条） |
| 租约过期后可抢占 | 假 Clock 推进后第 2 个成为 owner，且 `RowsAffected == 1` 才算成功（第 6 条） |
| 🔴 两个进程同时抢一个过期租约 | **只有一个** `RowsAffected == 1`，另一个继续等（第 6 条） |
| `ReleaseAll` | `inflight` 表里**只删自己 owner 的行**，别人的租约保留（第 12 条） |
| `cache=nil` | 仍能单飞（进程内），`commit=nil` 不 panic |

> **跨进程测试限制**：`:memory:` 库不跨连接共享。测跨进程语义时 MUST 用临时**文件**库（`t.TempDir()/c.db`）开两个 `sql.DB` 连接，模拟两个进程。

### 7 验收命令与期望输出

```bash
$ go test ./internal/resilience/ -race -v -run TestSingleflight
--- PASS: TestSingleflight (0.02s)
    --- PASS: TestSingleflight/同key只执行一次
    --- PASS: TestSingleflight/跨进程共享结果
    --- PASS: TestSingleflight/租约过期可抢占
```

### 8 完成定义（DoD）

- [ ] 🔴 17 个用例全绿（原写 18，§6 表实为 17 行；含跨进程那几个），`-race` 干净
- [ ] 🔴 **时序用例通过**：`commit` 先于 DELETE 租约，且窗口内到达的等待者不会让 `fn` 执行第 2 次（§3.1 竞态修复的证据）
- [ ] 🔴 `grep -n 'last_error_code' internal/resilience/cache/singleflight.go` 有命中（跨进程错误传播已实现，第 7.1 条）
- [ ] 🔴 `inflight` 表结构与 `ADR-006` 一致，且**多出 `last_error_code` 一列** ⇒ 需同步改 `ADR-006` 的建表 SQL（**在报告里提出，不要自己改 ADR**）
- [ ] `internal/resilience` 整体覆盖率 ≥ **80%**
- [ ] 提交：`feat(resilience): 跨进程 singleflight`
