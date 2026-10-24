# Task-01 · 项目骨架与构建系统

> **执行方式**：本文档包含 3 个原子任务。**每次开新窗口只执行一个**，执行完提交（commit）后再开下一个窗口。
> **MUST NOT** 在一个窗口里连续做多个原子任务 —— 会累积幻觉。
> **创建日期**：2026-10-20

---

## 任务总览

| 项 | 内容 |
|---|---|
| 目标 | 建立可编译、可测试、可发版的最小骨架。**不含任何业务逻辑** |
| 依赖 | 无（这是第一个任务） |
| 产出 | `GitHubSrc/` 可 `make build` 出二进制，`make test` 绿（虽然只有占位测试） |
| 必读 | `00-项目宪章` §三 规模红线、§五 依赖方向；`01-架构总览` §一 目录结构图；`17-版本与发布契约` |

| 原子 | 标题 | 文件数 | 预警 |
|:-:|---|:-:|---|
| A-01 | 目录骨架与模块初始化 | 8 | 目录名大小写写错是最常见的失败 |
| A-02 | Makefile 与版本注入 | 3 | `-ldflags` 引号嵌套容易漏 |
| A-03 | 配置样例、README、CHANGELOG、LICENSE | 5 | README MUST NOT 写死版本号 |

---

## 原子任务 A-01 · 目录骨架与模块初始化

### 0 前置

- 已 `cd` 到项目根（`GitHubSrc/` 的父目录）
- `go version` ≥ `go1.27`

### 1 上下文（自包含）

本项目是一个 MCP 服务器。目录分两侧：

- **`GitHubSrc/`** —— 唯一推到 GitHub 的部分。里面**全部小写**（Go 包目录强制小写）
- **部署侧目录** —— 不进 git。**全部 PascalCase**（`Bin/ Config/ Data/ Logs/ Scripts/ Ecosystem/`）

这个大小写差异是**故意的**：Go 的 import path 不允许有大写包目录做惯例，而部署侧是给人看的。MUST NOT 为了「统一」把 `GitHubSrc/internal` 改成 `Internal`。

### 2 要创建的文件

```
GitHubSrc/
├── go.mod
├── .gitignore
├── LICENSE
├── cmd/deepseeksearch/
│   └── main.go                       # 只打印版本占位，A-02 接管
└── internal/
    ├── domain/doc.go                 # 包注释占位
    ├── config/doc.go
    ├── deepseek/doc.go
    ├── resilience/doc.go
    ├── tools/doc.go
    └── transport/doc.go
```

**MUST NOT 创建**：`testdata/`、`docs/`、`Makefile`（下一个原子任务）、任何 `*_test.go`（下一个原子任务）。

### 3 实现要求（逐条可验证）

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | `go.mod` 的 module 行**逐字**为 `module github.com/LseKit/DeepSeekSearch` | `head -1 go.mod` |
| 2 | `go.mod` 的 go 行**逐字**为 `go 1.27` | `grep '^go ' go.mod` |
| 2.1 | 🔴 **`go.mod` MUST 有 `tool` 指令块，登记四个开发工具**（`Task-14` 的 `ci.yml` 依赖它，用 `go tool <name>` 调用）：<br>`go get -tool mvdan.cc/gofumpt@v0.12.0`<br>`go get -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0`<br>`go get -tool github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0`<br>`go get -tool golang.org/x/vuln/cmd/govulncheck@v1.1.4`<br>**为什么 MUST 走 `tool` 指令而不是 CI 里 `go install pkg@版本`**：后者把版本号写在 `ci.yml` 的 `env:` 里，而 **dependabot 管不到 `go install`**（它只解析 go.mod / package.json 这类清单）⇒ 等于承诺「四个版本号按月人工跟进」，那是最容易烂掉的维护项。走 `tool` 指令后：① dependabot 的 `gomod` ecosystem 自动开 PR 跟进；② 版本进 `go.sum` 有哈希，供应链可验证，Scorecard 的 `Pinned-Dependencies` 满意；③ 本地与 CI 用的是**同一个**版本，不会出现「本地过、CI 红」。<br>⚠️ MUST 用 `go get -tool` 写入，**MUST NOT 手编 `tool` 块而不同步 `require` 与 `go.sum`** —— 那样 `go tool` 会报找不到工具。<br>⚠️ 这四个是**开发期工具**，不是运行时依赖：`Contract/10-包依赖矩阵.md` §2.10.1 的「运行时第三方 4 个」口径不受影响（那 4 个是 mcp go-sdk / toml / godotenv / sqlite）。<br>🔴 **为什么 golangci-lint 是 v2.15.0 而不是 v2.6.2**：v2.6.2 内置的 staticcheck v0.6.1 在 Go 1.27 的 `internal/poll` 上 panic（`unexpected expr: *ast.KeyValueExpr`），`make lint` 恒红；v2.15.0 带 staticcheck v0.8.2，实测 `0 issues`。升级同时把 `mvdan.cc/gofumpt` 由 v0.11.0 顶到 **v0.12.0**（MVS 取最大版本）—— 以实际 `go.mod` 为准，MUST NOT 反向 pin 回 v0.11.0（会与 golangci-lint 的依赖冲突） | `grep -A5 '^tool' go.mod`；`go tool gofumpt --version` |
| 3 | 6 个 `doc.go` 各有一行包注释，格式 `// Package <name> <中文说明>。`。🔴 包注释**不在第 1 行**：H11/§5.5 要求每个 `.go` 文件先写 5 字段文件头，包注释在第 7 行 | `grep -h '^// Package ' internal/*/doc.go \| wc -l` = **6**（MUST NOT 用 `head -1` —— 那只会看到 `// 文件名: doc.go`） |
| 4 | `internal/domain/doc.go` 的注释 MUST 含「MUST NOT 导入本项目其它包」这句约束 | `grep -c 'MUST NOT 导入本项目其它包' internal/domain/doc.go` = 1 |
| 5 | `.gitignore` 含 8 行：`Bin/`、`Config/.env`、`Data/`、`Logs/`、`coverage.out`、`coverage.html`、`*.log`、`.DS_Store` | `wc -l .gitignore` |
| 6 | `LICENSE` 用 **MIT**，版权行 `Copyright (c) 2026 小逸 <https://github.com/LseKit/DeepSeekSearch>` | `head -3 LICENSE` |
| 7 | `main.go` 的 `package` 是 `main`，有一行 `func main() { fmt.Println("DeepSeekSearch v0.0.0-dev") }` | `go run ./cmd/deepseeksearch` |
| 8 | `go build ./...` 成功 | 退出码 0 |
| 9 | 🔴 `go.mod` 里有 `tool` 指令块，且四个工具都能被解析到真实二进制（证明 require/go.sum 同步正确） | `go tool gofumpt --version` 退出码 0；`go tool -n gocyclo` 打印路径且退出码 0。🔴 **MUST NOT 用 `go tool gocyclo -h` 判退出码 0** —— Go 的 flag 库约定 `-h` 打印用法后 `exit 2`，那不代表同步失败 |
| 10 | 🔴 运行时第三方依赖 = 0（Task-01 阶段没有业务 import） | `go list -deps ./... \| grep -E '^[a-z0-9.-]+\.[a-z]{2,}/' \| grep -vc '^github.com/LseKit/'` = 0 |
| 9 | `go vet ./...` 零输出 | 退出码 0 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 创建 `pkg/`、`api/`、`models/`、`utils/`、`common/`、`helpers/` —— 未在架构总览里出现的目录一律不建 |
| 2 | 🔴 MUST NOT 在**非测试代码**里 `import` 任何第三方包 —— 白名单只有 4 个：mcp go-sdk、BurntSushi/toml、joho/godotenv、modernc.org/sqlite。<br>⚠️ **`go.mod` 里出现 `require` 块不是违规**：`tool` 指令（§3 第 2.1 条）必然把四个工具的依赖树写进 `require`（约 10KB），那些是**工具依赖、不进二进制**，MUST NOT 手工删掉 —— 删了 `go tool <name>` 会报找不到工具，Task-14 的 CI 也会红。<br>**原规则「go.mod 除 module 与 go 两行不许有 require」已作废**：它与同一文档的 §3 第 2.1 条（MUST 用 `go get -tool`）**不可能同时满足** —— 前者禁止的正是后者必然产生的东西。判据要落在「import 了什么」而不是「go.mod 里有几行 require」 |
| 3 | MUST NOT 在 `main.go` 里做任何 IO |
| 4 | MUST NOT 写 `//go:build` 标签 |
| 5 | MUST NOT 创建空的业务 `.go` 文件（只有 `doc.go`） |

### 5 参考骨架

```go
// 文件名: main.go
// 用途: A-01 阶段的占位入口，只打印版本串以证明模块能编译；真正装配在 Task-12。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

import "fmt"

// main 是 A-01 阶段的占位入口，只证明模块能编译。
// 真正的装配逻辑在 Task-12 实现，此处 MUST NOT 提前写。
func main() {
	fmt.Println("DeepSeekSearch v0.0.0-dev")
}
```

```go
// 文件名: doc.go
// 用途: 定义全项目共用的类型、错误码与纯函数，并提供包级文档。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package domain 定义全项目共用的类型、错误码与纯函数。
//
// 本包 MUST NOT 导入本项目其它包，也 MUST NOT 做任何 IO。
package domain
```

### 6 测试代码

本原子任务**不写测试**（没有可测逻辑）。`make test` 在 A-02 才有意义。

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc && go build ./... && echo BUILD_OK
BUILD_OK

$ go vet ./... && echo VET_OK
VET_OK

$ head -1 go.mod
module github.com/LseKit/DeepSeekSearch

# 🔴 判据是「运行时 import 了哪些第三方」，不是「go.mod 有几行 require」。
#    tool 指令带来的 require 是工具依赖树（不进二进制），不在此列。
$ go list -deps ./... | grep -E '^[a-z0-9.-]+\.[a-z]{2,}/' | grep -v '^github.com/LseKit/' | sort -u
（此时为空。后续阶段应恰好是 4 个：mcp go-sdk / BurntSushi/toml / joho/godotenv / modernc.org/sqlite）

$ grep -c '^tool' go.mod
1

$ go tool gofumpt --version | head -1
v0.12.0

$ ls internal/
config  deepseek  domain  resilience  tools  transport
```

### 8 完成定义（DoD）

- [ ] 🔴 上述 **7** 条命令全部达到期望输出（原写 5，与 §7 实际的 7 条不符）
- [ ] 🔴 `git status --short` 显示的新增文件与 §2「要创建的文件」列表**完全一致**，无多余文件（仓库根就是 `GitHubSrc/`；这条防的是多建了 §2 没列的文件）。<br>⚠️ **唯一例外：`AGENTS.md`** —— 它是先于本任务存在的仓库级发布物（由 `Contract/19-AGENTS.md全文.md` 生成），MUST 一并纳入本次初始 commit，MUST NOT 留在未跟踪状态（否则后续每个窗口的 `git status` 都是脏的）。它的**内容正确性**由 `Task-13` 校验，本任务不负责改写
- [ ] 提交信息：`chore: 初始化模块骨架与包占位`

---

## 原子任务 A-02 · Makefile 与版本注入

### 0 前置

- A-01 已完成并提交

### 1 上下文（自包含）

版本号必须「烧进二进制」，不能靠读配置（配置描述环境，不描述二进制）。做法是 `Makefile` 里用一个变量作为**唯一真源**，通过 `-ldflags -X` 注入到 `main` 包的三个变量。

版本号会出现在**三处**，MUST 同源：`--version` 输出、MCP `serverInfo.version`、`/healthz` 的 `version` 字段。

### 2 要创建/修改的文件

| 文件 | 动作 |
|---|---|
| `GitHubSrc/Makefile` | 新建 |
| `GitHubSrc/cmd/deepseeksearch/version.go` | 新建 |
| `GitHubSrc/cmd/deepseeksearch/main.go` | 改写（加 flag 解析） |
| `GitHubSrc/cmd/deepseeksearch/version_test.go` | 新建 |
| `GitHubSrc/.golangci.yml` | 新建（内容见 `20-编码规范` §1.1） |

### 3 实现要求

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | `Makefile` 第 4 行左右**逐字**含 `VERSION    := v0.1.0` | `grep -F 'VERSION    := v0.1.0' Makefile` |
| 2 | `LDFLAGS` 注入三个变量：`main.version`、`main.commit`、`main.buildTime` | `grep -c 'X main\.' Makefile` = 3 |
| 3 | `build` 目标用 `CGO_ENABLED=0`、`-trimpath`、`-ldflags '$(LDFLAGS)'` | `grep -E 'CGO_ENABLED=0' Makefile` |
| 4 | 产出路径是 `../Bin/DeepSeekSearch`（项目根下的 `Bin/`，不是 `GitHubSrc/Bin/`） | `make build && ls ../Bin/` |
| 5 | 🔴 存在 **9** 个目标：`build` `test` `test-race` `test-integration` `lint` `cover` `cover-check` `cover-html` `clean`（原写 6 个，缺的三个分别由 `21-测试规范` §3.2 的 `cover-check` 规格、§一 的 L3b 隔离要求、§3.2 的 HTML 报告要求引入） | `grep -c '^[a-z-]*:' Makefile` ≥ 9 |
| 6 | `version.go` 定义 `version`/`commit`/`buildTime` 三变量，默认值 `"v0.0.0-dev"`/`"none"`/`"unknown"` | `grep -F 'v0.0.0-dev' version.go` |
| 7 | `main.go` 支持 `--version`，输出**单行**到 **stdout**，退出码 0 | `./Bin/DeepSeekSearch --version \| wc -l` = 1 |
| 7.1 | 🔴 **输出格式 MUST 与 `Contract/17-版本与发布契约` §3.1 逐字一致**：`DeepSeekSearch <version> (commit <sha>, built <RFC3339>, <go版本>)`。<br>其中 `<go版本>` MUST 由 **`runtime.Version()`** 在运行时取得，MUST NOT 通过 `-ldflags` 注入 —— 因为 §3 req 2 要求 `LDFLAGS` 里**恰好三个** `-X main.`，再加一个会破坏该断言；且 go 版本描述的是「这个二进制由哪个工具链编译」，本就只有运行时知道 | 输出里含 `, go1.27` 前缀；`grep -c 'X main\.' Makefile` 仍 = 3 |
| 8 | `--version` **不读任何配置文件**，且在配置目录不存在时仍成功 | 见验收命令 4 |
| 9 | `version_test.go` 有 `TestEffectiveVersion` 覆盖 V5/V6/V7（见 `17-版本与发布契约` §八） | `go test -run TestEffectiveVersion -v` |
| 10 | `make test` 通过 | 退出码 0 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 在 `main.go` 里用 `flag.String` 接收版本号 —— 版本号是编译期常量，不是运行时输入 |
| 2 | MUST NOT 让 `--version` 走配置加载路径。它必须在 `flag.Parse()` 之后**立即**返回 |
| 3 | MUST NOT 用 `log` 包输出 `--version`（`log` 默认写 stderr，脚本抓不到） |
| 4 | MUST NOT 在 `Makefile` 里写 `VERSION=v0.1.0`（等号两边要有对齐空格，这是本项目的可 grep 约定） |
| 5 | MUST NOT 引入 `golang.org/x/tools` 等依赖 |

### 5 参考骨架

```go
// 文件名: version.go
// 用途: 版本变量的定义与 EffectiveVersion 兜底逻辑。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

// 这三个变量只能由链接器注入，代码里的值是「未注入时的开发版标识」。
// MUST NOT 从 config.toml 或环境变量读取 —— 版本号必须描述「这个二进制是什么」，
// 而不是「它读到了什么配置」。注入方式见 Makefile 的 LDFLAGS。
var (
	version   = "v0.0.0-dev" // -X main.version=v0.1.0
	commit    = "none"       // -X main.commit=a1b2c3d
	buildTime = "unknown"    // -X main.buildTime=2026-10-20T13:05:12Z
)

// EffectiveVersion 返回实际对外展示的版本号。
// 注入值有效时忽略配置里的兜底值，保证协议层版本号永远描述二进制本身。
func EffectiveVersion(injected, fallback string) string {
	if injected != "" && injected != "v0.0.0-dev" {
		return injected
	}
	if fallback != "" {
		return fallback
	}
	return "v0.0.0-dev"
}
```

```makefile
# 版本号唯一真源。发版只改这一行。
VERSION    := v0.1.0
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# cover-check 用：模块路径必须与 coverage.out 每行第一列的前缀一致。
GOPKG := $(shell go list -m)

LDFLAGS := -s -w \
           -X main.version=$(VERSION) \
           -X main.commit=$(COMMIT) \
           -X main.buildTime=$(BUILD_TIME)

.PHONY: build test test-race test-integration lint cover cover-check cover-html clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' \
		-o ../Bin/DeepSeekSearch ./cmd/deepseeksearch

test:
	go test -count=1 ./...

test-race:
	go test -count=1 -race ./...

# 🔴 L3b（真实上游、真实计费）只由这个目标触发，靠 integration build tag 隔离。
#    `test` / `test-race` MUST NOT 跑到它 —— 见 21-测试规范 铁律 T1 与 T7。
test-integration:
	go test -count=1 -tags integration ./internal/integration/...

# 🔴 走 go tool：版本由 go.mod 的 tool 指令 + go.sum 锁定，与 CI 完全同源。
#    用全局 golangci-lint 会出现「本地过、CI 红」—— 因为两者版本不同。
lint:
	go tool golangci-lint run ./...

cover:
	go test -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

cover-html: cover
	go tool cover -html=coverage.out -o coverage.html

# 🔴 cover-check 是**唯一**能当门槛的覆盖率目标；`cover` 只打印一行总覆盖率，
#    无法判定 21-测试规范 §三 的**逐包**门槛。CI 的 cover job 与发版闸门
#    （17-版本与发布契约 §七 第 10 项）MUST 一律引用本目标。
#    规格唯一真源：21-测试规范 §3.2（排除项 / 逐包阈值 / 输出格式 / 退出条件）。
#    用内联 awk 而不是新增脚本文件 —— 新增脚本 MUST 登记进 18-仓库文件契约，
#    否则 CheckRepoFiles.sh 会拦（§3.2「实现方式」行）。
cover-check: cover
	@test -s coverage.out || { echo "cover-check: coverage.out 不存在或为空 FAIL"; exit 1; }
	@awk -v total_min=65 -v gopkg='$(GOPKG)' '
		function pkgof(f,  n,parts) { n=split(f,parts,"/"); parts[n]="";
		                             p=parts[1]; for(i=2;i<n;i++) p=p"/"parts[i]; return p }
		BEGIN {
			# §3.2 逐包阈值，MUST 与 21-测试规范 §三 表逐项相同
			th[gopkg "/internal/domain"]=80;     th[gopkg "/internal/config"]=80
			th[gopkg "/internal/resilience"]=80; th[gopkg "/internal/deepseek"]=70
			th[gopkg "/internal/tools"]=70;      th[gopkg "/internal/transport"]=60
		}
		NR==1 { next }                       # mode: 行
		{
			split($$1,a,":"); f=a[1]
			# §3.1 排除项：三项 MUST 在判定前从 profile 剔除
			if (f ~ /_test\.go$$/) next
			if (f ~ /cmd\/deepseeksearch\/main\.go$$/) next
			if (f ~ /internal\/domain\/codes\.go$$/) next
			p=pkgof(f); stmt[p]+=$$2; if ($$3>0) hit[p]+=$$2
			allstmt+=$$2; if ($$3>0) allhit+=$$2
		}
		END {
			bad=0
			n=asorti(th,order)
			for (i=1;i<=n;i++) {
				p=order[i]; t=th[p]
				act = (stmt[p]>0) ? int(hit[p]*1000/stmt[p])/10 : 0
				st = (stmt[p]>0 && act>=t) ? "PASS" : "FAIL"
				if (st=="FAIL") bad=1
				printf "cover-check: %s %s%% >= %s%% %s\n", p, act, t, st
			}
			act = (allstmt>0) ? int(allhit*1000/allstmt)/10 : 0
			st = (allstmt>0 && act>=total_min) ? "PASS" : "FAIL"
			if (st=="FAIL") bad=1
			printf "cover-check: TOTAL %s%% >= %s%% %s\n", act, total_min, st
			if (bad) { exit 1 }
			print "cover-check: 全部包达标"
		}
	' coverage.out

clean:
	rm -f coverage.out coverage.html
	rm -rf ../Bin
```

> 🔴 **`cover-check` 里 `GOPKG` 的处理**：`coverage.out` 的第一列是**完整模块路径**（形如 `github.com/LseKit/DeepSeekSearch/internal/tools/handler.go`），而阈值表按包名写。骨骼里用 `-v gopkg='$(GOPKG)'` 把 Make 变量传进 awk，再由 `gopkg "/internal/xxx"` 拼接成完整路径。<br>**为什么 MUST NOT 写成 `"$$GOPKG/internal/domain"`**：Make 会把 `$$` 还原成 `$` 交给 shell，而单引号内 shell 不展开 —— awk 收到的就是**字面量** `$GOPKG/internal/domain`，永远不等于任何真实包路径 ⇒ 每个包的 `stmt[p]` 都是 0 ⇒ **全部 FAIL**。这是「安全失败」，但排查起来像覆盖率真的不达标。<br>`GOPKG := $(shell go list -m)` 也 MUST NOT 换成硬编码字符串：改名或 fork 后同样全红。
> **`asorti` 依赖 gawk**：BusyBox/mawk 没有它。若 CI 镜像不含 gawk，改为显式遍历固定包名列表（顺序即输出顺序），MUST NOT 改成依赖 `for (k in th)` 的哈希序 —— 输出顺序不稳定会让 CI 日志的 diff 失去意义。

### 6 测试代码（完整可粘贴）

```go
package main

import "testing"

func TestEffectiveVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		injected string
		fallback string
		want     string
	}{
		{name: "注入值优先于兜底值", injected: "v0.1.0", fallback: "v9.9.9", want: "v0.1.0"},
		{name: "未注入时用兜底值", injected: "v0.0.0-dev", fallback: "v9.9.9", want: "v9.9.9"},
		{name: "都为空时返回开发版标识", injected: "", fallback: "", want: "v0.0.0-dev"},
		{name: "空字符串注入视为未注入", injected: "", fallback: "v1.2.3", want: "v1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := EffectiveVersion(tt.injected, tt.fallback); got != tt.want {
				t.Fatalf("EffectiveVersion(%q, %q) = %q, want %q",
					tt.injected, tt.fallback, got, tt.want)
			}
		})
	}
}
```

### 7 验收命令与期望输出

```bash
# 1. 构建
$ cd GitHubSrc && make build
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X main.version=v0.1.0 ...' -o ../Bin/DeepSeekSearch ./cmd/deepseeksearch

# 2. 版本输出正确且是单行（格式以 Contract/17 §3.1 为唯一真源，含 go 版本段）
$ ../Bin/DeepSeekSearch --version
DeepSeekSearch v0.1.0 (commit <sha>, built 2026-10-20T..Z, go1.27.x)
#    🔴 `go1.27.x` 的补丁号随**构建工具链**变化（三机与 CI 已于 2026-10-21 统一为 1.27.2，但该段仍只作信息），
#    它是**信息字段，MUST NOT 作为断言**。断言只看前缀 `, go1.` 与单行/退出码。

$ ../Bin/DeepSeekSearch --version | wc -l
1

# 3. 退出码 0
$ ../Bin/DeepSeekSearch --version; echo "exit=$?"
exit=0

# 4. 配置目录不存在时仍能查版本（证明不依赖配置）
$ cd /tmp && /mcp/DeepSeekSearch/Bin/DeepSeekSearch --version; echo "exit=$?"
DeepSeekSearch v0.1.0 (commit ...)
exit=0

# 5. 测试
$ cd /mcp/DeepSeekSearch/GitHubSrc && go test ./... -run TestEffectiveVersion -v
=== RUN   TestEffectiveVersion
=== RUN   TestEffectiveVersion/注入值优先于兜底值
=== RUN   TestEffectiveVersion/未注入时用兜底值
=== RUN   TestEffectiveVersion/都为空时返回开发版标识
=== RUN   TestEffectiveVersion/空字符串注入视为未注入
--- PASS: TestEffectiveVersion (0.00s)
PASS
ok      github.com/LseKit/DeepSeekSearch/cmd/deepseeksearch

# 6. 🔴 cover-check 的输出格式与退出码（骨架期包还是空的，所以逐包必然 FAIL ——
#    这里验的是**格式、排除项与「不达标必须非 0 退出」**，不是覆盖率数字本身）
$ cd /mcp/DeepSeekSearch/GitHubSrc && make cover-check; echo "exit=$?"   # 🔴 make 把 recipe 失败归一化为 2（底层 awk 退出 1）
cover-check: github.com/LseKit/DeepSeekSearch/internal/config 0% >= 80% FAIL
cover-check: github.com/LseKit/DeepSeekSearch/internal/deepseek 0% >= 70% FAIL
cover-check: github.com/LseKit/DeepSeekSearch/internal/domain 0% >= 80% FAIL
cover-check: github.com/LseKit/DeepSeekSearch/internal/resilience 0% >= 80% FAIL
cover-check: github.com/LseKit/DeepSeekSearch/internal/tools 80% >= 70% PASS
cover-check: github.com/LseKit/DeepSeekSearch/internal/transport 100% >= 60% PASS
cover-check: TOTAL 90% >= 65% PASS
exit=2

# ↑ 上面这份输出是**用一份合成 coverage.out 实跑 awk 得到的**，不是手写示意。
#   合成 profile 里故意放了三项该被排除的文件，用来证明排除真的生效：
#     internal/tools/handler_test.go   9 条语句 0 命中
#     internal/domain/codes.go        42 条语句 0 命中
#     cmd/deepseeksearch/main.go       6 条语句 0 命中
#   若排除失效，internal/tools 会从 80% 掉到 42%（8/19）而 FAIL。
#   ⇒ 落地时 MUST 用同样手法自证一次：往 profile 里塞一个 _test.go，
#      确认对应包的百分比**不变**。

# 7. 🔴 门槛不达标时 MUST NOT 退出 0（这条是 cover-check 存在的全部意义）
$ make cover-check > /dev/null 2>&1; echo "exit=$?"
exit=2

# 8. `make cover` 只打印一行总覆盖率，**不能**当门槛（21-测试规范 §3 的警告）
$ make cover
total:  (statements) 33.3%   # 真实骨架的数值（cmd 包部分覆盖）；90.0% 是合成 profile 的数，不是期望值
```

### 8 完成定义（DoD）

- [ ] 上述 8 条命令全部达到期望输出（**粘贴真实输出**）
- [ ] 🔴 `make cover-check` 的**输出格式**与第 6 条逐字同构（`cover-check: <包路径> <实际>% >= <阈值>% PASS/FAIL`，末行 TOTAL），且逐包阈值与 `21-测试规范` §3.2 的 6 项逐项相同
- [ ] 🔴 已用「往 profile 塞 `_test.go` / `codes.go` / `main.go`」的办法**自证排除项生效**（第 6 条的注），并把自证输出贴进提交说明
- [ ] 🔴 `grep -c '^[a-z-]*:' Makefile` ≥ **9**（`build` `test` `test-race` `test-integration` `lint` `cover` `cover-check` `cover-html` `clean`）
- [ ] `git tag -l` 里**还没有** `v0.1.0`（tag 在 Task-12 打）
- [ ] 提交信息：`build: 加入 Makefile 与编译期版本注入`

---

## 原子任务 A-03 · 配置样例、README、CHANGELOG、LICENSE

### 0 前置

- A-02 已完成并提交
- 已通读 `Contract/13-配置契约` 第二节（完整配置模板）

### 1 上下文（自包含）

`GitHubSrc/config.example.toml` 是**给用户复制用的**完整配置样例，内容 MUST 与 `docs/Contract/13-配置契约` 第二节的 toml 代码块**逐字一致**。这样做的原因是：任何「文档一份、代码一份」的配置都会漂移，而我们的防线是 `Scripts/CheckDocDrift.sh` 做 diff。

README 面向 GitHub 访客；CHANGELOG 记录版本；LICENSE 用 MIT。

### 2 要创建的文件

| 文件 | 内容来源 |
|---|---|
| `GitHubSrc/config.example.toml` | 逐字复制 `docs/Contract/13-配置契约` §二的 toml 代码块 |
| `GitHubSrc/README.md` | 按下文骨架写（**只是骨架**，最终发布版由 `Task-13` A-01 覆盖） |
| `GitHubSrc/CHANGELOG.md` | 按下文骨架写（**只是骨架**，最终发布版由 `Task-13` A-01 覆盖） |
| `GitHubSrc/docs/.gitkeep` | 空文件（`docs/` 是文档同步目标目录） |

### 3 实现要求

| # | 要求 | 验证 |
|:-:|---|---|
| 1 | `config.example.toml` 与文档 §二 toml 块逐字相同 | `Scripts/CheckDocDrift.sh`（Task-11 落地后生效） |
| 2 | `config.example.toml` 里 MUST NOT 出现**真实 API Key**（`sk-` + 一长串字母数字） | 🔴 `grep -cE '(^|[^A-Za-z0-9])sk-[A-Za-z0-9]' config.example.toml` = 0。<br>**MUST NOT 用 `grep -c 'sk-'`**：模板里逐字复制了 `Task-01`/`Task-06`/`Task-07`，其中的 `Ta**sk-**` 子串会被命中 5 次 → 判据恒为假红，而唯一的「修法」是篡改逐字副本（违反 req 1 与 H3） |
| 3 | README MUST NOT 写死版本号数字 | `grep -cE 'v[0-9]+\.[0-9]+\.[0-9]+' README.md` = 0 |
| 4 | README MUST 有「已知限制」章节，🔴 列出 **9 条**（原写 5 条，漏掉的 4 条里有**用户最容易撞上的那一条**）。9 条以 `Handover/91-验收总表.md` §四 为唯一真源，`Task-13` A-01 §3 第 4/15 条要求「≥9 条且第 1 条 MUST 是『部分主流站点抓不到』」：①部分主流站点抓不到（实测 15 个里 6 个失败，约 40%）②不支持 PDF/XML/SVG/ZIP ③无法区分 403/404/500 ④上游 30s 硬超时、超大单页返回 504 ⑤不做结果重排序 ⑥不做答案合成 ⑦不做 SSE 传输 ⑧`min_rerank`/`min_authority` 容易过度过滤 ⑨`ori_question` 实测几乎无效 | `awk '/^## 已知限制/,/^## [^已]/' README.md \| grep -cE '^[0-9]+\. '` ≥ 9 |
| 5 | README MUST 有「两种传输」章节，分别给 stdio 与 Streamable HTTP 的客户端配置片段 | 人工核对 |
| 6 | CHANGELOG MUST 有 `## [未发布]` 段与 `## [v0.1.0] - 2026-10-20` 段 | `grep -c '## \[' CHANGELOG.md` = 2 |
| 7 | CHANGELOG 的 `v0.1.0` 段 MUST 有「已知限制」小节 | `grep -A5 '已知限制' CHANGELOG.md` |
| 8 | LICENSE 是 MIT 全文，版权行 `Copyright (c) 2026 小逸 <https://github.com/LseKit/DeepSeekSearch>`，无改写 | `grep -c 'MIT License' LICENSE` = 1 且 `grep -c 'Permission is hereby granted' LICENSE` = 1 且 `grep -c 'LseKit/DeepSeekSearch' LICENSE` = 1 |

### 4 禁止事项

| # | 禁止 |
|:-:|---|
| 1 | MUST NOT 在 README 里写「性能卓越」「业界领先」类空话 |
| 2 | MUST NOT 在 README 里贴大段中文配置注释（指向 `docs/Contract/13-配置契约` 即可） |
| 3 | MUST NOT 在 `config.example.toml` 里填任何真实 API Key，哪怕是占位符也不要写 `sk-xxxx` |
| 4 | MUST NOT 写 `docs/` 下的任何文件（那是自动同步的） |

### 5 参考骨架

README 骨架：

```markdown
# DeepSeekSearch

把 DeepSeek 的 [Search / Fetch API](https://api-docs.deepseek.com/zh-cn/guides/search)
包装成一个 MCP 服务器。一个静态二进制，两种传输。

## 快速开始

（3 步：复制配置 / 填 Key / 起服务）

## 两种传输

| 传输 | 启动命令 | 适用 |
|---|---|---|
| `stdio` | `./DeepSeekSearch -transport stdio` | 本地客户端直连 |
| Streamable HTTP | `./DeepSeekSearch -transport streamable-http -config Config/config.toml` | 多客户端共享 / 网关 |

## 工具

| 工具 | 作用 |
|---|---|
| `ds_search` | 网页搜索，支持 site/lang/after/before/精确短语 |
| `ds_fetch` | 正文抓取，大文档按标题分节读取 |
| `ds_health` | 本地诊断：Key / 上游连通 / 缓存 / 生效配置 |

## 已知限制

<!-- 🔴 9 条，与 Handover/91-验收总表.md §四 逐条对应（那是唯一真源）。
     原骨架只有 5 条，漏掉的 4 条里第 1 条是用户最容易撞上的：
     ds_search 返回的结果里有相当比例来自抓不到的站点，
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
```

#### 5.8 `LICENSE`（MUST 逐字使用，不许改写一个字符）

MIT 正文是**法律文本**，任何改写都会让「本项目和标准 MIT 一致」这句话失效，也会让 GitHub 的许可识别失效（识别失败 → 徽章不显示、依赖扫描无法判断兼容性）。

```text
MIT License

Copyright (c) 2026 小逸 <https://github.com/LseKit/DeepSeekSearch>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

| 规则 | 说明 |
|---|---|
| 版权行 | MUST 逐字为 `Copyright (c) 2026 小逸 <https://github.com/LseKit/DeepSeekSearch>` |
| 正文 | MUST NOT 改写、增删、翻译、调整换行位置 |
| 文件末尾 | MUST 有且仅有一个换行（`SOFTWARE.` 后一个 `\n`） |
| 扩展名 | 无扩展名，就叫 `LICENSE`（不是 `LICENSE.md` / `LICENSE.txt`） |

为什么选 MIT 而不是更宽松的 0BSD / Unlicense：见 `Decisions/ADR.md` 的 **ADR-012**。

### 6 测试代码

本原子任务无 Go 测试代码。验证靠 §7 的命令。

### 7 验收命令与期望输出

```bash
$ cd GitHubSrc

$ grep -cE '(^|[^A-Za-z0-9])sk-[A-Za-z0-9]' config.example.toml || echo 0
0
# 🔴 不能用 grep -c 'sk-'：模板里的 Task-01/Task-06/Task-07 含子串 "sk-"，会误报 5 次

$ grep -cE 'v[0-9]+\.[0-9]+\.[0-9]+' README.md || echo 0
0

$ grep -c '## \[' CHANGELOG.md
2

$ ls -la config.example.toml README.md CHANGELOG.md LICENSE docs/
-rw-r--r-- ... config.example.toml
-rw-r--r-- ... README.md
-rw-r--r-- ... CHANGELOG.md
-rw-r--r-- ... LICENSE
docs/:

$ go build ./... && echo BUILD_OK
BUILD_OK
```

### 8 完成定义（DoD）

- [ ] 上述命令达到期望输出
- [ ] `config.example.toml` 的 toml 键数量与 `docs/Contract/13-配置契约` §二一致（人工数 3 个 section 头核对）
- [ ] 提交信息：`docs: 加入配置样例、README 与 CHANGELOG`

---

## Task-01 全部完成后的整体验收

```bash
$ cd /mcp/DeepSeekSearch/GitHubSrc
$ make build && make test && make lint
$ ../Bin/DeepSeekSearch --version
DeepSeekSearch v0.1.0 (commit <sha>, built <time>)
$ git log --oneline
<sha> docs: 加入配置样例、README 与 CHANGELOG
<sha> build: 加入 Makefile 与编译期版本注入
<sha> chore: 初始化模块骨架与包占位
```

三条 commit、一个可运行的二进制、零业务逻辑。**这就是 Task-01 的全部。**
