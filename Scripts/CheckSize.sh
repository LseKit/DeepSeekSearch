#!/usr/bin/env bash
# 文件名: CheckSize.sh
# 用途: 检查 F-S1 / F-S3 / F-S5 规模红线（基线棘轮，ADR-017）。
#       F-S2 / F-S4 由 internal/arch_test.go 的 TestFunctionLength / TestNestingDepth 检查。
#       存量违规冻结在 Scripts/size-baseline.json，判定 = 当前值 > 基线值 ⇒ FAIL。
# 创建日期: 2026-10-20
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
# 用法: CheckSize.sh <代码根> <文档根>
#   例: CheckSize.sh .        ../Doc     (在 GitHubSrc/ 下)
#       CheckSize.sh GitHubSrc Doc       (在部署根下)
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_ROOT="${1:-..}"
DOC_ROOT="${2:-../../Doc}"
EXEMPT_FILE="$DOC_ROOT/Engineering/豁免台账.md"
BASELINE="$SCRIPT_DIR/size-baseline.json"
FAIL=0

# CI 语境只有同步产物 docs/、没有 Doc/。TestSizeLimits 固定传 ../../Doc，
# 若不回退，domain 的 F-S5 豁免加载不到 ⇒ 正确的代码在 CI 里被判违规。
# 🔴 只在 CI 回退：本地漏传第二个参数仍 MUST 失败，否则「两参签名」失去意义。
if [ ! -f "$EXEMPT_FILE" ] && [ -n "${CI:-}" ] && [ -f "$SRC_ROOT/docs/Engineering/豁免台账.md" ]; then
  EXEMPT_FILE="$SRC_ROOT/docs/Engineering/豁免台账.md"
  echo "ℹ️  文档根无豁免台账，回退到 $EXEMPT_FILE（CI 语境）"
fi

# ---------------------------------------------------------------------------
# 读取豁免台账：解析 ```exempt 块，五段格式：
#   <文件相对路径>  <红线代号>  <实测值>  "<理由>"  <复核日期 YYYY-MM-DD>
# 红线代号为 "—" 的非规模类条目跳过。
# ---------------------------------------------------------------------------
declare -A EXEMPT=()
EXEMPT_N=0
if [ -f "$EXEMPT_FILE" ]; then
  while IFS= read -r line; do
    [ -z "${line// }" ] && continue
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
# 读取存量基线（ADR-017）。键格式：
#   F-S1/F-S2/F-S3/F-S4: <包>/<文件>[:<函数>]
#   F-S5:                 <包> → files/lines/exports
# 基线不存在时视为已清零：全部按原阈值判定。
# ---------------------------------------------------------------------------
declare -A B_S1=() B_S2=() B_S3=() B_S4=() B_S5_FILES=() B_S5_LINES=() B_S5_EXPORTS=()
BASELINE_N=0
if [ -f "$BASELINE" ]; then
  if ! command -v python3 >/dev/null 2>&1; then
    echo "[FAIL] 基线 $BASELINE 存在但 python3 不可用，无法执行棘轮判定"
    exit 1
  fi
  while IFS=$'\t' read -r section key v1 v2 v3; do
    [ -z "$section" ] && continue
    case "$section" in
      f_s1) B_S1["$key"]="$v1" ;;
      f_s2) B_S2["$key"]="$v1" ;;
      f_s3) B_S3["$key"]="$v1" ;;
      f_s4) B_S4["$key"]="$v1" ;;
      f_s5) B_S5_FILES["$key"]="$v1"; B_S5_LINES["$key"]="$v2"; B_S5_EXPORTS["$key"]="$v3" ;;
    esac
    BASELINE_N=$((BASELINE_N + 1))
  done < <(python3 - "$BASELINE" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    data = json.load(fh)
for key, entry in data.get("f_s1_file_lines", {}).items():
    print("f_s1\t%s\t%d" % (key, entry["value"]))
for key, entry in data.get("f_s2_function_lines", {}).items():
    print("f_s2\t%s\t%d" % (key, entry["value"]))
for key, entry in data.get("f_s3_cyclomatic", {}).items():
    print("f_s3\t%s\t%d" % (key, entry["value"]))
for key, entry in data.get("f_s4_nesting", {}).items():
    print("f_s4\t%s\t%d" % (key, entry["value"]))
for key, entry in data.get("f_s5_package", {}).items():
    print("f_s5\t%s\t%d\t%d\t%d" % (key, entry["files"], entry["lines"], entry["exports"]))
PY
)
  echo "已加载规模基线 $BASELINE_N 项"
else
  echo "ℹ️  未找到规模基线 $BASELINE（视作 DEBT-010 已清零，全部按原阈值判定）"
fi

# ---------------------------------------------------------------------------
# F-S1 单文件 ≤600 物理行（含空行与注释）
# ---------------------------------------------------------------------------
for base in internal cmd; do
  [ -d "$SRC_ROOT/$base" ] || continue
  while IFS= read -r f; do
    rel="${f#"$SRC_ROOT"/}"
    n=$(wc -l < "$f")
    if [ "$n" -gt 600 ]; then
      base_value="${B_S1[$rel]:-}"
      if [ -n "$base_value" ] && [ "$n" -le "$base_value" ]; then
        echo "F-S1 冻结: $rel 有 $n 行（基线 $base_value）"
      elif exempted "$rel" F-S1; then
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
    local metric="$1" current="$2" limit="$3" base="$4"
    [ "$current" -le "$limit" ] && return 0
    if [ -n "$base" ] && [ "$current" -le "$base" ]; then
      echo "F-S5 冻结: $rel $metric $current（基线 $base）"
      return 0
    fi
    echo "F-S5 $metric 违规: $rel $current (上限 $limit)"
    FAIL=1
  }
  check_metric files "$files" 10 "${B_S5_FILES[$rel]:-}"
  check_metric lines "$lines" 3000 "${B_S5_LINES[$rel]:-}"
  check_metric exports "$exports" 20 "${B_S5_EXPORTS[$rel]:-}"
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
    base_value="${B_S3[$key]:-}"
    if [ -n "$base_value" ] && [ "$cx" -le "$base_value" ]; then
      echo "F-S3 冻结: $key 复杂度 $cx（基线 $base_value）"
    else
      echo "F-S3 违规: $key 复杂度 $cx（上限 10）"
      FAIL=1
    fi
  done <<< "$cyclo_out"
else
  echo "⚠️  gocyclo 未安装，F-S3 未检查（go.mod 的 tool 指令或 90-Qoder执行手册 §二）"
fi

if [ "$FAIL" -eq 0 ]; then
  echo "✅ 规模红线全部通过（F-S1/F-S3/F-S5；F-S2/F-S4 见 TestSizeLimits）"
fi
exit "$FAIL"
