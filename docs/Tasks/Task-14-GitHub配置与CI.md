# Task-14 · `.github` 配置与 CI

> **执行方式**：2 个原子任务，**每次开新窗口只做一个**。
> **创建日期**：2026-10-20
> **前置**：Task-13 已完成（根目录发布文件到位 —— 那 11 个是 `ls` 的输出，不是「A 级文件数」；A 级 12 个里另有 5 个由 Task-01 交付，见 `Contract/18-仓库文件契约.md` §二）。
> **另需**：Task-11 A-03 已完成（`Scripts/CheckSize.sh`、`Scripts/CheckDocDrift.sh`、`Scripts/SyncDocs.sh` 三个脚本存在）—— 否则 `size` / `drift` job 无从跑起。

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | GitHub 上的自动化全链路：CI **6 个核心 job + 3 个平台/漏洞 job**、tag 触发发布（带两道闸门）、CodeQL、Scorecard、依赖更新、issue/PR 模板；**全链 pin 版本** |
| 依赖 | Task-13 |
| 产出 | 打开 PR 会自动跑检查（含 4 平台交叉编译、govulncheck、Windows tzdata 冒烟）；打 tag 会自动出 4 平台产物 + SHA256SUMS + **一份** SBOM；issue 有表单 |
| 必读 | `Contract/18-仓库文件契约.md` §三 §四（含 §4.1 的 job 清单、§4.2 的产物与两道闸门、§4.6 的 label 映射、§8.1 的五段检查）；`Engineering/21-测试规范.md` §三（覆盖率门槛，含 `internal/tools` ≥70%）；`Engineering/22-架构守护规则.md` §五（两个脚本的**两参签名**）与 §七（CI 执行顺序）；`Contract/17-版本与发布契约.md` §七（21 项发布检查单 + 变量语法铁律） |

| 原子 | 标题 | 文件数 | 预警 |
|:-:|---|:-:|---|
| A-01 | `.github` 配置与 4 个 workflow | 8 | 🔴 workflow 的 `permissions` 漏了、工具用 `@latest`、action 用 tag 引用，都会吃 Scorecard 扣分 |
| A-02 | `CheckRepoFiles.sh` 与 Makefile 聚合 | 3 | 脚本路径写死会在 CI 里挂；两个检查脚本都是**两参**签名，漏参会假绿或恒红 |

---

## 原子任务 A-01 · `.github` 配置与 4 个 workflow

### 0 前置

- Task-13 完成
- 仓库已 checkout 且 `git remote` 指向 `github.com/LseKit/DeepSeekSearch`

### 1 上下文（自包含）

CI 的第一原则是**便宜的先跑**。格式问题 3 秒能发现，测试要 2 分钟，文档漂移要 5 秒。所以顺序是 `fmt → lint → size → test → cover → drift`，而不是反过来。

第二原则是**权限最小**。默认 `GITHUB_TOKEN` 在很多仓库里是读写全开，一旦某个第三方 action 被投毒，攻击者就能改仓库。所有 workflow MUST 显式写 `permissions:`，只有 `release` 和 `scorecard` 需要额外权限。

第三原则是**别把二进制提交进 git**。OpenSSF Scorecard 的 `Binary-Artifacts` 检查看到仓库里有可执行文件直接判 0 分。二进制只出现在 Release 附件。

### 2 要创建的文件

| 文件 | 内容来源 |
|---|---|
| `.github/dependabot.yml` | §5.1 |
| `.github/ISSUE_TEMPLATE/bug_report.yml` | §5.2 |
| `.github/ISSUE_TEMPLATE/feature_request.yml` | §5.3 |
| `.github/ISSUE_TEMPLATE/config.yml` | §5.4 |
| `.github/PULL_REQUEST_TEMPLATE.md` | §5.5 |
| `.github/release.yml` | §5.6 |
| `.github/workflows/ci.yml` | §5.7 |
| `.github/workflows/release.yml` | §5.8 |
| `.github/workflows/codeql.yml` | §5.9 |
| `.github/workflows/scorecard.yml` | §5.10 |

> 10 个文件。上表「文件数 8」是笔误，以本表为准：**10 个**。

### 3 实现要求

> 🔴 **验收命令 MUST 从 §3.1 的代码块复制，MUST NOT 从下表复制。** 下表在 Markdown 表格里，单元格内的竖线必须写成 `\|` 才能不切断表格 —— 而 `grep -E` 的单引号里 `\|` 是**字面竖线**，`'^  (fmt\|lint\|size):'` 匹配的是「真的含有反斜杠竖线的行」，永远数不到 6。GFM 渲染时会把 `\|` 反转义成 `|`（所以**看渲染后的页面**是对的），但执行窗口读的是**原始 Markdown**，照抄就是一条恒假的判据。

| # | 要求 | 验证（命令见 §3.1，编号对应） |
|:-:|---|---|
| 1 | 4 个 workflow **全部**显式写 `permissions:` | 命令 1 |
| 2 | `ci.yml` 有 **6 个核心 job**：`fmt` `lint` `size` `test` `cover` `drift`（另有 `cross-build` / `vuln` / `win-smoke` 三个不参与顺序链，见要求 15） | 命令 2 |
| 3 | `ci.yml` 有 `concurrency` 且 `cancel-in-progress: true` | 命令 3 |
| 4 | `drift` job 调 `CheckDocDrift.sh docs .`（**用 `docs` 不是 `Doc`**，CI 里没 `Doc/`） | 命令 4 |
| 5 | `release.yml` 只在 tag `v*` 触发 | 命令 5 |
| 6 | `release.yml` 产出 SBOM（**一份**，不是每平台一份） | 命令 6 |
| 7 | `codeql.yml` 语言是 `go` | 命令 7 |
| 8 | `scorecard.yml` 用 `ossf/scorecard-action`、`publish_results: true`，且 checkout 带 `fetch-depth: 0` | 命令 8 |
| 9 | `dependabot.yml` 两个 ecosystem：`gomod` + `github-actions` | 命令 9 |
| 10 | 两个 issue 模板是 **yml Forms**（不是 md） | 命令 10 |
| 11 | `config.yml` 里 `blank_issues_enabled: false` | 命令 11 |
| 12 | 全部 YAML 能被解析 | 命令 12 |
| 13 | 🔴 **全链无 `@latest`**：Go 工具版本由 `go.mod` 的 `tool` 指令 + `go.sum` 锁定，step 里用 `go tool <name>` 调用（**不出现 `go install pkg@version`**）。第三方 action 仍 MUST pin 到 40 位 digest | 命令 13 |
| 14 | 🔴 **全部第三方 action pin 到 40 位 commit digest**，且 `PIN_DIGEST_` 占位符零残留（取值方法见 §5.11） | 命令 14、15 |
| 15 | 🔴 `ci.yml` 有 `cross-build`（4 平台编译矩阵）、`vuln`（govulncheck）、`win-smoke`（Windows 跑 `--version` 与 `-check`）三个 job | 命令 16 |
| 16 | 🔴 `ci.yml` 的 `on:` 里有 `workflow_call`，`release.yml` 的发布 job 有 `needs: [verify-tag, ci]` | 命令 17、18 |
| 17 | 🔴 `release.yml` 有 `verify-tag` job，用 `git rev-parse --verify` 校验 tag 存在**且是附注 tag** | 命令 18、19 |
| 18 | 🔴 `cover` job 的逐包门槛含 `internal/tools:70`（`Task-08`/`Task-09` 的承诺） | 命令 20 |
| 19 | 🔴 `drift` job 除 `CheckDocDrift.sh` 外还跑 `CheckRepoFiles.sh` 与 `SyncDocs.sh --check` | 命令 21 |
| 20 | 🔴 `size` job 用**两参**签名 `CheckSize.sh . docs`（漏第二个参数会加载不到豁免台账，job 恒红） | 命令 22 |

### 3.1 验收命令（🔴 MUST 从这里复制，不要从 §3 的表格复制）

```bash
cd GitHubSrc

# 1 四个 workflow 都显式声明 permissions
grep -l 'permissions:' .github/workflows/*.yml | wc -l          # → 4

# 2 ci.yml 恰好 6 个核心 job（ERE 交替，竖线不转义）
grep -cE '^  (fmt|lint|size|test|cover|drift):' .github/workflows/ci.yml   # → 6

# 3 并发取消
grep -c 'cancel-in-progress: true' .github/workflows/ci.yml     # → 1

# 4 文档漂移用 docs 而不是 Doc
grep -c 'CheckDocDrift.sh docs \.' .github/workflows/ci.yml     # → 1

# 5 release 只在 tag v* 触发
grep -c 'v\*' .github/workflows/release.yml                     # → ≥1

# 6 SBOM 一份（命名里 MUST NOT 再出现 <os>_<arch>）
grep -c 'sbom.json' .github/workflows/release.yml               # → ≥1
grep -c '<os>_<arch>.sbom' .github/workflows/release.yml        # → 0

# 7 CodeQL 语言
grep -c 'languages: go' .github/workflows/codeql.yml            # → 1

# 8 Scorecard：发布结果 + full clone
grep -c 'publish_results: true' .github/workflows/scorecard.yml # → 1
grep -c 'fetch-depth: 0' .github/workflows/scorecard.yml        # → 1

# 9 dependabot 两个 ecosystem
grep -c 'package-ecosystem' .github/dependabot.yml              # → 2

# 10 issue 模板是 Forms
ls -1 .github/ISSUE_TEMPLATE/*.yml | wc -l                      # → 3

# 11 关空白 issue
grep -c 'blank_issues_enabled: false' .github/ISSUE_TEMPLATE/config.yml   # → 1

# 12 全部 YAML 可解析
python3 -c "import yaml,glob; [yaml.safe_load(open(f)) for f in glob.glob('.github/**/*.yml',recursive=True)]; print('YAML OK')"

# 13 🔴 全链无 @latest
grep -rn '@latest' .github/workflows/ | wc -l                   # → 0

# 14 🔴 全部 uses: 都 pin 到 40 位 digest（两个数字 MUST 相等）
# 🔴 本地复用 workflow（uses: ./.github/workflows/ci.yml）不是第三方 action，不参与 digest pin
grep -rhoE 'uses: [^ ]+' .github/workflows/ | grep -v 'uses: \./' | wc -l    # → N（远程 uses 总数）
grep -rhoE 'uses: [A-Za-z0-9._/-]+@[0-9a-f]{40}' .github/workflows/ | wc -l   # → 同一个 N

# 15 🔴 占位符零残留（提交前 MUST 全部替换掉）
grep -rn 'PIN_DIGEST' .github/workflows/ | wc -l                # → 0

# 16 🔴 三个新 job 都在
grep -cE '^  (cross-build|vuln|win-smoke):' .github/workflows/ci.yml   # → 3

# 17 🔴 ci.yml 可被复用调用
grep -c 'workflow_call:' .github/workflows/ci.yml               # → 1

# 18 🔴 发布 job 有 needs
grep -c 'needs: \[verify-tag, ci\]' .github/workflows/release.yml      # → 1

# 19 🔴 tag 存在性校验
grep -c 'git rev-parse --verify' .github/workflows/release.yml  # → 1
grep -c 'git cat-file -t' .github/workflows/release.yml         # → 1

# 20 🔴 tools 的覆盖率门槛
grep -c 'internal/tools:70' .github/workflows/ci.yml            # → 1

# 21 🔴 drift job 跑三个脚本
grep -c 'CheckRepoFiles.sh' .github/workflows/ci.yml            # → 1
grep -c 'SyncDocs.sh --check' .github/workflows/ci.yml          # → 1

# 22 🔴 size job 两参签名
grep -c 'CheckSize.sh \. docs' .github/workflows/ci.yml         # → 1

# 23 🔴 二进制/密钥没被提交（ERE 交替，竖线不转义）
git ls-files | grep -cE '^(Bin/|.*\.exe$|.*\.test$)' || true    # → 0
git ls-files | grep -c 'Config/.env' || true                    # → 0
```

> 🔴 **命令 23 也是同一类错误的高发区**：原文写作 `git ls-files \| grep -cE '^(Bin/\|.*\.exe$)'`，其中第一个 `\|` 是 Markdown 表格转义（管道符），第二个 `\|` 在 ERE 里是**字面竖线** ⇒ 这个正则实际在找「以 `Bin/` 开头、或以 `.*\.exe$` 结尾」之外的东西，恒为 0 —— 也就是说**即使真有人把 `Bin/DeepSeekSearch.exe` 提交进来，这条判据也是绿的**。这是「假绿」而不是「假红」，比数不到 6 更危险。

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 省略 `permissions:`（默认权限可能过宽） |
| 2 | MUST NOT 在 `pull_request_target` 里 checkout PR 的代码（这是最经典的投毒路径，Scorecard `Dangerous-Workflow` 直接判 0） |
| 3 | MUST NOT 把 `Bin/` 提交进仓库（`Binary-Artifacts` 判 0） |
| 4 | MUST NOT 在 CI 里跑 `make test-integration`（会计费） |
| 5 | MUST NOT 用 `cron` 每天跑全量测试（浪费额度，只在 push/PR 与周计划跑 CodeQL） |
| 6 | MUST NOT 在 `release.yml` 里用 `actions/upload-release-asset`（已废弃） |
| 7 | 🔴 MUST NOT 用 `@latest` 装任何 Go 工具（gofumpt / golangci-lint / gocyclo / govulncheck）。理由见 §5.7 的 `env:` 注释：Scorecard 扣分、v2→v3 配置格式断代、同一 commit 两次结果不一致。🔴 **也 MUST NOT 用 `go install pkg@<固定版本>`** —— 那样版本号在 `ci.yml` 里而 dependabot 管不到，等于承诺人工按月跟进；MUST 走 `go.mod` 的 `tool` 指令（Task-01 A-01 §3 第 2.1 条） |
| 8 | 🔴 MUST NOT 用 tag 引用（`@v4`）第三方 action，MUST pin 到 40 位 commit digest。tag 可以被强推覆盖，digest 不能 |
| 9 | 🔴 MUST NOT 提交含 `PIN_DIGEST` 占位符的 workflow（§3.1 命令 15 会拦）；也 MUST NOT 从别的仓库抄一个 digest 充数 |
| 10 | 🔴 MUST NOT 让发布 job 没有 `needs`，也 MUST NOT 让 `workflow_dispatch` 的手输 tag 不经过存在性校验 —— 否则可以在 Actions 页面绕过 `Release.sh` 的 21 项检查与整个 CI 发版 |
| 11 | MUST NOT 把 `cross-build` / `vuln` / `win-smoke` 的名字写成 `fmt`/`lint`/`size`/`test`/`cover`/`drift` 之一 —— `CheckRepoFiles.sh` 用「恰好 6 个」判 ci.yml 的核心 job 数，重名会让判据失真 |
| 12 | MUST NOT 在 `win-smoke` 里用 `--version` 代替 `-check` 来验 tzdata：`--version` 在装配第 2 步就返回，**走不到**第 3 步的 `time.LoadLocation`（`Contract/11-接口契约.md` §七），测了等于没测 |

### 5 参考骨架（完整内容，逐字使用，MUST NOT 改写）

> 🔴 **唯一的例外：`PIN_DIGEST_vN` 占位符**。四个 workflow 里所有第三方 action 的 `uses:` 都写成 `PIN_DIGEST_<tag>` 形式，**落地时 MUST 逐个替换成 40 位 commit digest**（取值方法见 §5.11，验收见 §3.1 命令 14/15）。本文档 MUST NOT 直接写出一串 digest：① 写死就会过期，过期就没人敢动；② 更危险的是**抄来的 digest 可能指向别的版本甚至别的项目**，而它「看起来完全正常」—— pin digest 的全部意义就在于那串十六进制是本地现取的。
> 同理，`env:` 里四个 Go 工具的版本号是**起始值**，落地时 MUST 用 `go list -m -versions <module>` 复核（见 §5.7 注释）。除此之外骨架 MUST 逐字使用。

#### 5.1 `.github/dependabot.yml`

```yaml
# 依赖自动更新。两个 ecosystem 缺一不可：
#   - gomod：go.mod 里的 Go 依赖
#   - github-actions：workflow 里用到的 Action 版本
# 只开周更、限制并发 PR 数，避免刷屏把有用的 PR 埋掉。
version: 2

updates:
  - package-ecosystem: gomod
    directory: "/"
    schedule:
      interval: weekly
      day: monday
      time: "09:00"
      timezone: Asia/Shanghai
    open-pull-requests-limit: 5
    commit-message:
      prefix: "chore(deps)"
    groups:
      # 同一个生态的小版本合并成一个 PR，减少噪音
      go-minor-patch:
        patterns: ["*"]
        update-types: ["minor", "patch"]

  - package-ecosystem: github-actions
    directory: "/"
    schedule:
      interval: weekly
      day: monday
      time: "09:00"
      timezone: Asia/Shanghai
    open-pull-requests-limit: 5
    commit-message:
      prefix: "chore(ci)"
    groups:
      actions:
        patterns: ["*"]
```

#### 5.2 `.github/ISSUE_TEMPLATE/bug_report.yml`

```yaml
name: Bug 报告
description: 报告一个可复现的错误
title: "[bug] "
labels: ["bug", "triage"]
body:
  - type: markdown
    attributes:
      value: |
        感谢报告。开 issue 前请先确认：
        1. 已读 [已知限制](https://github.com/LseKit/DeepSeekSearch#已知限制)
        2. 已跑过 `DeepSeekSearch --version` 与 `-check`
        3. 日志里的 API Key 已替换成 `sk-***`

  - type: textarea
    id: version
    attributes:
      label: 版本与平台
      description: 贴 `DeepSeekSearch --version` 的完整输出
      placeholder: |
        DeepSeekSearch v0.1.0 (commit a1b2c3d, built 2026-10-20T13:05:12Z, go1.27.1)
        OS: Ubuntu 24.04 x86_64
    validations:
      required: true

  - type: dropdown
    id: transport
    attributes:
      label: 传输方式
      options:
        - stdio
        - streamable-http
    validations:
      required: true

  - type: dropdown
    id: tool
    attributes:
      label: 涉及哪个工具
      options:
        - ds_search
        - ds_fetch
        - ds_health
        - 服务启动 / 配置
        - 其他
    validations:
      required: true

  - type: textarea
    id: steps
    attributes:
      label: 复现步骤
      description: 越最小越好。能贴 curl 或工具调用的原始入参最好
      placeholder: |
        1. 配置 xxx
        2. 调用 ds_fetch，url=...
        3. 观察到的现象
    validations:
      required: true

  - type: textarea
    id: expected
    attributes:
      label: 期望行为
    validations:
      required: true

  - type: textarea
    id: logs
    attributes:
      label: 相关日志或错误体
      description: 工具返回的 JSON 错误体最有用（含 code / name / request_id）
      render: json
    validations:
      required: false

  - type: checkboxes
    id: checks
    attributes:
      label: 确认
      options:
        - label: 我已把日志中的 API Key 替换成 `sk-***`
          required: true
        - label: 这不是安全问题（安全问题请走 SECURITY.md 私密上报）
          required: true
```

#### 5.3 `.github/ISSUE_TEMPLATE/feature_request.yml`

```yaml
name: 功能需求
description: 提出一个新功能或改进
title: "[feat] "
labels: ["enhancement", "triage"]
body:
  - type: markdown
    attributes:
      value: |
        提需求前请先查两处，能省一轮来回：
        - [明确不做的功能](https://github.com/LseKit/DeepSeekSearch/blob/main/docs/Decisions/16-技术债登记表.md) §四
        - [已排期到阶段二的功能](https://github.com/LseKit/DeepSeekSearch/blob/main/docs/Decisions/16-技术债登记表.md) §二

  - type: textarea
    id: problem
    attributes:
      label: 你遇到的实际问题
      description: 描述场景，不要直接描述方案
      placeholder: 我在让 agent 做多页资料汇总，需要连着抓 5 个 URL，现在要调 5 次 ds_fetch
    validations:
      required: true

  - type: textarea
    id: proposal
    attributes:
      label: 你希望的方案
    validations:
      required: true

  - type: dropdown
    id: phase
    attributes:
      label: 你认为这属于哪个阶段
      options:
        - 阶段一（当前版本必须有）
        - 阶段二（可以等）
        - 不确定
    validations:
      required: true

  - type: input
    id: debt
    attributes:
      label: 如果对应已知技术债，填编号
      placeholder: DEBT-002

  - type: textarea
    id: alternatives
    attributes:
      label: 你考虑过的替代方案
    validations:
      required: false
```

#### 5.4 `.github/ISSUE_TEMPLATE/config.yml`

```yaml
# 关闭空白 issue —— 强制走模板，otherwise 一半 issue 缺少版本与复现步骤
blank_issues_enabled: false
contact_links:
  - name: 安全漏洞（不要开 issue）
    url: https://github.com/LseKit/DeepSeekSearch/security/advisories/new
    about: 请走私密上报，公开 issue 会直接关闭
  - name: 使用与配置问题
    url: https://github.com/LseKit/DeepSeekSearch/blob/main/docs/Contract/13-配置契约.md
    about: 配置每一项的含义、默认值、选了会怎样
  - name: 部署与故障排查
    url: https://github.com/LseKit/DeepSeekSearch/blob/main/docs/Ops/30-部署运维手册.md
    about: systemd、反向代理、症状 → grep → 结论
```

#### 5.5 `.github/PULL_REQUEST_TEMPLATE.md`

```markdown
## 这个 PR 做了什么

<!-- 一句话。对应哪个原子任务？ -->

- 对应 Task / 原子任务：`Task-NN / A-NN`
- 关联 issue：#

## 变更类型

- [ ] 新功能 `feat`
- [ ] 修 bug `fix`
- [ ] 只改文档 `docs`
- [ ] 重构（不改行为）`refactor`
- [ ] 只改测试 `test`
- [ ] 构建 / CI `build`

## 检查清单（逐条勾，缺一条会被打回）

- [ ] **只做了一个原子任务**（跨任务请拆 PR）
- [ ] `make check` 全绿（把输出贴在下面）
- [ ] 新增的 `exported` 符号都有 `Test<Name>`
- [ ] 契约有变更的话，`docs/Contract/` 已同步（不是只改代码）
- [ ] 没有提交 `Bin/` `.env` `Data/` `Logs/` 或任何二进制
- [ ] 注释写的是「为什么」而不是「做什么」，中文，Go doc 形式
- [ ] 没有为了让测试变绿而修改测试 / 加 `//nolint` / 加豁免

## `make check` 输出

```
（粘贴真实输出，不要写「应该通过了」）
```

## 需要 reviewer 特别注意的地方

<!-- 有没有反直觉的取舍？有没有为了绕开某个坑而写的怪代码？ -->
```

#### 5.6 `.github/release.yml`

```yaml
# 自动生成 Release notes 时的分类规则。
# 依据 PR 上的标签分组；`skip-changelog` 标签的 PR 不出现在 release notes 里。
# 🔴 GitHub 只支持「按 label 分组」，没有「按 commit message 前缀分组」这个能力，
#    所以 CONTRIBUTING.md 的 commit type 词表与本文件的 label 词表 MUST 有一张
#    显式映射表 + 明确「谁打 label」—— 见 Contract/18-仓库文件契约.md §4.6。
#    没有那张表时，generate_release_notes 产出的条目基本全落在「其他变更」。
changelog:
  exclude:
    labels:
      - skip-changelog
    authors:
      - dependabot
  categories:
    - title: 新增
      labels:
        - feat
        - enhancement
    - title: 修复
      labels:
        - fix
        - bug
    - title: 文档
      labels:
        - docs
    - title: 构建与 CI
      labels:
        - build
        - ci
    - title: 依赖更新
      labels:
        - dependencies
    - title: 其他变更
      labels:
        - "*"
```

#### 5.7 `.github/workflows/ci.yml`

```yaml
# 主 CI：6 个**核心** job（顺序按「便宜的在前」）+ 3 个平台/漏洞 job。
#   核心：格式 → 静态检查 → 规模 → 测试 → 覆盖率 → 文档漂移与发布文件
#   附加：cross-build（跨平台编译矩阵）/ vuln（govulncheck）/ win-smoke（Windows 冒烟）
# 全部 job 只读权限：这个 workflow 不需要写仓库。
# 🔴 本文件 MUST 可被 release.yml 用 workflow_call 调用（发布前强制重跑一遍 CI），
#    所以下面的 on: 里有 workflow_call。
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
  workflow_dispatch:
  workflow_call:            # 供 release.yml 的 `needs: ci` 调用

# 同一分支新提交取消旧跑，省额度
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

env:
  # 🔴 MUST pin **完整补丁版本**（`1.27.2`），MUST NOT 写 `1.27`。
  #    写 `1.27` 时 setup-go 每次解析到当时的最新补丁 ⇒ 同一个 commit 在不同时间
  #    构建出的二进制 `--version` 里 go 段不同（1.27.2 / 1.27.3 …），发布产物不可复现。
  #    当前取值依据：NUC（部署目标）为 go1.27.2，三台机器里最新；升补丁版本 = 一次显式提交。
  GO_VERSION: "1.27.2"
  # 🔴 工具版本 MUST pin 到**具体版本号**，MUST NOT 用 @latest。三重后果：
  #    ① Scorecard 的 Pinned-Dependencies 大面积扣分，本任务 §8 那句「Supply-Chain
  #       都能拿到分」就不成立；
  #    ② golangci-lint@latest 将来出 v3 时，v2 的配置格式会**直接红 CI**
  #       （Task-13 的 CONTRIBUTING 自己就警告过「v1 配置格式已废弃」这类事故）；
  #    ③ 同一个 commit 两次 CI 结果可能不一致 ⇒ 无法复现。
  #    🔴 **但版本号不再写在这里** —— 四个 Go 工具（gofumpt / golangci-lint / gocyclo /
  #    govulncheck）已改为 `go.mod` 的 `tool` 指令管理（见 Task-01 A-01 §3 第 2.1 条），
  #    step 里用 `go tool <name>` 调用，版本由 go.mod 的 require 行决定。
  #    **为什么改**：`go install pkg@version` 的版本号 dependabot **管不到**（它只看
  #    go.mod / package.json 这类清单文件），原设计等于承诺「按月人工跟进四个版本号」——
  #    那正是最容易烂掉的维护项。改成 tool 指令后 dependabot 的 `gomod` ecosystem
  #    自动开 PR 跟进，且 `go.sum` 里有哈希 ⇒ 供应链可验证，Scorecard 的
  #    Pinned-Dependencies 也满意（go.sum 锁定等价于 digest pin）。
  #    ⚠️ 落地时 MUST 先 `go get -tool mvdan.cc/gofumpt@v0.12.0`（其余三个同理）
  #    写入 go.mod，MUST NOT 手写 tool 块而不同步 require/go.sum。
  # 🔴 第三方 action MUST pin 到 **40 位 commit digest**（`@v4` 这种 tag 引用不算 pin）。
  #    下面所有 uses: 行的 `PIN_DIGEST_vN` 都是**占位符**，落地时 MUST 全部替换；
  #    取值方法见本原子任务 §5.11。验收命令（§7）会检查占位符零残留。

jobs:
  fmt:
    name: 格式（gofumpt）
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4          # 🔴 换成 40 位 digest
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      # 🔴 不再 `go install ...@版本`：工具由 go.mod 的 tool 指令锁定，用 `go tool` 调用。
      #    版本跟进交给 dependabot（gomod ecosystem），不再靠人工按月改 env。
      - name: 下载工具链
        run: go mod download
      - name: 检查格式
        run: |
          out="$(go tool gofumpt -l .)"
          if [ -n "$out" ]; then
            echo "以下文件未格式化，请跑 gofumpt -w .："
            echo "$out"
            exit 1
          fi
          echo "格式检查通过"

  lint:
    name: 静态检查（golangci-lint）
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: 下载工具链
        run: go mod download
      - run: go tool golangci-lint run ./...

  size:
    name: 规模红线（F-S1/F-S3/F-S5）
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
      - name: 下载工具链
        run: go mod download
      # 🔴 两参签名：CheckSize.sh <代码根> <文档根>。CI 里文档根是 docs（同步产物），
      #    不是 Doc。只传一个参数时文档根退化成默认的 Doc ⇒ 豁免台账加载不到 ⇒
      #    已登记的 internal/domain/errors.go（720 行）会直接触发 F-S1 FAIL，job 恒红。
      # 🔴 CheckSize.sh 内部调 gocyclo 判 F-S3；改用 go tool 后，脚本 MUST 用
      #    `go tool gocyclo` 或依赖 PATH 里有 gocyclo。裁决：脚本里写成
      #    `GOCYCLO="${GOCYCLO:-gocyclo}"`，CI 这个 step 传 `GOCYCLO="go tool gocyclo"`，
      #    本地开发装了二进制时走默认值 —— 两边都不硬编码。
      - run: GOCYCLO="go tool gocyclo" bash Scripts/CheckSize.sh . docs

  test:
    name: 测试（-race）
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - run: go build ./...
      - run: go test -race -count=1 ./...

  cover:
    name: 覆盖率门槛
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: 总覆盖率 ≥ 65%
        run: |
          go test -count=1 -covermode=atomic -coverprofile=coverage.out ./...
          total="$(go tool cover -func=coverage.out | awk '/^total:/ {print substr($3,1,length($3)-1)}')"
          echo "总覆盖率: ${total}%"
          awk -v t="$total" 'BEGIN { if (t+0 < 65) { printf "覆盖率不足：%.1f%% < 65%%\n", t; exit 1 } }'
      # 🔴 逐包门槛。原来只查 domain/config/resilience 三个 80%，而 Task-08 与 Task-09
      #    都承诺 internal/tools ≥70% ⇒ 承诺没有牙齿（21-测试规范 §三 也是这个口径）。
      #    阈值写在循环的第二个字段里，加一个包就加一行，MUST NOT 只改文档不改这里。
      - name: 逐包覆盖率门槛
        run: |
          set -euo pipefail
          for entry in "internal/domain:80" "internal/config:80" "internal/resilience:80" "internal/tools:70"; do
            pkg="${entry%%:*}"; want="${entry##*:}"
            go test -count=1 -covermode=atomic -coverprofile="/tmp/$(basename "$pkg").out" "./$pkg/"
            v="$(go tool cover -func="/tmp/$(basename "$pkg").out" | awk '/^total:/ {print substr($3,1,length($3)-1)}')"
            echo "$pkg: ${v}% (门槛 ${want}%)"
            awk -v t="$v" -v w="$want" -v p="$pkg" \
              'BEGIN { if (t+0 < w+0) { printf "%s 覆盖率不足：%.1f%% < %s%%\n", p, t, w; exit 1 } }'
          done
      # ⚠️ 逐包阈值判定 MUST 最终收敛到 `make cover-check`（归 Task-01 交付）。
      #    `make cover` 只跑 `go tool cover -func | tail -1` 打印总行，**无法**据此判定
      #    逐包门槛 —— 这正是 Contract/17-版本与发布契约 §七 第 10 项点名的问题。

  drift:
    name: 文档漂移与发布文件
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      # CI 里只有 docs/（同步产物），没有 Doc/。所以把 docs 当文档根，代码根为 .
      # 🔴 两参签名：CheckDocDrift.sh <文档根> <代码根>（Engineering/22-架构守护规则 §五）
      - run: bash Scripts/CheckDocDrift.sh docs .
      # 🔴 C13 新增：这两个脚本都是秒级，此前只在发版那一刻跑 ⇒
      #    AGENTS.md 漂移、docs/ 忘同步、A/B 级文件被误删，全都要等到打 tag 才暴露。
      - run: bash Scripts/CheckRepoFiles.sh .
      # CI 语境没有 Doc/，脚本会明确打印 [SKIP] 并退化为「docs/ 存在且非空」（真检查，
      # 能抓住「忘了提交 docs/」）；真正的比对由 Release.sh 第 18 项在发版机执行。
      - run: bash Scripts/SyncDocs.sh --check ../Doc .

  cross-build:
    name: 跨平台编译矩阵
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { goos: linux,   goarch: amd64 }
          - { goos: linux,   goarch: arm64 }
          - { goos: darwin,  goarch: arm64 }
          - { goos: windows, goarch: amd64 }
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      # 🔴 很便宜（只编译，不跑测试），但能提前几周发现「某平台编不过」。
      #    不加它，darwin/windows 产物要到 release 那一刻才**第一次**交叉编译。
      - name: 交叉编译
        run: |
          set -euo pipefail
          CGO_ENABLED=0 GOOS="${{ matrix.goos }}" GOARCH="${{ matrix.goarch }}" \
            go build -trimpath -o "/tmp/dss-${{ matrix.goos }}-${{ matrix.goarch }}" ./cmd/deepseeksearch
          ls -l "/tmp/dss-${{ matrix.goos }}-${{ matrix.goarch }}"

  vuln:
    name: 已知漏洞（govulncheck）
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: 下载工具链
        run: go mod download
      # 🔴 SECURITY.md 把「go.sum 里某个依赖的已知 CVE 影响本项目」列为**受理**的安全问题，
      #    而 CI 此前没有任何漏洞扫描 ⇒ 承诺没有牙齿。source 模式不需要先构建。
      - run: go tool govulncheck -mode=source ./...

  win-smoke:
    name: Windows 冒烟（守 time/tzdata）
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: 编译 windows/amd64
        shell: pwsh
        run: |
          $env:CGO_ENABLED = "0"
          go build -trimpath -o dss.exe ./cmd/deepseeksearch
      - name: --version（不读配置，Contract/17 §3.1 V3）
        shell: pwsh
        run: |
          ./dss.exe --version
          if ($LASTEXITCODE -ne 0) { throw "--version 退出码 $LASTEXITCODE" }
      - name: -check（🔴 验证 import _ "time/tzdata" 真的生效）
        shell: pwsh
        run: |
          # Windows runner **没有系统 IANA tzdata**。若 cmd 包漏了 import _ "time/tzdata"，
          # 装配第 3 步 time.LoadLocation("Asia/Shanghai") 会失败并 exit 1
          # （Contract/11-接口契约 §七、Contract/10-包依赖矩阵 §2.9 的 P0 条目）。
          # --version 在第 2 步就返回，**测不到** tzdata，所以 MUST 走到 -check。
          # 用假 Key：-check 不发起任何上游请求（不计费），但启动检查第 3 条要求 Key 非空。
          $env:DEEPSEEK_API_KEY = "sk-ci-smoke-not-a-real-key"
          # 默认 Config/.env 在 CI checkout 里不存在；applyDotEnv 会以 3040 拒绝启动
          New-Item -ItemType Directory -Force Config | Out-Null
          Set-Content -Path Config/.env -Value ""
          $out = ./dss.exe -check -config config.example.toml -env Config/.env 2>&1 | Out-String
          Write-Output $out
          if ($out -match "tzdata|unknown time zone|Asia/Shanghai") {
            throw "tzdata 未编进二进制：Windows 上 LoadLocation 失败"
          }
          if ($LASTEXITCODE -ne 0) { throw "-check 退出码 $LASTEXITCODE" }
```

#### 5.8 `.github/workflows/release.yml`

```yaml
# tag 推送触发发布。
# 产物：4 平台二进制 + SHA256SUMS + **一份** SBOM（SPDX JSON）。
#   🔴 SBOM 只有一份，不是「每平台一份」：anchore/sbom-action 扫的是**依赖图**
#     （go.mod / go.sum），而四个平台产物的依赖图完全相同 —— CGO_ENABLED=0 +
#     纯 Go SQLite 驱动 ⇒ 没有任何平台专属依赖。原注释描述了一个本 workflow
#     根本没实现的行为（见 Contract/18-仓库文件契约 §4.2）。
# 二进制只进 Release 附件，MUST NOT 提交进 git。
# 🔴 两道闸门（Contract/18-仓库文件契约 §4.2）：
#   ① verify-tag —— workflow_dispatch 可以手输任意 tag，而 softprops/action-gh-release
#      对**不存在**的 tag 会自动创建 tag + release ⇒ 不校验就能凭空发一个版本；
#   ② needs: ci —— tag 推送不触发 ci.yml 的 push.branches 规则，CI 根本不会跑
#      ⇒ 红代码也能发版。必须用 workflow_call 显式调一遍。
name: Release

on:
  push:
    tags:
      - "v*"
  workflow_dispatch:
    inputs:
      tag:
        description: "要发布的 tag（MUST 是仓库里已存在的**附注** tag，如 v0.1.0）"
        required: true

permissions:
  contents: write     # 创建 Release 与上传附件

env:
  # 🔴 与 ci.yml 的 GO_VERSION 逐字相同（完整补丁版本，理由见 ci.yml 顶部注释）。
  GO_VERSION: "1.27.2"

jobs:
  # ---------------------------------------------------------------- 闸门 ①
  verify-tag:
    name: 校验 tag 真实存在
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
        with:
          fetch-depth: 0        # 需要全部 tag 与历史

      - name: tag 存在性 + 附注 tag 校验
        run: |
          set -euo pipefail
          tag="${{ github.event.inputs.tag || github.ref_name }}"
          if ! git rev-parse --verify --quiet "refs/tags/${tag}" >/dev/null; then
            echo "❌ tag ${tag} 不存在，拒绝发布。"
            echo "   softprops/action-gh-release 对不存在的 tag 会自动创建 tag + release，"
            echo "   那等于绕过 Release.sh 的 21 项检查与整个 CI。"
            echo "   发版 MUST 先按 Contract/17-版本与发布契约 §五 打附注 tag 并推送。"
            exit 1
          fi
          # 附注 tag 的 object 类型是 tag；轻量 tag 是 commit（Release.sh 第 20 项要求附注）
          if [ "$(git cat-file -t "refs/tags/${tag}")" != "tag" ]; then
            echo "❌ ${tag} 是轻量 tag，MUST 用 git tag -a 打附注 tag"
            exit 1
          fi
          echo "✅ ${tag} 是附注 tag，指向 $(git rev-list -n 1 "refs/tags/${tag}")"

  # ---------------------------------------------------------------- 闸门 ②
  ci:
    name: 发布前完整 CI
    uses: ./.github/workflows/ci.yml   # ci.yml 的 on: 里 MUST 有 workflow_call
    permissions:
      contents: read

  release:
    name: 交叉编译并发布
    runs-on: ubuntu-latest
    needs: [verify-tag, ci]
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true

      - name: 确认 tag 与 Makefile 的 VERSION 一致
        run: |
          tag="${{ github.event.inputs.tag || github.ref_name }}"
          want="$(grep -E '^VERSION[[:space:]]*:=' Makefile | awk '{print $3}')"
          echo "tag=$tag  Makefile VERSION=$want"
          [ "$tag" = "$want" ] || { echo "tag 与 Makefile VERSION 不一致，拒绝发布"; exit 1; }

      - name: 交叉编译 4 平台
        run: |
          set -euo pipefail
          tag="${{ github.event.inputs.tag || github.ref_name }}"
          mkdir -p dist
          for target in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
            os="${target%/*}"; arch="${target#*/}"
            out="dist/DeepSeekSearch_${tag}_${os}_${arch}"
            [ "$os" = "windows" ] && out="${out}.exe"
            echo "→ $out"
            CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
              go build -trimpath -ldflags "-s -w -X main.version=${tag} -X main.commit=${GITHUB_SHA::7} -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
              -o "$out" ./cmd/deepseeksearch
          done

      - name: 生成校验和
        run: cd dist && sha256sum DeepSeekSearch_* > SHA256SUMS && cat SHA256SUMS

      - name: 生成 SBOM（SPDX JSON，全项目一份）
        uses: anchore/sbom-action@PIN_DIGEST_v0
        with:
          path: ./
          format: spdx-json
          output-file: dist/DeepSeekSearch_${{ github.event.inputs.tag || github.ref_name }}.sbom.json
          artifact-name: sbom

      - name: 创建 Release
        uses: softprops/action-gh-release@PIN_DIGEST_v2
        with:
          tag_name: ${{ github.event.inputs.tag || github.ref_name }}
          name: ${{ github.event.inputs.tag || github.ref_name }}
          generate_release_notes: true     # 分组规则见 .github/release.yml 与 Contract/18 §4.6
          files: |
            dist/DeepSeekSearch_*
            dist/SHA256SUMS
            dist/*.sbom.json
          fail_on_unmatched_files: true
```

> 🔴 **`workflow_dispatch` 手输 tag 的攻击面**（C13）：`softprops/action-gh-release` 在 `tag_name` 指向一个不存在的 ref 时会**创建**它。原来的 release job 既没有 `needs`，也没有任何 tag 存在性校验 ⇒ 任何有 `Actions: write` 权限的人在 Actions 页面手输 `v9.9.9` 就能：① 凭空造一个 tag；② 造一个 Release；③ 附上**从当前 `main` HEAD 编译**的二进制（而 `Makefile` 的 `VERSION` 校验只在 tag 与 VERSION 相等时才过 —— 手输 `v0.1.0` 而仓库里已有 `v0.1.0` 时，`verify-tag` 会过，但 `release.yml` 的第 1 项与 `Release.sh` 第 5 项「`${VERSION}` 未被占用」会在正常发版流程里拦住它）。加 `verify-tag` + `needs: ci` 后，手输路径 MUST 同时满足「tag 已存在且是附注 tag」与「完整 CI 绿」，与 `git push --tags` 路径等价。

#### 5.9 `.github/workflows/codeql.yml`

```yaml
# 代码安全扫描。Go 用 CodeQL 的 go 语言包（编译型，会真的 build 一次）。
name: CodeQL

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
  schedule:
    - cron: "23 3 * * 1"   # 每周一 03:23 UTC

permissions:
  contents: read

jobs:
  analyze:
    runs-on: ubuntu-latest
    permissions:
      security-events: write   # 上传 SARIF 结果必需
      actions: read
      contents: read
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
      - uses: github/codeql-action/init@PIN_DIGEST_v3
        with:
          languages: go
          queries: security-and-quality
      - uses: actions/setup-go@PIN_DIGEST_v5
        with:
          go-version: "1.27.2"
          cache: true
      - uses: github/codeql-action/autobuild@PIN_DIGEST_v3
      - uses: github/codeql-action/analyze@PIN_DIGEST_v3
        with:
          category: "/language:go"
```

#### 5.10 `.github/workflows/scorecard.yml`

```yaml
# OpenSSF Scorecard：供应链安全体检。
# 结果会发布到 scorecard.dev 的公开页面（需要仓库是 public）。
name: Scorecard

on:
  branch_protection_rule:
  schedule:
    - cron: "41 4 * * 2"   # 每周二 04:41 UTC
  push:
    branches: [main]

# 🔴 顶层 MUST 是 `{}`（deny-all）而不是 `read-all`。
#    Scorecard 的 Token-Permissions 检查按**顶层**判定：只要顶层写了 read-all，
#    下面 job 级的最小权限再严也会被判 high risk，得分直接扣掉。
#    也不能整行删掉 —— 删掉后生效的是仓库默认（老仓库常是 write-all），比 read-all 更糟。
#    `{}` = 默认一个令牌都不给，需要哪个由 job 自己声明。
permissions: {}

jobs:
  analysis:
    runs-on: ubuntu-latest
    permissions:
      security-events: write          # 上传 SARIF
      id-token: write                 # 用于签名发布结果
      contents: read
      actions: read
    steps:
      - uses: actions/checkout@PIN_DIGEST_v4
        with:
          persist-credentials: false
          # 🔴 MUST fetch-depth: 0（官方模板要求 full clone）。
          #    默认的 depth=1 会让 Fuzzing、活跃贡献者数、 commit 频率等检查失效 ⇒ 得分偏低，
          #    而且偏低的原因在 SARIF 里看不出来，只会以为「项目供应链差」。
          fetch-depth: 0

      - uses: ossf/scorecard-action@PIN_DIGEST_v2.4.0
        with:
          results_file: results.sarif
          results_format: sarif
          publish_results: true

      - uses: actions/upload-artifact@PIN_DIGEST_v4
        with:
          name: scorecard-results
          path: results.sarif
          retention-days: 5

      - uses: github/codeql-action/upload-sarif@PIN_DIGEST_v3
        with:
          sarif_file: results.sarif
```

#### 5.11 🔴 第三方 action 的 digest 取值方法（落地时 MUST 逐个替换 `PIN_DIGEST_vN`）

```bash
# 轻量 tag：直接取 object.sha
gh api repos/actions/checkout/git/ref/tags/v4.2.2 --jq '.object.sha'

# 附注 tag：上一步返回的 object.type == "tag"，MUST 再解引用一次才拿到 commit digest
gh api repos/actions/checkout/git/tags/<上一步的 sha> --jq '.object.sha'

# 替换后 MUST 保留 tag 注释，否则下一个人不知道这个 digest 是哪个版本：
#   - uses: actions/checkout@<40 位十六进制 digest>  # v4.2.2
#
# 🔴 MUST NOT 从别的仓库「抄一个看起来像的 digest」—— 那串十六进制必须是
#    用上面两条命令从**本仓库要用的那个 tag** 现取出来的，抄来的可能指向别的版本
#    甚至别的项目（这正是 Scorecard 要 pin digest 而不是 pin tag 的原因）。
```

| action | 出现位置 | 跟进方式 |
|---|---|---|
| `actions/checkout` | ci / release / codeql / scorecard | `.github/dependabot.yml` 的 `github-actions` ecosystem 会自动开 PR 更新 digest |
| `actions/setup-go` | ci / release / codeql | 同上 |
| `actions/upload-artifact` | scorecard | 同上 |
| `github/codeql-action/*` | codeql / scorecard | 同上 |
| `ossf/scorecard-action` | scorecard | 同上 |
| `anchore/sbom-action` | release | 同上 |
| `softprops/action-gh-release` | release | 同上 |

> 🔴 **Go 工具的版本（`env:` 那四个）dependabot 管不到** —— 它们不在 `go.mod` 里。两个可选的收口办法：① 每月手工跑一次 `go list -m -versions <module>`；② 把它们登记进 `go.mod` 的 `tool` 指令（Go 1.24+），这样 workflow 里可以写不带版本的 `go install mvdan.cc/gofumpt`，版本由 `go.mod`/`go.sum` 管、dependabot 的 `gomod` ecosystem 自动跟进。**推荐 ②**，但它需要改 `go.mod`（归 `Task-01`，已写进交付报告）。在 ② 落地前 MUST 用 ① 兜底，MUST NOT 退回 `@latest`。

### 6 测试代码

无 Go 测试。YAML 语法用 §7 的 Python 命令校验。

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc

$ ls -1 .github .github/workflows .github/ISSUE_TEMPLATE
.github:
CODEOWNERS
ISSUE_TEMPLATE
PULL_REQUEST_TEMPLATE.md
dependabot.yml
release.yml
workflows

.github/workflows:
ci.yml
codeql.yml
release.yml
scorecard.yml

.github/ISSUE_TEMPLATE:
bug_report.yml
config.yml
feature_request.yml

$ grep -l 'permissions:' .github/workflows/*.yml | wc -l
4

$ grep -cE '^  (fmt|lint|size|test|cover|drift):' .github/workflows/ci.yml
6

$ grep -c 'CheckDocDrift.sh docs' .github/workflows/ci.yml
1

$ grep -c 'publish_results: true' .github/workflows/scorecard.yml
1

$ grep -c 'package-ecosystem' .github/dependabot.yml
2

$ grep -c 'blank_issues_enabled: false' .github/ISSUE_TEMPLATE/config.yml
1

$ python3 -c "import yaml,glob,sys; [yaml.safe_load(open(f)) for f in glob.glob('.github/**/*.yml',recursive=True)]; print('YAML OK')"
YAML OK

# 🔴 C13 新增：pin 与发布闸门（完整清单见 §3.1，这里只列必须贴在交付记录里的 6 条）
$ grep -rn '@latest' .github/workflows/ | wc -l
0

$ grep -rn 'PIN_DIGEST' .github/workflows/ | wc -l
0

$ grep -cE '^  (cross-build|vuln|win-smoke):' .github/workflows/ci.yml
3

$ grep -c 'needs: \[verify-tag, ci\]' .github/workflows/release.yml
1

$ grep -c 'fetch-depth: 0' .github/workflows/scorecard.yml
1

$ grep -c 'internal/tools:70' .github/workflows/ci.yml
1
```

### 8 完成定义（DoD）

- [ ] §7 的 **14 条**命令达到期望输出（贴真实输出），且 §3.1 的 23 条全跑一遍
- [ ] 10 个文件全部存在
- [ ] 🔴 `grep -rn '@latest' .github/workflows/` 与 `grep -rn 'PIN_DIGEST' .github/workflows/` **都是 0**（前者证明工具已 pin 版本，后者证明占位符已全部换成真实 digest）
- [ ] 推送后在 GitHub 的 Actions 页看到 4 个 workflow
- [ ] 提一个 PR 时能看到 CI **9 个** job：6 个核心（`fmt` `lint` `size` `test` `cover` `drift`）+ `cross-build`（4 个矩阵腿）+ `vuln` + `win-smoke`
- [ ] 🔴 手工验证一次「手输不存在的 tag」：`workflow_dispatch` 输入 `v9.9.9` ⇒ `verify-tag` job 红、`release` job **不执行**（贴 Actions 截图或日志）
- [ ] 🔴 `win-smoke` 的 `-check` 步骤真的验到了 tzdata：临时把 `cmd` 包的 `import _ "time/tzdata"` 注释掉重跑，该 job MUST 变红（验证后恢复）
- [ ] 提交：`build(ci): 加入 CI、发布、CodeQL、Scorecard 与 issue 模板`

---

## 原子任务 A-02 · `CheckRepoFiles.sh` 与 Makefile 聚合

### 0 前置

- A-01 完成并提交
- Task-13 完成（根目录文件已齐）

### 1 上下文（自包含）

契约里写了「A 级文件缺一个不许打 tag」，但**写不等于执行**。本任务把它变成一个退出码：`Scripts/CheckRepoFiles.sh` 逐条检查，`Release.sh` 的**第 1 项**调它（`Contract/17-版本与发布契约.md` §七 第 1 项）—— 文件没齐，连「工作区是否干净」这一步都不用谈。

同时把 🔴 **7** 个检查聚合成 `make check` 一条命令（`build` `test-race` `lint` `cover-check` `size` `drift` `repo-files`）。没人愿意记 7 条命令。

🔴 **本脚本还是 `91-验收总表` 闸门 G4 的唯一实现**：G4 的判据是「A 级 12/12、B 级 17/17、**源文件作者全为 `小逸`**」。前半段是文件清单，后半段是**署名检查** —— 后者此前在 `Contract/18-仓库文件契约.md` 对脚本的功能描述里**完全没有对应段落**（已补为 §8.1 的第 ③ 段）。本任务的骨架里那一段就是它的实现。

### 2 要创建/修改的文件

| 文件 | 动作 |
|---|---|
| `GitHubSrc/Scripts/CheckRepoFiles.sh` | 新建（§5.1） |
| `GitHubSrc/Makefile` | 追加 `size` `drift` `repo-files` `check` 四个目标（§5.2） |
| `GitHubSrc/Scripts/Release.sh` | 核对第 1 项就是 `CheckRepoFiles.sh`（§5.3；**不是**插入「第 0 项」） |

### 3 实现要求

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | 🔴 脚本检查 `Contract/18-仓库文件契约.md` §二 的 **A 级**文件与 §三 的 **B 级**文件。**判据 MUST 是动态的**：`A_TOTAL` / `B_TOTAL` = 脚本里 `have A` / `have B` 的调用行数 = 18 号 §二 / §三 的表格行数（当前是 **12** 与 **17**）。MUST NOT 在任何文档里手抄这两个数字 —— 此前「11 个 A 级」「15 个 B 级」「12/17」三种说法并存，全部源于硬编码 | 输出 `A 级 A_OK/A_TOTAL` 与 `B 级 B_OK/B_TOTAL`，且 `A_TOTAL` = `grep -c '^| A[0-9]' 18 号 §二`、`B_TOTAL` = `grep -c '^| B[0-9]' 18 号 §三` |
| 2 | 检查 `AGENTS.md` 与 `docs/Contract/19-AGENTS.md全文.md` 的标记内内容一致 | 不一致时输出第一处差异行号 |
| 3 | 检查 `README.md` 无写死版本号 | §3.1 命令 |
| 4 | 检查 `ci.yml` 恰好 6 个**核心** job 且 `drift` 用 `docs`（`cross-build`/`vuln`/`win-smoke` 不计入这 6 个） | 见 A-01 §3.1 命令 2、4 |
| 5 | 检查没有二进制被提交（`git ls-files` 里无 `Bin/`、无 `*.exe`、无 `*.test`） | 见 A-01 §3.1 命令 23（🔴 ERE 交替 MUST 用真竖线，见该处的说明） |
| 6 | 检查 `.env` 未被提交 | `git ls-files` 里 `Config/.env` 命中数 = 0 |
| 7 | 脚本 `set -uo pipefail`（**不用 `-e`**，因为要收集全部失败） | `head -12` |
| 8 | 全部失败项收集完后一次性退出 1（不是第一个就退） | 造 2 个缺失时输出 2 条 FAIL |
| 9 | `make check` 依次跑 🔴 **7** 项（`build` `test-race` `lint` `cover-check` `size` `drift` `repo-files`），任一失败即停。⚠️ MUST 是 `cover-check` 而不是 `cover` —— 后者只打印总覆盖率，逐包门槛形同虚设 | `make check` 非 0 时打印 FAILED；且故意让某个包覆盖率不达标时 `make check` **必须**非 0 |
| 10 | 🔴 `Release.sh` 的**第 1 项**就是 `CheckRepoFiles.sh`（17 号 §七 已把它编为第 1 项，共 **21 项**）。本任务 MUST NOT 再插入「第 0 项」—— 那会让编号与 17 号的表错位 | `grep -n 'CheckRepoFiles' Scripts/Release.sh`；`grep -c '^step ' Scripts/Release.sh` = 21 |
| 11 | 🔴 **署名检查段**（18 号 §8.1 第 ③ 段 / 闸门 G4）：遍历 `*.go` 与 `*.sh`，排除 `./docs/` 与 `*_test.go`，断言有 `作者: 小逸` 行、前 10 行无 AI 署名、`作者:` 字段不是 AI 名字 | 把一个生产文件的作者行改成 `Hermes` ⇒ 输出一条 FAIL 且退出 1 |
| 12 | 🔴 **发版工具脚本段**（18 号 §8.2）：`Scripts/SyncDocs.sh`、`Scripts/Release.sh`、`Scripts/CheckRepoFiles.sh` 三者存在且有可执行位。它们**不计入** `A_TOTAL`/`B_TOTAL` | 删掉 `Scripts/SyncDocs.sh` ⇒ 输出一条 FAIL 且退出 1 |

### 3.1 验收命令

```bash
cd GitHubSrc

# A/B 级计数是动态的：与 18 号的表格行数比，不与硬编码数字比
grep -c '^| A[0-9]' ../Doc/Contract/18-仓库文件契约.md      # → A_TOTAL（当前 12）
grep -c '^| B[0-9]' ../Doc/Contract/18-仓库文件契约.md      # → B_TOTAL（当前 17）

# 🔴 ERE 交替 MUST 用真竖线。表格单元格里的 \| 是 Markdown 转义，
#    照抄进单引号就变成「字面竖线」，判据恒为 0 ⇒ 真提交了 Bin/ 也是绿的（假绿）。
git ls-files | grep -cE '^(Bin/|.*\.exe$|.*\.test$)' || true   # → 0
git ls-files | grep -c 'Config/.env' || true                   # → 0

# 署名段与工具脚本段真的有牙齿（负向验证，做完 MUST 删掉临时文件）
printf '#!/usr/bin/env bash\n# 作者: Hermes\n' > Scripts/_tmp_signtest.sh
bash Scripts/CheckRepoFiles.sh . | grep -c FAIL                 # → ≥1（两条：缺作者行 + 作者是 AI 名字）
rm -f Scripts/_tmp_signtest.sh

# 工具脚本段：删掉 SyncDocs.sh 就必须红
mv Scripts/SyncDocs.sh /tmp/ && bash Scripts/CheckRepoFiles.sh . | grep -c FAIL   # → ≥1
mv /tmp/SyncDocs.sh Scripts/
```

> ⚠️ **署名段会抓到一个既有问题**：`Engineering/22-架构守护规则.md` §3.1 给的 `CheckSize.sh` 骨架**只有 `# 文件名:` / `# 用途:` / `# 用法:` 三行注释，没有 `# 创建日期:` / `# 作者: 小逸` / `# 项目:`** ⇒ 逐字照抄它，本脚本的署名段就会对 `Scripts/CheckSize.sh` 报 FAIL。落地时 MUST 按 `Contract/19-AGENTS.md全文.md` H11 的五字段补齐文件头（`SyncDocs.sh`、`CheckRepoFiles.sh`、`CheckDocDrift.sh` 的骨架都是五字段齐全的，只有 22 号那份不是）。**22 号不归本任务改，已写进交付报告。**

### 4 禁止事项

| # | 禁止 |
|:-:|---|---|
| 1 | MUST NOT 用 `set -e`（第一个检查失败就退出，看不到其余问题） |
| 2 | MUST NOT 把 `dependabot` 判为「缺文件」（它是 B 级但可以后补 —— 本项目要求必须有，所以照常检查） |
| 3 | MUST NOT 在脚本里硬编码 `/mcp/DeepSeekSearch` 绝对路径（CI 里路径不同） |
| 4 | MUST NOT 让 `make check` 包含 `test-integration`（会计费） |
| 4.1 | 🔴 **`scorecard.yml` 的顶层 `permissions` MUST 是 `{}`，MUST NOT 是 `read-all`，也 MUST NOT 整行删掉**。Scorecard 的 `Token-Permissions` 检查按**顶层**判定 —— 顶层写 `read-all` 时，job 级再严的最小权限也会被判 high risk；而整行删掉会让**仓库默认**生效（老仓库常是 `write-all`，比 `read-all` 更糟）。`{}` = 默认不给任何令牌，需要哪个由 job 自己声明 |
| 5 | MUST NOT 在 `repo-files` 目标里跑 `git` 命令时假设 git 一定可用（`git ls-files` 失败要降级为跳过而不是崩溃） |

### 5 参考骨架（完整内容，逐字使用，MUST NOT 改写）

#### 5.1 `Scripts/CheckRepoFiles.sh`

```bash
#!/usr/bin/env bash
# 文件名: CheckRepoFiles.sh
# 用途: 检查 GitHub 发布文件是否齐备且满足硬规则（Contract/18 的 A 级与 B 级）。
#       任一项失败退出 1。收集全部失败后一次性输出，不在第一条就退出。
# 创建日期: 2026-10-20
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
# 用法: bash Scripts/CheckRepoFiles.sh [仓库根目录]
set -uo pipefail

ROOT="${1:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$ROOT" || { echo "无法进入 $ROOT"; exit 1; }

FAIL=0
A_OK=0; A_TOTAL=0
B_OK=0; B_TOTAL=0

ok()   { printf '  [OK]   %s\n' "$*"; }
bad()  { printf '  [FAIL] %s\n' "$*"; FAIL=1; }

# have <级别> <文件> <额外检查命令（可选）>
# 存在 + 额外检查通过 = OK；否则记一条 FAIL
have() {
    local lvl="$1" f="$2" extra="${3:-}"
    if [ "$lvl" = "A" ]; then A_TOTAL=$((A_TOTAL+1)); else B_TOTAL=$((B_TOTAL+1)); fi

    if [ ! -e "$f" ]; then
        bad "[$lvl] $f 缺失"
        return 1
    fi
    if [ -n "$extra" ]; then
        if eval "$extra" >/dev/null 2>&1; then :; else
            bad "[$lvl] $f 存在但不满足硬规则"
            return 1
        fi
    fi
    if [ "$lvl" = "A" ]; then A_OK=$((A_OK+1)); else B_OK=$((B_OK+1)); fi
    ok "[$lvl] $f"
    return 0
}

echo "=============================================="
echo " DeepSeekSearch 发布文件检查"
echo " 根目录: $ROOT"
echo "=============================================="
echo ""
echo "--- A 级：必须（缺一个不许打 tag）---"

have A "README.md"        '! grep -qE "v[0-9]+\.[0-9]+\.[0-9]+" README.md'
have A "LICENSE"          'grep -q "MIT License" LICENSE && grep -q "Permission is hereby granted" LICENSE && grep -q "LseKit/DeepSeekSearch" LICENSE'
have A "AGENTS.md"        'grep -q "^# AGENTS.md" AGENTS.md'
have A "CHANGELOG.md"     '[ "$(grep -c "^## \[" CHANGELOG.md)" -ge 2 ]'
have A "SECURITY.md"      'grep -qi "advisories/new" SECURITY.md'
have A "CONTRIBUTING.md"  'grep -q "make check" CONTRIBUTING.md'
have A "CODE_OF_CONDUCT.md" 'grep -q "Contributor Covenant" CODE_OF_CONDUCT.md'
have A ".gitignore"       'grep -q "Config/.env" .gitignore'
have A "go.mod"           'grep -q "^module github.com/LseKit/DeepSeekSearch$" go.mod'
have A "Makefile"         'grep -qE "^VERSION[[:space:]]*:=" Makefile'
have A "config.example.toml" '! grep -qE "(^|[^A-Za-z0-9])sk-[A-Za-z0-9]" config.example.toml'
have A "go.sum"

echo ""
echo "--- B 级：大厂标准 ---"

have B ".golangci.yml"
have B ".editorconfig"                 'grep -q "indent_style = tab" .editorconfig'
have B ".gitattributes"                'grep -q "eol=lf" .gitattributes'
have B "SUPPORT.md"
have B ".github/CODEOWNERS"
have B ".github/dependabot.yml"        '[ "$(grep -c "package-ecosystem" .github/dependabot.yml)" -eq 2 ]'
have B ".github/workflows/ci.yml"      '[ "$(grep -cE "^  (fmt|lint|size|test|cover|drift):" .github/workflows/ci.yml)" -eq 6 ]'
have B ".github/workflows/release.yml" 'grep -q "sbom" .github/workflows/release.yml'
have B ".github/workflows/codeql.yml"  'grep -q "languages: go" .github/workflows/codeql.yml'
have B ".github/workflows/scorecard.yml" 'grep -q "publish_results: true" .github/workflows/scorecard.yml'
have B ".github/ISSUE_TEMPLATE/bug_report.yml"
have B ".github/ISSUE_TEMPLATE/feature_request.yml"
have B ".github/ISSUE_TEMPLATE/config.yml" 'grep -q "blank_issues_enabled: false" .github/ISSUE_TEMPLATE/config.yml'
have B ".github/PULL_REQUEST_TEMPLATE.md"
have B ".github/release.yml"
have B "Scripts/CheckSize.sh"
have B "Scripts/CheckDocDrift.sh"

echo ""
echo "--- 源文件文件头与作者署名 ---"
HDR_MISS=0
while IFS= read -r f; do
    # 只看 .go 与 .sh 生产文件
    case "$f" in
        *_test.go) continue ;;
    esac
    if ! grep -q '^// 作者: 小逸$' "$f" && ! grep -q '^# 作者: 小逸$' "$f"; then
        bad "缺文件头作者行: $f"
        HDR_MISS=$((HDR_MISS+1))
    fi
    # 只查文件头前 10 行，避免误伤正文里正常提到的客户端名字
    # （AGENTS.md 里会列 Claude Desktop / Hermes / Qoder 作为 MCP 客户端，那不算署名）
    if head -10 "$f" | grep -qiE 'co-authored-by|generated by|ai 协助|ai 生成|ai-assisted'; then
        bad "出现禁止的 AI 署名: $f"
        HDR_MISS=$((HDR_MISS+1))
    fi
    if head -10 "$f" | grep -qE '作者:[[:space:]]*(Hermes|Claude|GPT|Qoder|AI)' ; then
        bad "作者字段被写成了 AI 名字: $f"
        HDR_MISS=$((HDR_MISS+1))
    fi
done < <(find . -name '*.go' -o -name '*.sh' | grep -v '^./docs/')
[ "$HDR_MISS" -eq 0 ] && ok "源文件文件头与作者署名合规"

echo ""
echo "--- 发版工具脚本（不计入 A/B 级，见 Contract/18-仓库文件契约.md §8.2）---"
# 🔴 SyncDocs.sh 此前**无任何 Task 交付**，却被 17 号 §七 第 18 项、19 号 X11、
#    91 号 12-A03-4、90 号手册引用 ⇒ 发版闸门指向一个不存在的脚本。
#    现由 Task-11 A-03 交付，这里 MUST 检查它存在且可执行；MUST NOT 静默放过。
TOOL_MISS=0
for s in Scripts/SyncDocs.sh Scripts/Release.sh Scripts/CheckRepoFiles.sh \
         Scripts/CheckSize.sh Scripts/CheckDocDrift.sh; do
    if [ ! -f "$s" ]; then
        bad "发版工具脚本缺失: $s"
        TOOL_MISS=$((TOOL_MISS+1))
    elif [ ! -x "$s" ]; then
        bad "发版工具脚本没有可执行位: $s（chmod 0755）"
        TOOL_MISS=$((TOOL_MISS+1))
    fi
done
[ "$TOOL_MISS" -eq 0 ] && ok "5 个发版工具脚本齐备且可执行"

echo ""
echo "--- 交叉一致性 ---"

# AGENTS.md 与 19 号文档的标记内内容必须字节相同
AGENTS_SRC=""
for cand in "docs/Contract/19-AGENTS.md全文.md" "../Doc/Contract/19-AGENTS.md全文.md"; do
    [ -f "$cand" ] && { AGENTS_SRC="$cand"; break; }
done
if [ -z "$AGENTS_SRC" ]; then
    echo "  [SKIP] 找不到 19-AGENTS.md全文.md（CI 里应有 docs/Contract/）"
elif [ ! -f "AGENTS.md" ]; then
    bad "AGENTS.md 缺失"
else
    tmp1="$(mktemp)"; tmp2="$(mktemp)"
    # 标记必须恰好出现 1 次，否则切片起点是错的
    NB="$(grep -c 'BEGIN AGENTS.md' "$AGENTS_SRC" || true)"
    [ "$NB" -eq 1 ] || { bad "19 号文档的 BEGIN 标记出现 $NB 次（必须 1 次）"; }
    # sed 删掉前导空行：标记后紧跟一个空行，直接输出会让 AGENTS.md 以空行开头
    awk '/<!-- BEGIN AGENTS.md -->/{f=1;next} /<!-- END AGENTS.md -->/{f=0} f' "$AGENTS_SRC" \
        | sed '/./,$!d' > "$tmp1"
    cp AGENTS.md "$tmp2"
    if diff -q "$tmp1" "$tmp2" >/dev/null 2>&1; then
        ok "AGENTS.md 与 19 号文档一致"
    else
        bad "AGENTS.md 与 19 号文档不一致："
        diff "$tmp1" "$tmp2" | head -20 | sed 's/^/         /'
    fi
    rm -f "$tmp1" "$tmp2"
fi

# 版本号三处同源（Makefile 是真源）
if [ -f Makefile ]; then
    V="$(grep -E '^VERSION[[:space:]]*:=' Makefile | awk '{print $3}')"
    SRC17=""
    for cand in "docs/Contract/17-版本与发布契约.md" "Doc/Contract/17-版本与发布契约.md"; do
        [ -f "$cand" ] && { SRC17="$cand"; break; }
    done
    if [ -z "$V" ]; then
        bad "Makefile 里找不到 VERSION 变量"
    elif [ -z "$SRC17" ]; then
        echo "  [SKIP] 找不到 17-版本与发布契约.md"
    elif grep -q "$V" "$SRC17"; then
        ok "Makefile VERSION=$V 与 17 号文档一致"
    else
        bad "17 号文档里找不到 VERSION=$V"
    fi
fi

# 不该被提交的东西
if git rev-parse --git-dir >/dev/null 2>&1; then
    if git ls-files | grep -qE '^(Bin/|.*\.exe$|.*\.test$)'; then
        bad "仓库里提交了二进制产物"
    else
        ok "无二进制产物被提交"
    fi
    if git ls-files | grep -q 'Config/.env'; then
        bad "仓库里提交了 Config/.env（密钥泄漏！）"
    else
        ok "无密钥文件被提交"
    fi
else
    echo "  [SKIP] 不是 git 仓库，跳过提交内容检查"
fi

echo ""
echo "=============================================="
echo " A 级: $A_OK/$A_TOTAL    B 级: $B_OK/$B_TOTAL"
if [ "$FAIL" -eq 0 ]; then
    echo " 结果: 全部通过"
else
    echo " 结果: 有缺失或不合规项，修完再发版"
fi
echo "=============================================="
exit "$FAIL"
```

#### 5.2 `Makefile` 追加内容

在原有目标（`build` `test` `test-race` `lint` `cover` `clean`）之后**追加**：

```makefile
# ---- 质量门禁 ----

.PHONY: size drift repo-files check

# 规模红线 F-S1 / F-S3 / F-S5（F-S2 / F-S4 由 internal/arch_test.go 用 go/ast 强制）
# 🔴 两参签名 <代码根> <文档根>。开发机在 GitHubSrc/ 下执行，文档根是 ../Doc；
#    漏掉第二个参数会让豁免台账加载不到 ⇒ domain/errors.go 的 720 行直接 F-S1 FAIL。
size:
	GOCYCLO="go tool gocyclo" bash Scripts/CheckSize.sh . ../Doc

# 文档漂移 8 项检查（本地开发时 Doc/ 与代码都在，直接把 Doc 当文档根）
# 🔴 两参签名 <文档根> <代码根>，与 size 的参数顺序**相反**，别抄串了
drift:
	bash Scripts/CheckDocDrift.sh ../Doc .

# GitHub 发布文件齐备性（Contract/18 的 A 级 + B 级 + 署名 + 工具脚本）
repo-files:
	bash Scripts/CheckRepoFiles.sh .

# 🔴 收尾只需这一条：编译 + 竞态测试 + 静态检查 + 覆盖率**门槛** + 规模 + 漂移 + 发布文件齐备
#    两处与原设计的差别，都是「写了但没被任何命令执行」的漏洞：
#    ① cover -> cover-check：`cover` 只打印一行总覆盖率，无法判定 21-测试规范 §三 的
#       **逐包**门槛（cover-check 已依赖 cover，所以覆盖率仍会生成）。
#    ② 补 repo-files：19-AGENTS.md全文 说「收尾时只需跑 make check」，而 A/B 级文件齐备
#       是闸门 G4 与 Release.sh 第 1 项的判据 —— 不纳入 check，就要等到打 tag 时才发现缺文件。
check: build test-race lint cover-check size drift repo-files
	@echo ""
	@echo "=============================================="
	@echo " make check 全部通过"
	@echo "=============================================="
```

> 🔴 **`make cover-check` 不在本任务**：`Contract/17-版本与发布契约.md` §七 第 10 项要求 `make cover-check` 做**逐包**阈值判定（domain/resilience/config ≥80%、deepseek/tools ≥70%、transport ≥60%、整体 ≥65%），而现有的 `make cover` 只跑 `go tool cover -func | tail -1` 打印总行 —— 逐包门槛**无法**据此判定。该目标归 **`Task-01`** 交付（本任务不改 Task-01，已写进交付报告）。在它落地前，A-01 的 `cover` job 里那段 `for entry in ...` 循环是逐包门槛的唯一实现，两边口径 MUST 一致。

#### 5.3 `Scripts/Release.sh` 的编号核对（🔴 不是「插入第 0 项」）

`Contract/17-版本与发布契约.md` §七 已经把发布检查单重排为 **21 项**，其中**第 1 项**就是 `bash Scripts/CheckRepoFiles.sh .`（第 2 项是 `Release.sh` 自身的 `$(VER` + `SION)` 字面量自检，第 3 项才是「工作区干净」）。

⇒ 本任务 **MUST NOT** 再在 `step 1` 之前插入「step 0」—— 那会让脚本编号与 17 号的表错位一项，而 17 号 §七 明确写着「本表项数是唯一真源」。要做的只是**核对**：

```bash
cd GitHubSrc

# 第 1 项就是发布文件检查
grep -n 'CheckRepoFiles' Scripts/Release.sh            # → 命中，且在 step 1 的位置

# 项数与 17 号 §七 的表格行数一致（动态判据，MUST NOT 手抄 21）
grep -c '^step ' Scripts/Release.sh
grep -c '^| [0-9]' ../Doc/Contract/17-版本与发布契约.md

# 分母写的是 /21 而不是 /16 或 /17
grep -n '%02d/' Scripts/Release.sh

# 🔴 第 2 项：Release.sh 自身 MUST NOT 出现命令替换形式的版本变量（17 号 §七 的
#    「变量语法铁律」）。bash 把它当「执行名为 VERSION 的命令」→ 展开为空串 →
#    grep -F "" 匹配一切 → 版本一致性检查**永远通过**（假绿）。这是本项目认定的
#    「发版第一号事故源」。
#    ⚠️ 自检命令本身要匹配那个字面量，而 CheckConsistency.py 的 K5 会扫描所有 bash
#    代码块并把该字面量判成误用 ⇒ 这里用字符串拼接构造模式，不把字面量写进代码块。
pat='$(VER''SION)'
grep -c -- "$pat" Scripts/Release.sh                    # → 0
```

> 原文写「把 `step() { printf '\n[%02d/16] %s\n' ... }` 改成 `/17`，全部 16 项编号顺延为 1~17」—— 那是 17 号还是 16/17 项时的旧口径。现在分母 MUST 是 **21**，且**项数以 17 号 §七 的表格行数为准**，脚本里的分母 MUST 用动态取值或在改动时同步（17 号 §七 自己就规定「MUST NOT 在别处硬编码数字」）。

### 6 测试代码

无 Go 测试。用 §7 的「故意破坏」验证脚本真的会红。

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc

# 1. 正常情况：全绿
$ bash Scripts/CheckRepoFiles.sh .
...
 A 级: 12/12    B 级: 17/17
 结果: 全部通过
$ echo "exit=$?"
exit=0

# 2. 故意删一个 A 级文件
$ mv SECURITY.md /tmp/ && bash Scripts/CheckRepoFiles.sh .; echo "exit=$?"
  [FAIL] [A] SECURITY.md 缺失
 A 级: 11/12    B 级: 17/17
 结果: 有缺失或不合规项，修完再发版
exit=1
$ mv /tmp/SECURITY.md .

# 3. 故意改坏 README（写死版本号）
$ echo "当前版本 v0.1.0" >> README.md
$ bash Scripts/CheckRepoFiles.sh . | grep FAIL
  [FAIL] [A] README.md 存在但不满足硬规则
$ git checkout README.md

# 4. 故意让 AGENTS.md 漂移
$ echo "" >> AGENTS.md && echo "多出来一行" >> AGENTS.md
$ bash Scripts/CheckRepoFiles.sh . | grep -A3 FAIL
  [FAIL] AGENTS.md 与 19 号文档不一致：
          > 多出来一行
$ git checkout AGENTS.md

# 5. make check 聚合
$ make check
...
 make check 全部通过

# 6. 🔴 故意让署名不合规（守 91 号闸门 G4 的后半段）
$ printf '#!/usr/bin/env bash\n# 作者: Hermes\n' > Scripts/_tmp_signtest.sh
$ bash Scripts/CheckRepoFiles.sh . | grep FAIL
  [FAIL] 缺文件头作者行: ./Scripts/_tmp_signtest.sh
  [FAIL] 作者字段被写成了 AI 名字: ./Scripts/_tmp_signtest.sh
$ rm -f Scripts/_tmp_signtest.sh

# 7. 🔴 故意删掉一个发版工具脚本（SyncDocs.sh 由 Task-11 A-03 交付）
$ mv Scripts/SyncDocs.sh /tmp/ && bash Scripts/CheckRepoFiles.sh . | grep FAIL; echo "exit=$?"
  [FAIL] 发版工具脚本缺失: Scripts/SyncDocs.sh
exit=1
$ mv /tmp/SyncDocs.sh Scripts/

# 8. 🔴 规模脚本的两参签名（漏第二个参数会加载不到豁免台账）
$ bash Scripts/CheckSize.sh .            # 只传一个参数
⚠️  豁免台账不存在: Doc/Engineering/豁免台账.md（本次不放行任何豁免）
F-S1 违规: internal/domain/errors.go 有 720 行 (上限 600)
$ bash Scripts/CheckSize.sh . ../Doc     # 正确
已加载规模豁免 1 条
✅ 规模红线全部通过（F-S1/F-S3/F-S5；F-S2/F-S4 见 TestSizeLimits）
```

> 场景 8 是**故意留下的对照实验**：它证明 `CheckSize.sh` 的第二个参数不是可选装饰。CI 的 `size` job 里 MUST 写 `bash Scripts/CheckSize.sh . docs`（CI 的文档根是同步产物 `docs/`）。

### 8 完成定义（DoD）

- [ ] §7 的 **8 组**场景全部达到期望（**贴真实输出，含四处「故意破坏」的验证**：README 版本号、AGENTS.md 漂移、署名、工具脚本缺失）
- [ ] `make check` 一条命令跑完 🔴 **7** 项（`build` `test-race` `lint` `cover-check` `size` `drift` `repo-files`）
- [ ] 🔴 `Scripts/Release.sh` 共 **21 项**（= `Contract/17-版本与发布契约.md` §七 的表格行数），**第 1 项**是发布文件检查，分母写 `/21`，且 `grep -c` 那个命令替换字面量的自检为 0
- [ ] `Scripts/CheckRepoFiles.sh` 的 `A_TOTAL` / `B_TOTAL` 与 18 号 §二 / §三 的表格行数**动态相等**（本文件与本脚本里都 MUST NOT 硬编码 12 / 17）
- [ ] 提交：`build(repo): 加入发布文件检查与 make check 聚合`

---

## Task-14 完成后的整体验收

```bash
$ cd /mcp/DeepSeekSearch/GitHubSrc
$ bash Scripts/CheckRepoFiles.sh . && make check
 A 级: 12/12    B 级: 17/17
 结果: 全部通过
...
 make check 全部通过
```

> 上面的 `12/12`、`17/17` 是**当前**清单行数下的输出快照，不是判据。判据是「`A_OK == A_TOTAL` 且 `B_OK == B_TOTAL`，而 `A_TOTAL`/`B_TOTAL` = `Contract/18-仓库文件契约.md` §二/§三 的表格行数」。

到这里，仓库在 GitHub 上同时满足三件事：

1. **社区健康度满分** —— community profile 的 6 项检查全绿
2. **供应链可评分** —— OpenSSF Scorecard 的 Security-Policy / Dependency-Update-Tool / Token-Permissions / CI-Tests / SAST / Packaging / 🔴 **Pinned-Dependencies** 都能拿到分。最后一项**只在全链 pin 之后才成立**：Go 工具用 `@latest`、action 用 `@v4` tag 引用时它会大面积扣分，那时这句话就是假的（见 A-01 §3 要求 13/14）
3. **agent 可工作** —— `AGENTS.md` 让任何编码 agent 拿到构建、测试、契约与禁止清单
