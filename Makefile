# 版本号唯一真源。发版只改这一行。
VERSION    := v0.1.0
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# cover-check 的逐包阈值键按完整模块路径拼，MUST 由 go list -m 提供，MUST NOT 硬编码字符串
# （改名或 fork 后硬编码会让每个包 stmt=0 而全部 FAIL）。
GOPKG      := $(shell go list -m)

LDFLAGS := -s -w \
           -X main.version=$(VERSION) \
           -X main.commit=$(COMMIT) \
           -X main.buildTime=$(BUILD_TIME)

.PHONY: build test test-race test-integration lint cover cover-check cover-html clean release dist

# 🔴 产物 Go 版本的真源是 go.mod 的 `toolchain` 行；本机三台的工具链已与该行一致
#    （GOTOOLCHAIN=local，禁止静默联网拉工具链）。构建机若仍是旧补丁版本 + local，
#    这里不会有任何报错 —— 发布前 MUST 用 `-version` 核对 go 段，MUST NOT 只看 go.mod。
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' \
		-o ../Bin/DeepSeekSearch ./cmd/deepseeksearch

test:
	go test -count=1 ./...

test-race:
	go test -count=1 -race ./...

# L3b（真实上游、真实计费）只由这个目标触发，靠 integration build tag 隔离。
#    test / test-race MUST NOT 跑到它 —— 见 21-测试规范 铁律 T1 与 T7。
test-integration:
	go test -count=1 -tags integration ./internal/integration/...

# 走 go tool：版本由 go.mod 的 tool 指令 + go.sum 锁定，与 CI 完全同源。
#    用全局 golangci-lint 会出现「本地过、CI 红」—— 因为两者版本不同。
lint:
	go tool golangci-lint run ./...

cover:
	go test -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

cover-html: cover
	go tool cover -html=coverage.out -o coverage.html

# cover-check 是唯一能当门槛的覆盖率目标；cover 只打印一行总覆盖率，无法判定逐包门槛。
#    规格：排除项 / 逐包阈值 / 输出格式 / 退出条件。
#    排除项（_test.go / cmd/deepseeksearch/main.go / internal/domain/codes.go）MUST 在判定前剔除。
#    用内联 awk 而不是新增脚本文件 —— 新增脚本 MUST 登记进 18-仓库文件契约，否则 CheckRepoFiles.sh 会拦。
#    awk 程序写成单物理行：Make 默认每recipe行独立起一个 shell，多行单引号程序会被拆开。
cover-check: cover
	@test -s coverage.out || { echo "cover-check: coverage.out 不存在或为空 FAIL"; exit 1; }
	@awk -v total_min=65 -v gopkg='$(GOPKG)' 'function pkgof(f,  n,parts){n=split(f,parts,"/");parts[n]="";p=parts[1];for(i=2;i<n;i++)p=p "/" parts[i];return p} BEGIN{th[gopkg "/internal/domain"]=80;th[gopkg "/internal/config"]=80;th[gopkg "/internal/resilience"]=80;th[gopkg "/internal/resilience/ratelimit"]=80;th[gopkg "/internal/resilience/retry"]=80;th[gopkg "/internal/resilience/budget"]=80;th[gopkg "/internal/resilience/circuit"]=80;th[gopkg "/internal/resilience/cache"]=80;th[gopkg "/internal/deepseek"]=70;th[gopkg "/internal/tools"]=70;th[gopkg "/internal/transport"]=60} NR==1{next} {split($$1,a,":");f=a[1];if(f~/_test\.go$$/)next;if(f~/cmd\/deepseeksearch\/main\.go$$/)next;if(f~/internal\/domain\/codes\.go$$/)next;p=pkgof(f);stmt[p]+=$$2;if($$3>0)hit[p]+=$$2;allstmt+=$$2;if($$3>0)allhit+=$$2} END{bad=0;n=asorti(th,order);for(i=1;i<=n;i++){p=order[i];t=th[p];act=(stmt[p]>0)?int(hit[p]*1000/stmt[p])/10:0;st=(stmt[p]>0&&act>=t)?"PASS":"FAIL";if(st=="FAIL")bad=1;printf "cover-check: %s %s%% >= %s%% %s\n",p,act,t,st}act=(allstmt>0)?int(allhit*1000/allstmt)/10:0;st=(allstmt>0&&act>=total_min)?"PASS":"FAIL";if(st=="FAIL")bad=1;printf "cover-check: TOTAL %s%% >= %s%% %s\n",act,total_min,st;if(bad){exit 1}print "cover-check: 全部包达标"}' coverage.out

clean:
	rm -f coverage.out coverage.html
	rm -rf ../Bin

# ---- 发布物 -----------------------------------------------------------------
# 四平台交叉编译；先清空 Bin，MUST NOT 留旧版本文件。
dist:
	rm -rf ../Bin
	mkdir -p ../Bin
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o ../Bin/DeepSeekSearch_$(VERSION)_linux_amd64   ./cmd/deepseeksearch
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o ../Bin/DeepSeekSearch_$(VERSION)_linux_arm64   ./cmd/deepseeksearch
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o ../Bin/DeepSeekSearch_$(VERSION)_darwin_arm64  ./cmd/deepseeksearch
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o ../Bin/DeepSeekSearch_$(VERSION)_windows_amd64.exe ./cmd/deepseeksearch
	cd ../Bin && sha256sum DeepSeekSearch_$(VERSION)_* > SHA256SUMS

# release 只负责产出发布物；tag/推送/签名由 Scripts/Release.sh 执行。
release: dist
	@echo "发布物已生成到 ../Bin/；随后运行 bash Scripts/Release.sh 完成 21 项检查与打 tag"

# ---- 质量门禁 ----

.PHONY: size drift repo-files check

# 规模红线 F-S1 / F-S3 / F-S5（F-S2 / F-S4 由 internal/arch_test.go 用 go/ast 强制）
# 两参签名 <代码根> <文档根>；docs/ 是仓库内唯一文档根。
size:
	bash Scripts/CheckSize.sh . docs

# 文档漂移 8 项检查（两参签名 <文档根> <代码根>，与 size 的参数顺序相反）
drift:
	bash Scripts/CheckDocDrift.sh docs .

# GitHub 发布文件齐备性（A 级 + B 级 + 署名 + 工具脚本）
repo-files:
	bash Scripts/CheckRepoFiles.sh .

# 收尾只需这一条：编译 + 竞态测试 + 静态检查 + 覆盖率门槛 + 规模 + 漂移 + 发布文件齐备
check: build test-race lint cover-check size drift repo-files
	@echo ""
	@echo "=============================================="
	@echo " make check 全部通过"
	@echo "=============================================="
