#!/usr/bin/env bash
# 文件名: SyncDocs.sh
# 用途: Doc/ → GitHubSrc/docs/ 单向同步。--check 只比对不写。
#       docs/ 是产物，MUST NOT 手写（19-AGENTS.md全文 X11）。
# 创建日期: 2026-10-20
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
# 用法: SyncDocs.sh [--check] [<文档根> [<代码根>]]
#   开发机（在 GitHubSrc/ 下）: bash Scripts/SyncDocs.sh --check ../Doc .
#   CI（仓库根，没有 Doc/）  : bash Scripts/SyncDocs.sh --check ../Doc .
set -euo pipefail

MODE="sync"
if [ "${1:-}" = "--check" ]; then MODE="check"; shift; fi
DOC_ROOT="${1:-../Doc}"
CODE_ROOT="${2:-.}"
DEST="$CODE_ROOT/docs"

if [ ! -d "$DOC_ROOT" ]; then
  # 🔴 CI 语境：仓库里只有同步产物 docs/，没有 Doc/。
  #    MUST NOT 假装比对通过；退化为「docs/ 存在且非空」——
  #    这一条能抓住「忘了把 docs/ 提交上来」，是真检查。
  echo "[SKIP] 文档根 $DOC_ROOT 不存在（CI 语境只有 docs/）"
  [ -d "$DEST" ] || { echo "[FAIL] $DEST 不存在 —— docs/ 忘了提交"; exit 1; }
  n=$(find "$DEST" -name '*.md' | wc -l)
  [ "$n" -gt 0 ] || { echo "[FAIL] $DEST 是空的"; exit 1; }
  echo "[OK] $DEST 存在且含 $n 份 .md"
  exit 0
fi

if [ "$MODE" = "check" ]; then
  if diff -r -q "$DOC_ROOT" "$DEST" >/dev/null 2>&1; then
    echo "[OK] docs/ 与 Doc/ 无差异"
    exit 0
  fi
  echo "[FAIL] docs/ 与 Doc/ 有差异（MUST 跑 bash Scripts/SyncDocs.sh 后重新提交）："
  diff -r -q "$DOC_ROOT" "$DEST" || true
  exit 1
fi

# 同步模式：先清空再整目录复制。MUST NOT 用软链接（项目铁律），MUST NOT 反向同步。
mkdir -p "$DEST"
find "$DEST" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
cp -R "$DOC_ROOT"/. "$DEST"/
echo "[OK] 已同步 $(find "$DEST" -name '*.md' | wc -l) 份文档到 $DEST"
