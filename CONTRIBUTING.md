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
| `make check` | **上面七个的总和**（收尾只需这一条） |
| `make test-integration` | 真实调用上游，**会计费**，只在明确需要时跑 |

`make test` 与 `make check` **不会**产生任何计费调用；计费测试全部藏在 `-tags integration` 后面。

## 提 PR 之前

```bash
make check
```

七项全绿，然后逐条勾 PR 模板里的清单。

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
