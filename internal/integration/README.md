# L3b 真实上游集成测试

这一层是**唯一会真实计费**的测试：每次 `ds_search` / `ds_fetch` 约 ¥0.005（¥5 / 1000 次）。

## 运行

```bash
cd GitHubSrc
go test -tags integration ./internal/integration/ -v -timeout 300s
# 或
make test-integration
```

- Key 只从部署根 `Config/.env` 的 `DEEPSEEK_API_KEY` 读取；读不到时全部 `t.Skip`。
- 每个用例都能单独 `-run TestXxx` 运行。
- 单轮总上游调用 ≤ **10** 次（`e2e_integration_test.go` 的 `maxUpstreamCalls`）。
- 文件头之后、`package` 之前 MUST 有 `//go:build integration`；漏掉会让默认 `go test ./...` 产生计费调用（铁律 T1）。

## 用例

| 用例 | 断言 |
|---|---|
| `TestSearch_Real` | `ds_search` count=3，≥1 条结果且 `rerank_score` 降序 |
| `TestFetch_Real` | `ds_fetch example.com`，正文与 `cache_id` 非空 |
| `TestFetch_SnapshotReuse` | 同一 `cache_id` 取 `section` 不重新抓取（`cache_id` 不变 + `from_cache=true`） |
| `TestStdioE2E` | SDK client 连 `-transport stdio`，`tools/list` + `tools/call` |
| `TestProtocol_P17_P18` | P17 stdio 原始管道只有 JSON-RPC 帧；P18 `json_response=false` 的 SSE 首包 < 1s |
| `TestVersionConsistency_P26` | `/healthz` = `serverInfo` = `--version` |

> P18 的负向验证（把 `statusWriter.Flush` 改成空实现）在 `Task-11` A-02 的单元用例完成；这里不重复，避免再产生一次计费调用。
