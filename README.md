# DeepSeekSearch

把 DeepSeek 的 [Search / Fetch API](https://api-docs.deepseek.com/zh-cn/guides/search)
包装成一个 MCP 服务器。一个静态二进制，两种传输。

## 快速开始

1. 复制配置样例：`cp config.example.toml Config/config.toml`
2. 填 Key：把 `DEEPSEEK_API_KEY=...` 写入 `Config/.env`（权限 `600`）
3. 起服务：`./DeepSeekSearch -transport stdio`（或 Streamable HTTP，见下）

## 两种传输

| 传输 | 启动命令 | 适用 |
|---|---|---|
| `stdio` | `./DeepSeekSearch -transport stdio` | 本地客户端直连 |
| Streamable HTTP | `./DeepSeekSearch -transport streamable-http -config Config/config.toml` | 多客户端共享 / 网关 |

`stdio` 客户端配置片段（由客户端拉起本进程，标准输入输出即协议通道）：

```json
{
  "mcpServers": {
    "deepseek-search": {
      "command": "/absolute/path/to/DeepSeekSearch",
      "args": ["-transport", "stdio"]
    }
  }
}
```

Streamable HTTP 客户端配置片段（先自行起服务，客户端用 URL 连）：

```json
{
  "mcpServers": {
    "deepseek-search": {
      "url": "http://127.0.0.1:8765/mcp"
    }
  }
}
```

## 工具

| 工具 | 作用 |
|---|---|
| `ds_search` | 网页搜索，支持 site/lang/after/before/精确短语 |
| `ds_fetch` | 正文抓取，大文档按标题分节读取 |
| `ds_health` | 本地诊断：Key / 上游连通 / 缓存 / 生效配置 |

## 已知限制

<!-- 9 条，与 Handover/91-验收总表.md §四 逐条对应（那是唯一真源）。
     第 1 条是用户最容易撞上的：ds_search 返回的结果里有相当比例来自抓不到的站点，
     于是「搜到了却 fetch 不出来」会被当成 bug 报上来。 -->

1. **部分主流站点抓不到** —— 实测 15 个主流站点里 **6 个失败（约 40%）**：维基百科（中/英）、知乎、Reddit、Hacker News、`raw.githubusercontent.com` 全部塌缩为 404，与真 404 不可区分。`ds_search` 的结果里有相当比例来自这些站点，因此 `ds_search → ds_fetch` 工作流会频繁失败（`docs/Contract/23-上游能力实测报告.md` §4.3）
2. 不支持 PDF / XML / SVG / ZIP —— 上游返回 404（同上 §4.3）
3. 无法区分 403 / 404 / 500 —— 上游统一塌缩为 404（同上 §4.3、`14-错误契约` §2.3）
4. 上游抓取有 **30 秒硬超时** —— 超大单页返回 504，重试通常也无效（同上 §4.6，实测 30.126s / 30.131s）
5. 不做结果重排序 —— 上游已按 `rerank_score` 降序（`16-技术债登记表` N3）
6. 不做答案合成 —— 由调用方（LLM）自行组织，保留引用（`ADR-010`）
7. 不做 SSE 传输 —— MCP 2026-07-28 只定义 stdio 与 Streamable HTTP（`ADR-002`）
8. `min_rerank` / `min_authority` 容易过度过滤 —— 冷门 query 的正常结果普遍低于 1.5，设置后可能返回 0 条，故默认 `null`
9. `ori_question` 实测几乎无效 —— 4 组对照里 3 组字节级相同，默认不传（`12-数据契约` §4.1.1）

## 文档

完整设计与契约在 [`docs/`](docs/)。版本历史见 [CHANGELOG.md](CHANGELOG.md)。

## 许可

MIT
