# Task-03 · Config 层（加载、优先级、校验）

> **执行方式**：3 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 把 `config.toml` + `Config/.env` + 环境变量合成一个不可变 `Config`，且**校验不过就拒绝启动** |
| 依赖 | Task-02 |
| 产出 | `internal/config` 覆盖率 ≥ 80%；拼错参数名当场报错并给行号 |
| 必读 | `Contract/13-配置契约` 全文（尤其 §二 模板与 §三 详表）；`Contract/11-接口契约` §二；`Engineering/20-编码规范` §二 G10 |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | 结构体 + 默认值 | 字段与 toml 键一一对应，漏一个就漂移 |
| A-02 | 加载与优先级 | 优先级顺序错会让人「改了配置不生效」 |
| A-03 | Validate 与启动检查 | 7 项启动检查一条都不能省 |

---

## 原子任务 A-01 · Config 结构体与默认值
### 0 前置

- Task-02 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

配置按 section 分组，共 21 个 struct。`Config` 是**值类型**返回（不是指针），运行期只读。每个 struct 字段的 `toml` tag MUST 与 `13-配置契约` §二 模板里的键名逐字一致 —— `Scripts/CheckDocDrift.sh` 第 5 项会做集合 diff。

### 2 要创建的文件

`internal/config/config.go`（16 个 struct + `Default()`）、`internal/config/config_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|
| 1 | 21 个 struct：`Config` + `AppConfig` `ServerConfig` `ServerHttpConfig` `UpstreamConfig` `RateLimitConfig` `RateLimitBucketConfig` `RateLimitWarnConfig` `TimeoutConfig` `TimeoutToolConfig` `RetryConfig` `RetryBudgetConfig` `CircuitConfig` `LimitsConfig` `LimitsBudgetConfig` `CacheConfig` `DedupeConfig` `SearchConfig` `SecurityConfig` `LogConfig` `SynthConfig`。🔴 `HedgeConfig` **不存在**：`[hedge]` 已整段移出阶段一（DEBT-007），按 A-02 要求必须被当作未知键拒绝 |
| 2 | `ApiKey` 与 `HttpToken` 字段 tag 为 `toml:"-" json:"-"`（MUST NOT 出现在配置解析与任何序列化） |
| 3 | `Default() Config` 返回**与文档 §三 默认值列逐格相同**的配置 |
| 4 | `Redacted() Config` 把 `ApiKey`/`HttpToken` 换成 `"***"`（为空时也换，避免泄漏「是否配置」） |
| 5 | 每个 struct 有中文注释说明它管什么 |
| 6 | `Source(key string) SourceKind` + `sources map[string]SourceKind` 字段（`json:"-" toml:"-"`） |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用 `interface{}` 或 `map[string]any` 承载任何配置项 |
| 2 | MUST NOT 在**叶字段**上省略 `toml` tag（漏了就会用字段名小写匹配，行为不确定）；**struct 字段 MUST NOT 加 `toml` tag** —— `CheckDocDrift.sh` 会把所有 tag 当配置键，段名加 tag 会造成「代码有而文档无」的假漂移，而 BurntSushi 对段名本就按字段名大小写不敏感匹配 |
| 3 | MUST NOT 在 `config.go` 里创建目录、打开文件、发网络请求 |
| 4 | MUST NOT 给 `ApiKey` 定义任何 `String()` 方法（`fmt` 会调它，容易泄漏） |

### 5 参考骨架（示范 3 个 struct，其余按 `Contract/13-配置契约.md` §二 一一对应）

```go
// ServerConfig 管「怎么和客户端说话」。
type ServerConfig struct {
    Transport string           `toml:"transport"` // "stdio" | "streamable-http"
    Http      ServerHttpConfig `toml:"http"`
}

// ServerHttpConfig 只在 Transport == "streamable-http" 时生效。
// 默认值全部指向「最安全」的一端：只听回环、开 Origin 校验、开 Host 校验、不鉴权。
type ServerHttpConfig struct {
    Host                    string   `toml:"host"`
    Port                    int      `toml:"port"`
    Path                    string   `toml:"path"`
    JsonResponse            bool     `toml:"json_response"`
    MaxRequestBodyBytes     int64    `toml:"max_request_body_bytes"`
    // 🔴 原有的 SessionTimeoutS 已删除：ADR-003 决定只做无状态且不给 mode 开关，
    //    该字段没有任何生效路径，属孤儿配置（13-配置契约 §1.1 第 10 条、DEBT-008）。
    //    Validate() MUST 把 config.toml 里出现的 session_timeout_s 当**未知键**拒绝启动。
    DisableLocalhostProtection bool  `toml:"disable_localhost_protection"`
    EnableCrossOriginProtection bool `toml:"enable_cross_origin_protection"`
    AllowedOrigins          []string `toml:"allowed_origins"`
    AllowNonLocalHost       bool     `toml:"allow_non_local_host"`
    HealthzPath             string   `toml:"healthz_path"`
    AuthEnabled             bool     `toml:"auth_enabled"`
    AuthTokenEnv            string   `toml:"auth_token_env"`
}

// CacheConfig 是省钱的核心。默认 backend 只有 sqlite 一个合法值。
type CacheConfig struct {
    Enabled         bool   `toml:"enabled"`
    Backend         string `toml:"backend"`   // "sqlite"
    DbPath          string `toml:"db_path"`
    TtlFetchS       int    `toml:"ttl_fetch_s"`   // 21600
    TtlSearchS      int    `toml:"ttl_search_s"`  // 0 = 不缓存（实测上游非确定性）
    MaxEntries      int    `toml:"max_entries"`
    MaxBytesMb      int    `toml:"max_bytes_mb"`
    MaxEntryKb      int    `toml:"max_entry_kb"`
    WaitMs          int    `toml:"wait_ms"`
    PruneIntervalS  int    `toml:"prune_interval_s"`
    DegradedOk      bool   `toml:"degraded_ok"`
}
```

### 6 测试代码

- `TestDefault`：断言 `Default().Cache.TtlFetchS == 21600`、`Server.Http.Port == 8765`、`Server.Transport == "streamable-http"` 等 ≥10 个关键默认值
- `TestRedacted`：`Redacted()` 后 `ApiKey == "***"`，且原 Config 未被修改（值语义）

### 7 验收命令与期望输出

```bash
$ go test ./internal/config/ -run 'TestDefault|TestRedacted' -v
--- PASS: TestDefault (0.00s)
--- PASS: TestRedacted (0.00s)
```

### 8 完成定义（DoD）

- [ ] 21 个 struct 全部落地，`toml` tag 与文档 §二 键名一致（人工逐行核对一次）
- [ ] `TestDefault` 覆盖 ≥10 个关键默认值
- [ ] 提交：`feat(config): 配置结构体与默认值`

---

## 原子任务 A-02 · 加载与优先级
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

优先级 `环境变量 > Config/.env > config.toml > 内置默认值`。实现方式：先把 `Default()` 填进去，然后按**从低到高**的顺序覆盖，每覆盖一项就记一条 `sources[key]`。这样 `Source()` 能回答「这个值到底从哪来的」—— 用户排查「我改了配置为什么没生效」时这一条最有用。

### 2 要创建的文件

`internal/config/load.go`、`internal/config/env.go`、`internal/config/load_test.go`、`internal/config/testdata/*.toml`

### 3 实现要求

| # | 要求 |
|:-:|---|
| 1 | `Load(configPath, envPath string) (Config, error)`；`LoadFromEnvOnly() (Config, error)` |
| 2 | `config.toml` 用 `github.com/BurntSushi/toml`（**本包**唯一允许的配置解析依赖），开启未知键检测 |
| 3 | 未知键 → 返回 error（码 **3090** `LOC_CONFIG_INVALID`，`Detail` 写明「未知键 `<键名>`」），消息含**行号**与键名 |
| 4 | `.env` MUST 用 `github.com/joho/godotenv` 的 **`Read()`**（返回 `map[string]string`，不写环境），且 **MUST NOT 覆盖已存在的真实环境变量**。🔴 **MUST NOT 用 `Load()` / `Overload()`** —— 它们内部就是 `os.Setenv`，会直接违反下方「禁止事项 1」，且污染进程环境（影响子进程与同进程的后续测试），很难被发现 |
| 5 | `.env` 权限校验规则为 **`perm & 0o077 == 0`**（group 与 other 无任何权限）→ 通过；否则返回 error（码 **3090** `LOC_CONFIG_INVALID`，`Detail` 写明「`.env` 权限过宽：0NNN」）。Linux/macOS 生效，Windows 跳过本项。<br>🔴 **MUST NOT 写成 `perm != 0600`** —— 那会拒绝**更安全**的 `0400`（只读），而 Ansible / Kubernetes Secret 挂载默认就是 `0400` 或 `0440`，用户会遇到「明明更严格却被拒绝启动」 |
| 6 | `config.toml` 里出现**密钥类键名或密钥形态的值** → 返回 error（码 **3090** `LOC_CONFIG_INVALID`，`Detail` 写明违规的键名或「值形态疑似密钥」）+ 行号。判定规则见下方「6.1 密钥检测规则」 |
| 7 | 环境变量覆盖表：`DEEPSEEK_API_KEY` `DEEPSEEKSEARCH_HTTP_TOKEN` `DEEPSEEK_BASE_URL` `DEEPSEEKSEARCH_TRANSPORT` `DEEPSEEKSEARCH_LOG_LEVEL` `DEEPSEEKSEARCH_CONFIG`（只有这 6 个）。其中 `DEEPSEEKSEARCH_CONFIG` 只决定 `config.toml` 的路径（覆盖 `Load` 的 `configPath` 参数），不是 `Config` 字段；其余 5 个都是值覆盖 |
| 8 | 每一项**值覆盖** MUST 同步更新 `sources`（`DEEPSEEKSEARCH_CONFIG` 是路径而非值，不写 `sources`） |
| 9 | 路径不存在时 `Load` 返回 error（MUST NOT 静默用默认值），`LoadFromEnvOnly` 不读任何文件 |
| 10 | `synth.enabled = true` → 返回 error。阶段一 MUST 为 `false`（`13-配置契约` §1.1 写明了这条，但原检查表漏列，语义见错误码 **1095**） |
| 11 | 配置模板中 **MUST NOT 出现 `[hedge]` 段** —— 阶段一无任何 Task 认领该实现（见 `16-技术债登记表` DEBT-007）。若解析到 `hedge` 段，按未知键处理（要求 3） |
| 12 | `transport = "stdio"` 且日志目的地包含 stdout → 返回 error。stdio 下 stdout 是 MCP 协议通道，一行日志就能打断整个会话（见 `Task-11`） |

#### 6.1 密钥检测规则（🔴 本条曾打死自家默认模板，改动前 MUST 读完）

**原规则是子串正则 `(?i)(key|token|secret|password|credential)`，它有一个致命缺陷**：官方配置模板自带

```toml
[server.http]
auth_token_env = "DEEPSEEKSEARCH_HTTP_TOKEN"   # 见 13-配置契约 §二，键说明表同处
```

键名 `auth_token_env` 含 `token` 子串 ⇒ 子串匹配下，**逐字复制 `config.example.toml` 的用户 100% 无法启动**。这是最坏的一类 bug：安全防线打死 happy path，而且是在「照文档做」的路径上。

**正确规则（两道防线，都要实现）**：

| 防线 | 规则 | 说明 |
|:-:|---|---|
| ① 精确键名黑名单 | 键名（大小写不敏感、取 `[section]` 前缀之后的**最后一段**）**完整等于**下列之一才违规：`api_key` `apikey` `api_secret` `token` `secret` `password` `passwd` `credential` `credentials` `private_key` | 是**全等**而非子串。`auth_token_env`、`token_header_name` 这类「描述用途」的键名不违规 |
| ② 值形态检测 | 任何键的值若匹配 `sk-[A-Za-z0-9]{16,}`，**或** 长度 ≥32 且 Shannon 熵 ≥4.0 → 违规 | 抓住「键名无害但值是密钥」的情况（如 `note = "sk-..."`），这是防线 ① 覆盖不到的 |

**验收 MUST 包含以下三条测试**（缺一条即不算完成）：

1. **默认 `config.example.toml` 逐字加载 → 通过全部启动检查**（这条专门守住本次事故不复发）；
2. `[upstream] api_key = "sk-xxxx"` → 拒绝，error 含行号与键名；
3. `[app] note = "sk-abcdefghijklmnopqrst"` → 拒绝（值形态检测生效），error 指明是「值」而非「键名」违规。

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用 `os.Setenv` 把 `.env` 的内容写回真实环境（会污染子进程） |
| 2 | MUST NOT 让 `.env` 覆盖真实环境变量 |
| 3 | MUST NOT 在 `Load` 里创建 `Data/` `Logs/` 目录（那是 Cmd 的事） |
| 4 | MUST NOT 忽略 `toml.Decode` 返回的 `MetaData.Undecoded()`（这就是未知键的来源） |

### 5 参考骨架

```go
// Load 按「环境变量 > Config/.env > config.toml > 内置默认值」优先级合并。
// 返回的 Config 已通过 Validate；任一项不合法则返回 error，调用方 MUST 直接退出。
func Load(configPath, envPath string) (Config, error) {
    cfg := Default()
    cfg.sources = make(map[string]SourceKind)

    // 第三优先级：config.toml
    if err := applyToml(&cfg, configPath); err != nil {
        return Config{}, fmt.Errorf("config: %w", err)
    }
    // 第二优先级：.env
    if err := applyDotEnv(&cfg, envPath); err != nil {
        return Config{}, fmt.Errorf("config: %w", err)
    }
    // 第一优先级：真实环境变量
    applyEnv(&cfg)

    if _, err := cfg.Validate(); err != nil {
        return Config{}, fmt.Errorf("config: %w", err)
    }
    return cfg, nil
}
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 只有 toml，rps=99 | `RateLimit.Search.Rps == 99`，`Source("ratelimit.search.rps") == file` |
| toml 里 `log.level=warn` + 环境变量 `DEEPSEEKSEARCH_LOG_LEVEL=debug` | `Log.Level == "debug"`，`Source("log.level") == env` |
| 值类型返回 | 修改返回值不影响再次 `Default()` |
| 未知键 `[server] hosst = "x"` | error 含 `hosst` 与行号 |
| toml 里写 `api_key = "sk-x"` | error 含「禁止在 config.toml 放密钥」 |
| `.env` 权限 644 | error |
| `.env` 存在但环境已有同名变量 | 用环境的（不被覆盖） |

### 7 验收命令与期望输出

```bash
$ go test ./internal/config/ -v -run TestLoad
--- PASS: TestLoad (0.01s)
    --- PASS: TestLoad/未知键报错含行号
    --- PASS: TestLoad/env_优先于_toml
    ...
```

### 8 完成定义（DoD）

- [ ] 7 个用例全绿
- [ ] `go.mod` 只多两个依赖：`BurntSushi/toml`、`joho/godotenv`
- [ ] 提交：`feat(config): 加载、优先级与未知键检测`

---

## 原子任务 A-03 · Validate 与启动检查
### 0 前置

- A-02 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

`13-配置契约` §1.1 列了启动时强制检查的 7 件事。第 6 条（未知键）在 A-02 已做。本任务做剩下的 6 条 + 跨字段一致性校验（如 `host=0.0.0.0` 必须配 `auth_enabled=true`）。

**为什么这么严**：上游 DeepSeek 会静默忽略未知字段（实测）。我们绝不在自己的配置上重犯同一个错 —— 写错参数名必须当场报错，不能装作没看见。

### 2 要创建的文件

`internal/config/validate.go`、`internal/config/validate_test.go`

### 3 实现要求

| # | 检查 | 失败码 |
|:-:|---|---|
| 1 | `.env` 权限满足 `perm & 0o077 == 0`（A-02 已实现，含 Windows 跳过；不是「必须等于 600」） | `3090` |
| 2 | `DEEPSEEK_API_KEY` 非空 | `3040` |
| 3 | `auth_enabled=true` 时 `HttpToken` 非空且 ≥16 字节 | `3041` |
| 4 | 枚举值合法：`transport ∈ {stdio, streamable-http}`、`cache.backend ∈ {sqlite}`、`log.level ∈ {debug,info,warn,error}`、`log.format ∈ {text,json}`、`retry.jitter_mode ∈ {fixed_plus_random,full,none}`、`search.default_detail ∈ {minimal,snippet,summary,content}` | `3090` |
| 5 | 数值区间：`port 1~65535`、`count 1~50`、`rps > 0`、🔴 `max_inflight **3**~64`、`max_wait_ms 1~300000`、`max_attempts 1~6`、`summary_chars ≥ 0` | `3090` |
| 5.1 | 🔴 **新增键 `ratelimit.search_share`：0 < x < 1（开区间）**。0 或 1 都说明用户误解了这个键（两端各有 `max(1,·)` / `max(2,·)` 兜底，填边界值等于没填）。完整区间清单 MUST 逐条对齐 `13-配置契约` §1.1.2 —— 那里是穷举表（20 余行），本行只是摘要 | `3090` |
| 6 | `Asia/Shanghai` 可加载（`domain` 包已 panic，这里只做 preflight 提示） | `3090` |
| 7 | 🔴 跨字段：`host == "0.0.0.0"` 或 `allow_non_local_host == true` → 必须 `auth_enabled == true` | `3092` |
| 8 | 跨字段：`per_attempt_ms × max_attempts + Σ(fixed_ms + spread_ms) ≤ total_timeout_ms`。🔴 **search 与 fetch 各自校验**（原设计只写了 fetch；search 的 `max_attempts` 默认是 **4**，漏检会让 search 的重试永远跑不完。默认值：`12000×4+19000=67000`，故 `total_timeout_ms` 默认 MUST 为 67000） | `3090` |
| 9 | 跨字段：`fetch.per_attempt_ms > 31000`（必须大于上游 30s 网关超时） | `3090` |
| 10 | 跨字段：`total_timeout_ms ≤ 115000`（必须小于 Hermes 120s） | `3090`（warning 而非 error，允许用户明确接受超时风险） |
| 10.1 | 🔴 **补齐 `13-配置契约` §1.1.3 里此前未落地的 4 条跨字段约束**（原表只有 7/8/9/10 四条，实现必然漏掉这些）：<br>① `max_wait_ms < min(search.total_timeout_ms, fetch.total_timeout_ms)` → **拒绝启动**。否则「排队 5 分钟」的语义永远达不到，请求总在 total 处被掐死成 `3020`<br>② `per_attempt_ms ≥ dial_ms + tls_handshake_ms + response_header_ms` → **warning**。分层超时会先生效，`per_attempt_ms` 形同虚设<br>③ `len(retry.fixed_ms) ≥ max_attempts - 1` 且 `len(fixed_ms) == len(spread_ms)` → **拒绝启动**。否则 `Delay(attempt)` 越界行为未定义（search/fetch 各自校验）<br>④ `cache.wait_ms` 远小于 `fetch.per_attempt_ms` → **warning**，并给出调参指引（跨进程等待者几乎必然拿到 `3075`；默认值下就是这种情形，属有意取舍） | `3090` |
| 10.2 | 🔴 `max_inflight ≥ inflight.search + inflight.fetch`，两个配额由 `search_share` 推导且各有下限（search 1、fetch 2）。`max_inflight < 3` 时下限会把配额之和顶到 3、超过声明的总额度 ⇒ 配置自相矛盾，MUST 当场报错而不是静默取整。公式见 `Task-06` §A-01 3.2 | `3090` |
| 10.3 | 🔴 `Validate()` 的签名 MUST 是 `Validate() (warnings []string, err error)`。原设计只有 `Validate() error` + `errors.Join`，**warning 没有返回通道**，上面 ①②④ 与第 10 条就无法实现。warning 由 `cmd` 在启动日志里逐条打印，`-check` 模式下同样输出 | — |
| 11 | 错误消息 MUST 含**字段路径**，如 `"ratelimit.search.rps 必须 > 0，实际 0"` | — |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 在 `Validate` 里做网络请求或文件写入 |
| 2 | MUST NOT 只返回第一个错误就退出 —— MUST 收集**全部**错误一起返回（用户不用改一轮跑一次） |
| 3 | MUST NOT 对 `auth_enabled=false` + `host=127.0.0.1` 报错（这是合法的默认配置） |
| 4 | MUST NOT 用 `panic` —— 返回 error |

### 5 参考骨架

```go
// Validate 收集全部配置错误后一次性返回，并把 warning 单独交给 cmd 打印。
func (c Config) Validate() (warnings []string, err error) {
    var errs []error
    if e := checkPhaseOne(&c); e != nil {
        errs = append(errs, e)
    }
    errs = append(errs, c.validateServer()...)
    errs = append(errs, c.validateUpstream()...)
    errs = append(errs, c.validateRateLimit()...)
    errs = append(errs, c.validateTimeout()...)
    errs = append(errs, c.validateRetry()...)
    errs = append(errs, c.validateLimits()...)
    errs = append(errs, c.validateCache()...)
    errs = append(errs, c.validateLog()...)
    return c.collectWarnings(), errors.Join(errs...)
}

// validateServer 检查「暴露面与安全」的交叉约束。
// 独立成函数是因为这是唯一会出现「两个字段必须联动」的地方，
// 混在通用枚举检查里会看不清为什么报错。
func (c *Config) validateServer() []error {
    var errs []error
    // 裸奔检查：监听外网 + 不鉴权 = 任何人连上都能花你的钱
    if (c.Server.Http.Host == "0.0.0.0" || c.Server.Http.AllowNonLocalHost) && !c.Server.Http.AuthEnabled {
        errs = append(errs, configErr(domain.CodeLocHTTPExposedNoAuth,
            "server.http: host=0.0.0.0 或 allow_non_local_host=true 时必须 auth_enabled=true"))
    }
    return errs
}
```

### 6 测试代码

| 用例 | 期望 |
|---|---|
| 默认配置 | `Validate() == nil` |
| `transport = "http"`（旧写法） | error 含 `streamable-http` |
| `rps = 0` | error 含 `ratelimit.search.rps` |
| `port = 70000` | error 含 `server.http.port` |
| `host=0.0.0.0` + `auth_enabled=false` | error 含 `3092` 语义 |
| `host=0.0.0.0` + `auth_enabled=true` + token 空 | error 含 `3041` |
| 两个错误同时存在 | 一次返回 2 条 |

### 7 验收命令与期望输出

```bash
$ go test ./internal/config/ -cover
coverage: 8x.x% of statements

$ cd /mcp/DeepSeekSearch && ./Bin/DeepSeekSearch -check -config Config/config.toml
配置校验通过（transport=streamable-http, host=127.0.0.1, port=8765, cache=sqlite）
```

> ⚠️ 第二条命令依赖 Task-12 的 Cmd 装配（`-check` flag 与 `Config/config.toml` 都不在 Task-03 的产出里）。Task-03 阶段只执行 `go test`；二进制检查必须在 Task-12 验收时补跑，MUST NOT 为了本条在 Task-03 里提前实现 `cmd`。

### 8 完成定义（DoD）

- [ ] `internal/config` 覆盖率 ≥ 80%
- [ ] 11 条检查全部实现
- [ ] 提交：`feat(config): 校验与启动检查`
