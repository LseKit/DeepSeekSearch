# Task-11 · Transport 层与架构守护

> **执行方式**：3 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | stdio + 无状态 Streamable HTTP 两个传输，配完整中间件链；架构守护测试落地 |
| 依赖 | Task-02 ~ Task-10 |
| 产出 | 协议契约 **P1~P26 全 26 条**全绿；`arch_test.go` 能拦住违规 import（**项目内 + 外部两组**）；F-S2/F-S4 有自动化牙齿；`SyncDocs.sh` 交付 |
| 必读 | `Contract/15-协议契约` 全文（尤其 §六 中间件铁律、**§六之二 中间件链顺序**、§十 验收清单）；`Engineering/22-架构守护规则` 全文（尤其 §2.1 两张白名单表、§2.3、§三 分工表、§五 调用签名）；`Contract/11-接口契约` §六；`Contract/10-包依赖矩阵` §2.1~2.9 |

| 原子 | 标题 | 关键风险 |
|:-:|---|:-:|
| A-01 | stdio 传输 | stdout 混入日志（**含依赖库**，靠 fd 重定向兜底） |
| A-02 | Streamable HTTP + 中间件链 + `/healthz` | 🔴 忘了 `Flush` → 静默卡死；🔴 `crossOrigin` 装在 `authGuard` 之内 → 浏览器客户端必挂 |
| A-03 | 架构守护测试与脚本 | 白名单只查一组 / 文档漂移 / 脚本签名传反 |

---

## 原子任务 A-01 · stdio 传输
### 0 前置

- Task-02 ~ Task-10 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节
- 工作区干净（`git status --short` 无输出）

### 1 上下文（自包含）

stdio 是最常见的部署形态（客户端自己 spawn 进程）。唯一的致命陷阱：**fd 1 只能出协议帧**。任何 `fmt.Println` / `log.Printf` / 库内部的 stdout 输出都会让 JSON-RPC 解析失败，表现为「客户端连上了但什么都不响应」。

🔴 **只靠「禁止事项 + 一个自家代码的测试」守不住这条**。`TestStdio_NoStdoutPollution` 只能证明**我们写的**代码没往 stdout 写，证明不了未来的依赖库不写。三类现实来源：

| 来源 | 例子 | 为什么原来的测试抓不到 |
|---|---|---|
| SQLite 驱动 | `modernc.org/sqlite` 的诊断输出、`database/sql` 的调试 `println` 残留 | 只在特定错误路径触发，测试用的是临时库，走不到那条路径 |
| SDK 升级 | go-sdk 某个版本在握手失败时 `fmt.Fprintln(os.Stdout, ...)` | 升级时没人会重跑「stdout 干净」这条断言 |
| slog handler 误配 | 某个新包的 `init()` 里装了 `slog.NewTextHandler(os.Stdout, nil)` | `13-配置契约` §1.1 第 11 条只在**启动期**校验配置里的日志目的地，管不到代码里硬写的 handler |

⇒ **运行时加固**：stdio 模式启动时把包级变量 `os.Stdout` 改指向 `os.Stderr`，协议通道用**事先保存的原始 `*os.File`（fd 1）**。此后任何走 `os.Stdout` 的写入（Go 侧绝大多数库都是这条路径）自动落到 stderr，协议流不受污染。

这是**兜底**，不是替代禁令。它在本项目之所以完备，是因为 `CGO_ENABLED=0`（`17-版本与发布契约` §2.2）⇒ 没有 C 代码绕过 Go 的 `os.Stdout` 直接 `write(1, ...)`。

### 2 要创建的文件

`internal/transport/transport.go`（`Handler` 接口 + `Build` + `Available`）、`internal/transport/stdio.go`、`internal/transport/stdio_test.go`

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | `Handler` 接口与 `Build(name, cfg, build, logger)` 签名逐字按 `11-接口契约` §六 |
| 2 | `Build` 名字未知 → 返回 error，MUST NOT panic |
| 3 | `Available()` 返回 `["stdio", "streamable-http"]`（字典序） |
| 4 | stdio 用 `&mcp.IOTransport{Reader: os.Stdin, Writer: <原始 fd 1 的 *os.File>}` + `srv.Run(ctx, transport)`。🔴 go-sdk v1.8.0 **没有** `mcp.NewStdioTransport()`，且 `&mcp.StdioTransport{}` 的 `Connect` 在**连接时**才读全局 `os.Stdout`，重定向后协议帧会被带去 stderr ⇒ MUST 显式注入保存的原始文件 |
| 5 | 收到 `SIGTERM` 或 stdin EOF → 优雅退出（`Serve` 返回 nil） |
| 6 | 启动时打 `app.transport`，含 `transport=stdio` 与 `version` |
| 7 | `Serve` MUST 在返回前完成资源释放 |
| 8 | 🔴 **stdout 加固（三步，顺序 MUST NOT 调换）**：① `proto := os.Stdout` 保存原始 fd 1 的 `*os.File`；② 用它显式构造 `&mcp.IOTransport{Reader: os.Stdin, Writer: nopCloseWriter{proto}}`（writer MUST NOT 让 SDK 在关闭会话时关掉 fd 1）；③ `os.Stdout = os.Stderr`。此后协议帧仍走 fd 1，任何库写 `os.Stdout` 都落到 stderr |
| 9 | 🔴 第 8 条 MUST 用 §6 的**第二段**测试实测确认：重定向之后 SDK 仍能写出协议帧。**实测结论（go-sdk v1.8.0）**：`NewStdioTransport` 不存在，`StdioTransport.Connect` 在连接时才读 `os.Stdout` ⇒ 第 8 条的显式注入是唯一可行路径（保存的 `*os.File` 直接写 fd 1，不需要 `syscall.Dup`，也不破坏 Windows 构建）。MUST NOT 回退到 `&mcp.StdioTransport{}` |
| 10 | 日志目的地 MUST NOT 含 stdout（`13-配置契约` §1.1 第 11 条已在启动期以 `3090` 拒绝）；本条在 transport 层**再断言一次**：第 8 条的重定向会把「配错了但没被发现」变成静默行为，断言是唯一能让它显形的地方 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | 🔴 MUST NOT 向 fd 1 写任何非协议内容 |
| 2 | MUST NOT 在 `Serve` 里自己读 stdin 做协议解析（SDK 负责） |
| 3 | MUST NOT 让 `Build` panic |
| 4 | MUST NOT 在 stdio 模式下尝试监听端口 |
| 5 | 🔴 MUST NOT 在构造 SDK transport **之前**就把 `os.Stdout` 指向 stderr —— 协议帧会跟着日志一起进 stderr，客户端读到 EOF，症状与「库污染 stdout」一模一样但更难查 |
| 6 | MUST NOT 用 `os.Stdout = nil` 或关闭 fd 1 的方式「防污染」（SDK 写入得到 `EBADF`，且 `os.Stderr` 可能同受影响） |
| 7 | MUST NOT 只在 `streamable-http` 分支做加固而漏掉 stdio 的其它入口 |

### 5 参考骨架

```go
// stdioHandler 通过标准输入输出跑 MCP 协议。
// 致命约束：fd 1 只能出协议帧。日志必须走文件或 stderr，
// 否则客户端表现为「连上了但什么都不响应」，且没有任何错误提示。
type stdioHandler struct {
    cfg   config.Config
    build domain.BuildInfo
    log   *slog.Logger
}

func (h *stdioHandler) Name() string { return "stdio" }

func (h *stdioHandler) Serve(ctx context.Context, srv *mcp.Server) error {
    h.log.Info("app.transport", "transport", "stdio",
        "version", h.build.Version, "protocol", "2026-07-28")

    // 🔴 三步顺序固定（§3 第 8 条）：① 保存原始 fd 1；② 用它显式构造 transport；
    // ③ 才把 os.Stdout 改指向 stderr。第 ③ 步之后，任何依赖库写 os.Stdout 都自动
    // 落到 stderr —— 这是兜底，因为「禁止写 stdout」这条禁令只能约束我们自己写的
    // 代码，约束不了 sqlite 驱动、SDK 升级、以及别处 init() 里装错的 slog handler。
    proto := os.Stdout
    tr := &mcp.IOTransport{Reader: os.Stdin, Writer: nopCloseWriter{proto}}
    os.Stdout = os.Stderr

    return srv.Run(ctx, tr)
}
```

### 6 测试代码

- `TestBuild`：`Build("stdio", ...)` 成功；`Build("sse", ...)` 返回 error；`Build("", ...)` 返回 error
- `TestAvailable`：`["stdio","streamable-http"]`
- `TestStdio_NoStdoutPollution`（**两段，MUST 都有**）：
  - **第一段（原有）**：把 fd 1 接到管道，跑一次 `Serve` 到 EOF，断言管道里只有合法 JSON-RPC 帧、没有多余字节
  - **第二段（新增，🔴 守 §3 第 8/9 条）**：在 `Serve` 运行期间，由一个**模拟第三方库**的 goroutine 执行 `fmt.Fprintln(os.Stdout, "library noise")`，断言 ① 协议管道里**没有** `library noise`；② 该行出现在 stderr；③ SDK 仍能写出协议帧（证明重定向没把协议通道一起带走）。用例 MUST 用 `defer` 还原 `os.Stdout`，否则污染同一测试二进制里的其它用例

### 7 验收命令与期望输出

```bash
$ go test ./internal/transport/ -v -run 'TestBuild|TestAvailable|TestStdio'
--- PASS: TestBuild (0.00s)
--- PASS: TestAvailable (0.00s)
--- PASS: TestStdio_NoStdoutPollution (0.00s)
```

### 8 完成定义（DoD）

- [ ] 3 个用例全绿，其中 `TestStdio_NoStdoutPollution` 的**两段**都通过（第二段是 stdout 加固的唯一守卫，MUST NOT 只跑第一段就打勾）
- [ ] `grep -n 'os.Stdout = os.Stderr' internal/transport/stdio.go` 恰好 1 处，且位于 `mcp.IOTransport` 构造**之后**
- [ ] 提交：`feat(transport): stdio 传输`

---

## 原子任务 A-02 · Streamable HTTP、中间件链与 `/healthz`
### 0 前置

- A-01 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

**必须记住的三个实测事实**：

| 事实 | 后果 |
|---|---|
| 🔴 包装 `http.ResponseWriter` 不实现 `Flush` + `Unwrap` → 流式响应**静默卡死** 3 分钟，零错误日志 | 中间件必须严格实现这两个方法 |
| SDK **默认不校验 `Origin`**（带 `Origin: https://evil.example.com` 照样 200） | 必须自己加 `http.NewCrossOriginProtection()` |
| SDK 的 DNS 重绑定防护靠 `Host` 头判断，反代转发原始 Host 时**必然 403** | 需要 `allow_non_local_host` 开关 |

🔴 **中间件顺序固定（C10 裁决后）**：

```
recover → accessLog → securityHeaders → healthz → admission → crossOrigin → hostGuard → authGuard → bodyLimit → mcp
```

> **原顺序是 `… healthz → hostGuard → authGuard → crossOrigin → bodyLimit → mcp`，并标注「MUST NOT 调换」。它是错的，而且错在「auth_enabled=true 时任何浏览器端 MCP 客户端都不可用」这个量级上。**
>
> | # | 原顺序的后果 | 依据 |
> |:-:|---|---|
> | 1 | CORS 规范规定 **preflight 的 `OPTIONS` 请求不携带 `Authorization`**（浏览器只发 `Origin` + `Access-Control-Request-Method` + `Access-Control-Request-Headers`）。`crossOrigin` 在 `authGuard` **之内** ⇒ preflight 先被 `authGuard` 判 401，`Access-Control-Allow-*` 头永远发不出去 ⇒ 浏览器把整个请求判为 CORS 失败，真正的 `POST` 根本不会发出 | Fetch 标准 §4.8「preflight request」；`authGuard` 的 401 语义见 `14-错误契约` `3091` |
> | 2 | 即使没有 preflight（简单请求），`authGuard` 的 **401/403 响应也不带 CORS 头** ⇒ 浏览器对跨源响应「无 CORS 头 = 不可读」，客户端只能看到 `TypeError: Failed to fetch`，拿不到 `3091` 错误体 ⇒ 用户无法区分「token 写错了」和「服务挂了」 | 同上；`15-协议契约` §六之二 |
> | 3 | 跨源攻击者可以拿 `authGuard` 当 token 探测器（401 vs 200 的时序/状态差异），而 `crossOrigin` 本该在他之前就把请求拒掉 | `13-配置契约` §3.1 对 `enable_cross_origin_protection` 的定位就是「防止恶意网页通过浏览器调用你的本地工具」 |
>
> **裁决：把 `crossOrigin` 提到 `hostGuard` 与 `authGuard` 之外**（即上面那条链），而不是「让 `authGuard` 对 `OPTIONS` 放行」。理由：
>
> - **不放行 `OPTIONS` 的理由**：MCP 端点根本不接受 `OPTIONS` —— `15-协议契约` §五 实测 `GET /mcp` 与 `DELETE /mcp` 都是 `405 Allow: POST`。在鉴权层为 `OPTIONS` 开一个例外，等于在**最敏感的中间件**里引入一条「按 method 分支的未定义路径」，而 `Access-Control-Allow-*` 头仍然由 `crossOrigin` 发 —— 顺序问题一点没解决，只是把 401 换成了一个没人定义过语义的响应。
> - **提到外面的额外收益**：上面第 2、3 条后果一并消失（401/403 也经过 `crossOrigin`；跨源请求到不了鉴权层）。
> - **`healthz` 仍在 `crossOrigin` 之外**：探活必须能被 systemd / nginx / 监控匿名调用（`15-协议契约` §七之三「不鉴权」），这些调用方不发 `Origin` 头，本来也不会被 `crossOrigin` 拦；把它留在最外是为了让反代转发公网 `Host` 时探活仍然可用（`hostGuard` 在 `healthz` 之后）。
>
> **本项目是否支持浏览器客户端**：**支持**。判据是 `13-配置契约` §二 存在 `server.http.allowed_origins` 这个键，其文档原文就是「你在浏览器里有个自建面板要直接调这个 MCP，才需要把面板地址加进来」。既然配置层承诺了这个场景，传输层就 MUST 让 preflight 走得通 —— 否则那个键是孤儿配置（同 `respect_retry_after` 的教训，见 23 号 §6.3）。
>
> 🔴 顺序的 wire 级后果已同步写进 `Contract/15-协议契约` §六之二；`Contract/11-接口契约` §6.2 仍是旧顺序，**MUST 由该文件的负责人同步**（本任务无权改 11 号）。

### 2 要创建的文件

`internal/transport/http.go`、`internal/transport/middleware.go`、`internal/transport/healthz.go`、`internal/transport/http_test.go`

### 3 实现要求

| # | 要求 |
|---|---|
| 1 | `mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: cfg.JsonResponse})` |
| 2 | 🔴 `Stateless` **恒为 true**，MUST NOT 从配置读取（见 `ADR-003`） |
| 3 | `statusWriter` MUST 实现 `WriteHeader` / `Write` / `Flush` / `Unwrap` 四个方法（逐字用 `11-接口契约` §6.1 的实现） |
| 4 | `recover` 中间件：panic → 500 + 日志 `err`（含 stack），不影响进程 |
| 5 | `accessLog`：记录 `method` `path` `status` `bytes` `ms`；`ms > slow_ms` 加 `warn.slow` |
| 6 | `securityHeaders`：`X-Content-Type-Options: nosniff` |
| 7 | `healthz`：命中 `healthz_path` 直接返回 JSON，**不鉴权、不校验 Origin/Host**，`GET` 之外 405 |
| 8 | `hostGuard`：`allow_non_local_host=false` 时 Host 必须是 `127.0.0.1`/`localhost`/`::1`（可带端口），否则 403 + `3093` |
| 9 | `authGuard`：`auth_enabled=true` 时校验 `Authorization: Bearer <token>`（**常量时间比较** `crypto/subtle`），否则 401 + `3091`。🔴 MUST 位于 `crossOrigin` **之内**（见 §1 的顺序裁决），MUST NOT 为 `OPTIONS` 开任何例外分支 |
| 10 | `crossOrigin`：`http.NewCrossOriginProtection()` + `AddTrustedOrigin(allowed_origins...)`。🔴 MUST 位于 `hostGuard` 与 `authGuard` **之外**、`healthz` **之内**；`enable_cross_origin_protection=false` 时该层 MUST 完全透传（不改变 `ResponseWriter` 的包装层数，否则 §3 第 3 条的 `Flush`/`Unwrap` 又要重验一遍）。<br>🔴 **实现补充（实测 Go 1.27）**：标准库 `CrossOriginProtection` 把 `OPTIONS` 视为安全方法**一律放行**，且只拒绝、**不发** `Access-Control-Allow-*`。因此本层 MUST 自行：① 对可信 `Origin` 的 preflight 回 `204` + `Access-Control-Allow-Origin/Methods/Headers`；② 对不可信 `Origin` 的 preflight 直接 `403`（不是 `401`）；③ 对可信 `Origin` 的非 preflight 响应补 `Access-Control-Allow-Origin`，否则浏览器读不到 `401/403` 错误体 |
| 11 | `bodyLimit`：`http.MaxBytesReader(w, r.Body, max_request_body_bytes)` |
| 12 | `/healthz` 响应字段与 `15-协议契约` §七之三**逐字一致** |
| 12.1 | 🔴 **`cache` 三字段 MUST 来自注入的统计源，MUST NOT 恒为零值**：`transport` 定义 `CacheStats` 接口（避免 import `resilience`，10 号矩阵），`BuildWithCache` 把它传给 `healthz`；`cache == nil` 时才允许零值。测试 MUST 用假 `Stats` 断言 `entries`/`bytes`/`degraded` 原样出现在 JSON 里 —— `degraded` 恒 false 会掩盖「SQLite 已降级为直连」 |
| 13 | 优雅关闭：`http.Server.Shutdown(ctx)` 带 15s 超时 |
| 14 | 启动日志打 `app.ready` 含实际监听地址与 `config_sha256` |
| 15 | 🔴 中间件链的装配顺序 MUST 由**一个**函数（`chain(...)`）按 §1 那条链的字面顺序写死，MUST NOT 在各处分别 `Wrap`。理由：顺序错一位的后果是「浏览器客户端全挂」或「SSE 静默卡死 3 分钟」，都属于**不报错**的故障；集中在一处才能让 review 和 `TestMiddlewareOrder` 有唯一锚点 |
| 16 | `TestMiddlewareOrder` MUST 用反射/字符串化把实际装配顺序与 §1 的期望序列做全等比较，不一致即红（顺序是唯一无法靠单个中间件的自测覆盖的属性） |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | 🔴 MUST NOT 包装 `ResponseWriter` 只用 `struct{ http.ResponseWriter }` 而不加 `Flush`/`Unwrap` |
| 2 | MUST NOT 让 `Stateless` 可配置 |
| 3 | MUST NOT 让 `healthz` 需要 token |
| 4 | MUST NOT 在 `healthz` 里发起任何上游请求 |
| 5 | MUST NOT 用 `==` 比较 token（必须 `subtle.ConstantTimeCompare`） |
| 6 | MUST NOT 在 401/403 响应里回显期望的 token 或 Host |
| 7 | MUST NOT 省略 `allow_non_local_host=true` 时强制 `auth_enabled=true` 的交叉校验（Task-03 已做，这里再断言） |
| 8 | 🔴 MUST NOT 把 `authGuard` 或 `hostGuard` 装在 `crossOrigin` **之外**（后果见 §1 的三条：浏览器 preflight 必 401、401/403 响应浏览器读不到、跨源可探测 token） |
| 9 | 🔴 MUST NOT 在 `authGuard` 里为 `OPTIONS` 开「直接放行」的例外 —— 裁决见 §1；例外分支会让鉴权层出现一条无人定义语义的路径，而 CORS 头仍然发不出去 |
| 10 | MUST NOT 把 `healthz` 装到 `crossOrigin` / `hostGuard` / `authGuard` 之内（探活必须能被 systemd、nginx、监控**匿名且带公网 Host** 调用，见 `15-协议契约` §七之三） |
| 11 | MUST NOT 在中间件里包装 `ResponseWriter` 时丢掉 `Flush`/`Unwrap`，也 MUST NOT 依赖 `http.ResponseController` 而不实现 `Unwrap`（它靠 `Unwrap` 逐层解包） |

### 5 参考骨架

```go
// statusWriter 必须实现 Flush 与 Unwrap。
// 少了 Flush，整个流式响应会静默卡死 —— 实测 curl 3 秒收到 0 字节、
// SDK 客户端 hang 满 3 分钟，且没有任何错误日志。这是本项目最贵的坑。
type statusWriter struct {
    http.ResponseWriter
    code int
    n    int64
}

func (s *statusWriter) Flush() {
    if f, ok := s.ResponseWriter.(http.Flusher); ok {
        f.Flush()
    }
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// authGuard 用常量时间比较，避免时序侧信道泄漏 token 前缀。
// 🔴 它 MUST 被 crossOrigin 包在里面（§1 的顺序裁决）。
func authGuard(want string, next http.Handler) http.Handler

// chain 按 §1 的字面顺序装配。顺序写死在这一处，
// 因为错一位的两种后果（浏览器客户端全挂 / SSE 静默卡死）都不报错。
// 写法：从最内层往外包，所以代码顺序与请求经过顺序**相反**。
func chain(cfg config.Config, log *slog.Logger, mcpHandler http.Handler) http.Handler {
    h := mcpHandler
    h = bodyLimit(cfg, h)     // 8 最内
    h = authGuard(cfg, h)     // 7
    h = hostGuard(cfg, h)     // 6
    h = crossOrigin(cfg, h)   // 5 🔴 MUST 在 hostGuard/authGuard 之外
    h = healthz(cfg, h)       // 4 🔴 MUST 在 crossOrigin 之外（探活匿名可达）
    h = securityHeaders(h)    // 3
    h = accessLog(log, cfg, h) // 2
    return recoverMW(log, h)  // 1 最外
}

// middlewareNames 供 TestMiddlewareOrder 断言实际顺序。
// 与 chain 的包装顺序一一对应，改 chain MUST 同时改这里。
var middlewareNames = []string{
    "recover", "accessLog", "securityHeaders", "healthz", "admission",
    "crossOrigin", "hostGuard", "authGuard", "bodyLimit", "mcp",
}
```

### 6 测试代码（`httptest.NewServer`）

| # | 用例 | 期望 |
|:-:|---|---|
| P1 | `server/discover` → `supportedVersions[0]` | `"2026-07-28"` |
| P2 | `server/discover` → `ttlMs` | `> 0` |
| P2.1 | 🔴 **`SetCacheable` MUST 按 URI 分派，三个 Resource 的值互不相同** | 对 `ds://error-codes` / `ds://effective-config` / `ds://usage` 分别断言 `(300000,"public")` / `(300000,"private")` / `(1000,"private")`；**任意两个相同即 FAIL** |
| P3 | modern `tools/list`（无 initialize） | 200 |
| P4 | modern `tools/call`（带 `Mcp-Name`） | 200 |
| P5 | 缺 `Mcp-Method` | 400 `-32020` |
| P6 | `Mcp-Name` 不一致 | 400 `-32020` |
| P7 | 缺 `_meta.protocolVersion` | 400 `-32602` |
| P8 | 版本 `1999-01-01` | 400 `text/plain` |
| P9 | 未知方法 | 404 `-32601` |
| P10 | `GET /mcp` | 405 |
| P11 | `DELETE /mcp` | 405 |
| P12 | legacy 2024-11-05 initialize | 200 + 回显 |
| P13 | legacy 2025-11-25 `tools/call` | 200 |
| P14 | `notifications/initialized` | 202 空 body |
| P15 | `Origin: https://evil.example.com`（**两个子断言**） | ① `POST /mcp` → **非 200**；② 🔴 `OPTIONS /mcp` preflight（带 `Origin` + `Access-Control-Request-Method: POST`，**不带 `Authorization`**）在 `auth_enabled=true` 时 MUST NOT 返回 401 —— 这条守 §1 的中间件顺序裁决 |
| P16 | `tools/list` 两次 | 字节相同 |
| P17 | stdio 传输一次 `tools/call` 端到端（用 stub 工具，**不计费**） | 成功，且协议管道里只有合法 JSON-RPC 帧、无多余字节 |
| P18 | 🔴 HTTP 中间件场景下的 SSE 流 | 首包 < 1s（验证 `Flush` 生效）。**MUST 以 `json_response=false` 另起一个 handler 实例来测**：默认 `json_response=true` 时响应是 `application/json`、根本没有 SSE 流，本断言会**永不执行**却显示为 PASS（详见 §6.1） |
| P19 | `GET /healthz` | 200 + 含 `version` |
| P20 | `POST /healthz` | 405 |
| P21 | auth 开 + 无头 | 401 + `3091` |
| P22 | auth 开 + 错 token | 401 |
| P23 | auth 开 + 对 token | 200 |
| P24 | Host 非回环 + 未放行 | 403 + `3093` |
| P25 | `ds_search.annotations.readOnlyHint` | `true` |
| P26 | `serverInfo.version` == `--version` | 一致 |

> P17/P18 此前被推给 `Task-12` 的 L3 集成测试，于是 `91-验收总表` 的 11-A02 段只覆盖 **24 条**断言（11-A02-3 的 P1~P16 共 16 条 + 11-A02-4 的 P19~P26 共 8 条），**恰好把 P17/P18 两条漏在两张表之间的缝里**。而 P18 正是「SSE 少一次 `Flush` 导致流式静默卡死 3 分钟」这个**本项目最贵的坑**（§5 骨架注释、`15-协议契约` §6.3）的唯一直接验证 —— 漏掉它等于把最贵的坑交给「上线后用户报障」来发现。
>
> 现在两条都落在本原子任务（L1/L2，用 stub 工具，**不计费**）；`Task-12` A-02 的 L3-5 保留为「真实上游再跑一遍」，两者不重复也不互替。

### 6.1 🔴 P18 在默认配置下测不到（必须显式起一个 SSE 实例）

| 事实 | 出处 |
|---|---|
| `server.http.json_response` 默认 **`true`** | `13-配置契约` §二 `[server.http]`、§3.1 |
| `JSONResponse: true` 时 modern 与 legacy 请求**都返回 `application/json`**，没有 SSE 流 | `15-协议契约` §七 实测 |
| 「少一次 `Flush`」只在 `text/event-stream` 形态下才有症状：`curl` 3 秒收到 **0 字节**、SDK 客户端 hang 满 **3 分钟**、零错误日志 | `15-协议契约` §6.3 |

⇒ **P18 MUST 用一个 `JSONResponse: false` 的 handler 实例**（`httptest.NewServer` 起第二个 server，或直接构造 `mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: false})` 并套上完整 `chain`），断言：

1. 响应 `Content-Type` 是 `text/event-stream`（否则说明这个实例根本没走 SSE 路径，测试是假绿）；
2. **首包**（第一个 `event: message` 或第一个 `data:` 行）在 **1 秒内**到达 —— 用 `bufio.Scanner` 读到第一行就记时，MUST NOT 等整个响应结束再算总耗时（等结束的话，`Flush` 缺失时 `httptest` 会在 handler 返回时一次性刷出，耗时依然很短 ⇒ 断言恒真）；
3. 把 `statusWriter.Flush()` 临时改成空实现，本用例 MUST 变红（**负向验证**，手工做一次并把输出贴进交付记录）。

🔴 第 2 条的写法是这条断言有没有牙齿的分水岭。`Task-12` 的 L3-5 也 MUST 按同样口径起 `json_response=false` 实例（**该文件不归本任务改，已写进交付报告**）。

### 7 验收命令与期望输出

```bash
$ go test ./internal/transport/ -race -v -run 'TestProtocol|TestHealthz|TestAuth|TestHostGuard|TestMiddlewareOrder'
--- PASS: TestProtocol (0.05s)
    --- PASS: TestProtocol/P1_discover版本
    --- PASS: TestProtocol/P5_缺Mcp_Method
    ...
$ echo "共 26 个断言（P1~P26 全覆盖），全部 PASS"
```

> 🔴 **计数修正**：原文写「共 22 个断言」，而 §6 的表在补回 P17/P18 后是 **26 条**（P1~P26 连续无缺号）。即使不算 P17/P18，原表也已经是 **24** 条（P1~P16 的 16 条 + P19~P26 的 8 条），「22」这个数**从来就不对** —— 它既不是当前表也不是补 P17/P18 前的表。验收 MUST 用动态判据：`grep -cE '^\\| P[0-9]+ ' <本文件>` = 26，MUST NOT 手抄数字。🔴 原判据 `grep -c '^| P[0-9]'` 会把 `P2.1` 也算进去得到 **27**（`P2.1` 是 P2 的子断言，不单独占编号），是判据自身的 bug，已修正。

### 8 完成定义（DoD）

- [ ] **P1~P26 全绿（26 条，无缺号）**。🔴 原 DoD 写「P1~P16 + P19~P26 全绿（P17/P18 在 Task-12 的集成测试里）」，那正是 P17/P18 从验收总表里消失的原因；现在两条都在本原子任务落地，`Task-12` A-02 的 L3-5 只是「用真实上游再跑一遍」
- [ ] P18 是**以 `json_response=false` 的实例**测的，且断言的是**首包时间**而不是总耗时（§6.1 第 2 条）
- [ ] P18 做过一次**负向验证**：把 `statusWriter.Flush()` 改成空实现，用例变红（贴输出）
- [ ] `TestMiddlewareOrder` 绿，且 `middlewareNames` 与 §1 的链**逐字相同**
- [ ] P15 的第 ② 个子断言（`OPTIONS` preflight 不带 `Authorization` 时不 401）绿
- [ ] `grep -n 'Stateless' internal/transport/*.go` 只有一处且硬编码 `true`
- [ ] 提交：`feat(transport): 无状态 Streamable HTTP 与中间件链`

---

## 原子任务 A-03 · 架构守护测试与脚本
### 0 前置

- A-02 已完成并提交
- 已读本任务「任务总览」里「必读」列出的文档章节

### 1 上下文（自包含）

「高内聚低耦合」如果只是文档里的一句话，AI 每次实现的解读都不一样。本任务把 `Engineering/22-架构守护规则` 的六条规则变成**跑得起来的测试**：违规就红。

### 2 要创建的文件

| 文件 | 动作 | 内容 |
|---|---|---|
| `GitHubSrc/internal/arch_test.go` | 新建 | AR-1/AR-2/AR-3/AR-4(F-S2/F-S4)/AR-5/AR-6 全部守护测试（**一个文件**，理由见 §3 第 1 条） |
| `GitHubSrc/Scripts/CheckSize.sh` | 新建 | F-S1/F-S3/F-S5（逐字用 `Engineering/22-架构守护规则` §3.1） |
| `GitHubSrc/Scripts/CheckDocDrift.sh` | 新建 | 文档漂移 **8 项**（逐字用 22 号 §五 的口径；`/mcp/DeepSeekSearch/Scripts/CheckDocDrift.sh` 是它的参考实现） |
| `GitHubSrc/Scripts/SyncDocs.sh` | 新建 | 🔴 `Doc/` → `docs/` 单向同步。**此前无任何 Task 交付它**，裁决归本任务（见 §3 第 12~15 条） |
| `Doc/Engineering/豁免台账.md` | **已存在，只核对** | F-S6。核对 ` ```exempt ` 块是五段格式（见 §3 第 11 条），MUST NOT 为了过检查往里加行 |

> 🔴 **`CheckRepoFiles.sh` 不在本任务**：它由 `Task-14` A-02 交付。22 号 §5.2 说的「与 `CheckSize.sh`、`CheckRepoFiles.sh` 同批」指的是「同一批脚本类交付物」，不是同一个 Task。

### 3 实现要求

| # | 要求 |
|:-:|---|---|
| 1 | 全部守护测试放在**一个** `internal/arch_test.go`（`package internal_test`，位于 `internal/` 根）。理由：22 号 §2.3 给的 import 清单里含 `os/exec`（注释写明「TestSizeLimits 要跑 CheckSize.sh」），说明 `TestSizeLimits` 与依赖守护同文件；而 22 号 §3.2 把它标为 `size_test.go` —— 两处冲突时**以 §2.3 的 import 清单为准**，因为 import 清单直接决定能不能编译（Go 的未使用 import 是编译错误）。`go/ast` 因此也在同一文件 |
| 2 | 🔴 **文件头顺序 MUST 是**：文件头注释块（五字段，见 `Contract/19-AGENTS.md全文` H11）→ 空行 → `//go:build !integration` → 空行 → `package internal_test`。Go 工具链要求 build 约束前只能有空行与行注释。**代价**：`package` 前有空行 ⇒ 该文件没有 package doc comment，对测试文件无影响（22 号 §2.3 的 E28 裁决，三处 MUST 一致） |
| 3 | `arch_test.go` 用 `go/parser` 的 `ImportsOnly` 模式解析依赖图，MUST NOT 用 `go list`（要能在无网络、无模块缓存的环境跑） |
| 4 | 🔴 **两组白名单都要查**：项目内 import 查 `allowedImports`，标准库 + 第三方 import 查 `allowedExternal`。只查项目内的后果见 22 号 §2.3 的表（`deepseek ↛ mcp`、`domain ↛ net/http`、`resilience ↛ net/http`、`tools ↛ net/http` 四条不变式**一条都检不出**） |
| 5 | `allowedExternal` 的匹配规则：以 `/` 结尾的项按**前缀**匹配（`github.com/modelcontextprotocol/go-sdk/` 放行其全部子包），其余按**全等**匹配 |
| 6 | 白名单里**没有的包**（即 `internal/` 下新增了一个包但没登记）MUST 报错，MUST NOT 静默放过 |
| 7 | 依赖环检测用 DFS，**只走项目内那一组**（外部依赖不构成我们的架构环），报错时输出完整环路径 |
| 8 | `domain` 包的项目内 import 数 MUST 为 0 |
| 9 | `TestToolNames` 写死 `want = ["ds_fetch","ds_health","ds_search"]`；`TestToolAnnotations` 断言三个工具的 `ReadOnlyHint==true` 与 `IdempotentHint==true`（都是 `bool`），以及 `OpenWorldHint != nil && *OpenWorldHint==true`（`*bool`）—— go-sdk v1.8.0 的类型口径见 `15-协议契约` §七之二 |
| 10 | `CheckSize.sh` 实现 **F-S1/F-S3/F-S5**；F-S3 用 `gocyclo`（未安装时打印警告，判据是**输出非空**而不是退出码 —— 22 号 §3.1 的 🔴 注释）。F-S5 的导出符号数 MUST 用 `Scripts/exportcount.go` 按 `go/ast` 精确统计（顶层声明 + 方法 + const/var/type 块成员，**MUST NOT 把 struct 字段算进去**）；文件内联的 awk 近似会把 `config` 的 33 个导出数吹到 117 |
| 11 | `CheckSize.sh` 从 `豁免台账.md` 的 ` ```exempt ` 块读豁免，格式是**五段**：`<文件相对路径> <红线代号> <实测值> "<理由>" <复核日期 YYYY-MM-DD>`（空格分隔）。红线代号为 `—` 的**非规模类**条目（台账现有第 2 条 `domain/time.go` 的 G6 例外）MUST 跳过而不是解析失败。MUST 打印已加载的豁免条数。规模存量违规走 `size-baseline.json`（ADR-017），不占豁免额度 |
| 12 | 🔴 **F-S2（函数 ≤50 行）与 F-S4（嵌套 ≤3 层）由 `TestFunctionLength` / `TestNestingDepth` 用 `go/ast` 实现**（22 号 §三 的分工表）。两者 MUST 读 `Scripts/size-baseline.json`：基线存在的 key 按基线值放行（当前值 ≤ 基线），基线不存在或当前值 > 基线 ⇒ 红 |
| 13 | F-S2 口径：数函数体 `{` 与 `}` 之间的**非注释非空**行数；整行注释（`//` 开头）与完全落在块注释区间内的行不计；**行尾注释所在行算代码行**（它确实占了一行代码） |
| 14 | F-S4 口径：`if` / `for` / `range` / `switch` / `type switch` / `select` / `func` 字面量各 +1 层，函数体本身是第 0 层（与 20 号 §三「卫语句提前 return」的意图一致） |
| 15 | 🔴 `TestSizeLimits` 调 `Scripts/CheckSize.sh`，参数 MUST 是 `("..", "../../Doc")` —— `go test` 的工作目录是**包目录** `GitHubSrc/internal/`，所以代码根是 `..`、文档根是 `../../Doc`（部署根的 `Doc/`）。写成 `"../Doc"` 会解析成 `GitHubSrc/Doc`（不存在）⇒ 豁免台账加载不到、domain 的 F-S5 直接 FAIL。CI 语境没有 `Doc/` 时，脚本回退到 `<代码根>/docs/Engineering/豁免台账.md` |
| 16 | 🔴 `TestSizeLimits` 在 `os.Stat` 找不到脚本时：**CI 环境（`os.Getenv("CI") != ""`）MUST `t.Fatalf`**，只有本地才允许 `t.Skip`。静默 Skip 会让架构测试在脚本缺失时变绿 —— 正是 22 号 §六 明令禁止的捷径 |
| 17 | F-S2/F-S4/F-S1/F-S5 的作用域都 MUST **排除 `_test.go`**（与 `CheckSize.sh` 的 `! -name '*_test.go'` 同口径）。否则本文件自己的 `detectCycle` 会被 F-S4 判违规，逼实现者去改测试 —— 那就是拆护栏 |
| 18 | 🔴 **`SyncDocs.sh` 的交付要求**（此前 17 号 §七 第 18 项、`19-AGENTS.md全文` X11、`91-验收总表` 12-A03-4、`90-Qoder执行手册` 都引用它，却无处交付）：签名 `SyncDocs.sh [--check] [<文档根> [<代码根>]]`，默认 `../Doc` 与 `.`；`--check` 无 diff 退出 0、有 diff 退出 **1** 并打印差异清单；同步模式先清空 `<代码根>/docs/` 再整目录复制 |
| 19 | `SyncDocs.sh` 在**文档根不存在**（CI 语境：仓库里只有 `docs/`，没有 `Doc/`）时 MUST 打印明确的 `[SKIP]` 原因并退化为「验证 `docs/` 存在且非空」—— 这一条能抓住「忘了提交 `docs/`」，是真检查而不是空转。MUST NOT 直接退出 0 什么都不做 |
| 20 | `CheckDocDrift.sh` 做 **8 项**比对，逐项与 22 号 §5.1 的表一一对应（`[N/8]` 的 `8` 以脚本 `say` 的分母为准）。调用签名固定两参：`CheckDocDrift.sh <文档根> <代码根>` |
| 21 | 全部脚本 `set -euo pipefail`（`CheckSize.sh` / `CheckRepoFiles.sh` 例外用 `set -uo pipefail`，因为要收集全部失败 —— 见 22 号 §3.1）+ 可执行位 `0755` |
| 22 | 🔴 **存量规模基线（ADR-017）**：首次执行 F-S1~F-S5 时实测存量有 29 处超限（F-S2 5 / F-S3 20 / F-S5 4 包；F-S4 的 1 处当场修复）。这些 MUST 写入 `GitHubSrc/Scripts/size-baseline.json`，判定 = 当前值 > 基线值 ⇒ FAIL，当前值 ≤ 基线值 ⇒ PASS；基线里不存在的新函数/新文件/新包按原阈值判定，超标即 FAIL。MUST NOT 往基线里新增条目或调大数值 |
| 23 | 基线条目 MUST 含 `phase` / `reason` / `remediation` 三个字段，`reason` 非空；F-S5 以包为 key 记录 `files` / `lines` / `exports` 与对应 `limits`。基线 schema 与分工见 `Engineering/22-架构守护规则` §3.4 |
| 24 | 必须补三条**变异测试**（手工执行并贴输出）：① 基线任一项 +1 ⇒ 对应守护变红；② 删除基线中某项但代码仍超标 ⇒ 变红；③ 新增一个复杂度 11 的函数且不进基线 ⇒ 变红 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | 🔴 MUST NOT 用 `t.Skip` 绕过违规（唯一允许的 Skip 见 §3 第 16 条：本地且脚本尚不存在） |
| 2 | MUST NOT 在脚本里用 `ln -s` 软链接（项目铁律：不用软硬链接） |
| 3 | MUST NOT 让 `SyncDocs.sh` 反向同步（`docs/` → `Doc/`），也 MUST NOT 让它双向合并 |
| 4 | MUST NOT 在 `CheckSize.sh` 里静默跳过豁免（必须打印豁免数量） |
| 5 | MUST NOT 把 `arch_test.go` 放进任何业务包（放 `internal/` 根，package `internal_test`） |
| 6 | 🔴 MUST NOT 只查项目内 import 就宣称「白名单已强制」—— 那样 `deepseek import mcp` 这种最核心的禁令检不出（§3 第 4 条） |
| 7 | MUST NOT 在本文件里单独增删白名单条目。改白名单 MUST 走 22 号 §六 的流程（ADR → 10 号 → 22 号 → 本文件），三处不一致 `CheckDocDrift.sh` 会红 |
| 8 | MUST NOT 为了让 F-S2/F-S4 通过而把 `_test.go` 纳入或排除的口径改来改去（口径见 §3 第 17 条） |
| 9 | 🔴 `arch_test.go` 里的工具名比较用 `strings.Join` 而不是 `cmp.Diff`。**理由不是「go-cmp 是第 5 个依赖」**—— 它是**仅测试**依赖，在 `_test.go` 里用完全合法（`10-包依赖矩阵` §2.9.1：运行时 4 个 + 仅测试 1 个，AR-1 的第 1 步就跳过 `_test.go`，根本管不到它）。真正的理由是：want 只有 3 个短字符串，`cmp.Diff` 的结构化 diff 带不来任何信息，却会让人误以为「守护依赖数的文件自己依赖 go-cmp」。<br>⚠️ **MUST NOT 把这条推广成「全项目禁用 go-cmp」** —— `21-测试规范` §二 与 `19-AGENTS.md全文` T8 都允许在测试里用它做深比较（配置结构体、错误 payload 这类），手写 `reflect.DeepEqual` + 自拼 diff 会丢掉它的全部价值。禁止的是 **testify**，不是 go-cmp |

### 5 参考骨架（🔴 完整可运行实现，MUST 按此落地，语义不得偏离）

> **文件头顺序**：注释块 → 空行 → `//go:build !integration` → 空行 → `package internal_test`（§3 第 2 条）。
> **白名单两张表**是 22 号 §2.1 的**第三份副本**（`CheckDocDrift.sh` 只比对 10 号 ↔ 22 号两处）。改白名单 MUST 三处同改，见 §4 第 7 条。

```go
// 文件名: arch_test.go
// 用途: 包依赖白名单（项目内 + 外部）、依赖环、规模红线 F-S2/F-S4 的静态守护。
//       MUST NOT 含任何业务逻辑。
// 创建日期: 2026-11-XX
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

//go:build !integration

package internal_test

import (
    "go/ast"
    "go/parser"
    "go/token"
    "os"
    "os/exec" // TestSizeLimits 要跑 Scripts/CheckSize.sh
    "path/filepath"
    "sort"
    "strings"
    "testing"
)

// modulePath 与 go.mod 的 module 行一致。
const modulePath = "github.com/LseKit/DeepSeekSearch"

// allowedImports 是包级的**项目内** import 白名单（逐字复制 22 号 §2.1）。
var allowedImports = map[string][]string{
    "internal/domain":     {}, // 零依赖
    "internal/config":     {"internal/domain"},
    "internal/logging":    {"internal/domain", "internal/config"},
    "internal/deepseek":   {"internal/domain", "internal/config"},
    "internal/resilience": {"internal/domain", "internal/config"},
    "internal/tools":      {"internal/domain", "internal/config", "internal/deepseek", "internal/resilience"},
    "internal/transport":  {"internal/domain", "internal/config", "internal/logging"},
    "cmd/deepseeksearch":  {"internal/domain", "internal/config", "internal/logging", "internal/deepseek", "internal/resilience", "internal/tools", "internal/transport"},
}

// allowedExternal 是包级的**标准库 + 第三方**白名单（逐字复制 22 号 §2.1）。
// 以 "/" 结尾的项按前缀匹配，其余按全等匹配（见 matchExternal）。
var allowedExternal = map[string][]string{
    "internal/domain": {
        "errors", "fmt", "strings", "strconv", "net/url", "net/netip", "time", "sort",
        "regexp", "unicode", "unicode/utf8", "hash/fnv", "math/rand/v2", "crypto/rand",
        "context", "encoding/json",
    },
    "internal/config": {
        "os", "fmt", "errors", "strconv", "strings", "regexp", "time", "path/filepath", "runtime",
        "github.com/BurntSushi/toml", "github.com/joho/godotenv",
    },
    "internal/logging": {
        "io", "os", "fmt", "errors", "sync", "sync/atomic", "time", "log/slog", "path/filepath",
        "compress/gzip", "sort", "strconv", "strings", "regexp", "crypto/sha256", "encoding/hex",
        "context",
    },
    "internal/deepseek": {
        "net/http", "encoding/json", "context", "time", "io", "bytes", "fmt", "errors",
        "net/url", "net", "regexp", "strings", "unicode/utf8", "crypto/tls",
    },
    "internal/resilience": {
        // 没有 golang.org/x/time/rate —— rate.Limiter 无法注入 FakeClock，令牌桶自研
        "context", "time", "sync", "sync/atomic", "math", "math/rand/v2", "container/list",
        "errors", "fmt", "hash/fnv", "database/sql", "os", "path/filepath", "strings", "strconv",
        "modernc.org/sqlite",
    },
    "internal/tools": {
        "context", "fmt", "errors", "strings", "sort", "time", "net/url", "net", "net/netip",
        "regexp", "log/slog", "sync", "sync/atomic", "crypto/rand", "crypto/sha256",
        "encoding/hex", "encoding/json", "unicode/utf8", "strconv",
        "github.com/modelcontextprotocol/go-sdk/mcp",
    },
    "internal/transport": {
        "context", "net/http", "os", "fmt", "errors", "time", "log/slog", "crypto/subtle",
        "encoding/json", "net", "strings",
        "github.com/modelcontextprotocol/go-sdk/mcp",
    },
    "cmd/deepseeksearch": {
        "context", "os", "os/signal", "syscall", "fmt", "flag", "log/slog", "time",
        "time/tzdata", // 副作用导入：Windows 无系统 tzdata，缺它则启动即 exit 1
        "runtime", "io", "errors", "strings",
        "github.com/modelcontextprotocol/go-sdk/mcp",
    },
}

// contains 判定白名单里有没有这一项。不用 slices.Contains 是为了少一个 import。
func contains(list []string, want string) bool {
    for _, v := range list {
        if v == want {
            return true
        }
    }
    return false
}

// collectPkgs 遍历 internal/ 与 cmd/ 下的非测试 .go 文件，
// 返回「包路径 → 该包全部 import 路径」的依赖图。
// 用 go/parser 的 ImportsOnly 模式：不建类型信息、不需要模块缓存与网络，
// 所以它能当 CI 的第一个步骤跑。
func collectPkgs(t *testing.T, root string) map[string][]string {
    t.Helper()
    graph := make(map[string][]string)
    for _, base := range []string{"internal", "cmd"} {
        dir := filepath.Join(root, base)
        if _, err := os.Stat(dir); err != nil {
            t.Fatalf("%s 不存在 —— root 参数错了吗？go test 的工作目录是包目录", dir)
        }
        err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
            if err != nil {
                return err
            }
            if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
                return nil
            }
            fset := token.NewFileSet()
            f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
            if perr != nil {
                return perr
            }
            rel, rerr := filepath.Rel(root, filepath.Dir(path))
            if rerr != nil {
                return rerr
            }
            pkg := filepath.ToSlash(rel)
            if _, seen := graph[pkg]; !seen {
                graph[pkg] = nil // 保证「零 import 的包」也进图，否则它逃过白名单登记检查
            }
            for _, imp := range f.Imports {
                graph[pkg] = append(graph[pkg], strings.Trim(imp.Path.Value, "\""))
            }
            return nil
        })
        if err != nil {
            t.Fatalf("遍历 %s 失败: %v", dir, err)
        }
    }
    return graph
}

// matchExternal 判定一个外部 import（标准库或第三方）是否被放行。
// 以 "/" 结尾的白名单项按前缀匹配（放行整个模块的子包），其余按全等匹配。
func matchExternal(allow []string, imp string) bool {
    for _, a := range allow {
        if strings.HasSuffix(a, "/") {
            if strings.HasPrefix(imp, a) {
                return true
            }
            continue
        }
        if imp == a {
            return true
        }
    }
    return false
}

// detectCycle 在**项目内**依赖图上做 DFS 找环，返回第一条环路径（闭合形式）。
// 只走项目内那一组：外部依赖（标准库/第三方）不构成我们的架构环，
// 而且它们的图我们也没有解析。
func detectCycle(graph map[string][]string) []string {
    const (
        white = 0 // 未访问
        gray  = 1 // 在当前 DFS 栈上
        black = 2 // 已完成
    )
    color := make(map[string]int, len(graph))
    stack := make([]string, 0, len(graph))
    var cycle []string

    var visit func(n string) bool
    visit = func(n string) bool {
        color[n] = gray
        stack = append(stack, n)
        for _, imp := range graph[n] {
            if !strings.HasPrefix(imp, modulePath+"/") {
                continue // 外部 import：不进环检测
            }
            next := strings.TrimPrefix(imp, modulePath+"/")
            switch color[next] {
            case gray:
                for i, s := range stack {
                    if s == next {
                        cycle = append(append([]string{}, stack[i:]...), next)
                        break
                    }
                }
                return true
            case white:
                if visit(next) {
                    return true
                }
            }
        }
        stack = stack[:len(stack)-1]
        color[n] = black
        return false
    }

    keys := make([]string, 0, len(graph))
    for k := range graph {
        keys = append(keys, k)
    }
    sort.Strings(keys) // 固定遍历顺序，保证报错可复现
    for _, k := range keys {
        if color[k] == white && visit(k) {
            return cycle
        }
    }
    return nil
}

// TestPackageDependencies 校验每个包的 import 都在白名单内（**两组都查**），
// 且项目内依赖图无环。
func TestPackageDependencies(t *testing.T) {
    graph := collectPkgs(t, "..") // go test 的工作目录是 GitHubSrc/internal/
    if len(graph) == 0 {
        t.Fatal("没有解析到任何包 —— 检查 root 参数，MUST NOT 让本测试空转变绿")
    }

    pkgs := make([]string, 0, len(graph))
    for k := range graph {
        pkgs = append(pkgs, k)
    }
    sort.Strings(pkgs)

    for _, pkg := range pkgs {
        allowIn, okIn := allowedImports[pkg]
        allowExt, okExt := allowedExternal[pkg]
        if !okIn || !okExt {
            t.Errorf("包 %s 未登记进白名单（新增包 MUST 先改 10 号 §二 与 22 号 §2.1，再改这里）", pkg)
            continue
        }
        internal := 0
        for _, imp := range graph[pkg] {
            if strings.HasPrefix(imp, modulePath+"/") {
                internal++
                target := strings.TrimPrefix(imp, modulePath+"/")
                if !contains(allowIn, target) {
                    t.Errorf("包 %s 违规 import 项目内包 %s（白名单：%v）", pkg, target, allowIn)
                }
                continue
            }
            if !matchExternal(allowExt, imp) {
                t.Errorf("包 %s 违规 import 外部包 %s（白名单：%v）", pkg, imp, allowExt)
            }
        }
        if pkg == "internal/domain" && internal != 0 {
            t.Errorf("domain 必须零内部依赖，实际有 %d 个", internal)
        }
    }

    if c := detectCycle(graph); c != nil {
        t.Errorf("依赖环：%s", strings.Join(c, " -> "))
    }
}
```

#### 5.1 规模守护（同文件的另外三个测试）

```go
// prodFile 是一个生产（非测试）.go 文件的解析结果。
type prodFile struct {
    path string
    fset *token.FileSet
    file *ast.File
    src  []string // 按行切开的源文件，用于 F-S2 的「非注释非空行」计数
}

// parseProdFiles 解析 internal/ 与 cmd/ 下全部非 _test.go 文件。
// 🔴 排除 _test.go 与 CheckSize.sh 的 find 口径一致（§3 第 17 条）。
func parseProdFiles(t *testing.T, root string) []prodFile {
    t.Helper()
    var out []prodFile
    for _, base := range []string{"internal", "cmd"} {
        dir := filepath.Join(root, base)
        if _, err := os.Stat(dir); err != nil {
            t.Fatalf("%s 不存在 —— root 参数错了吗？", dir)
        }
        err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
            if err != nil {
                return err
            }
            if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
                return nil
            }
            fset := token.NewFileSet()
            f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
            if perr != nil {
                return perr
            }
            raw, rerr := os.ReadFile(path)
            if rerr != nil {
                return rerr
            }
            out = append(out, prodFile{
                path: filepath.ToSlash(path),
                fset: fset,
                file: f,
                src:  strings.Split(string(raw), "\n"),
            })
            return nil
        })
        if err != nil {
            t.Fatalf("解析 %s 失败: %v", dir, err)
        }
    }
    sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
    return out
}

// blockCommentLines 返回完全落在 /* */ 块注释内的行号集合。
func blockCommentLines(pf prodFile) map[int]bool {
    lines := make(map[int]bool)
    for _, cg := range pf.file.Comments {
        for _, c := range cg.List {
            if !strings.HasPrefix(c.Text, "/*") {
                continue
            }
            tf := pf.fset.File(c.Pos())
            for ln := tf.Line(c.Pos()); ln <= tf.Line(c.End()); ln++ {
                lines[ln] = true
            }
        }
    }
    return lines
}

// TestFunctionLength 强制 F-S2：单函数 ≤50 行（不含注释与空行）。
// 口径见 §3 第 13 条；20 号 §三 是同一条规则的文档表述。
func TestFunctionLength(t *testing.T) {
    const limit = 50
    files := parseProdFiles(t, "..")
    if len(files) == 0 {
        t.Fatal("没有解析到任何生产文件")
    }
    for _, pf := range files {
        blocks := blockCommentLines(pf)
        ast.Inspect(pf.file, func(n ast.Node) bool {
            fd, ok := n.(*ast.FuncDecl)
            if !ok || fd.Body == nil {
                return true
            }
            tf := pf.fset.File(fd.Body.Lbrace)
            lo, hi := tf.Line(fd.Body.Lbrace)+1, tf.Line(fd.Body.Rbrace)-1
            count := 0
            for ln := lo; ln <= hi && ln <= len(pf.src); ln++ {
                s := strings.TrimSpace(pf.src[ln-1])
                if s == "" || strings.HasPrefix(s, "//") || blocks[ln] {
                    continue
                }
                count++
            }
            if count > limit {
                t.Errorf("F-S2 违规: %s 的 %s 有 %d 行非注释代码（上限 %d）",
                    pf.path, fd.Name.Name, count, limit)
            }
            return true
        })
    }
}

// nestingOf 递归求最大嵌套深度。
// 口径（§3 第 14 条）：if / for / range / switch / type switch / select / func 字面量各 +1，
// 函数体本身是第 0 层。
func nestingOf(n ast.Node, depth int) int {
    best := depth
    ast.Inspect(n, func(c ast.Node) bool {
        if c == nil || c == n {
            return true // 只在**直接子节点**上递归，避免重复计数
        }
        d := depth
        switch c.(type) {
        case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
            *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncLit:
            d++
        }
        if v := nestingOf(c, d); v > best {
            best = v
        }
        return false // 已由 nestingOf 递归下去，这里不再 descend
    })
    return best
}

// TestNestingDepth 强制 F-S4：嵌套 ≤3 层。
func TestNestingDepth(t *testing.T) {
    const limit = 3
    for _, pf := range parseProdFiles(t, "..") {
        ast.Inspect(pf.file, func(n ast.Node) bool {
            fd, ok := n.(*ast.FuncDecl)
            if !ok || fd.Body == nil {
                return true
            }
            if d := nestingOf(fd.Body, 0); d > limit {
                t.Errorf("F-S4 违规: %s 的 %s 嵌套 %d 层（上限 %d）",
                    pf.path, fd.Name.Name, d, limit)
            }
            return false
        })
    }
}

// TestSizeLimits 把 CheckSize.sh（F-S1/F-S3/F-S5）接进 go test。
//
// 🔴 路径口径：go test 的工作目录是**包目录**（GitHubSrc/internal/），
//   所以脚本在 ../Scripts/、代码根是 ..（即 GitHubSrc 本身）、文档根是 ../../Doc（部署根的 Doc/）。
// 🔴 MUST NOT 用 t.Skip 兜底：CI 里脚本缺失 = 架构测试静默变绿，
//   正是 22 号 §六 明令禁止的捷径。
func TestSizeLimits(t *testing.T) {
    const script = "../Scripts/CheckSize.sh"
    if _, err := os.Stat(script); err != nil {
        if os.Getenv("CI") != "" {
            t.Fatalf("CI 环境中 %s 不存在，规模红线无法验证: %v", script, err)
        }
        t.Skipf("本地未找到 %s（Task-11 之后才存在）；改用 20-编码规范 §3.1 的自查命令", script)
        return
    }
    out, err := exec.Command("bash", script, "..", "../../Doc").CombinedOutput()
    if err != nil {
        t.Fatalf("规模红线违规:\n%s", out)
    }
    t.Logf("CheckSize.sh 输出:\n%s", out)
}
```

#### 5.2 `Scripts/SyncDocs.sh`（🔴 本任务交付，此前无处落地）

```bash
#!/usr/bin/env bash
# 文件名: SyncDocs.sh
# 用途: Doc/ → GitHubSrc/docs/ 单向同步。--check 只比对不写。
#       docs/ 是产物，MUST NOT 手写（19-AGENTS.md全文 X11）。
# 创建日期: 2026-11-XX
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
# 用法: SyncDocs.sh [--check] [<文档根> [<代码根>]]
#   开发机（在 GitHubSrc/ 下）: bash Scripts/SyncDocs.sh --check ../Doc .
#   CI（仓库根，没有 Doc/）  : bash Scripts/SyncDocs.sh --check ../Doc .
set -euo pipefail

MODE="sync"
if [ "${1:-}" = "--check" ]; then MODE="check"; shift; fi
DOC_ROOT="${1:-../Doc}"
CODE_ROOT="${2:-.}"
DEST="$CODE_ROOT/docs"

if [ ! -d "$DOC_ROOT" ]; then
  # 🔴 CI 语境：仓库里只有同步产物 docs/，没有 Doc/。
  #    MUST NOT 假装比对通过；退化为「docs/ 存在且非空」——
  #    这一条能抓住「忘了把 docs/ 提交上来」，是真检查。
  #    真正的同步比对由 Release.sh 第 18 项在发版机执行（17-版本与发布契约 §七）。
  echo "[SKIP] 文档根 $DOC_ROOT 不存在（CI 语境只有 docs/）"
  [ -d "$DEST" ] || { echo "[FAIL] $DEST 不存在 —— docs/ 忘了提交"; exit 1; }
  n=$(find "$DEST" -name '*.md' | wc -l)
  [ "$n" -gt 0 ] || { echo "[FAIL] $DEST 是空的"; exit 1; }
  echo "[OK] $DEST 存在且含 $n 份 .md"
  exit 0
fi

if [ "$MODE" = "check" ]; then
  if diff -r -q "$DOC_ROOT" "$DEST" >/dev/null 2>&1; then
    echo "[OK] docs/ 与 Doc/ 无差异"
    exit 0
  fi
  echo "[FAIL] docs/ 与 Doc/ 有差异（MUST 跑 bash Scripts/SyncDocs.sh 后重新提交）："
  diff -r -q "$DOC_ROOT" "$DEST" || true
  exit 1
fi

# 同步模式：先清空再整目录复制。MUST NOT 用软链接（项目铁律），MUST NOT 反向同步。
mkdir -p "$DEST"
find "$DEST" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
cp -R "$DOC_ROOT"/. "$DEST"/
echo "[OK] 已同步 $(find "$DEST" -name '*.md' | wc -l) 份文档到 $DEST"
```

### 6 测试代码（故意违规验证守卫有效）

| 用例 | 期望 |
|---|---|
| 临时在 `domain` 里 import `config` | `TestPackageDependencies` 红，报错含包名 |
| 临时在 `deepseek` 里 import `github.com/modelcontextprotocol/go-sdk/mcp` | 🔴 `TestPackageDependencies` 红（**这条只有 `allowedExternal` 那一组能抓到**，用来验证 §3 第 4 条不是空话） |
| 临时在 `domain` 里 import `net/http` | 同上，红 |
| 临时让 `config` import `tools` | 报依赖环，且环路径完整 |
| 临时加一个 `search`（无前缀）工具 | `TestToolNames` 红 |
| 临时去掉某个 annotation | `TestToolAnnotations` 红 |
| 临时把某文件写到 601 行 | `CheckSize.sh` 退出 1（除非豁免台账已登记） |
| 临时写一个 51 行非注释代码的函数 | `TestFunctionLength` 红（F-S2 有牙齿） |
| 临时写一个 4 层嵌套的 `if` | `TestNestingDepth` 红（F-S4 有牙齿） |
| 临时把 `Scripts/CheckSize.sh` 改名 + `CI=1` | `TestSizeLimits` **Fatalf**（不是 Skip） |
| 文档加一个错误码但代码没加 | `CheckDocDrift.sh` 退出 1 |
| 改 `Doc/` 里一个字但不跑同步 | `SyncDocs.sh --check` 退出 1 并打印差异 |
| 🔴 基线任一项 `value`/`exports`/`files`/`lines` +1（代码不变） | `CheckSize.sh` / `TestFunctionLength` 变红（ADR-017 只减不增） |
| 🔴 删除基线中 `pruneByAge` 条目（代码仍 68 行） | `TestFunctionLength` 变红 |
| 🔴 新增一个复杂度 11 的函数且不进基线 | `CheckSize.sh` 的 F-S3 变红 |

> 以上是**手工验证步骤**，不进测试套件（否则就是「测试测试」）。执行完 MUST 恢复原状并 `git status` 干净。

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc

# go test 的工作目录是包目录，所以 root 参数是 ..
$ go test ./internal/ -v -run 'TestPackageDependencies|TestFunctionLength|TestNestingDepth|TestSizeLimits|TestToolNames|TestToolAnnotations'
--- PASS: TestPackageDependencies (0.02s)
--- PASS: TestFunctionLength (0.05s)
--- PASS: TestNestingDepth (0.04s)
--- PASS: TestSizeLimits (0.31s)
--- PASS: TestToolNames (0.00s)
--- PASS: TestToolAnnotations (0.00s)

# 🔴 两个脚本都是**两参**签名，别传反（22 号 §五）
#   CheckSize.sh     <代码根> <文档根>   —— 开发机在 GitHubSrc/ 下执行
$ bash Scripts/CheckSize.sh . ../Doc && echo SIZE_OK
已加载规模豁免 1 条
✅ 规模红线全部通过（F-S1/F-S3/F-S5；F-S2/F-S4 见 TestSizeLimits）
SIZE_OK

#   CheckDocDrift.sh <文档根> <代码根>
$ bash Scripts/CheckDocDrift.sh ../Doc . && echo DRIFT_OK
DRIFT_OK

$ bash Scripts/SyncDocs.sh --check ../Doc . && echo SYNC_OK
[OK] docs/ 与 Doc/ 无差异
SYNC_OK
```

> 🔴 **签名核对（E38 裁决）**：`CheckDocDrift.sh <文档根> <代码根>`，**两个参数**。
> 三种语境 MUST 分别是：开发机（`GitHubSrc/` 下）`../Doc .`；CI / `GitHubSrc` 语境 `docs .`；部署根 `Doc GitHubSrc`。
> 本任务此前写成单参 `.`，而 `Task-14` 的 `ci.yml` 写 `docs .`、`Makefile` 写 `../Doc .` —— 三处不一致已按 22 号 §五 对齐（`ci.yml` 与 `Makefile` 两处本来就是对的，改的是本文件）。
> **传反了不会报错**，只会得到一堆 `ACTUAL=0` 的误导性 FAIL，且脚本对空输入的部分判据会判 OK ⇒ 假绿。

> 🔴 `CheckSize.sh` 同样是两参（`<代码根> <文档根>`）。只传一个参数时文档根退化成默认的 `Doc`，而 CI 里那个目录不存在 ⇒ 豁免台账加载不到 ⇒ `domain/errors.go` 的 720 行会直接触发 F-S1 FAIL。**CI 里 MUST 写 `bash Scripts/CheckSize.sh . docs`**（见 `Task-14` §5.7 的 `size` job）。

### 8 完成定义（DoD）

- [ ] 3 个脚本（`CheckSize.sh` / `CheckDocDrift.sh` / `SyncDocs.sh`）全部有可执行位且按 §7 的两参签名退出 0
- [ ] 6 个守护测试（`TestPackageDependencies` / `TestFunctionLength` / `TestNestingDepth` / `TestSizeLimits` / `TestToolNames` / `TestToolAnnotations`）全绿
- [ ] 🔴 `Scripts/size-baseline.json` 已落地：`schema=1`，F-S2/F-S3/F-S5 存量条目齐全，每条含非空 `reason` 与 `remediation`；`CheckSize.sh` 打印已加载基线条数；`TestFunctionLength` / `TestNestingDepth` 读同一份基线
- [ ] 🔴 三条基线变异测试（§6 末尾）各验证一次并贴输出：基线 +1 变红、删除条目后代码仍超标变红、新增复杂度 11 的函数不进基线变红
- [ ] §6 的 15 个「故意违规」场景各验证一次（贴输出），其中**第 2、3 条**（外部 import）与**第 8、9 条**（F-S2/F-S4）MUST 都真的变红
- [ ] `TestSizeLimits` 在 `CI=1` + 脚本缺失时是 `Fatalf` 而不是 `Skip`（贴输出）
- [ ] `arch_test.go` 的文件头顺序符合 §3 第 2 条（`grep -n -m6 '' internal/arch_test.go` 检查前 6 行）
- [ ] `git status` 干净（验证后恢复）
- [ ] 提交：`test: 架构守护测试与文档漂移检查`
