# 90 · Qoder 执行手册

> **文件路径**：`/mcp/DeepSeekSearch/Doc/Handover/90-Qoder执行手册.md`
> **用途**：开发机上的执行模型**照着做就能把项目做完**。这份是操作手册，不是设计文档。
> **创建日期**：2026-10-20
> **适用**：全新 IDE 窗口 + 已 checkout 空仓库 + 一个模型干全部活

---

## 〇、🔴 开工前第一件事：确认当前处于哪个阶段

本项目有**两个阶段**，手册的读法完全不同：

| 阶段 | 判据 | 你该读什么 |
|---|---|---|
| **阶段 0 · 文档加固** | ~~`GitHubSrc/` 不存在~~ ✅ **已于 2026-10-20 完成** | 结果：`CheckConsistency` OK 90 / FAIL 0、`CheckDocDrift` 17 项全过、独立交叉核对 0 条问题。剩余项（`92` 号 §七）**全部非阻塞**，不影响编码 |
| **阶段 1 · 编码实现** | **`GitHubSrc/` 不存在也没关系 —— 现在就是从这里开始** | 读本手册全文，按 §三 的顺序表逐个原子任务开窗口。**第一个窗口做 Task-01 A-01**（它负责创建 `GitHubSrc/`），做完后 `GitHubSrc/` 才存在 |

**为什么有这个区分**：2026-10-20 的文档审计发现了 7 项「照文档逐字执行必然失败」的 P0 缺陷与 42 项数字矛盾，正在按台账逐项修复。**在台账清零之前进入编码阶段，执行窗口会反复撞上验收红灯**，而最可能的反应是「改检查凑绿」—— 那会摧毁整套护栏。

```bash
cd /mcp/DeepSeekSearch
[ -d GitHubSrc ] && echo "阶段 1：读 90 号手册全文" || echo "阶段 0：先读 92-审计修复台账.md"
bash Scripts/CheckDocDrift.sh Doc . ; echo "drift EXIT=$?"
python3 Scripts/CheckConsistency.py ; echo "consistency EXIT=$?"
```

---

## 一、开工前：把这份手册读完（10 分钟）

### 1.1 你将面对什么

| 事实 | 应对 |
|---|---|
| 有 **14** 个 Task 文件（Task-01~14），每个含 2~3 个原子任务（共 34 个） | 🔴 **一个 Task 文档 = 一个窗口**，窗口内按顺序做完该文档的全部原子任务。**但 commit 粒度仍然是「一个原子任务一次」** —— 见下方「窗口内的三条纪律」 |
| 文档总量约 200KB | **不需要全文读**。每个原子任务的「必读」只列了它真正需要的 1~3 个文档的 1~3 个章节 |
| 你会想「顺手把下一个 Task 文档也做了」 | 🔴 不要。**跨文档**会累积上下文噪音，导致幻觉出文档里没有的字段。同一文档内的原子任务是同一套契约，跨文档则换了整套接口 |
| 你会觉得某些约束「太啰嗦」 | 每一条约束后面都贴着实测数据（现集中在 `Contract/23-上游能力实测报告.md`）。删掉约束 = 重新踩一遍已经踩过的坑 |
| 你会发现文档之间对不上（数字、命名、错误码） | 🔴 **MUST NOT 自行裁决后继续**。先查 `92-审计修复台账` §三 看是否已登记；未登记就追加到队尾并在交接日志里写明，**不要当场改** |

### 1.2 目录约定（写错就返工）

```
/mcp/DeepSeekSearch/
├── GitHubSrc/                ← 唯一推到 GitHub 的部分，里面全部小写
│   ├── cmd/deepseeksearch/
│   ├── internal/{domain,config,deepseek,resilience,tools,logging,transport}/
│   ├── testdata/
│   ├── docs/                 ← 由 Doc/ 单向同步，MUST NOT 手写
│   ├── Scripts/
│   └── go.mod go.sum Makefile .golangci.yml LICENSE README.md CHANGELOG.md
├── Bin/ Config/ Data/ Logs/ Doc/ Scripts/ Ecosystem/   ← 部署侧，全部 PascalCase
```

🔴 **`GitHubSrc/` 内全部小写**（Go 包目录惯例）；**外层全部 PascalCase**。MUST NOT 为了「统一」改。

### 1.2.1 🔴 git 仓库根 = `GitHubSrc/`（2026-10-20 裁决 A）

| 事实 | 规定 |
|---|---|
| 仓库根 | **`/mcp/DeepSeekSearch/GitHubSrc/`**（`.git` 就在这一层） |
| 谁在仓库里 | 只有源码与发布物：`cmd/ internal/ testdata/ docs/ Scripts/` + 根级 A/B 级文件 |
| 谁不在仓库里 | `Doc/`、部署根的 `Scripts/`、`Bin/ Config/ Data/ Logs/` |
| 提交命令 | 所有 `git` 操作 MUST 在 `GitHubSrc/` 内执行 |

**为什么**：`Contract/18-仓库文件契约.md` 的 A/B 级文件路径都是相对**仓库根**写的（`README.md`、`AGENTS.md`、`go.mod`、`Makefile` 等），而 `90` 号与 `Task-13` 都把它们放在 `GitHubSrc/` 下。若在部署根 `git init`，`git add -A` 会把 `Doc/` 一起提交，且 `AGENTS.md` 在仓库里的位置会变成 `GitHubSrc/AGENTS.md`（嵌套一层）—— 与 18 号 A3 的「根」不符，`CheckRepoFiles.sh` 与 `Release.sh` 全部失效。

> ⚠️ 部署根的 `AGENTS.md` 是**给 agent 加载的副本**（窗口开在部署根时才会被读到），它**不在仓库里**；仓库里的那份是 `GitHubSrc/AGENTS.md`。两份都由 `Doc/Contract/19-AGENTS.md全文.md` 生成、逐字节相同，由 `CheckDocDrift.sh` 第 8 项校验。
>
> 🔴 MUST NOT 在部署根再 `git init`。若已误建：确认零提交后 `mv .git GitHubSrc/.git` 即可（有提交的话先 `git bundle` 备份）。

### 1.3 环境准备（一次）

```bash
go version                 # 🔴 三机基线均为 go1.27.2（2026-10-21 统一；HPE / NUC / 开发机）
go env GOTOOLCHAIN         # 必须 local —— auto 会在缺工具链时静默联网下载，破坏可复现性
cd GitHubSrc && go mod download   # 拉取 tool 指令登记的四个工具
go tool golangci-lint version      # 必须 v2.x；版本由 go.mod 的 tool 指令锁定
# 🔴 MUST NOT 用全局 `golangci-lint`：机器上可能同时存在另一个版本（如 2.14.0），
#    本地用全局、CI 用 go tool 就会出现「本地绿、CI 红」。一律 `go tool <name>`。
git config user.name "..." && git config user.email "..."
```

| 机器 | 地址 | Go | 角色 |
|---|---|:-:|---|
| HPE | 10.10.10.6 | 1.27.2 | 部署目标 |
| NUC | 10.10.10.7 | 1.27.2 | 部署目标 |
| 开发机 | 10.10.10.245 | 1.27.2 | 开发 / 构建自测 |

> 🔴 发布产物 MUST 由 CI 构建**一次**（`GO_VERSION: "1.27.2"`），部署机只下载 + 校验 + install。
> 即使三机版本相同，各自构建仍会因构建时间、commit 等不同而产生不同二进制（`--version` 与 `SHA256SUMS` 都不一致）。

---

## 二、每一轮的标准动作（照抄）

```
┌─ 1. 开新窗口，把模型切到 Qwen3.8MAX（不要换模型）
│
├─ 2. 贴入下面对话模板（只改 Task 编号）
│
├─ 3. 模型读整个 Task 文档 → 逐个原子任务：
│      答三问 → 实现 → 跑该任务的验收命令 → 贴真实输出
│      → 你核对 DoD → git commit（一次一个原子任务）
│
├─ 4. 该文档最后一个原子任务完成后：跑全局护栏（§七）→ 关窗口
│
└─ 5. 下一份 Task 文档 → 回到 1
```

### 2.0 🔴 窗口内的三条纪律（放宽窗口粒度后，这三条是唯一的防线）

原设计是「一个原子任务一个窗口」，上下文天然隔离。现在改成一个文档一个窗口，**必须靠纪律补上隔离**：

| # | 纪律 | 为什么 |
|:-:|---|---|
| 1 | **commit 粒度不变**：每完成一个原子任务立刻 commit，MUST NOT 攒到文档末尾一起提交 | 窗口中途挂掉时，能精确回到最后一个已验收的状态；也让 `git bisect` 有意义 |
| 2 | **切换原子任务前 MUST 重新读下一个任务的八节**，MUST NOT 凭前一个任务的记忆写 | 这是上下文噪音的唯一实际来源。同一个文档里的原子任务虽然共用契约，但**入参、返回体、错误码、测试断言各不相同**，凭记忆写就会幻觉出不存在的字段 |
| 3 | **每个原子任务开工前仍要答三问**，答错就停 | 成本是「重读一节」，而写完再纠正的成本是「重写一个包」 |

### 2.1 对话模板（每份 Task 文档第一句话，逐字复制）

````text
你现在做完这一份 Task 文档里的全部原子任务。

项目根目录：/mcp/DeepSeekSearch
任务文件：Doc/Tasks/Task-<NN>-<名称>.md
本文件的原子任务：A-01 <标题> → A-02 <标题> → …（按文档里的顺序）

硬规则：
1. 读这份 Task 文档的全部原子任务，以及各原子任务「必读」里点名的文档章节。
   MUST NOT 读整个 Doc/ 目录，MUST NOT 提前读别的 Task 文档。
2. MUST NOT 修改 Doc/Contract/ 下的任何文件。契约是冻结的。
   发现文档之间对不上 → 停下来报告，MUST NOT 自行裁决后继续。
3. 按文档顺序逐个做，MUST NOT 跳序、MUST NOT 合并两个原子任务一起实现。
4. 🔴 每切换到一个新原子任务，MUST 重新读它的八节（0 前置 / 1 上下文 /
   2 要创建的文件 / 3 实现要求 / 4 禁止事项 / 5 参考骨架 / 6 测试代码 /
   7 验收命令），MUST NOT 凭上一个任务的记忆写。
5. 🔴 每完成一个原子任务就立刻 commit（用该任务给定的 commit message），
   MUST NOT 攒到文档末尾一起提交。
6. 每个原子任务完成前 MUST 逐条跑它自己的「7 验收命令」并贴真实输出。
   MUST NOT 说「应该通过了」。
7. 任何一条验收失败 → 贴失败原文并停下。MUST NOT 用改测试、加 t.Skip、
   放宽断言的方式让它变绿。
8. 按 Doc/Engineering/20-编码规范：命名、注释只写「为什么」、F-S1~F-S6 规模红线。
9. 所有字段名 / 错误码 / 配置键 MUST 与文档逐字一致，MUST NOT 发明。
10. 每个源文件带五字段文件头，作者恒为「小逸」（19-AGENTS.md全文 §5.5）。

节奏：先只做第一个原子任务的三个问题回答，等我确认后再写代码。
之后每完成一个原子任务，先贴：验收输出 + DoD 逐条 [x]/[ ] + git status，
等我确认后再进入下一个。

开始前只需回答第一个原子任务的三个问题（各一行）：
  Q1 本原子任务要创建/修改哪几个文件？
  Q2 哪一条约束最容易被写错？
  Q3 验收命令有几条？
````

### 2.2 每份 Task 文档结束的收尾模板

```
请做收尾：
1. 在 GitHubSrc/ 内贴出 git status --short 与 git log --oneline -<本文件的原子任务数>
2. 贴出本 Task 文档**每个原子任务**的 DoD 清单，逐条标注 [x] 或 [ ]，[ ] 的说明原因
3. 跑全局验证并贴真实输出（按当前阶段选一条，见下方阶段表）
4. 给出本文件每个原子任务的建议 commit message
```

### 2.2.1 🔴 收尾验证命令随阶段变化（贴错命令 = 必然报「命令不存在」）

| 阶段 | 用什么 | 为什么 |
|---|---|---|
| **Task-01 A-01**（Makefile 尚未创建） | `cd GitHubSrc && go build ./... && go vet ./...` | 该阶段**没有 Makefile**，`make lint` / `make test-race` 都返回 `No rule to make target` |
| **Task-01 A-02 之后、Task-11 之前** | `cd GitHubSrc && make lint && make test-race` | `make check` 还没定义 |
| **Task-11 之后** | `cd GitHubSrc && make check` | 一条覆盖 `build` `test-race` `lint` `cover-check` `size` `drift` `repo-files` |

另外，**每个写了代码的窗口**都应在收尾时多跑一条「代码 ↔ 文档」交叉比对（Task-11 之后它已包含在 `make check` 的 `drift` 里，之前要手动跑）：

```bash
cd /mcp/DeepSeekSearch/GitHubSrc
bash ../Scripts/CheckDocDrift.sh ../Doc .
```

> 为什么要从 `GitHubSrc/` 跑：从部署根跑 `Doc .` 时代码根是 `.`，第 7 项（`codes.go` 常量数 / 配置键 / 工具数）会 **SKIP**，等于没查。

**文档层两个护栏只在窗口改过文档时才跑**（正常执行窗口 MUST NOT 改 `Doc/`，所以通常不用）：

```bash
cd /mcp/DeepSeekSearch
python3 Scripts/CheckConsistency.py
bash Scripts/CheckDocDrift.sh Doc .        # 文档语境；第 7 项 SKIP 属预期
```

---

## 三、14 个 Task 的执行顺序（MUST 按此顺序）

| 顺序 | Task | 原子数 | 完成后你应该能做的验证 |
|:-:|---|:-:|---|
| 1 | Task-01 项目骨架与构建系统 | 3 | `make build` 出二进制，`--version` 打印 `v0.1.0` |
| 2 | Task-02 Domain 层 | 3 | `go test ./internal/domain/ -cover` ≥ 80% |
| 3 | Task-03 Config 层 | 3 | `-check` 能报出配置错误字段路径 |
| 4 | Task-04 日志与轮转 | 2 | `Logs/` 下出现按时间命名的日志文件 |
| 5 | Task-05 上游客户端 | 2 | mock 测试全绿（还不连真上游） |
| 6 | Task-06 限流重试预算熔断 | 3 | `-race` 干净，测试总耗时 < 3s |
| 7 | Task-07 缓存与 singleflight | 2 | 5 并发同 URL 只打 1 次上游 |
| 8 | Task-08 ds_search | 2 | `tools/list` 里有 `ds_search` |
| 9 | Task-09 ds_fetch | 2 | 大页面返回目录而非全文 |
| 10 | Task-10 ds_health 与 Resources | 2 | 能读 `ds://error-codes` |
| 11 | Task-11 Transport 与架构守护 | 3 | `curl /healthz` 有 JSON；`arch_test` 绿 |
| 12 | Task-12 Cmd 装配与发布 | 3 | `git tag -l` 显示 `v0.1.0` |
| **13** | **Task-13 发布文件与社区治理** | 2 | 根目录 11 个文件齐备（**注意：这不是 A 级总数**，A 级是 12 个，见 Task-13:16/952）；`CheckRepoFiles.sh` 报 `A 级 12/12`、`B 级 17/17`；`AGENTS.md` 与 19 号文档 diff 无差异 |
| **14** | **Task-14 `.github` 配置与 CI** | 2 | CI 有 **6 个核心 job**（`fmt` `lint` `size` `test` `cover` `drift`）+ **3 个附加**（`cross-build` `vuln` `win-smoke`）；`bash Scripts/CheckRepoFiles.sh .` 全绿；`make check` 一条跑完（7 项） |

**依赖关系**：`01 → 02 → 03 → 04 → 05 → 06 → 07 → 08 → 09 → 10 → 11 → 12`，然后 `13 → 14`。
MUST NOT 跳序。想并行会撞 import 循环与接口不一致。

> **为什么 13/14 排在最后**：它们检查的文件里有很多要引用代码产物（如 `Scripts/CheckSize.sh` 要在 Task-11 落地）。但**如果你只想先让仓库「看起来完整」，可以先跑 Task-13**（根目录文件不依赖代码），Task-14 再等 Task-11 之后。这是唯一允许的乱序。

---

## 四、必读文档的精确位置（省时间的读法）

| 你要做什么 | 只读这些 |
|---|---|
| 建目录、写 Makefile | `00-项目宪章` §三 规模红线、§五 依赖方向；`01-架构总览` §一 目录结构图；`Contract/17-版本与发布契约.md` §一 §二 |
| 写任何 Go 代码 | `Engineering/20-编码规范` §二 §三 §四（4 节，约 200 行） |
| 写任何测试 | `Engineering/21-测试规范` §一 §二 §三 §九（4 节） |
| 定义数据结构/字段 | `Contract/12-数据契约.md` 对应小节（**逐字抄**, 不许改写字段名） |
| 定义函数签名 | `Contract/11-接口契约`（**逐字抄**） |
| 定义错误码 | `Contract/14-错误契约` §二 §三 |
| 写配置项 | `Contract/13-配置契约` §二（模板）→ §三（详表） |
| 写传输层 | `Contract/15-协议契约` §四 §六 §七之二 §七之三 §十 |
| 加/改工具 | `Engineering/22-架构守护规则` §四（工具名与注解被测试写死） |
| 想「为什么不用 XX」 | `Decisions/ADR.md` 对应 ADR |
| 要建/改发布文件（README/AGENTS/SECURITY/CI） | `Contract/18-仓库文件契约.md` §二 §三；`Contract/19-AGENTS.md全文.md` |
| 想「这个功能什么时候做」 | `Decisions/16-技术债登记表`（阶段二的事**现在不做**） |

---

## 五、六条不能违反的硬规则（违反必返工）

| # | 规则 | 违反后果 |
|:-:|---|---|
| 1 | **一次只做一个原子任务，做完就关窗口** | 跨任务累积上下文 → 幻觉出不存在的字段 → 整个包重写 |
| 2 | **MUST NOT 修改 `Contract/` 下的任何文件** | 契约是冻结的。真需要改 → 停下来问，走 ADR |
| 3 | **MUST NOT 用「改测试」让测试变绿** | 规模/架构/契约测试是护栏，改它等于拆护栏 |
| 4 | **MUST NOT 自己发明字段名/错误码/配置键** | 三处文档与代码有 diff 检查，名字不一致直接红 |
| 5 | **MUST NOT 在 `Doc/` 与 `GitHubSrc/docs/` 两处手写同一内容** | `docs/` 是同步产物，手写会被覆盖 |
| 6 | **每个「测试通过」必须贴真实命令输出** | 没有输出的「通过」= 没测 |
| 7 | **每个源文件 MUST 带文件头，`作者` 恒为 `小逸`** | 法务声明。MUST NOT 出现 AI / 助手 / 生成工具署名。格式见 `AGENTS.md` §5.5 |

---

## 六、最常见的 8 个失败模式（提前知道能省半天）

| # | 现象 | 原因 | 处理 |
|:-:|---|---|---|
| 1 | 客户端连上了但什么都不响应 | 中间件包装 `ResponseWriter` 没实现 `Flush` + `Unwrap` | 见 `11-接口契约` §6.1，逐字抄 |
| 2 | 搜索结果日期差一天 | 直接用 UTC 取日期 | `Contract/12-数据契约.md` §二 D1：必须先 `In(cst8)` |
| 3 | 中文正文末尾出现 `�` | 按 byte 切片 | 用 `domain.SliceByRune` |
| 4 | 代码块里的 `\n` 变成真换行 | 反转义时删了所有反斜杠 | 只反转义实测全集 **32** 个字符（原 11 字符表漏了 21 种，ADR-013），见 `Contract/12-数据契约.md` §6.2 |
| 5 | 改配置不生效 | 优先级顺序写反 / `toml` tag 拼错 | `Config.Source(key)` 打印来源 |
| 6 | 5 个并发请求花了 5 次钱 | 没做 singleflight | Task-07 A-02，这是必需不是优化 |
| 7 | 反代后全部 403 | SDK 的 Host 校验 | `allow_non_local_host=true` **且** `auth_enabled=true` |
| 8 | `golangci-lint` 一次性报几百个 issue | 用 v1 配置格式跑了 v2 | 用 `20-编码规范` §1.1 的 v2 配置 |

---

## 七、每个 Task 完成后你自己要做的 3 个验证（不看模型自述）

```bash
# ① 真实编译 + 真实测试 + 静态检查
cd /mcp/DeepSeekSearch/GitHubSrc
make lint && make test-race

# ② 规模红线
# 🔴 MUST 两个参数：CheckSize.sh <代码根> <文档根>
#    只传一个时 DOC_ROOT 退化为默认的 Doc，在 GitHubSrc/ 下不存在 ⇒ 豁免台账加载不到
#    ⇒ 已登记的 internal/domain/errors.go（720 行）直接判 F-S1 FAIL，CI 恒红。
#    ⚠️ 注意它与 CheckDocDrift.sh 的参数顺序**相反**（那个是 <文档根> <代码根>）。
bash Scripts/CheckSize.sh . ../Doc

# ③ 文档漂移（Task-11 之后才有）
bash Scripts/CheckDocDrift.sh ../Doc .

# ④ 发布文件齐备（Task-13/14 之后才有）
bash Scripts/CheckRepoFiles.sh .
```

> 🔴 **`make check` 已经是这四条的合集**：`build` `test-race` `lint` `cover-check` `size` `drift` `repo-files`（7 项）。Task-14 之后直接跑 `make check` 即可，不必再逐条跑。
> ⚠️ `make check` MUST NOT 含 `test-integration`（会计费）。

全部绿才 commit。任何一条红 → 回到该 Task 的验收节，**自己**跑一遍对应命令看真实错误。

---

## 八、发布前最后一道门

```bash
cd /mcp/DeepSeekSearch/GitHubSrc
bash Scripts/Release.sh --dry-run      # 🔴 21 项逐条打印（真源：Contract/17 号 §七 的表行数）
```

🔴 21 项里第 9/10/11 项是版本一致性（`--version` / `/healthz` / `serverInfo.version`），这是发版第一号事故源 —— **三处必须同源**。项数 MUST 由脚本自己数 17 号 §七 的表行数得出，MUST NOT 在此处硬编码。

---

## 九、你不需要做的事（明确排除，节省时间）

| 不做 | 原因 |
|---|---|
| SSE 传输 | `ADR-002`：2026-07-28 只定义 stdio 与 Streamable HTTP |
| 结果重排序 / 加权 | `ADR` + `16-技术债` N3：上游已按 `rerank_score` 降序 |
| 固定阈值过滤 | N4：实测同阈值保留率 0%~100% |
| 答案合成 | `ADR-010` |
| Key 池 | `ADR-008` |
| 多租户 / BYOK | N7 |
| `ds_research` / `ds_extract` / `no_cache` / `SKILL.md` | DEBT-001~004，阶段二 |
| 手写 `docs/` | 由 `SyncDocs.sh` 单向生成 |

---

## 十、卡住时的处理顺序

1. **先看报错原文**，不要猜
2. 查本文档 §六（8 个常见失败模式）
3. 查 `Contract/14-错误契约`（错误码在说什么）
4. 查 `Decisions/ADR.md`（是不是「本来就这么设计的」）
5. 以上都不解决 → **停下来**，把报错原文 + 你已尝试的动作列出来问，MUST NOT 自己发明一套方案绕过
