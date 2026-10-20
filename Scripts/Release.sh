#!/usr/bin/env bash
# 文件名: Release.sh
# 用途: 按 Contract/17 §七 的检查单顺序发版；项数由该表数据行数动态得出。
# 创建日期: 2026-10-21
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
# 用法: Release.sh [--dry-run] [--skip-signature] [--no-push]
#   环境：RELEASE_DEBT_REVIEWED=1 表示已完成 16 号 §五 的技术债逐条复核
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
DOC17="$ROOT/docs/Contract/17-版本与发布契约.md"
VERSION=$(grep -oP 'VERSION\s*:=\s*\K\S+' Makefile)
DRY_RUN=0
SKIP_SIGN=0
NO_PUSH=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --skip-signature) SKIP_SIGN=1 ;;
    --no-push) NO_PUSH=1 ;;
    *) echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done
FAIL=0

# 项数唯一真源：17 号 §七 的表格数据行。MUST NOT 在脚本里硬编码。
TOTAL=$(awk '/^## 七、发布检查单/{f=1;next} f && /^## /{f=0} f && /^\| [0-9]+ \|/{n++} END{print n+0}' "$DOC17")
if [ "$TOTAL" -lt 1 ]; then
  echo "[FAIL] 无法从 $DOC17 解析检查项数" >&2
  exit 1
fi

step() { printf '\n[%02d/%02d] %s\n' "$1" "$TOTAL" "$2"; }
ok()   { printf '  OK   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1" >&2; FAIL=1; }
skip() { printf '  SKIP %s\n' "$1"; }
die_if_failed() {
  if [ "$FAIL" -ne 0 ]; then
    printf '\n发布中止：第 %s 项未通过\n' "$1" >&2
    exit 1
  fi
}

# 1 发布文件齐备 + 源文件署名
step 1 "发布文件齐备（CheckRepoFiles.sh）"
if [ -f Scripts/CheckRepoFiles.sh ]; then
  bash Scripts/CheckRepoFiles.sh . || bad "发布文件不齐或署名缺失"
  [ "$FAIL" -eq 0 ] && ok "A/B 级文件齐全"
else
  bad "Scripts/CheckRepoFiles.sh 不存在（Task-14 交付）"
fi
die_if_failed 1

# 2 脚本自身不得出现命令替换式的版本变量
step 2 "Release.sh 无命令替换式版本变量"
PAT='$''(VERSION)'
N_BAD=$(grep -cF "$PAT" Scripts/Release.sh || true)
if [ "$N_BAD" -eq 0 ]; then ok "0 处"; else bad "发现 $N_BAD 处命令替换写法"; fi
die_if_failed 2

# 3 工作区干净
step 3 "工作区干净"
if [ -z "$(git status --porcelain)" ]; then ok "git status 为空"; else bad "工作区有未提交改动"; fi
die_if_failed 3

# 4 main 分支
step 4 "在 main 分支"
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$BRANCH" = "main" ]; then ok "main"; else bad "当前分支 $BRANCH"; fi
die_if_failed 4

# 5 版本未被占用
step 5 "${VERSION} 未被 tag 占用"
if [ -z "$(git tag -l "$VERSION")" ]; then ok "$VERSION 可用"; else bad "$VERSION 已存在"; fi
die_if_failed 5

# 6 全量测试（含 race）
step 6 "make test-race"
make test-race || bad "make test-race 失败"
die_if_failed 6

# 7 lint
step 7 "make lint"
make lint || bad "make lint 失败"
die_if_failed 7

# 8 规模红线
step 8 "make size"
make size || bad "make size 失败"
die_if_failed 8

# 9 文档漂移
step 9 "make drift"
make drift || bad "make drift 失败"
die_if_failed 9

# 10 覆盖率
step 10 "make cover-check"
make cover-check || bad "make cover-check 失败"
die_if_failed 10

# 11 四平台交叉编译
step 11 "四平台交叉编译（make dist）"
if [ "$DRY_RUN" -eq 1 ]; then
  skip "dry-run 不产出二进制"
else
  make dist || bad "make dist 失败"
fi
die_if_failed 11

# 12 --version 与 VERSION 一致
step 12 "--version 与 ${VERSION} 一致"
LINUX_BIN="../Bin/DeepSeekSearch_${VERSION}_linux_amd64"
if [ "$DRY_RUN" -eq 1 ]; then
  skip "dry-run 跳过"
elif [ ! -x "$LINUX_BIN" ]; then
  bad "$LINUX_BIN 不存在"
elif "$LINUX_BIN" --version | grep -Fq "$VERSION"; then
  ok "--version 含 $VERSION"
else
  bad "--version 不含 $VERSION"
fi
die_if_failed 12

# 13 /healthz 的 version 与 VERSION 一致
step 13 "/healthz version 与 ${VERSION} 一致"
if [ "$DRY_RUN" -eq 1 ]; then
  skip "dry-run 跳过"
else
  TMP=$(mktemp -d)
  PORT=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)
  KEY=$(grep -oP '^DEEPSEEK_API_KEY=\K.*' ../Config/.env 2>/dev/null || true)
  cat > "$TMP/config.toml" <<EOF
[server]
transport = "streamable-http"
[server.http]
host = "127.0.0.1"
port = $PORT
auth_enabled = false
[log]
dir = "Logs"
also_stderr = false
[cache]
enabled = false
EOF
  : > "$TMP/.env"
  chmod 600 "$TMP/.env"
  (cd "$TMP" && DEEPSEEK_API_KEY="$KEY" "$ROOT/$LINUX_BIN" -config config.toml -env .env >/dev/null 2>&1) &
  SRV_PID=$!
  HEALTH=""
  for _ in $(seq 1 50); do
    HEALTH=$(curl -sf "http://127.0.0.1:$PORT/healthz" 2>/dev/null || true)
    [ -n "$HEALTH" ] && break
    sleep 0.1
  done
  kill "$SRV_PID" 2>/dev/null || true
  wait "$SRV_PID" 2>/dev/null || true
  rm -rf "$TMP"
  if printf '%s' "$HEALTH" | grep -Fq "$VERSION"; then ok "healthz 含 $VERSION"; else bad "healthz 不含 $VERSION"; fi
fi
die_if_failed 13

# 14 serverInfo.version 与 VERSION 一致（P26，无计费）
step 14 "serverInfo.version 与 ${VERSION} 一致（P26）"
if [ "$DRY_RUN" -eq 1 ]; then
  skip "dry-run 跳过"
elif go test -count=1 -tags integration ./internal/integration/ -run TestVersionConsistency_P26 >/dev/null 2>&1; then
  ok "P26 通过"
else
  bad "P26 失败"
fi
die_if_failed 14

# 15 三处版本号统一带 v
step 15 "三处版本号统一带 v 前缀"
case "$VERSION" in
  v*) ok "$VERSION 带 v 前缀" ;;
  *) bad "$VERSION 缺少 v 前缀" ;;
esac
die_if_failed 15

# 16 CHANGELOG 已翻转
step 16 "CHANGELOG 已翻转（$VERSION 有独立段、未发布为空）"
if [ ! -f CHANGELOG.md ]; then
  bad "CHANGELOG.md 不存在"
elif ! grep -Fq "## [${VERSION}]" CHANGELOG.md; then
  bad "CHANGELOG.md 缺少 ## [$VERSION] 段"
else
  UNRELEASED=$(awk '/^## \[未发布\]/{f=1;next} /^## \[/{f=0} f' CHANGELOG.md | grep -cE '^\s*[-*] ' || true)
  if [ "$UNRELEASED" -eq 0 ]; then ok "已翻转"; else bad "未发布段仍有 $UNRELEASED 条"; fi
fi
die_if_failed 16

# 17 技术债复核
step 17 "16 号 §五 技术债已逐条复核"
if [ "${RELEASE_DEBT_REVIEWED:-}" = "1" ]; then
  ok "RELEASE_DEBT_REVIEWED=1"
else
  bad "未设置 RELEASE_DEBT_REVIEWED=1（复核结论 MUST 记入发版记录）"
fi
die_if_failed 17

# 18 文档已同步
step 18 "docs/ 与 Doc/ 无 diff"
if bash Scripts/SyncDocs.sh --check ../Doc . >/dev/null 2>&1; then ok "SyncDocs --check 通过"; else bad "docs/ 未同步"; fi
die_if_failed 18

# 19 校验和 + 签名
step 19 "SHA256SUMS + cosign 签名"
if [ "$DRY_RUN" -eq 1 ]; then
  skip "dry-run 跳过"
elif [ "$SKIP_SIGN" -eq 1 ]; then
  skip "--skip-signature：签名留给 CI release.yml"
elif ! command -v cosign >/dev/null 2>&1; then
  bad "cosign 未安装（或使用 --skip-signature 并在发版记录里注明由 CI 签名）"
else
  (cd ../Bin && sha256sum "DeepSeekSearch_${VERSION}_"* > SHA256SUMS) || bad "生成 SHA256SUMS 失败"
  cosign sign-blob --yes "../Bin/SHA256SUMS" > "../Bin/SHA256SUMS.sig" 2>/dev/null || bad "cosign 签名失败"
  [ "$FAIL" -eq 0 ] && ok "SHA256SUMS + SHA256SUMS.sig"
fi
die_if_failed 19

# 20 打附注 tag 并推送
step 20 "打附注 tag 并推送"
if [ "$DRY_RUN" -eq 1 ]; then
  skip "dry-run 不创建 tag"
else
  if git tag -a "$VERSION" -m "$VERSION"; then ok "已创建 $VERSION"; else bad "创建 tag 失败"; fi
  if [ "$NO_PUSH" -eq 1 ]; then
    skip "--no-push：tag 仅在本地"
  elif ! git remote get-url origin >/dev/null 2>&1; then
    skip "未配置 origin，tag 仅在本地"
  else
    git push origin "$VERSION" || bad "推送 tag 失败"
  fi
fi
die_if_failed 20

# 21 Release 附件（手工）
step 21 "GitHub Release 附件齐全"
skip "手工检查：4 个二进制 + SHA256SUMS + 签名 + *.sbom.json"

printf '\n发版完成：%s\n' "$VERSION"
