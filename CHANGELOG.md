# 变更日志

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。
版本号规则见 [`docs/Contract/17-版本与发布契约.md`](docs/Contract/17-版本与发布契约.md)。

分类固定四类：`新增` / `变更` / `修复` / `移除`。发版时把 `[未发布]` 段改成 `[vX.Y.Z] - YYYY-MM-DD`。

## [未发布]

## [v0.1.0] - 2026-10-25

首个公开发布版本。包含 2026-10-20 的功能基线，以及 2026-10-24 ~ 10-25 两轮审计（外部 DeepSeek 审计报告 18 项 + 本地 L2/L3 安全审查 + 三路分包审计 + 实测）的全部修复。

### 新增

- `ds_search`：DeepSeek 网页搜索。支持 `site:` / `-site:` / `lang:` / `after:` / `before:` / 精确短语等结构化语法，响应始终带 `query_effective` 与 `warnings`
- `ds_fetch`：网页正文抓取。`content` 超过 8000 字符时返回目录（按 Markdown 标题分节），可用 `cache_id` + `section` 按节取正文
- `ds_health`：本地诊断工具。检查 Key / 上游连通 / 缓存 / 生效配置，**不发起计费请求**
- `ds_research`：组合研究工具。1 次搜索 + 并发抓取前 N 条来源，返回带编号引用的材料
- `ds_extract`：批量抓取工具。1~10 个 URL 并发抓取，单条失败只影响该条
- 传输：`stdio` 与**无状态 `Streamable HTTP`**（MCP 2026-07-28，端点 `/mcp`）
- 健康检查端点 `/healthz`（与 MCP 同端口，免鉴权）
- 缓存：SQLite（WAL）+ 跨进程 singleflight，防止并发重复计费
- 韧性：per-key 令牌桶、抖动重试、重试预算、连续失败熔断、三层预算硬顶
- MCP Resource：`ds://error-codes`（42 个错误码）、`ds://effective-config`（脱敏配置）、`ds://usage`（调用统计）
- 安全：SSRF 私网黑名单（12 个网段 + 3 个域名后缀 + 6 种 IP 混淆写法）、Origin 校验、可选 Bearer 鉴权、常量时间比较
- 版本号：编译期 `-ldflags` 注入，`--version` / `serverInfo.version` / `/healthz` 三处同源
- `ds_search`：`after` 晚于 `before` 时记 warning（此前静默打一次上游拿 0 条，白花一次计费）
- 工具入参严格化：**未知/拼错的参数键一律拒绝**（`1000`，`details` 带库给出的键名与合法键清单）。schema 早就写了 `additionalProperties:false`，但 SDK 与 `encoding/json` 默认都静默忽略未知键
- `ds_fetch`：URL **端口越界**（如 `:99999`）本地拒绝，不再放给上游变成一次计费的 `2400`

### 变更

- `/healthz` **不再回显** `commit` / `build_time` / `go_version` / `upstream.base_url`：该端点按设计免鉴权，那四项合起来是一份精确版本指纹（拿到 `go1.27.x` 就能对上该补丁版本的已知 CVE）。完整构建信息仍走 `ds_health` 与 `--version`
- 所有响应加 `Cache-Control: no-store`（响应体是抓取内容与诊断数据，不该被中间层留存）
- `Authorization` 的 `bearer` scheme 改为**大小写不敏感**（RFC 7235）；此前写 `bearer ` 的合法客户端吃 401
- 上游客户端**不再跟随重定向**（`http.ErrUseLastResponse`）：跟随会把 Bearer 送给跳转目标，还会把一次误配的 302 放大成最多 10 次计费
- 上游连接池放开：`MaxIdleConnsPerHost` 2 → 20、`MaxIdleConns` 100、`IdleConnTimeout` 90s（默认 2 个空闲连接让每批并发抓取都重新做 TLS 握手）
- `panic` 的请求现在**也有访问日志**（`accessLog` 改 `defer` 记录并原样重抛），此前最该留痕的 500 一条都没有
- `ds_research` / `ds_extract` 遇到大文档（`ds_fetch` 只返回 outline）时，降级文本**带上每节 `preview` 摘录**；此前只给 `- [c0]（8000 字）`，调用方给了 `per_source_chars` 预算却拿到零正文
- `ds_research` 的 bundle：空标题兜底为 URL 的 host（此前渲染成 `## [2] `）；预算改为**按整条来源**取舍，不再拦腰截断（截断点会落在 `<untrusted_content>` 中间留下未闭合标签）；超限 warning 不再声称「其余来源见 `sources[]`」（`sources[].content` 根本不参与序列化）
- `dedupe.max_per_domain` 真正成为 `ds_search` 同名参数的默认值（此前是零读取点的死键，硬编码 3）；配置 `0` = 不做域名配额，调用方显式传参仍按 schema 的 1~10 校验
- `log.timezone` 真正驱动日志时间戳（此前 `cmd` 里硬编码 `Asia/Shanghai`，改配置只有 `ds_health` 的显示跟着变）
- `log.max_body_log_kb` 开启时，**响应体先脱敏再截断**（`RedactSecrets`）；日志写入层同时对 `resp_body` 键兜底再脱敏一次。此前上游 401 体里回显的 Key 会原样落盘
- `domain.RedactURL` **无条件剥掉 userinfo**：带 `user:pass@` 的 URL 在参数校验之前就进 `call.begin` 日志
- `domain.Section` 增加 `Start`（本节正文的 rune 偏移），取节改用它；此前消费方累加 `Chars` 推偏移，**纯空白前言不生成节**会让其后每一节整体错位
- 跨进程 singleflight：带错误码的租约行**过期后可被抢占**。此前 `readLeaseState` 不判过期即返回错误，而「标记失败 + 删除」不是原子的 —— 进程被 SIGKILL 就留下一行毒化记录，该 key 对所有进程永久失败、重启也不恢复
- 上游响应体读取成功后，`Body.Close()` 失败不再把成功响应变成错误（否则已计费的内容被丢弃并重试 = 双重计费）
- 构建：`go.mod` 钉 `toolchain go1.27.3`，CI 的 `GO_VERSION` 同步为 `1.27.3`；本机工具链已升到同一版本，Makefile 因此保持 `GOTOOLCHAIN=local`（禁止静默联网拉工具链），发布前用 `-version` 核对 go 段
- 部署：PM2 `kill_timeout` 20s → 30s（优雅关闭最坏链约 25s，20s 会让 SIGKILL 落在租约释放之前）
- `internal/tools` 的用量统计器移入 `internal/health`（`UsageTracker`）：tools 已贴到 F-S5 的 3000 行上限，移入后 tools 2916 行 / 8 导出，health 633 行 / 7 导出，且没有新增依赖方向
- `isStdoutDest` 补 `/dev/fd/1` 与 `/proc/self/fd/1`（同一个 fd 的另两种写法，漏了等于给 stdio 的协议通道留后门）

### 修复

- Go 工具链 1.27.2 → **1.27.3**：修 GO-2026-6809（`crypto/x509` 未吊销被攻陷的中间证书），影响本项目唯一的出站 TLS 链路。`govulncheck` 现为 0 漏洞
- 配置校验的告警文案把因果说反了：`per_attempt_ms < 分层超时之和` 时，实际生效的硬闸门是 `per_attempt_ms`、形同虚设的是靠后的分层值（尤其 `response_header_ms`），原文案正好写反，会把人指向错误的调参方向
- 移除的配置键会让**部署配置拒绝启动**（`3090`）：`Config/config.toml` 已同步（本轮实测踩到，服务一度进不了监听）
- 缓存键的追踪参数剥离在 **query 含 `;` 或非法转义**时会整段跳过（Go 1.17 起 `url.ParseQuery` 直接报错）⇒ `?utm_source=a;b` 与 `?` 落成两条缓存记录，同一页面白花一次计费。现在退化为「只删追踪片段、其余字节原样保留」

### 移除

- `dedupe.merge_reposts`：转载合并是 `domain.Dedupe` 恒定开启的一层，全仓零读取点 —— 设成 `false` 也不生效的假开关，写这个键现在按未知键拒绝启动
- `/healthz` 响应里的 `commit` / `build_time` / `go_version` / `upstream.base_url`（见「变更」）
- `[ratelimit.warn]` 与 `[search]` 两个配置段：前者（`window_s` / `warn_at_calls`）的软告警从未实现，后者的 4 个键也没有任何读取点（工具层的 `min_rerank` / `min_authority` 是**请求参数**，与此无关）。写这些键现在会按未知键拒绝启动
- `config.example.toml` 里所有「某键已移除/已废弃」的历史叙事：模板只列生效键与有效说明

### 已知限制

- 不支持 PDF / XML / SVG / ZIP（上游返回 404）
- 无法区分 403 / 404 / 500（上游统一塌缩为 404）
- 不做结果重排序、不做固定阈值过滤、不做答案合成
- 不做 SSE 传输（2026-07-28 只定义 stdio 与 Streamable HTTP）
- 上游抓取有 30 秒硬超时，超大单页返回 `504`，重试通常也无效
- `ori_question` 实测几乎无效，默认不传
