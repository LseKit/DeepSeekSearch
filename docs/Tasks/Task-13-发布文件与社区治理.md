# Task-13 · 发布文件（根目录与社区治理）

> **执行方式**：2 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20
> **前置**：Task-01 已完成（`GitHubSrc/` 骨架存在，`LICENSE`/`.gitignore`/`README`/`CHANGELOG` 已有**最小骨架**）

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 让仓库在 GitHub 上「看起来是个正经项目」：本任务交付 7 个 A 级 + 3 个 B 级 + 1 个 `.github/` 文件，全部到位且满足硬规则 |
| 依赖 | Task-01 |
| 产出 | `Scripts/CheckRepoFiles.sh` 的根目录部分全绿 |
| 必读 | `Contract/18-仓库文件契约.md` §二 §三 §四；`Contract/19-AGENTS.md全文.md`；`Contract/17-版本与发布契约.md` §六 |

> 🔴 **计数口径（C12 修正）**：`Contract/18-仓库文件契约.md` §二 是 **A 级 12 个**、§三 是 **B 级 17 个**，`Scripts/CheckRepoFiles.sh` 实跑也是 `A 级 12/12`、`B 级 17/17`（`91-验收总表` 闸门 G4 同口径）。本任务原文写「11 个 A 级文件」是错的 —— 它数的其实是 §整体验收 那条 `ls` 输出的 **11 个根目录文件**（含 Task-01 留下的 `LICENSE`），既不是 A 级总数，也不是本任务的交付数。
>
> **验收判据 MUST 写成动态的**：A/B 级文件数 = `Scripts/CheckRepoFiles.sh` 清单的行数（脚本输出末尾的 `A_OK/A_TOTAL`、`B_OK/B_TOTAL`），MUST NOT 在任何文档里手抄数字。硬编码正是「11 / 12 / 15 / 17 四种说法并存」的生成机制（同 `Contract/17-版本与发布契约.md` §七 对发布检查单项数的裁决）。
>
> 本任务实际交付：**A 级 7 个**（`README.md` 覆盖、`AGENTS.md`、`CHANGELOG.md` 覆盖、`SECURITY.md`、`.gitignore` 覆盖、`CONTRIBUTING.md`、`CODE_OF_CONDUCT.md`）+ **B 级 4 个**（`SUPPORT.md`、`.gitattributes`、`.editorconfig`、`.github/CODEOWNERS`）。A 级其余 5 个（`LICENSE`、`go.mod`、`go.sum`、`Makefile`、`config.example.toml`）由 `Task-01` 交付。

| 原子 | 标题 | 文件数 | 预警 |
|:-:|---|:-:|---|
| A-01 | 根目录发布文件（8 个，含 AGENTS.md 与完整版 README/CHANGELOG） | 8 | AGENTS.md MUST 逐字复制，不许改写 |
| A-02 | 社区治理文件（3 个） | 3 | CODE_OF_CONDUCT 必须用 v2.1 原文，不许自创 |

---

## 原子任务 A-01 · 根目录发布文件

### 0 前置

- `GitHubSrc/` 存在，且 Task-01 A-03 已建 `README.md` `CHANGELOG.md` `LICENSE` `.gitignore`
- 本任务会**覆盖** Task-01 留下的 `README.md` `CHANGELOG.md` `.gitignore` 骨架版 —— 那是预期的，不是冲突

### 1 上下文（自包含）

Task-01 建的是「能让 `make build` 通过」的最小骨架。本任务把它们升级成**发布版**：一个陌生人打开仓库，40 行内要知道这东西是什么、怎么跑、做不到什么。

`AGENTS.md` 是新增的：**它不是 README 的替代品**。README 给人，AGENTS.md 给编码 agent（构建命令、测试命令、契约、禁止清单）。60k+ 开源项目已采用这个格式，Codex / Cursor / Qoder / Jules / Amp / Copilot 等都会读它。

### 2 要创建/修改的文件

| 文件 | 动作 | 内容来源 |
|---|---|---|
| `GitHubSrc/AGENTS.md` | 新建 | **逐字复制** `Doc/Contract/19-AGENTS.md全文.md` 的两个标记之间的内容 |
| `GitHubSrc/README.md` | **覆盖** | 下文 §5.1 全文 |
| `GitHubSrc/CHANGELOG.md` | **覆盖** | 下文 §5.2 全文 |
| `GitHubSrc/SECURITY.md` | 新建 | 下文 §5.3 全文 |
| `GitHubSrc/SUPPORT.md` | 新建 | 下文 §5.4 全文 |
| `GitHubSrc/.gitignore` | **覆盖** | 下文 §5.5 全文 |
| `GitHubSrc/.gitattributes` | 新建 | 下文 §5.6 全文 |
| `GitHubSrc/.editorconfig` | 新建 | 下文 §5.7 全文 |

### 3 实现要求（逐条可验证）

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | `AGENTS.md` 与 19 号文档两个标记之间的内容**字节相同** | `make drift` 的第 **8** 项（`Engineering/22-架构守护规则` §5.1；原文写「第 7 项」是 8 项重写前的旧编号） |
| 2 | `AGENTS.md` 以 `# AGENTS.md` 开头，含 §1~§13 全部 13 个一级章节 | `grep -c '^## [0-9]' AGENTS.md` = 13 |
| 3 | `README.md` **不含**任何 `vX.Y.Z` 形式的版本号数字 | `grep -cE 'v[0-9]+\.[0-9]+\.[0-9]+' README.md` = 0 |
| 4 | `README.md` 含 `## 已知限制` 章节，且列出 **≥9 条**编号项（骨架当前就是 9 条；第 1 条 MUST 是「部分主流站点抓不到」） | `awk '/^## 已知限制/,/^## [^已]/' README.md \| grep -cE '^[0-9]+\. '` ≥ 9 |
| 5 | `README.md` 含两种传输的客户端配置片段（`stdio` 与 `streamable-http` 各一段） | 人工核对 |
| 6 | `CHANGELOG.md` 含 `## [未发布]` 与 `## [v0.1.0]` 两段 | `grep -c '## \['` = 2 |
| 7 | `CHANGELOG.md` 的 `v0.1.0` 段含「已知限制」小节 | `awk '/^## \[v0\.1\.0\]/{f=1} f' CHANGELOG.md \| grep -c '已知限制'` = 1。🔴 原判据用 `grep -A20 'v0.1.0'`，而「已知限制」在标题后第 29 行 ⇒ 永远数到 0；判据改成「从 v0.1.0 段抽到文件末尾」（它是最后一节） |
| 8 | `SECURITY.md` 含四要素：支持版本表 / 私密上报链接 / 响应时限 / 「哪些不算漏洞」 | 人工核对四项 |
| 9 | `SECURITY.md` MUST NOT 让漏洞走公开 issue | `grep -c '公开 issue' SECURITY.md` ≥ 1 且上下文是否定 |
| 10 | `.gitignore` 含 `Bin/` `Data/` `Logs/` `Config/.env` `coverage.out` | `grep -c 'Config/.env'` = 1 |
| 11 | `.gitattributes` 含 `* text=auto eol=lf` 与 `go.sum linguist-generated=true` | `grep -c 'eol=lf'` ≥ 1 |
| 12 | `.editorconfig` 里 Go 文件用 **tab**，其它用 2 空格 | `grep -A3 '\[\*.go\]' .editorconfig` |
| 13 | 全部文件 UTF-8、LF 换行、末尾有换行 | `file *.md` 无 `CRLF` |
| 14 | 🔴 `SECURITY.md` 的「哪些算安全问题」一节 MUST NOT 出现「DNS 重绑定」；它 MUST 出现在「哪些不算安全问题」里，且带三条理由（上游执行抓取 / 不做 DNS 解析 / 实测内网 IP 一律 404）与「将来直连时 MUST 用 `net.Dialer.Control`」的正解 | `awk '/^## 哪些算安全问题/,/^## 哪些不算安全问题/' SECURITY.md \| grep -c 'DNS 重绑定'` = **0**；`awk '/^## 哪些不算安全问题/,0' SECURITY.md \| grep -c 'DNS 重绑定'` = **1**；`grep -c 'net.Dialer.Control' SECURITY.md` = 1 |
| 15 | `README.md`「已知限制」第 1 条 MUST 点名抓不到的站点（维基百科中/英、知乎、Reddit、Hacker News、`raw.githubusercontent.com`）并给出 23 号 §4.3 的出处 | `awk '/^## 已知限制/,/^## [^已]/' README.md \| grep -c 'raw.githubusercontent.com'` = 1 |
| 16 | `LICENSE` 的仓库 URL 判据锚定**版权行**（`Task-01` §5.8 的逐字正文里就有），MUST NOT 为了过 `grep` 而改 MIT 正文 | `grep -c 'LseKit/DeepSeekSearch' LICENSE` = 1 且 `grep -c 'Permission is hereby granted' LICENSE` = 1 |

> 🔴 **要求 3 曾与本任务的 README 骨架形成死锁**：骨架的「快速开始」里原本写着 `install -m 0755 DeepSeekSearch_v0.1.0_linux_amd64 ...`，而要求 3 规定 README 里版本号数字的 grep 计数必须为 **0**，同时要求「骨架逐字使用、MUST NOT 改写」，且 `Task-14` 的 `CheckRepoFiles.sh` 会硬拦同一条规则。三条约束叠加 ⇒ **照文档做就永远过不了发版检查、永远打不了 tag**。
> 现已修正：骨架改用 `DeepSeekSearch_<version>_linux_amd64` 占位符。**修改骨架时 MUST 保持这一点** —— README 是发布产物里唯一「跨版本不变」的文件，写死版本号意味着每次发版都要改它，而要求 3 的存在正是为了强制这个不变性。

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 改写 `AGENTS.md` 的任何一行「顺口」表述。它是冻结契约，改要回 19 号文档改 |
| 2 | MUST NOT 在 `README.md` 里写「性能卓越」「业界领先」「革命性」这类空话 |
| 3 | MUST NOT 在 `README.md` 里贴大段中文配置注释（指向 `docs/Contract/13-配置契约.md` 即可） |
| 4 | MUST NOT 在 `SECURITY.md` 里承诺做不到的响应时限（如「2 小时内修复」） |
| 5 | MUST NOT 在 `.gitignore` 里漏掉 `Config/.env`（漏了就会有人把 Key 提交上来） |
| 6 | MUST NOT 在 `README.md` 里写死任何 `vX.Y.Z`（见上方死锁说明），包括示例命令、下载链接、徽章 |
| 7 | MUST NOT 把「DNS 重绑定绕过私网黑名单」列为 `SECURITY.md` 受理的漏洞 —— `Task-09` 明确禁止对目标 URL 做 DNS 解析（TOCTOU），且**抓取由上游执行、我方进程从不直连目标 URL**，该攻击面在本架构下不存在。应写入「哪些不算安全问题」并注明理由，否则会收到无法闭环的报告（裁决与完整理由见 §5.3 的骨架注释） |
| 8 | MUST NOT 在 `LICENSE` 里改写 MIT 正文（逐字正文见 `Task-01` §5.8） |
| 9 | 🔴 MUST NOT 因为「`Task-14` 的 `CheckRepoFiles.sh` 要 `grep -q "LseKit/DeepSeekSearch" LICENSE`」就往 MIT **正文**里塞仓库 URL。**这两条不冲突**：`Task-01` §5.8 的逐字正文里，版权行本身就是 `Copyright (c) 2026 小逸 <https://github.com/LseKit/DeepSeekSearch>`，URL 在**版权行**而不在正文，`grep` 命中的就是它（`Contract/18-仓库文件契约.md` A2 的验收 `grep -c 'LseKit/DeepSeekSearch' LICENSE` = 1 也是同一处）。往正文里加 URL 会同时破坏 MIT 逐字性与 GitHub 的许可识别 |
| 10 | MUST NOT 手写 `docs/` 下任何文件 |

### 5 参考骨架（完整内容，逐字使用，MUST NOT 改写）

#### 5.1 `README.md`

````markdown
# DeepSeekSearch

把 [DeepSeek 的 Search / Fetch API](https://api-docs.deepseek.com/zh-cn/guides/search)
包装成一个 [MCP](https://modelcontextprotocol.io) 服务器。

一个静态二进制，两种传输，三个工具。

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

## 三个工具

| 工具 | 作用 | 典型耗时 |
|---|---|---|
| `ds_search` | 网页搜索。支持 `site:` / `-site:` / `lang:` / `after:` / `before:` / 精确短语 | 1~5s |
| `ds_fetch` | 正文抓取。超过 8000 字符返回目录，按 `section` 取节 | 0.1~30s |
| `ds_health` | 本地诊断。检查 Key / 上游连通 / 缓存 / 生效配置。**不计费** | <50ms |

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
````

#### 5.2 `CHANGELOG.md`

````markdown
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
````

#### 5.3 `SECURITY.md`

````markdown
# 安全策略

## 支持版本

只有最新发布版本接收安全修复。

| 版本 | 支持 |
|---|:-:|
| v0.1.x（最新） | ✅ |
| 更早版本 | ❌ 请先升级 |

## 上报漏洞

**请使用 GitHub 私密漏洞上报**，不要开公开 issue：

<https://github.com/LseKit/DeepSeekSearch/security/advisories/new>

私密上报的内容至少包含：

1. 受影响的版本（`DeepSeekSearch --version` 的完整输出）
2. 传输方式（`stdio` 或 `streamable-http`）与相关配置片段（**API Key 请替换成 `sk-***`**）
3. 复现步骤
4. 影响描述：能做什么、需要什么前提

**MUST NOT** 在公开 issue、讨论区、社交媒体上披露未修复的漏洞。

## 响应时限

这是个人维护的开源项目，以下是**尽力而为**的承诺，不是 SLA：

| 阶段 | 时限 |
|---|---|
| 确认收到 | 72 小时内 |
| 初步评估（是否成立 / 严重度） | 7 天内 |
| Critical（远程可利用 / 密钥泄漏） | 7 天内修复 |
| High | 30 天内修复 |
| 其他 | 90 天内或在下一个 minor 版本 |

## 哪些算安全问题

| 类型 | 例子 |
|---|---|
| 凭据泄漏 | API Key / HTTP token 出现在日志、错误消息、`/healthz`、Resource 响应里 |
| SSRF 绕过 | 用 **IP 混淆写法**（八/十/十六进制、IPv4-mapped IPv6）或**重定向**绕过私网黑名单，让服务端请求内网地址 |
| 鉴权绕过 | `auth_enabled=true` 时无 token 仍能调用工具；token 比较存在时序侧信道 |
| 暴露面 | `host=0.0.0.0` 且未开鉴权（应在启动期就拒绝）；Origin / Host 校验缺失 |
| 注入 | 上游正文逃逸 `<untrusted_content>` 包裹，把 prompt injection 传给调用方 |
| 依赖漏洞 | `go.sum` 中某个依赖的已知 CVE 影响本项目（CI 有 govulncheck job，见 `.github/workflows/ci.yml`） |

## 哪些不算安全问题

下面这些是**上游行为**或**设计决定**，不当漏洞处理（提 issue 会被转成 `wontfix`）：

| 现象 | 原因 |
|---|---|
| **DNS 重绑定「绕过私网黑名单」** | 🔴 **本架构下这个攻击面不存在**。两件事叠在一起：① 抓取由 **DeepSeek 上游**执行，我方进程从不直连目标 URL，所以「让服务端请求内网地址」里的「服务端」不是我们；② 我方**故意不做 DNS 解析**（只对字面量 host 判断），因为「先解析再连接」在解析与连接之间存在 TOCTOU，是个假防线。实测上游对 `127.0.0.1`、`localhost`、`10.0.0.1`、`169.254.169.254`、十进制混淆 IP `2130706433` **一律返回 404**（见 [`docs/Contract/23-上游能力实测报告.md`](docs/Contract/23-上游能力实测报告.md) §4.2）。<br>⚠️ 但请注意：上游的 404 与真 404 **不可区分**，所以我们不把「上游拦住了」当成安全保证 —— 本地黑名单是**纵深防御**（避免本服务被当作探测放大器、并覆盖 `base_url` 被指向自建代理的情形），不是唯一防线。<br>**若将来改为我方直连目标 URL**，MUST 用 `net.Dialer.Control` 在**实际连接的 IP** 上二次校验（这才是无 TOCTOU 的正解），届时本行 MUST 移回「哪些算安全问题」 |
| 搜索结果质量差、搜不到某个页面 | DeepSeek 服务端的排序结果，本项目不做重排序 |
| 某个网页抓不到（返回 404） | 上游把 403/404/500/不支持的内容类型统一塌缩成 404，见 `docs/Contract/14-错误契约.md` §2.3。**实测 15 个主流站点里 6 个抓不到**（维基百科中/英、知乎、Reddit、Hacker News、`raw.githubusercontent.com`），见 23 号 §4.3 |
| 超大单页抓取失败（返回 `504`） | 上游 fetch 网关有 **30 秒硬超时**（实测 30.126s / 30.131s），超大单页必然超时，重试通常也无效。这是**上游行为**，不是本项目的 bug；`504` 会映射为 `2504` 且标记为可重试 |
<!-- 🔴 原行写「上游静默截断，我们只能标记 truncated_by="upstream"」。该结论已被
     23 号 §4.6/§4.7 推翻：上游没有静默截断这回事，真实失败模式是 504。
     把我们无法观测的行为写进 SECURITY.md 的「不算漏洞」清单，
     等于向报告者承诺一个我方并不具备的检测能力。 -->
| 正文里有反斜杠转义 | 上游对 Markdown 做了强制转义，用 `unescape=true`（默认）处理 |
| 没有 SSE 传输 | MCP 2026-07-28 只定义 `stdio` 与 `Streamable HTTP`，见 `docs/Decisions/ADR.md` ADR-002 |
| 缓存命中后看到旧内容 | `ttl_fetch_s` 默认 6 小时，调小即可 |
| 免费额度 / 计费争议 | DeepSeek 账号侧问题，与本项目无关 |

## 本项目自身的安全基线

| 项 | 实现 |
|---|---|
| 密钥隔离 | `Config/.env`（权限 600），`config.toml` 出现密钥类键名直接拒绝启动 |
| 暴露保护 | `host` 非回环 或 `allow_non_local_host=true` 时，必须 `auth_enabled=true`，否则拒绝启动（错误码 `3092`） |
| 请求校验 | Origin 白名单 + Host 校验 + 请求体大小上限 |
| 日志脱敏 | API Key / token 永不进日志；配置输出前先 `Redacted()` |
| 二进制 | `CGO_ENABLED=0` 静态编译，发布附带 `SHA256SUMS` |
````

> 🔴 **C12 裁决：DNS 重绑定 MUST NOT 出现在「哪些算安全问题」里**（骨架已按此改）。
>
> | 项 | 内容 |
> |---|---|
> | 原状态 | 「SSRF 绕过」那一行把 **DNS 重绑定** 列为受理漏洞 |
> | 冲突 | `Task-09` A-01 §4 禁止事项第 1 条明确 **MUST NOT** 用「先 `net.LookupIP` 再连接」的模式做 SSRF 判定，我方**只对字面量 host 判断**、从不解析 DNS。安全政策承诺了设计明确不做的东西 ⇒ 必然招来**无法闭环**的报告（报告者 PoC 成立，我们却按设计不修） |
> | 裁决 | **维持不做 DNS 解析**，把 DNS 重绑定移进「哪些不算安全问题」 |
> | 理由 1 | 抓取由 **DeepSeek 上游**执行，我方进程从不直连目标 URL ⇒ 「让服务端请求内网地址」的服务端不是我们 |
> | 理由 2（实测） | 23 号 §4.2：上游对 `http://127.0.0.1/`、`http://localhost/`、`http://10.0.0.1/`、`http://169.254.169.254/latest/meta-data/`、`http://2130706433/`（十进制混淆 IP）**一律返回 404** `failed to fetch URL` |
> | 理由 3（诚实披露） | 23 号 §4.2 同时指出：上游的拦截响应与真 404 **完全不可区分** ⇒ 我方**不能**通过响应判断上游是否拦截成功。所以骨架里 MUST 保留「本地黑名单是纵深防御，不是唯一防线」这句，MUST NOT 写成「上游已经拦住了所以不用管」 |
> | 将来若改为我方直连 | MUST 用 `net.Dialer.Control` 在**实际连接的 IP** 上二次校验（这才是无 TOCTOU 的正解 —— 原禁令否定的是「先 Lookup 再连接」这个弱方案，不是否定一切校验）；届时把这一行移回「哪些算安全问题」 |
>
> 与 `Task-09` A-01 §4 禁止事项第 1.1 条的裁决一致（那里写的是「取 ①，理由见 `Task-13`」，理由就是本表）。

#### 5.4 `SUPPORT.md`

````markdown
# 获取帮助

先看这里，能解决 80% 的问题。

## 我该去哪里问

| 你想做什么 | 去哪里 |
|---|---|
| 报告一个 bug | [新建 issue](https://github.com/LseKit/DeepSeekSearch/issues/new?template=bug_report.yml) |
| 提一个功能需求 | [新建 issue](https://github.com/LseKit/DeepSeekSearch/issues/new?template=feature_request.yml) |
| 问「怎么配置」 | 先读 [`docs/Contract/13-配置契约.md`](docs/Contract/13-配置契约.md)，仍不懂再开 issue |
| 问「部署出错怎么办」 | 先读 [`docs/Ops/30-部署运维手册.md`](docs/Ops/30-部署运维手册.md) §六「故障排查」 |
| 上报安全漏洞 | **不要开 issue**，见 [SECURITY.md](SECURITY.md) |
| 想知道某个功能什么时候做 | 查 [`docs/Decisions/16-技术债登记表.md`](docs/Decisions/16-技术债登记表.md) |

## 开 issue 前请自检

1. 跑一次 `DeepSeekSearch --version`，把完整输出贴进 issue
2. 跑一次 `./Bin/DeepSeekSearch -check -config Config/config.toml`，确认配置本身没问题
3. 如果是「搜不到 / 抓不到」，先看 [`docs/Contract/14-错误契约.md`](docs/Contract/14-错误契约.md) §2.3 —— 大部分情况是上游统一返回 404
4. 贴日志时**把 API Key 替换成 `sk-***`**

## 本项目不提供

- DeepSeek 账号、余额、计费相关的支持（找 DeepSeek 官方）
- 上游搜索质量 / 抓取成功率的保证
- 商业支持或 SLA
````

#### 5.5 `.gitignore`

```gitignore
# ---- 构建产物 ----
/Bin/
*.exe
*.test
*.out

# ---- 运行时数据（含缓存与日志）----
/Data/
/Logs/
*.log
*.log.gz

# ---- 密钥（绝对不许提交）----
/Config/.env
.env
*.key
*.pem

# ---- 测试与覆盖率 ----
coverage.out
coverage.html
*.cover

# ---- 编辑器 / 系统 ----
.DS_Store
Thumbs.db
.idea/
.vscode/
*.swp
*~

# ---- 本地临时 ----
/tmp/
*.bak
```

#### 5.6 `.gitattributes`

```gitattributes
# 默认：文本文件统一 LF，检出时按平台转换由 eol 决定
* text=auto eol=lf

# Go 与文档一律 LF
*.go        text eol=lf
*.md        text eol=lf
*.toml      text eol=lf
*.yml       text eol=lf
*.yaml      text eol=lf
*.json      text eol=lf
*.sh        text eol=lf
Makefile    text eol=lf

# Windows 脚本保留 CRLF
*.bat       text eol=crlf
*.ps1       text eol=crlf

# 二进制
*.png       binary
*.jpg       binary
*.gif       binary
*.ico       binary
*.gz        binary

# 生成文件：折叠 diff、不计入语言统计
go.sum              linguist-generated=true
/go.sum             linguist-generated=true
docs/**             linguist-documentation=true
```

#### 5.7 `.editorconfig`

```ini
# https://editorconfig.org
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
trim_trailing_whitespace = true
indent_style = space
indent_size = 2

# Go 官方使用 tab 缩进
[*.go]
indent_style = tab

[*.{mod,sum}]
indent_style = tab

[Makefile]
indent_style = tab

[*.md]
trim_trailing_whitespace = false

[*.{yml,yaml,json,toml,ini}]
indent_style = space
indent_size = 2

[*.sh]
indent_style = space
indent_size = 2
```

### 6 测试代码

本任务无 Go 测试。验收靠 §7 的命令与 `Scripts/CheckRepoFiles.sh`（Task-14 A-02 落地）。

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc

$ grep -c '^## [0-9]' AGENTS.md
13

$ grep -cE 'v[0-9]+\.[0-9]+\.[0-9]+' README.md
0

$ grep -c '## \[' CHANGELOG.md
2

# 🔴 原判据是 grep -A20：v0.1.0 标题后第 29 行才是「已知限制」，-A20 恒为 0
$ awk '/^## \[v0\.1\.0\]/{f=1} f' CHANGELOG.md | grep -c '已知限制'
1

$ grep -c 'Config/.env' .gitignore
1

$ grep -c 'eol=lf' .gitattributes | head -1
9

$ grep -A2 '^\[\*\.go\]' .editorconfig
[*.go]
indent_style = tab

$ file *.md | grep -c CRLF || echo "0 个 CRLF"
0 个 CRLF

# 🔴 C12 新增（对应 §3 要求 14/15/16）
# 注意：grep -c 无匹配时**既打印 0 又退出 1**，所以用 `|| true` 而不是 `|| echo 0`
# （后者会多打一个 0，变成两行输出 —— CheckDocDrift.sh 里有同一条教训的注释）
$ awk '/^## 哪些算安全问题/,/^## 哪些不算安全问题/' SECURITY.md | grep -c 'DNS 重绑定' || true
0

$ awk '/^## 哪些不算安全问题/,0' SECURITY.md | grep -c 'DNS 重绑定'
1

$ grep -c 'net.Dialer.Control' SECURITY.md
1

$ awk '/^## 已知限制/,/^## [^已]/' README.md | grep -c 'raw.githubusercontent.com'
1

$ grep -c 'LseKit/DeepSeekSearch' LICENSE
1
```

### 8 完成定义（DoD）

- [ ] §7 的 **12 条**命令达到期望输出（贴真实输出）。其中「DNS 重绑定」那两条是 C12 新增的牙齿：第一条 MUST 是 0（受理表里不许有），第二条 MUST 是 1（不算表里必须有）
- [ ] `AGENTS.md` 与 19 号文档 diff 无差异（`make drift` 第 8 项，见 `Engineering/22-架构守护规则` §5.1）
- [ ] 8 个文件全部存在，`file` 显示 UTF-8 且无 CRLF
- [ ] 提交：`docs(repo): 加入 AGENTS.md 与根目录发布文件`

---

## 原子任务 A-02 · 社区治理文件

### 0 前置

- A-01 已完成并提交
- 已读 `Contract/18-仓库文件契约.md` §三 的 B5、§四 的 4.5

### 1 上下文（自包含）

GitHub 的 community profile 会检查这几个文件。`CODE_OF_CONDUCT.md` 必须用 **Contributor Covenant v2.1 原文**（GitHub 的「Add code of conduct」按钮内置的就是它）—— 自己改写的版本会让「我们遵守行业标准」这句话变成空话。

`CONTRIBUTING.md` 里必须写清**本项目特有的三条规则**（文档优先 / 一 PR 一原子任务 / 不许改测试），否则外部贡献者一定违反。

### 2 要创建的文件

| 文件 | 内容来源 |
|---|---|
| `GitHubSrc/CONTRIBUTING.md` | 下文 §5.1 |
| `GitHubSrc/CODE_OF_CONDUCT.md` | 下文 §5.2（Contributor Covenant v2.1 原文） |
| `GitHubSrc/.github/CODEOWNERS` | 下文 §5.3 |

### 3 实现要求

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | 🔴 `CODE_OF_CONDUCT.md` 是 Contributor Covenant **v2.1 中文译文**，含归属段的名称、版本号与官网 URL | `grep -c 'Contributor Covenant'` = **1**；`grep -c '版本 2.1'` = **1**；`grep -c 'contributor-covenant.org'` = **4** |
| 2 | `CODE_OF_CONDUCT.md` 含执行联系方式（占位 `conduct@` 也行，但必须写明「如何私密报告」） | `grep -c '报告'` ≥ 2 |
| 3 | `CONTRIBUTING.md` 含 `make check` 命令与三项项目特有规则 | 人工核对 |
| 4 | `CONTRIBUTING.md` MUST 有「提交信息格式」段落，且 type 词表与 `Contract/18-仓库文件契约.md` §4.6 的 release notes 分组映射一致 | `grep -c 'feat('` ≥ 1 |
| 5 | `CODEOWNERS` 里 `*` 指向 `@LseKit`，且覆盖 `internal/` `docs/` `.github/` 三个路径 | `grep -c '@LseKit'` = **13**（骨架实际 13 行；判据 MUST 是「= 骨架里带 owner 的规则行数」，MUST NOT 手抄数字 —— 加一条规则就要同步改这里） |

> 🔴 **C12 修正：原来两条判据的期望值都是错的**
>
> | 判据 | 原期望 | 实际 | 错在哪 |
> |---|:-:|:-:|---|
> | `grep -c 'Contributor Covenant' CODE_OF_CONDUCT.md` | 4（§7）/ ≥3（本表要求 1） | **1** | `grep -c` 数的是**匹配行数**。冻结的 v2.1 **中文译文**里，大小写敏感且带空格的 `Contributor Covenant` 只在「归属」段的链接文字 `[Contributor Covenant][homepage]` 出现 **1 次**；其余 4 处全是小写连字符的 URL `contributor-covenant.org`，`grep 'Contributor Covenant'` 一个都匹配不到。原期望值是照着**英文原文**（"Contributor Covenant" 在英文正文里出现多次）数的 |
> | `grep -c '@LseKit' .github/CODEOWNERS` | 10（§7） | **13** | 骨架里带 owner 的规则行是 13 行（`*` + `/internal/` `/cmd/` `/Scripts/` + `/docs/Contract/` `/docs/Decisions/` `/AGENTS.md` `/SECURITY.md` + `/.github/` `/go.mod` `/go.sum` `/Makefile` `/.golangci.yml`）。§3 要求 5 的「≥ 4」虽然不算错，但没有牙齿（少写 9 条也过） |
>
> **为什么改判据而不是改期望值**：`Contributor Covenant` 这一条两处都改 —— 期望值改成真实的 **1**，同时**加两条更有牙齿的判据**（`版本 2.1` = 1、`contributor-covenant.org` = 4）。只把 4 改成 1 会让判据变弱（把归属段整段删掉也只剩 0，而 1 这个值本身不说明「用的是 v2.1 原文」）；加上版本号与 URL 计数后，「自创正文」和「换了别的 CoC」都会被抓到。
>
> `CODEOWNERS` 这一条改期望值为 **13** 并把口径写成「= 骨架里带 owner 的规则行数」，理由同上：写死数字是漂移的生成机制。

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 自创 Code of Conduct 正文（必须 v2.1 原文） |
| 2 | MUST NOT 在 `CONTRIBUTING.md` 里写「欢迎随便提 PR」——必须写明流程与检查项 |
| 3 | MUST NOT 在 `CODEOWNERS` 里写不存在的用户名（会导致 review 请求发不出去） |
| 4 | MUST NOT 把 `CODEOWNERS` 放错位置（`/` `/docs/` 或 `/.github/` 三者之一，本项目选 `/.github/`） |

### 5 参考骨架（完整内容，逐字使用，MUST NOT 改写）

#### 5.1 `CONTRIBUTING.md`

````markdown
# 贡献指南

感谢你愿意花时间。这个项目有**三条与普通 Go 项目不同**的规则，先读完再动手，能省下双方很多来回。

## 三条项目特有规则

### 1. 文档优先

改行为**必须**先改 `docs/Contract/`，再改代码。

`docs/` 下的契约文档是这个项目的唯一真源：接口签名、错误码、配置键、工具参数都写在那里。
代码是契约的实现，不是反过来。CI 里有一个 `drift` job 专门检查「文档与代码是否一致」，代码先行一定红。

### 2. 一个 PR 一个原子任务

`docs/Tasks/` 下每个 Task 被拆成 2~3 个**原子任务**（`A-01`、`A-02`…）。
一个 PR 只做一个原子任务。跨任务的 PR 无法用 `docs/Handover/91-验收总表.md` 逐条核对，会被要求拆分。

### 3. 不要为了让测试变绿而改测试

`make size`（规模红线 F-S1/F-S3/F-S5）、`internal/arch_test.go`（依赖白名单 + F-S2/F-S4）、`make drift`（文档一致性）是**护栏**。
改它们等于拆护栏。撞线了就拆代码，拆不动就在 `docs/Engineering/豁免台账.md` 登记豁免并说明理由。

## 开发环境

```bash
go version                 # ≥ go1.27
golangci-lint --version    # v2.x（v1 配置格式已废弃）
```

可选（用于规模检查 F-S3）：

```bash
# 🔴 版本由 go.mod 的 tool 指令 + go.sum 锁定，本地与 CI 同源。
#    MUST NOT 用 @latest，也 MUST NOT 手动指定版本安装（会与 CI 跑不同版本）。
cd GitHubSrc
go mod download          # 拉取 tool 指令登记的工具
go tool gocyclo -over 10 ./...
```

## 常用命令

| 命令 | 作用 |
|---|---|
| `make build` | 编译到 `../Bin/DeepSeekSearch` |
| `make test` | 单元测试 |
| `make test-race` | 带竞态检测（提交前必跑） |
| `make lint` | 静态检查 |
| `make cover` | 覆盖率 |
| `make size` | 规模红线 F-S1 / F-S3 / F-S5（F-S2 / F-S4 由 `go test ./internal/` 强制） |
| `make drift` | 文档漂移 8 项 |
| `make check` | **上面六个的总和**（收尾只需这一条） |
| `make test-integration` | 真实调用上游，**会计费**，只在明确需要时跑 |

`make test` 与 `make check` **不会**产生任何计费调用；计费测试全部藏在 `-tags integration` 后面。

## 提 PR 之前

```bash
make check
```

六项全绿，然后逐条勾 PR 模板里的清单。

## 提交信息格式

`<type>(<scope>): <中文短句>`

| type | 用途 |
|---|---|
| `feat` | 新功能 |
| `fix` | 修 bug |
| `docs` | 只改文档 |
| `refactor` | 不改行为 |
| `test` | 只改测试 |
| `build` | 构建 / Makefile / CI |
| `chore` | 杂项 |

`scope` 用包名：`domain` `config` `logging` `deepseek` `resilience` `tools` `transport` `cmd` `repo`。

```
feat(domain): 时间/URL/切片/反转义纯函数
fix(transport): statusWriter 补 Flush 与 Unwrap，修复流式响应卡死
docs(contract): 配置契约新增 [limits.budget] 四项
```

不要写 `update` / `fix bug` / `改进` 这种没有信息量的。

## 代码风格

**必须**遵守 [`.golangci.yml`](.golangci.yml) 与 [`AGENTS.md`](AGENTS.md) §5。
最常被退回的四条：

1. **注释写「为什么」，不写「做什么」**，用中文，Go doc 形式（以标识符名开头、句号结尾）
2. **时间从注入的 `Clock` 取**，不要直接 `time.Now()`
3. **文本长度按 rune**，不要按 byte（`s[0:8000]` 会切坏中文）
4. **错误字符串小写、无标点结尾**，用 `%w` 包装，用 `errors.Is/As` 判断

## 新增功能前先看

| 想知道 | 看 |
|---|---|
| 这个功能是不是已经决定「不做」 | [`docs/Decisions/16-技术债登记表.md`](docs/Decisions/16-技术债登记表.md) §四 |
| 这个功能是不是排在阶段二 | 同上 §二（DEBT-001~004） |
| 为什么当初这么设计 | [`docs/Decisions/ADR.md`](docs/Decisions/ADR.md) |

被登记为「不做」的功能，PR 会被关闭并指向对应条目。

## 安全问题

**不要**开公开 issue。见 [SECURITY.md](SECURITY.md)。

## 许可

提交 PR 即表示你同意以 [MIT](LICENSE) 许可发布你的贡献。
````

#### 5.2 `CODE_OF_CONDUCT.md`

````markdown
# 贡献者行为准则

## 我们的承诺

作为成员、贡献者和领导者，我们承诺让参与本社区的体验对所有人都不存在骚扰，
无论其年龄、体型、可见或不可见的残障、族裔、性别特征、性别认同与表达、经验水平、
教育程度、社会经济地位、国籍、个人外貌、种族、宗教或性认同与性取向。

我们承诺以有助于建立开放、友好、多元、包容和健康社区的方式行事和互动。

## 我们的标准

有助于营造积极环境的行为包括：

* 对他人表现出同理心和善意
* 尊重不同的意见、观点和经历
* 给出并优雅地接受建设性反馈
* 承担责任，向受我们错误影响的人道歉，并从经验中学习
* 关注的不仅是个人，更是整个社区的最佳利益

不可接受的行为包括：

* 使用性化的语言或意象，以及任何形式的性关注或性挑逗
* 挑衅、侮辱或贬损性评论，以及人身或政治攻击
* 公开或私下的骚扰
* 未经明确许可，公布他人的私人信息，如物理地址或电子邮件地址
* 其他在专业环境中可被合理认定为不当的行为

## 执行责任

社区领导者负责澄清和执行我们可接受行为的标准，并将对其认为不当、威胁、
冒犯或有害的任何行为采取适当且公平的纠正措施。

社区领导者有权并有责任删除、编辑或拒绝与本行为准则不符的评论、提交、代码、
wiki 编辑、issue 和其他贡献，并在适当时说明理由。

## 适用范围

本行为准则适用于所有社区场合，也适用于个人在公共场合正式代表社区时。
代表社区的示例包括：使用官方电子邮件地址、通过官方社交媒体账号发布、
在线上或线下活动中担任指定代表。

## 执行

如遇辱骂、骚扰或其他不可接受的行为，请向负责执行的社区领导者报告：
**conduct@lsekit.dev**（若该邮箱尚未启用，请通过
<https://github.com/LseKit> 主页上的联系方式私信维护者）。

所有投诉都将及时、公平地审查和调查。

所有社区领导者都有义务尊重任何事件报告者的隐私和安全。

## 执行准则

社区领导者将遵循以下社区影响准则，确定违反本行为准则的任何行为的后果：

### 1. 纠正

**社区影响**：使用了不当语言或其他被认定为不专业或不受欢迎的行为。

**后果**：社区领导者发出私下的书面警告，说明违规性质，并解释为何该行为不当。
可能会要求公开道歉。

### 2. 警告

**社区影响**：通过单次事件或一系列行为造成的违规。

**后果**：警告并说明继续该行为的后果。在指定时间内，不得与相关人员互动，
包括主动与执行本行为准则的人员互动。这包括避免在社区场合以及社交媒体等外部渠道的互动。
违反这些条款可能导致临时或永久封禁。

### 3. 临时封禁

**社区影响**：严重违反社区标准，包括持续的不当行为。

**后果**：在指定时间内，禁止与社区进行任何形式的互动或公开交流。
在此期间，不允许与相关人员进行任何公开或私下互动，包括主动与执行本行为准则的人员互动。
违反这些条款可能导致永久封禁。

### 4. 永久封禁

**社区影响**：表现出违反社区标准的模式，包括持续的不当行为、骚扰个人，
或对某类个人的攻击或贬低。

**后果**：永久禁止在社区内进行任何形式的公开互动。

## 归属

本行为准则改编自 [Contributor Covenant][homepage]，版本 2.1，
可在 <https://www.contributor-covenant.org/version/2/1/code_of_conduct.html> 获取。

社区影响准则参考了 [Mozilla 的行为准则执行阶梯][mozilla]。

关于本行为准则的常见问题，请见 <https://www.contributor-covenant.org/faq>。
译本见 <https://www.contributor-covenant.org/translations>。

[homepage]: https://www.contributor-covenant.org
[mozilla]: https://github.com/mozilla/inclusion
````

#### 5.3 `.github/CODEOWNERS`

```text
# CODEOWNERS —— 自动指派审查人
# 语法：<路径模式> <@用户或@组织/团队>
# 后出现的规则覆盖先出现的。

# 默认：所有文件
*                       @LseKit

# 代码
/internal/              @LseKit
/cmd/                   @LseKit
/Scripts/               @LseKit

# 契约与决策（改动需格外小心）
/docs/Contract/         @LseKit
/docs/Decisions/        @LseKit
/AGENTS.md              @LseKit
/SECURITY.md            @LseKit

# CI 与仓库配置
/.github/               @LseKit
/go.mod                 @LseKit
/go.sum                 @LseKit
/Makefile               @LseKit
/.golangci.yml          @LseKit
```

### 6 测试代码

无 Go 测试。验收靠 §7。

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc

# 🔴 C12：期望值已按骨架实际值改正（原写 4，实际 1；理由见 §3 的修正说明）
$ grep -c 'Contributor Covenant' CODE_OF_CONDUCT.md
1

$ grep -c '版本 2.1' CODE_OF_CONDUCT.md
1

$ grep -c 'contributor-covenant.org' CODE_OF_CONDUCT.md
4

# 🔴 C12：原写 10，骨架实际 13 行
$ grep -c '@LseKit' .github/CODEOWNERS
13

# 🔴 原写 2，骨架实际 3 行（命令表 1 行 + 「不会产生计费调用」1 行 + 「提 PR 之前」代码块 1 行）
$ grep -c 'make check' CONTRIBUTING.md
3

$ grep -c 'feat(' CONTRIBUTING.md
1

$ ls -1 CONTRIBUTING.md CODE_OF_CONDUCT.md .github/CODEOWNERS
CONTRIBUTING.md
CODE_OF_CONDUCT.md
.github/CODEOWNERS
```

> 🔴 上面 4 个数字（1 / 4 / 13 / 3）都是**从 §5 的骨架数出来的实测值**，不是估计。核对方法：把 §5.2 的 ` ```` ` 之间的内容单独存成一个文件再 `grep -c`。改骨架 MUST 重新数一遍并同步这里 —— 更好的做法是把判据写成「= 骨架里的行数」，别写死。

### 8 完成定义（DoD）

- [ ] §7 的 **8 条**命令达到期望输出（贴真实输出）
- [ ] `CODE_OF_CONDUCT.md` 是 Contributor Covenant v2.1 中文译文（未改写正文条款），归属段的名称/版本号/URL 三处齐全
- [ ] `CODEOWNERS` 路径全部真实存在（`/Scripts/` 等目录已建）
- [ ] 提交：`docs(repo): 加入贡献指南、行为准则与 CODEOWNERS`

---

## Task-13 完成后的整体验收

```bash
$ cd /mcp/DeepSeekSearch/GitHubSrc
$ ls -1a | grep -E '^(README|LICENSE|AGENTS|CHANGELOG|SECURITY|SUPPORT|CONTRIBUTING|CODE_OF_CONDUCT|\.gitignore|\.gitattributes|\.editorconfig)'
AGENTS.md
CHANGELOG.md
CODE_OF_CONDUCT.md
CONTRIBUTING.md
LICENSE
README.md
SECURITY.md
SUPPORT.md
.editorconfig
.gitattributes
.gitignore
$ git log --oneline | head -3
```

**11 个根目录文件齐备，且每个都不是占位符。**

> 🔴 这 11 个**不是**「A 级文件总数」。`Contract/18-仓库文件契约.md` §二 的 A 级是 **12 个**，其中 `LICENSE`、`go.mod`、`go.sum`、`Makefile`、`config.example.toml` 由 `Task-01` 交付；上面这条 `ls` 数到 11，是因为它只列根目录里**由本任务覆盖或新建的 10 个** + `Task-01` 留下的 `LICENSE`，而 `go.mod`/`go.sum`/`Makefile`/`config.example.toml` 不在这条 `grep -E` 的模式里，`CODEOWNERS` 又在 `.github/` 下。
>
> 发版判据 MUST 用动态口径：`bash Scripts/CheckRepoFiles.sh .` 输出的 `A 级 A_OK/A_TOTAL` 与 `B 级 B_OK/B_TOTAL` 全等（当前 `A_TOTAL` = 12、`B_TOTAL` = 17，以脚本清单行数为准，MUST NOT 手抄）。
