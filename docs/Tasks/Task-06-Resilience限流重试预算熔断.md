# Task-06 · Resilience 层（限流 / 重试 / 预算 / 熔断）

> **执行方式**：3 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20
> **2026-10-21 包布局变更（DEBT-010）**：接口与共享 `Clock`/`Config` 仍在 `internal/resilience` 根包，实现已迁入 `internal/resilience/ratelimit`（限流）、`retry`、`budget`、`circuit`。下列路径与 `-run` 命令保留交付时口径，当前运行用 `go test ./internal/resilience/...`。

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 四个保护器 + 可注入时钟。**不感知业务**，只知道 key 字符串（`"search"` / `"fetch"`） |
| 依赖 | Task-02（domain）、Task-03（config） |
| 产出 | `internal/resilience` 覆盖率 ≥ 80%；全部测试用假 Clock，零 `time.Sleep` |
| 必读 | `Contract/11-接口契约` §四；`Contract/13-配置契约` → `[ratelimit]` `[timeout]` `[retry]` `[circuit]`；`Contract/14-错误契约` §六（可重试判定表） |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | Clock + Limiter | `max_wait_ms` 是唯一不能去掉的硬边界 |
| A-02 | Retry + Budget | 对 404 重试 = 白付钱 |
| A-03 | Circuit | 半开状态的探测次数 |

---

## 原子任务 A-01 · Clock 与 Limiter
### 0 前置

- Task-02（domain）、Task-03（config） 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

限流器的意义不是「提高性能」，而是**保护上游与自己的钱包**。实测：200 并发跑 8 秒约 26 QPS 全部 200，0 次 429；但这是当前的上限，不是承诺。所以 rps 必须可配，且等待必须有上限。

`max_wait_ms` 是唯一不能设为 0 的边界：设为 0 等于「拿不到令牌立即失败」，用户会看到大量 `3000` 错误。

### 2 要创建的文件

`internal/resilience/clock.go`、`internal/resilience/ratelimit/limiter.go`、`internal/resilience/ratelimit/fake_clock_test.go`、`internal/resilience/ratelimit/limiter_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | `Clock` 接口 + `SystemClock()` 实现（返回值类型，不是指针） |
| 2 | 测试用 `FakeClock` 支持 `Advance(d)`，且 `Sleep` 能配合 `Advance` 推进 |
| 3 | `Limiter.Wait(ctx, key)`：key 只能是 `"search"`/`"fetch"`，其它返回 error |
| 4 | 🔴 **令牌桶 MUST 自研（约 60 行），MUST NOT 用 `golang.org/x/time/rate`** —— 二者**互斥**，理由见 §3.1 |
| 5 | 每个 key 一个桶：`bucket{ rate float64; burst int; tokens float64; nextFree time.Time }`，全部时间取自注入的 `Clock` |
| 6 | `Wait(ctx, key)`：超 `max_wait_ms` 返回 `*domain.LocalError`（`3000`）带 `queue_depth` 与 `waited_ms`（进 `details`） |
| 7 | 🔴 **`max_inflight` MUST 是 per-key 配额，不是全局共享**（原设计是全局 `chan struct{}` 容量 8）。裁决与理由见 §3.2 |
| 8 | `Depth(key)` 返回当前排队数（`atomic.Int32` 维护） |
| 9 | 全部方法 goroutine-safe |
| 10 | 🔴 **双段等待的顺序、回滚与最坏延迟 MUST 定死**（原设计完全未定义）。裁决见 §3.3 |
| 11 | 🔴 `Limiter` MUST 提供 `Release(key)` 释放在飞名额；`Wait` 成功后调用方 MUST 配对调用。`NewLimiter` 签名 MUST 含注入的 `clk Clock`（否则令牌桶无法用假 Clock 测试），见 `11-接口契约` §四 |

#### 3.1 🔴 为什么 MUST NOT 用 `golang.org/x/time/rate`

原文写「令牌桶用 `golang.org/x/time/rate`（唯一第三方依赖）」，同时 §4 禁止事项第 5 条又要求「MUST NOT 直接 `time.Now()`（用注入的 Clock）」。**这两条互斥**：

| 事实 | 后果 |
|---|---|
| `rate.Limiter` 内部用 `time.Now()` 取的**单调时钟**，没有任何注入点 | 无法用 `FakeClock` 驱动 |
| A-01 §6 的测试要求「假 Clock 推进 1s 后 `Wait` 返回 nil」 | 用真实的 `x/time/rate` **不可能按描述发生** —— 它只看真实挂钟，测试要么真睡 1 秒（慢且 flaky），要么根本测不到 |
| `21-测试规范` 要求 resilience 覆盖率 ≥ **80%** | 限流是 resilience 的核心，测不动就达不到 |

⇒ **裁决：自研约 60 行令牌桶**。这不是 NIH 综合征 —— 我们要的语义（可注入时钟 + per-key + 可回滚的预约）恰好是 `x/time/rate` 不提供的那三点，而令牌桶本身是 20 行算术。

```go
// bucket 是可注入时钟的令牌桶。约 60 行的全部复杂度都在「预约 + 可取消」上。
type bucket struct {
    mu       sync.Mutex
    rate     float64   // 每秒补多少令牌
    burst    int       // 桶容量
    tokens   float64   // 当前令牌数（可为负，表示已预约出去）
    nextFree time.Time // 下一个令牌可用的时刻（全部取自 Clock）
    clock    Clock
}

// reserve 返回「需要等多久」与一个 cancel 函数。
// 🔴 必须返回 cancel：拿了令牌却发现 inflight 满了时，MUST 把令牌还回去，
//    否则那段等待时间被白白吃掉，等效于把 rps 悄悄调低（见 §3.3）。
func (b *bucket) reserve(now time.Time) (wait time.Duration, cancel func())
```

第三方依赖因此仍是 **4 个**（mcp go-sdk、BurntSushi/toml、joho/godotenv、modernc.org/sqlite），与 `10-包依赖矩阵` §2.9 一致 —— `golang.org/x/time/rate` 已从白名单移除。

#### 3.2 🔴 全局 `max_inflight` 会重新引入它自己要防的饿死

§4 禁止事项第 3 条明说「MUST NOT 让 search 与 fetch 共享桶 —— 否则搜索会把抓取饿死」。**但桶隔离了、inflight 没隔离**：原设计是全局 `chan struct{}` 容量 `max_inflight=8`。

后果：search 快（实测 0.3s 级）且 `rps=30`，瞬时就能占满 8 个名额；fetch 是 45s 级，只能排队到 `max_wait_ms`（30s）超时返回 `3000`。这正是「搜索把抓取饿死」，只是从令牌桶挪到了信号量。

⇒ **裁决：per-key inflight 配额**，且给 fetch **预留**：

```
inflight.search = max(1, floor(max_inflight × search_share))   # 默认 search_share = 0.5
inflight.fetch  = max(2, max_inflight - inflight.search)       # 🔴 fetch 下限 2，MUST NOT 为 0
```

`fetch` 下限为 2 的理由：单次 fetch 最长 45s，配额为 1 时一个慢站就能让第二个 fetch 请求排满 30s 后失败；配额为 0 则 fetch 完全不可用。`max_inflight` 默认 8 ⇒ search 4 / fetch 4。

🔴 **连带影响（已同步，实现时 MUST 一起看）**：

| 被牵连的地方 | 改了什么 |
|---|---|
| `13-配置契约` §二 模板 | 新增键 `[ratelimit] search_share = 0.5`（含推导公式与理由注释） |
| `13-配置契约` §1.1.2 / §3.3 / §五 / §六 | `max_inflight` 合法区间由 1~64 **收紧为 3~64**；新增 §1.1.3 第 8 条跨字段约束（配额之和不得超过总额度） |
| `11-接口契约` §四 | `NewLimiter(spec, maxInflight, **searchShare**, maxWait)` 多一个参数；`Wait` 注释写明两段等待的顺序、回滚与共享 deadline |
| `Task-03` A-02 §3 第 5/5.1/10.2 条 | Validate 的区间与跨字段校验同步 |

#### 3.3 🔴 双段等待的顺序、回滚与最坏延迟

原设计只说「令牌桶 + 全局 inflight，两者都要等 `max_wait_ms`」，未定义：先后顺序、拿不到第二段时第一段是否回滚、最坏总延迟、排队时间是否计入 `total_timeout_ms`。裁决：

| 问题 | 裁决 | 理由 |
|---|---|---|
| 先令牌还是先 inflight | **先令牌，后 inflight** | 令牌是「速率」闸门，先过它能避免大量 goroutine 堆在 inflight 队列里空转 |
| 拿到令牌但 inflight 满 | **`cancel()` 把令牌还回去**，再等 inflight；两段共用**同一个** `max_wait_ms` 截止时间 | 不还令牌 = 白白消耗配额，等效于把 rps 悄悄调低，且这个偏差无法从配置上看出来 |
| 最坏总延迟 | **≤ `max_wait_ms`**（不是 2×） | 两段共享一个 deadline：`deadline = now + max_wait_ms`，第二段用 `deadline - now` 作为自己的上限 |
| 排队是否计入 `total_timeout_ms` | **计入** | `total_timeout_ms` 是「用户视角这次调用最多花多久」，排队当然是其中一部分。Tools 层在入口就用 `context.WithTimeout` 建好总闸门，`Limiter.Wait` 收到的就是那个 ctx（见 `14-错误契约` 3020 与 F5 的归属裁决） |
| 两个 `3000` 怎么区分 | `details.stage` = `"token"` 或 `"inflight"` | 运维要能分辨「速率不够」还是「并发不够」，二者调的旋钮不同（`rps` vs `max_inflight`） |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用 `time.Sleep` 实现等待（必须尊重 ctx） |
| 2 | MUST NOT 让 `max_wait_ms = 0` 通过校验（Validate 已拦，这里再断言一次） |
| 3 | MUST NOT 让 search 与 fetch 共享桶（必须隔离，否则搜索会把抓取饿死） |
| 4 | MUST NOT 在 `Wait` 里打日志（调用方自己打 `call.limit.wait`） |
| 5 | MUST NOT 直接 `time.Now()`（用注入的 Clock） |
| 6 | 🔴 MUST NOT 引入 `golang.org/x/time/rate`（与第 5 条互斥，见 §3.1）。`10-包依赖矩阵` 的白名单里已经没有它 |
| 7 | 🔴 MUST NOT 让 search 与 fetch **共享 inflight 配额**（桶隔离了、inflight 也得隔离，见 §3.2） |
| 8 | MUST NOT 在「拿到令牌但 inflight 满」时不回滚令牌（见 §3.3） |

### 5 参考骨架

```go
// limiter 是 per-key 令牌桶 + per-key 在飞数配额。
// 两个限制必须同时存在：令牌桶控速率，在飞数控瞬时并发。
// 🔴 两者都 MUST 按 key 隔离（见 §3.1 与 §3.2）：
//    - 桶用自研 bucket，不用 x/time/rate（无法注入 Clock）
//    - 在飞配额不用全局 chan，否则 search 会把 fetch 饿死
type limiter struct {
    clk      Clock
    buckets  map[string]*bucket         // "search" / "fetch"
    sems     map[string]chan struct{}   // 容量 = 该 key 的 inflight 配额
    maxWait  time.Duration
    depth    map[string]*atomic.Int32   // 排队深度，供 3000 的 details 与 ds_health 使用
}

// Wait 阻塞直到取得令牌与在飞名额，或 ctx 结束，或等待超过 maxWait。
// 超时返回 3000 而不是无限等，因为调用方（MCP 客户端）有自己的超时，
// 我们等到它先超时只会让错误更难理解。
// 🔴 顺序与回滚按 §3.3：先令牌后 inflight；两段共用一个 deadline；
//    拿不到第二段时 MUST 调用 reserve 返回的 cancel 把令牌还回去；
//    3000 的 details.stage 标明卡在 "token" 还是 "inflight"。
func (l *limiter) Wait(ctx context.Context, key string) error
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| `key="other"` | error |
| rps=1, burst=1, 连发 2 次 | 第 2 次等待；假 Clock 推进 1s 后返回 nil |
| `max_wait_ms=100`，桶空 | 100ms 后返回 `3000`，带 `queue_depth` |
| ctx 取消 | 立即返回 ctx error |
| search 与 fetch 独立 | 打满 search 后 fetch 仍可立即通过 |
| `fetch` 配额=2（`max_inflight=3`、`search_share=0.1`），3 并发 | 前 2 个立即通过，第 3 个等待；释放 1 个名额后第 3 个通过 |
| 100 goroutine 并发 | `-race` 干净，成功数精确 |

### 7 验收命令与期望输出

```bash
$ go test ./internal/resilience/ -race -v -run 'TestLimiter|TestClock'
--- PASS: TestLimiter (0.01s)
    --- PASS: TestLimiter/max_inflight_封顶
    ...
```

### 8 完成定义（DoD）

- [ ] 7 个用例全绿，`-race` 干净
- [ ] 测试总耗时 < 2s（证明没真等）
- [ ] 提交：`feat(resilience): 可注入时钟与令牌桶限流`

---

## 原子任务 A-02 · Retry 与 Budget
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

重试有两个必须记住的实测事实：

1. **上游把 403/404/500 塌缩成同一个 404**。所以「只对 5xx 重试」这条常规策略在这里失效 —— 我们按**错误码**判定，不做状态码猜测。
2. **Fetch 慢站实测 30.1s 才返回 504**。所以 `per_attempt_ms` 必须 > 31000，且 `max_attempts × per_attempt + 退避` 必须 < 客户端超时（Hermes 默认 120s）。

预算（Budget）有三层，但默认只开第一层（字符数），另两层默认关闭 —— 因为用户可能不想被硬顶打断。

### 2 要创建的文件

`internal/resilience/retry/retry.go`、`internal/resilience/budget/budget.go`、`internal/resilience/retry/retry_test.go`、`internal/resilience/budget/budget_test.go`

### 3 实现要求

#### Retry

| # | 要求 |
|:-:|---|---|
| 1 | 🔴 `Should(err, attempt)` 返回 **`(bool, Reason)`** 而不是裸 `bool`：`Reason` ∈ `ok` / `attempts_exhausted` / `not_retryable` / `budget_exhausted` / `disabled`。**为什么**：裸 bool 无法区分「这个错误本来就不该重试」与「预算耗尽（`3010`）」，而后者 MUST 作为独立错误码返回给调用方（`14-错误契约` §二 有 `3010 LOC_RETRY_BUDGET`），前者只是正常终止。没有 Reason，`3010` 永远不会被返回 |
| 1.1 | 🔴 `retry.enabled = false` 时 `Should` MUST 直接返回 `(false, disabled)`，且 **`Delay` MUST NOT 被调用**（`13-配置契约` 的这个键此前无任何处理要求） |
| 2 | MUST 用 `errors.As(&*domain.UpstreamError)` / `*domain.LocalError` 取码，MUST NOT 匹配错误字符串 |
| 3 | `Delay(attempt)` 支持三种 jitter：`fixed_plus_random`（默认）= `fixed[i] + rand[0, spread[i]]`；`full` = **`rand(0, fixed[i] × 2^attempt)`**；`none` = `fixed[i]` |
| 3.1 | 🔴 **`full` 的公式以 `13-配置契约` 为准**（E6）：本文原写 `rand(0, fixed[i])`，配置契约的注释写的是 `rand(0, fixed_ms[i] × 2^attempt)`。两者差一个指数因子 —— 少了它，「full jitter」退化成「固定区间随机」，在第 3 次重试时无法拉开与多进程同频重打的距离，也就失去了 jitter 的唯一目的 |
| 3.2 | 🔴 **`attempt` 越界 MUST clamp**：`max_attempts` 可配到 **6**，而 `fixed_ms` 默认只有 **3 项**。原设计未定义 `i >= len(fixed)` 时怎么办（Go 会 panic）。裁决：`i = min(attempt-1, len(fixed)-1)`，即**超出部分复用最后一档**；`2^attempt` 因子 MUST 封顶在 `2^10`（防 `attempt` 被配成异常值时整数溢出） |
| 3.3 | `spread_ms` 长度与 `fixed_ms` 不一致时（配置校验只要求 `len(fixed_ms) ≥ max_attempts-1`）：`spread[i]` 越界取 **0**（退化为无抖动），并记一条 warn |
| 4 | 🔴 **随机源 MUST 是接口 `RandSource { Float64() float64 }`，MUST NOT 直接持有 `*rand.Rand`**（E-并发）。理由：`retrier` 是**全局共享**对象、多 goroutine 并发调 `Delay`，而 `math/rand.Rand` **不是并发安全的** ⇒ `go test -race` 必炸，而 `21-测试规范` 要求 CI 跑 `-race`。生产实现用带锁包装或直接用 `math/rand/v2` 的**包级函数**（v2 的包级 API 本身是并发安全的）；测试注入确定性实现 |
| 5 | 重试预算：`budget.window_s`（默认 60）滑动窗口内 `retries/(calls+retries) ≤ ratio`（默认 0.10），超了 `Should` 返回 `(false, budget_exhausted)` ⇒ 调用方返回 **`3010`** |
| 5.1 | 🔴 **MUST 有最小样本量，否则低 QPS 下预算形同虚设**：窗口内 `calls=1`、首次失败 ⇒ `1/1 = 100% > 10%` ⇒ 立刻拒绝重试。自用工具每分钟可能就几次调用，等于 `retry.enabled=true` 完全无效。裁决：**`calls + retries < 20` 时不启用预算判定**（直接放行），并在 `Delay`/`Should` 的 doc comment 里写明。测试 MUST 覆盖冷启动（见 §6） |
| 5.2 | 预算计数 MUST 是 per-key 的（`search` 与 `fetch` 各自一份）。🔴 `Retry` 接口没有 key 参数，故调用方 MUST **为每个 key 各建一个 `NewRetry` 实例**；实例内部的重试预算天然隔离 |

#### Budget

| # | 要求 |
|:-:|---|---|
| 1 | `CheckWindow(key)`：窗口内调用次数 ≥ `limits.budget.max_calls_per_window`（默认 **0 = 不限**）。软告警的 `ratelimit.warn.warn_at_calls` 是另一套，MUST NOT 混用 |
| 2 | `CheckDaily(key)`：日累计 ≥ `limits.budget.max_calls_per_day`（默认 **0 = 不限**），跨日按 `log.timezone` 切分 |
| 3 | `Record(key, n)`：累加窗口计数与日计数；跨日（按配置时区）自动清零 |
| 4 | `TrimChars(s)`：按**剩余字符预算**截断，返回 `(截断后, 是否截断)`；用 `domain.SliceByRune` |
| 5 | 单次响应字符预算取 `limits.max_total_chars` |
| 6 | `Reset()` 清零全部 |
| 7 | 全部 goroutine-safe |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 对 `2402`（余额不足）重试 |
| 2 | MUST NOT 对 `2404` 重试（实测 403 也塌缩成 404，重试只会重复付费） |
| 3 | MUST NOT 无限重试：`max_attempts` 上限 6，硬校验 |
| 4 | MUST NOT 让 `Delay` 返回负数，也 MUST NOT 让它在 `attempt > len(fixed_ms)` 时 **panic**（第 3.2 条的 clamp） |
| 5 | MUST NOT 用 `time.Now()` 判断跨日（用 Clock + 配置时区） |
| 6 | 🔴 MUST NOT 直接持有 `*rand.Rand`（非并发安全，`-race` 必炸，第 4 条） |
| 7 | MUST NOT 让 `Should` 只返回 `bool`（无法区分「不可重试」与「预算耗尽 `3010`」，第 1 条） |
| 8 | MUST NOT 在 `calls+retries < 20` 时启用预算判定（冷启动，第 5.1 条） |

### 5 参考骨架

```go
// Should 是重试的唯一判断入口。
// 它只信任 CodeRegistry 的 Retryable 字段 —— 不做任何状态码猜测，
// 因为实测上游把 403/404/500 都塌缩成 404，猜必然猜错。
type Reason int

const (
    ReasonOK Reason = iota
    ReasonDisabled         // retry.enabled = false
    ReasonAttemptsExhausted
    ReasonNotRetryable
    ReasonBudgetExhausted  // ⇒ 调用方 MUST 返回 3010 LOC_RETRY_BUDGET
)

// 🔴 返回 Reason 而不是裸 bool：没有它，3010 永远不会被返回 ——
// 「预算耗尽」与「本来就不该重试」在调用方看来会一模一样。
func (r *retrier) Should(err error, attempt int) (bool, Reason) {
    if !r.cfg.Enabled {
        return false, ReasonDisabled
    }
    if attempt >= r.cfg.MaxAttempts {
        return false, ReasonAttemptsExhausted
    }
    code := domain.CodeOf(err)
    info, ok := domain.CodeRegistry[code]
    if !ok || !info.Retryable {
        return false, ReasonNotRetryable
    }
    if !r.budgetAllow(code) {   // per-key，且 calls+retries < 20 时恒真（冷启动）
        return false, ReasonBudgetExhausted
    }
    return true, ReasonOK
}

// RandSource 是随机源接口。🔴 MUST NOT 直接用 *rand.Rand ——
// retrier 是全局共享对象、多 goroutine 并发调 Delay，而 math/rand.Rand
// 不是并发安全的，`go test -race` 必炸。
type RandSource interface{ Float64() float64 }

// Delay 返回第 attempt 次失败后的等待时长（含抖动）。
// 抖动的作用不是「更随机」，而是让多进程不要同频重打 ——
// 三个客户端同时退避 1s 然后同时重试，等于没退避。
func (r *retrier) Delay(attempt int) time.Duration
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 429 + attempt=1 + max=4 | `Should=true` |
| 404 + attempt=1 | `Should=false` |
| 402 + attempt=1 | `Should=false` |
| attempt=4 + max=4 | `Should=false` |
| `fixed_plus_random` 第 1 次 | 落在 `[1000, 2000)` ms |
| `fixed_plus_random` 第 3 次 | 落在 `[8000, 12000)` ms |
| 累计退避 | `[12000, 19000)` ms 区间 |
| 重试预算 ratio=0.1 | 冷启动（`calls+retries < 20`）放行；累计足够样本后返回 `budget_exhausted`（不对「第 11 次」这种边界过拟合） |
| `TrimChars` 超预算 | 返回截断后的串 + true |
| `TrimChars` 未超 | 原串 + false |
| 跨日清零 | 假 Clock +24h 后 `CheckDaily` 通过 |

### 7 验收命令与期望输出

```bash
$ go test ./internal/resilience/ -race -v -run 'TestRetry|TestBudget'
--- PASS: TestRetry (0.00s)
--- PASS: TestBudget (0.00s)
```

### 8 完成定义（DoD）

- [ ] 11 个用例全绿
- [ ] `grep -rn 'time.Sleep' internal/resilience/` 零命中
- [ ] 提交：`feat(resilience): 重试策略与三层预算`

---

## 原子任务 A-03 · Circuit
### 0 前置

- A-02 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

熔断的作用是「上游已经明确挂了，别再猛打」。「挂了」的判据是**连续**失败次数，不是失败率 —— 因为我们的调用量可能只有每分钟几次，失败率没有统计意义。

状态机：`closed` →（连续 5 次失败）→ `open`（15s）→ `half-open`（放行 2 个探测）→ 全成功回 `closed`，任一失败回 `open`。

### 2 要创建的文件

`internal/resilience/circuit/circuit.go`、`internal/resilience/circuit/circuit_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | per-key 独立状态（`"search"` / `"fetch"`） |
| 2 | `closed`：累计连续失败；成功清零 |
| 3 | 连续失败达 **`circuit.fail_threshold`**（默认 5）→ `open`，记 `opened_at`。🔴 配置键名是 `fail_threshold`，**不是** `failure_threshold`（E7）—— 本项目对未知键零容忍（`13-配置契约` §1.1 第 6 条），文档里先拼错，实现者照抄就会拒绝启动 |
| 4 | `open` 期间 `Allow` 返回 `*domain.LocalError`（`3030`）带 `retry_after_ms`（进 `details`，LLM 需要能机器读取它） |
| 5 | `open` 超过 `open_ms`（默认 15000）→ 转 `half-open` |
| 6 | `half-open` 放行 `half_open_probes`（默认 2）个请求；期间其它请求仍被拒。🔴 `half_open_probes` MUST ≥1，否则一次限流会造成**永久**拒绝服务 |
| 7 | `half-open` 全成功 → `closed`，计数清零；任一失败 → 立刻回 `open` |
| 8 | `State(key)` 返回 `"closed"|"open"|"half-open"` |
| 9 | 时间全部来自 Clock |
| 10 | 🔴 **`Record(key, err)` 的失败判定 MUST 按 `14-错误契约` §6.1 收紧**。这是本任务最危险的一条，理由见 §3.4 |

#### 3.4 🔴 `Record` 若按「`err != nil` 即记失败」，会造成本地拥塞伪装成上游故障

`11-接口契约` 里 `Breaker` 的 `Record(key, err)` 注释写的是「`err != nil` 即记失败」。照它实现会得到这条链：

```
本地限流拥塞 → 3000 排队超时 → 计入连续失败 → 达 5 次 → 熔断 Open
            → 之后所有调用直接 3030 → 上游明明是好的，却全部拒绝服务
            → 3030 标记 retryable → 客户端重试 → 更多排队 → 更拥塞
```

这是**自我实现的故障放大**：一次流量高峰就能让服务进入「拒绝一切」的状态，而且看起来像上游挂了，运维会去查上游。

`1xxx`（用户传错参数）同理 —— 一个把 `count` 传成 0 的客户端，能连续 5 次把熔断打开，惩罚所有其它调用方。

⇒ **裁决：只有下列码计入熔断失败**（与 `14-错误契约` §6.1 逐字一致）：

| 计入 | 理由 |
|---|---|
| `2408` `2429` `2500` `2503` `2504` | 上游 5xx / 限流 / 读体超时 —— 真故障 |
| `2998` | 网络层不可达（ADR-016 新增）—— 真故障 |
| `2999` | 上游 200 但响应体不合契约 —— 上游变更或故障 |

**明确不计入**：全部 `1xxx`、`2400` `2401` `2402` `2403` `2404` `2413` `2000`（用户输入或配置问题，重试无用且与上游健康无关）、全部 `3xxx`（我方资源状态，含 `3099` —— 我方 bug 不该表现为「上游熔断」）。

> `2429` 计入是**有意**的：持续限流说明我方速率配置超出上游配额，熔断降速是正确反应。但这依赖 `half_open_probes ≥ 1`（第 6 条）。

```go
// breakerCodes 是「计入熔断失败」的错误码白名单。
// 🔴 MUST 与 14-错误契约 §6.1 逐字一致；改一处 MUST 改另一处
//   （92-审计修复台账 §四 的传播矩阵已登记这一对关系）。
var breakerCodes = map[int]bool{
    domain.CodeUpTimeout:        true, // 2408
    domain.CodeUpRateLimited:    true, // 2429
    domain.CodeUpServer:         true, // 2500
    domain.CodeUpUnavailable:    true, // 2503
    domain.CodeUpGatewayTimeout: true, // 2504
    domain.CodeUpNetwork:        true, // 2998
    domain.CodeUpParse:          true, // 2999
}

// Record：只有 breakerCodes 里的码才累加连续失败计数，
// 其余（含 nil）一律按成功清零。
func (b *breaker) Record(key string, err error)

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用失败率（Denominator 太小，无统计意义） |
| 2 | MUST NOT 让 `half-open` 无限放行探测（必须限额） |
| 3 | MUST NOT 在 `closed` 下拦任何请求 |
| 4 | MUST NOT 让熔断影响 `ds_health` 的本地检查部分 |

### 5 参考骨架

```go
// circuit 是 per-key 连续失败计数熔断器。
// 用「连续失败次数」而不是「失败率」：我们的 QPS 可能低到每分钟几次，
// 失败率在这种样本量下没有意义。
type circuit struct {
    clk   Clock
    cfg   Config
    mu    sync.Mutex
    state map[string]*state
}

// Allow 返回是否放行。处于 open 时返回 3030 并带 retry_after_ms，
// 让调用方知道「多久之后再试」而不是盲目重试。
func (c *circuit) Allow(key string) error
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 4 次连续失败 | 仍 `closed`，放行 |
| 5 次连续失败 | `open` |
| `open` 后立即 `Allow` | `3030` + `retry_after_ms > 0` |
| 假 Clock +15s | 转 `half-open`，放行 1、2 个 |
| `half-open` 第 3 个 | 被拒 |
| `half-open` 2 个全成功 | `closed`，放行 |
| `half-open` 1 个失败 | 回 `open` |
| 失败 3 次后成功 1 次 | 计数清零（不累计） |
| search 熔断不影响 fetch | fetch 仍 `closed` |

### 7 验收命令与期望输出

```bash
$ go test ./internal/resilience/ -race -cover -v -run TestCircuit
--- PASS: TestCircuit (0.00s)
    --- PASS: TestCircuit/half_open_限额探测
    ...
coverage: 8x.x% of statements
```

### 8 完成定义（DoD）

- [ ] 9 个用例全绿
- [ ] `internal/resilience` 整体覆盖率 ≥ **80%**
- [ ] 提交：`feat(resilience): 连续失败熔断器`
