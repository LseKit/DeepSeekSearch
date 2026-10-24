# DeepSeekSearch

把 [DeepSeek 的 Search / Fetch API](https://api-docs.deepseek.com/zh-cn/guides/search)
包装成一个 [MCP](https://modelcontextprotocol.io) 服务器。

一个静态二进制，两种传输，五个工具。

[![CI](https://github.com/LseKit/DeepSeekSearch/actions/workflows/ci.yml/badge.svg)](https://github.com/LseKit/DeepSeekSearch/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/LseKit/DeepSeekSearch.svg)](https://pkg.go.dev/github.com/LseKit/DeepSeekSearch)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

---

## 它解决什么问题

MCP 客户端（Claude Desktop、Cursor、Hermes、Qoder、Codex……）需要「搜网页」和「读网页」两种能力。
大多数方案要么另起一个搜索服务，要么让模型的上下文被整页 HTML 撑爆。

DeepSeekSearch 做两件事：

1. **`ds_search`** —— 调 DeepSeek 搜索接口，返回**已按相关性降序**的结果，并按 `detail` 四档分级，让调用方自己决定要多少信息。
2. **`ds_fetch`** —— 抓网页正文，**大文档按标题分节**返回目录，模型按需取节，不会一次塞进 200k token。

## 五个工具

| 工具 | 作用 | 典型耗时 |
|---|---|---|
| `ds_search` | 网页搜索。支持 `site:` / `-site:` / `lang:` / `after:` / `before:` / 精确短语 | 1~5s |
| `ds_fetch` | 正文抓取。超过 8000 字符返回目录，按 `section` 取节 | 0.1~30s |
| `ds_health` | 本地诊断。检查 Key / 上游连通 / 缓存 / 生效配置。**不计费** | <50ms |
| `ds_research` | 组合研究：1 次搜索 + 并发抓取前 N 条来源，返回带编号引用的材料 | 数秒 |
| `ds_extract` | 批量抓取：1~10 个 URL 并发抓取，单条失败只影响该条 | 数秒 |

## 快速开始

```bash
# 1. 从 Releases 页下载对应平台的二进制，放到 Bin/
#    ⚠️ 文件名里的版本号请照抄 Releases 页的实际产物名，README MUST NOT 写死版本号
#    （否则每次发版都要改 README，且会被 CheckRepoFiles 的「README 禁版本号」硬规则拦下）
install -m 0755 DeepSeekSearch_<version>_linux_amd64 Bin/DeepSeekSearch

# 2. 配置
cp config.example.toml Config/config.toml

# 3. 填 Key（.env 的 group/other 必须无任何权限，即 600 或更严的 400，否则拒绝启动）
printf 'DEEPSEEK_API_KEY=%s\n' 'sk-你的key' > Config/.env
chmod 600 Config/.env

# 4. 校验配置（不建目录、不起服务）
./Bin/DeepSeekSearch -check -config Config/config.toml

# 5. 起服务
./Bin/DeepSeekSearch -transport streamable-http -config Config/config.toml
```

## 两种传输

| 传输 | 启动命令 | 适用 |
|---|---|---|
| `stdio` | `DeepSeekSearch -transport stdio` | 本地客户端直连。**一个客户端一个进程** |
| `streamable-http` | `DeepSeekSearch -transport streamable-http -config Config/config.toml` | 多客户端共享 / 反代 / 网关 |

### 客户端配置：stdio

```json
{
  "mcpServers": {
    "deepseek-search": {
      "command": "/path/to/Bin/DeepSeekSearch",
      "args": ["-transport", "stdio"],
      "env": { "DEEPSEEK_API_KEY": "sk-..." }
    }
  }
}
```

### 客户端配置：Streamable HTTP

```json
{
  "mcpServers": {
    "deepseek-search": {
      "type": "streamable-http",
      "url": "http://127.0.0.1:8765/mcp"
    }
  }
}
```

协议版本：**MCP 2026-07-28**（无状态 Streamable HTTP）。老版本客户端（2024-11-05 起）在同一个端点上同样可用。

## 语言与依赖

- Go 1.27，`CGO_ENABLED=0` 纯静态二进制（约 9MB）
- SQLite 用纯 Go 驱动（`modernc.org/sqlite`），不需要系统装 libsqlite
- 运行时只写项目目录内的 `Data/` 与 `Logs/`

## 计费

DeepSeek 按次计费：**¥5 / 1000 次**，Search 与 Fetch 各自计算。
只有「HTTP 200 且拿到内容」才计费；失败、空结果、超时不计费。
本项目的 SQLite 缓存与跨进程 singleflight 能显著减少重复计费。

## 已知限制

1. **部分主流站点抓不到** —— 实测 15 个主流站点中 **9 成功 / 6 失败（约 40%）**：维基百科（中/英）、知乎、Reddit、Hacker News、`raw.githubusercontent.com`。上游把它们与真 404 塌缩成同一个 `404`，我们无法区分，也无法绕过。**这直接影响「先搜索再抓正文」的工作流**：搜索结果里有相当比例来自这些站点（实测清单见 [`docs/Contract/23-上游能力实测报告.md`](docs/Contract/23-上游能力实测报告.md) §4.3）
2. **不支持 PDF / XML / SVG / ZIP** —— 上游对这类内容一律返回 `404`（实测）
3. **无法区分 403 / 404 / 500** —— 上游把三者塌缩成同一个 `404`，我们只能把全部成因写进错误提示
4. **上游抓取有 30 秒硬超时** —— 超大单页（如 `html.spec.whatwg.org` 单页版、W3C 单页规范）会返回 `504`，重试通常也无效
5. **不做结果重排序 / 加权** —— 上游返回时已按 `rerank_score` 降序，重排是用更少的信息覆盖更多的信息
6. **不做答案合成** —— 保留原始引用，由调用方（LLM）自行组织答案
7. **不做 SSE 传输** —— MCP 2026-07-28 只定义 `stdio` 与 `Streamable HTTP`
8. **`min_rerank` / `min_authority` 容易过度过滤** —— 官方文档说「`rerank_score ≥ 1.5` 为优质结果」，但实测冷门/专业 query 的正常结果**普遍低于 1.5**（最低观测 0.196）。不确定时**不要传这两个参数**，否则可能返回 0 条
9. **`ori_question` 实测几乎无效** —— 4 组对照里 3 组结果字节级相同，默认不传

## 文档

完整设计与契约在 [`docs/`](docs/)：

| 想知道 | 看 |
|---|---|
| 为什么这样设计 | [`docs/Decisions/ADR.md`](docs/Decisions/ADR.md) |
| 配置每一项是什么意思 | [`docs/Contract/13-配置契约.md`](docs/Contract/13-配置契约.md) |
| 错误码是什么 | [`docs/Contract/14-错误契约.md`](docs/Contract/14-错误契约.md) |
| 上游到底有哪些怪癖（实测数据） | [`docs/Contract/23-上游能力实测报告.md`](docs/Contract/23-上游能力实测报告.md) |
| 怎么部署 / 出错怎么办 | [`docs/Ops/30-部署运维手册.md`](docs/Ops/30-部署运维手册.md) |
| 给 AI agent 的项目宪法 | [`AGENTS.md`](AGENTS.md) |

版本历史见 [CHANGELOG.md](CHANGELOG.md)。参与贡献见 [CONTRIBUTING.md](CONTRIBUTING.md)。
安全问题请**不要**开公开 issue，见 [SECURITY.md](SECURITY.md)。

## 许可

[MIT](LICENSE) © 2026 小逸
