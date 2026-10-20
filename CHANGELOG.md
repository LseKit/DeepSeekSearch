# 变更日志

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。
版本号规则见 [`docs/Contract/17-版本与发布契约.md`](docs/Contract/17-版本与发布契约.md)。

分类固定四类：`新增` / `变更` / `修复` / `移除`。发版时把 `[未发布]` 段改成 `[vX.Y.Z] - YYYY-MM-DD`。

## [未发布]

## [v0.1.0] - 2026-10-20

首个可发布版本。

### 新增

- `ds_search`：DeepSeek 网页搜索。支持 `site:` / `-site:` / `lang:` / `after:` / `before:` / 精确短语等结构化语法，响应始终带 `query_effective` 与 `warnings`
- `ds_fetch`：网页正文抓取。`content` 超过 8000 字符时返回目录（按 Markdown 标题分节），可用 `cache_id` + `section` 按节取正文
- `ds_health`：本地诊断工具。检查 Key / 上游连通 / 缓存 / 生效配置，**不发起计费请求**
- 传输：`stdio` 与**无状态 `Streamable HTTP`**（MCP 2026-07-28，端点 `/mcp`）
- 健康检查端点 `/healthz`（与 MCP 同端口，免鉴权）
- 缓存：SQLite（WAL）+ 跨进程 singleflight，防止并发重复计费
- 韧性：per-key 令牌桶、抖动重试、重试预算、连续失败熔断、三层预算硬顶
- MCP Resource：`ds://error-codes`（41 个错误码）、`ds://effective-config`（脱敏配置）、`ds://usage`（调用统计）
- 安全：SSRF 私网黑名单（8 网段 + 3 种 IP 混淆）、Origin 校验、可选 Bearer 鉴权、常量时间比较
- 版本号：编译期 `-ldflags` 注入，`--version` / `serverInfo.version` / `/healthz` 三处同源

### 变更

- 无（首版）

### 修复

- 无（首版）

### 移除

- 无（首版）

### 已知限制

- 不支持 PDF / XML / SVG / ZIP（上游返回 404）
- 无法区分 403 / 404 / 500（上游统一塌缩为 404）
- 不做结果重排序、不做固定阈值过滤、不做答案合成
- 不做 SSE 传输（2026-07-28 只定义 stdio 与 Streamable HTTP）
- 上游抓取有 30 秒硬超时，超大单页返回 `504`，重试通常也无效
<!-- 🔴 原来这里写的是「`truncated_by="upstream"` 为启发式判定」，该启发式已随
     23 号 §4.6/§4.7 的实测推翻而删除（上游真实失败模式是 504 超时，不是静默截断；
     成功的并发响应字节级全等）。`truncated_by` 现在只允许 `"budget"`。
     CHANGELOG 是发布产物，MUST NOT 继续向用户宣告一个不存在的字段。 -->
- `ori_question` 实测几乎无效，默认不传
