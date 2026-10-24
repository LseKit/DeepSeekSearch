#!/usr/bin/env bash
# 文件名: CheckSize.sh
# 用途: 检查 F-S1 / F-S3 / F-S5 规模红线。
#       F-S2 / F-S4 由 internal/arch_test.go 的 TestFunctionLength / TestNestingDepth 检查。
#       无存量基线：超标即 FAIL。
# 创建日期: 2026-10-20
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
# 用法: CheckSize.sh <代码根> <文档根>
#   例: CheckSize.sh .        docs     (在 GitHubSrc/ 下)
#       CheckSize.sh GitHubSrc GitHubSrc/docs
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_ROOT="${1:-..}"
DOC_ROOT="${2:-$SRC_ROOT/docs}"
EXEMPT_FILE="$DOC_ROOT/Engineering/豁免台账.md"
FAIL=0

# ---------------------------------------------------------------------------
# 读取豁免台账：解析 ```exempt 块，五段格式：
#   <文件相对路径>  <红线代号>  <实测值>  "<理由>"  <复核日期 YYYY-MM-DD>
# 红线代号为 "—" 的非规模类条目跳过。
# ---------------------------------------------------------------------------
declare -A EXEMPT=()
EXEMPT_N=0
TODAY="$(date +%F)"
if [ -f "$EXEMPT_FILE" ]; then
  while IFS= read -r line; do
    [ -z "${line// }" ] && continue
    review="$(awk '{print $NF}' <<<"$line")"
    if [[ "$review" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] && [[ "$review" < "$TODAY" ]]; then
      echo "F-S6 豁免已过期: $line"
      FAIL=1
    fi
    rule=$(awk '{print $2}' <<<"$line")
    [ "$rule" = "—" ] || [ "$rule" = "-" ] && continue
    file=$(awk '{print $1}' <<<"$line")
    EXEMPT["$file|$rule"]=1
    EXEMPT_N=$((EXEMPT_N + 1))
  done < <(awk '/^```exempt$/{f=1;next} /^```$/{f=0} f' "$EXEMPT_FILE")
else
  echo "⚠️  豁免台账不存在: $EXEMPT_FILE（本次不放行任何豁免）"
fi
echo "已加载规模豁免 $EXEMPT_N 条"

exempted() { [ -n "${EXEMPT["$1|$2"]:-}" ]; }

# ---------------------------------------------------------------------------
# F-S1 单文件 ≤600 物理行（含空行与注释）
# ---------------------------------------------------------------------------
for base in internal cmd; do
  [ -d "$SRC_ROOT/$base" ] || continue
  while IFS= read -r f; do
    rel="${f#"$SRC_ROOT"/}"
    n=$(wc -l < "$f")
    if [ "$n" -gt 600 ]; then
      if exempted "$rel" F-S1; then
        echo "F-S1 豁免: $rel 有 $n 行（台账放行）"
      else
        echo "F-S1 违规: $rel 有 $n 行 (上限 600)"
        FAIL=1
      fi
    fi
  done < <(find "$SRC_ROOT/$base" -name '*.go' ! -name '*_test.go' 2>/dev/null)
done

# ---------------------------------------------------------------------------
# F-S5 单包 ≤3000 行 / ≤10 文件 / ≤20 导出符号
#   导出数用 Scripts/exportcount.go 按 go/ast 精确统计（MUST NOT 把 struct 字段算入）。
# ---------------------------------------------------------------------------
if ! SIZE_ROWS=$(cd "$SRC_ROOT" && go run "$SCRIPT_DIR/exportcount.go" .); then
  echo "[FAIL] exportcount.go 执行失败，F-S5 无法验证"
  exit 1
fi

while read -r rel files lines exports; do
  [ -z "$rel" ] && continue
  if exempted "$rel" F-S5; then
    echo "F-S5 豁免: $rel（台账放行）"
    continue
  fi
  check_metric() {
    local metric="$1" current="$2" limit="$3"
    [ "$current" -le "$limit" ] && return 0
    echo "F-S5 $metric 违规: $rel $current (上限 $limit)"
    FAIL=1
  }
  check_metric files "$files" 10
  check_metric lines "$lines" 3000
  check_metric exports "$exports" 20
done <<< "$SIZE_ROWS"

# ---------------------------------------------------------------------------
# F-S3 圈复杂度 ≤10
#   判据是 gocyclo 的**输出是否非空**，不是退出码（gocyclo 超限时也退出 0）。
# ---------------------------------------------------------------------------
GOCYCLO_BIN=""
if command -v gocyclo >/dev/null 2>&1; then
  GOCYCLO_BIN=$(command -v gocyclo)
elif GOCYCLO_BIN=$(go tool -n gocyclo 2>/dev/null); then
  :
fi

if [ -n "$GOCYCLO_BIN" ]; then
  cyclo_out=$("$GOCYCLO_BIN" -over 10 -ignore '_test\.go' "$SRC_ROOT/internal" "$SRC_ROOT/cmd" 2>/dev/null || true)
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    # gocyclo 输出：<复杂度> <包名> <函数名> <路径>:<行>:<列>
    cx=$(awk '{print $1}' <<<"$line")
    fn=$(awk '{print $3}' <<<"$line")
    fn="${fn##*.}"
    loc=$(awk '{print $4}' <<<"$line")
    file="${loc%:*:*}"
    rel="${file#"$SRC_ROOT"/}"
    key="$rel:$fn"
    echo "F-S3 违规: $key 复杂度 $cx（上限 10）"
    FAIL=1
  done <<< "$cyclo_out"
else
  echo "⚠️  gocyclo 未安装，圈复杂度未检查（用 go.mod 的 tool 指令安装）"
fi

if [ "$FAIL" -eq 0 ]; then
  echo "✅ 规模红线全部通过（F-S1/F-S3/F-S5；F-S2/F-S4 见 TestSizeLimits）"
fi
exit "$FAIL"
