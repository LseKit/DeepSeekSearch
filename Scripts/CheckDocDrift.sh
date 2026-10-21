#!/usr/bin/env bash
# 文件名: CheckDocDrift.sh
# 用途: 检查 Doc/ 内部一致性，防止文档漂移。8 项检查全部通过才退出 0。
#       本脚本在「只有文档、还没有代码」的阶段也能跑（第 7 项在代码存在时才生效，第 8 项在 AGENTS.md 生成后才生效）。
# 创建日期: 2026-10-20
# 作者: 小逸
# 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
set -uo pipefail

DOC_DIR="${1:-$(cd "$(dirname "$0")/../../Doc" 2>/dev/null && pwd || echo "../../Doc")}"
SRC_DIR="${2:-$(cd "$(dirname "$0")/.." 2>/dev/null && pwd || echo "..")}"
FAIL=0

say()  { printf '%s\n' "$*"; }
ok()   { printf '  [OK]   %s\n' "$*"; }
bad()  { printf '  [FAIL] %s\n' "$*"; FAIL=1; }

say "=============================================="
say " DeepSeekSearch 文档漂移检查"
say " Doc = $DOC_DIR"
say " Src = ${SRC_DIR:-(不存在，跳过代码侧比对)}"
say "=============================================="

# ---------------------------------------------------------------- 检查 1
say ""
say "[1/8] 引用的文档路径是否都存在"
# 抽取所有形如 `XX-名字.md` 的反引号引用，逐个确认文件存在
REFS=$(grep -rhoP '`[0-9]{2}-[^`]+\.md`' "$DOC_DIR" 2>/dev/null | tr -d '`' | sort -u)
NR=0; NM=0
while IFS= read -r ref; do
    [ -z "$ref" ] && continue
    NR=$((NR+1))
    if ! find "$DOC_DIR" -name "$ref" -print -quit | grep -q .; then
        bad "引用了不存在的文档: $ref"
        NM=$((NM+1))
    fi
done <<< "$REFS"
[ "$NM" -eq 0 ] && ok "共 $NR 个文档引用，全部存在"

# 禁止 `Contract/13` 这种「只有编号没有文件名」的引用：读者无法定位，改名后也无人发现
SHORT=$(grep -rnoP '`(Contract|Engineering|Decisions|Tasks|Ops|Handover)/[0-9]{2}(?![0-9~-])' "$DOC_DIR" 2>/dev/null | grep -v '/99-' || true)
if [ -n "$SHORT" ]; then
    while IFS= read -r line; do bad "短路径引用（必须写全文件名）: $line"; done <<< "$SHORT"
else
    ok "无短路径引用"
fi

# ---------------------------------------------------------------- 检查 2
say ""
say "[2/8] 错误码条数一致性（已排除 99-* 审计文档）"
# 🔴 改为**正向抽取**：把全库每一处「N 个/条 [1xxx|2xxx|3xxx] 错误码」声明都抽出来，
#    逐个与码表实际条数/分段数比对。
#    原实现是「黑名单式」—— 硬编码 (34|38|40|41) 四个历史数字当「旧表述」。两个致命缺陷：
#      ① 码表真的变成 41 条以后，41 自己进了黑名单 ⇒ 正确表述被判 FAIL（本轮就撞上了）；
#      ② 写「37 个错误码」或「15 个 2xxx」这类**从未出现过的**错数字全部放行。
#    正向抽取对两者都免疫。
CODES=$(grep -oP '^\| \*\*\K[0-9]{4}' "$DOC_DIR/Contract/14-错误契约.md" 2>/dev/null | sort -u)
ACTUAL=$(echo "$CODES" | grep -c . || true)
SEG1=$(echo "$CODES" | grep -c '^1' || true)
SEG2=$(echo "$CODES" | grep -c '^2' || true)
SEG3=$(echo "$CODES" | grep -c '^3' || true)
say "  码表实际: 共 $ACTUAL 条（1xxx=$SEG1 2xxx=$SEG2 3xxx=$SEG3）"

# 审计/验证类文档（99-*.md）会**引用**历史错误值来说明「修了什么」，
# 它们不是规则来源，因此从一致性检查里排除。排除逻辑显式写在这里，不藏在别处。
# ADR.md 与 14 号 §七 也会写「由 39 → 41」这类**变更记录**，同样排除（它们的语义是「旧值」）。
# 🔴 MUST 用 grep -rn（**不带 -o**）取到完整源行，否则拿不到判定「引用以推翻」所需的上下文。
#    带 -o 时输出只有匹配片段，`grep -v '→'` 过滤的是片段自己 —— 永远过滤不掉任何东西，
#    于是台账/ADR 里「码表 39 → 41」这种**正确的修正说明**会被判成 FAIL。
DECL=$(grep -rnP '(?<![0-9])[0-9]{1,3}\s*(个|条)\s*(1xxx|2xxx|3xxx)?\s*(错误)?码' "$DOC_DIR" 2>/dev/null \
       | grep -v '/99-' | grep -v '/Decisions/ADR.md' || true)
DECL_BAD=""
DECL_N=0
# 引用旧数字**以便说明它已被改掉**的行，不算漂移。与 CheckConsistency.py 的
# REFUTE_MARKERS 保持同一套语义（两个脚本口径必须一致，否则一绿一红）。
REFUTE='推翻|已被|已删|旧文档|旧设计|旧判据|旧结论|旧实现|MUST NOT|不成立|假阳性|错误的说法|复测否定|不再|→|改为|已改'
if [ -n "$DECL" ]; then
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        loc="${line%%:[0-9]*}"                 # 文件名（路径里无「:数字」，故安全）
        lineno=$(echo "$line" | grep -oP '^\Q'"$loc"'\E:\K[0-9]+')
        content="${line#*:*:}"                 # 源行内容
        # 一行里可能有多处声明（如「码表 39 → 41，分段 11/13/15 → 11/14/16」），逐个判
        while IFS= read -r txt; do
            [ -z "$txt" ] && continue
            DECL_N=$((DECL_N + 1))
            n=$(echo "$txt" | grep -oP '^[0-9]{1,3}')
            seg=$(echo "$txt" | grep -oP '1xxx|2xxx|3xxx' || true)
            case "$seg" in
                1xxx) want=$SEG1 ;;
                2xxx) want=$SEG2 ;;
                3xxx) want=$SEG3 ;;
                *)    want=$ACTUAL ;;
            esac
            if [ "$n" != "$want" ]; then
                if echo "$content" | grep -qE "$REFUTE"; then
                    continue                   # 引用以推翻 / 变更记录，合法
                fi
                DECL_BAD="${DECL_BAD}  ${loc}:${lineno}: 声明「${txt}」，实际 ${want}
"
            fi
        done <<< "$(echo "$content" | grep -oP '(?<![0-9])[0-9]{1,3}\s*(个|条)\s*(1xxx|2xxx|3xxx)?\s*(错误)?码')"
    done <<< "$DECL"
fi
if [ -n "$DECL_BAD" ]; then
    while IFS= read -r line; do [ -n "$line" ] && bad "错误码条数声明不符 → $line"; done <<< "$DECL_BAD"
else
    ok "全部 $DECL_N 处条数声明与码表一致（含分段声明）"
fi

# 🔴 补充：14 号开头「码表规模：**41 条**（1xxx=11 / 2xxx=14 / 3xxx=16）」是**最显眼**
#    的一处声明，但上面那条正则要求「N 条码」连续出现（"41 条（…" 后面是括号），
#    所以它**从来没被校验过**。变异测试把 41 改成 39 时本脚本仍判绿，就是这条漏检。
SEG_SRC="$DOC_DIR/Contract/14-错误契约.md"
SEG_LINE=$(grep -nP '码表规模|码表(共|实际)' "$SEG_SRC" 2>/dev/null | head -1 || true)
if [ -n "$SEG_LINE" ]; then
    TOT=$(echo "$SEG_LINE" | grep -oP '(?<![0-9])[0-9]{1,3}(?=\s*\**) ' | head -1 || true)
    TOT=$(echo "$SEG_LINE" | grep -oP '[0-9]{1,3}(?=\s*\*?\*?\s*条)' | head -1 || true)
    SA=$(echo "$SEG_LINE" | grep -oP '1xxx\s*=\s*\K[0-9]+' | head -1 || true)
    SB=$(echo "$SEG_LINE" | grep -oP '2xxx\s*=\s*\K[0-9]+' | head -1 || true)
    SC=$(echo "$SEG_LINE" | grep -oP '3xxx\s*=\s*\K[0-9]+' | head -1 || true)
    if [ -n "$TOT" ]; then
        [ "$TOT" = "$ACTUAL" ] && ok "码表规模声明 = $TOT（与实测一致）" \
                              || bad "码表规模声明 $TOT ≠ 实测 $ACTUAL"
    else
        bad "14 号「码表规模」行存在但抽不出总数（格式变了？请检查正则）"
    fi
    if [ -n "$SA" ] && [ "$SA/$SB/$SC" != "$SEG1/$SEG2/$SEG3" ]; then
        bad "分段声明 $SA/$SB/$SC ≠ 实测 $SEG1/$SEG2/$SEG3"
    fi
fi

# ---------------------------------------------------------------- 检查 3
say ""
say "[3/8] 工具名一致性（必须只有 ds_search / ds_fetch / ds_health / ds_research / ds_extract；已排除 99-*）"
OLD=$(grep -rnoP '\b(search_web|fetch_page|searchWeb|fetchPage)\b' "$DOC_DIR" 2>/dev/null \
      | grep -v '/99-' || true)
if [ -n "$OLD" ]; then
    while IFS= read -r line; do bad "残留旧工具名: $line"; done <<< "$OLD"
else
    ok "无旧工具名残留"
fi
for t in ds_search ds_fetch ds_health ds_research ds_extract; do
    n=$(grep -rl "$t" "$DOC_DIR" 2>/dev/null | wc -l)
    [ "$n" -ge 3 ] && ok "$t 出现在 $n 份文档中" || bad "$t 只出现在 $n 份文档中（应为 ≥3）"
done

# ---------------------------------------------------------------- 检查 4
say ""
say "[4/8] 包白名单两处一致（Contract/10 与 Engineering/22）"
A=$(grep -oP '^### 2\.[0-9.]+ `internal/\K[a-z/]+' "$DOC_DIR/Contract/10-包依赖矩阵.md" | sort -u)
B=$(grep -oP '^\s+"internal/\K[a-z/]+(?=")' "$DOC_DIR/Engineering/22-架构守护规则.md" | sort -u)
if [ "$A" = "$B" ]; then
    ok "两处包名集合一致: $(echo "$A" | tr '\n' ' ')"
else
    bad "包名集合不一致"
    say "    10 号文档: $(echo "$A" | tr '\n' ' ')"
    say "    22 号文档: $(echo "$B" | tr '\n' ' ')"
fi
NA=$(echo "$A" | grep -c . || true); NB=$(echo "$B" | grep -c . || true)
NCMD=$(grep -cE '^### 2\.[0-9.]+ `cmd/' "$DOC_DIR/Contract/10-包依赖矩阵.md")
if [ "$NA" = "$NB" ] && [ "$NA" = 16 ] && [ "$NCMD" = 1 ]; then
    ok "包数量 = 17（16 个 internal + 1 个 cmd）"
else
    bad "包数量不一致（10: internal=$NA cmd=$NCMD, 22: internal=$NB）"
fi

# ------------------------------------------------------------------ 4b
# 🔴 只比「包名集合」是不够的（D2/C10）。有人把 allowedImports["internal/tools"] 的值
#    加上 "internal/transport"（正是 §262 点名的「拆护栏」行为），包名集合完全不变 ⇒ 脚本全绿。
#    下面把两处都归一成 "包 依赖" 行，做**值级双向 diff**。
PKGS="domain config logging deepseek health mcputil resilience resilience/budget resilience/cache resilience/circuit resilience/ratelimit resilience/retry tools toolschema toolvalidate transport"

# 4b-1：22 号 allowedImports（Go map 字面量，每项单行）→ "internal/x internal/y"
MAP22=$(awk '
    /^var allowedImports[[:space:]]*=/ {f=1; next}
    f && /^}/ {f=0}
    f {
        if (match($0, /"(internal|cmd)\/[a-z/]+"/)) {
            key = substr($0, RSTART+1, RLENGTH-2)
            rest = substr($0, RSTART+RLENGTH)
            n = split(rest, a, "\"")
            for (i=2; i<=n; i+=2)
                if (a[i] ~ /^(internal|cmd)\//) print key " " a[i]
        }
    }' "$DOC_DIR/Engineering/22-架构守护规则.md" | sort -u)

# 4b-2：10 号 §2.x 表格「允许」列 → "internal/x internal/y"
#        cmd 段写的是「全部 `internal/*`」，展开为 16 条。
MAP10=$(awk -v pkgs="$PKGS" '
    BEGIN { np = split(pkgs, P, " ") }
    /^### 2\.[0-9.]+ `/ {
        # 🔴 MUST 用 match() 取**第一对**反引号：sub(/^.*`/) 是贪婪的，
        #    对 "### 2.1 `internal/domain`" 会吃到最后一个反引号 ⇒ pkg 变空串。
        if (match($0, /`[^`]+`/)) pkg = substr($0, RSTART+1, RLENGTH-2)
        next
    }
    /^\|/ && pkg != "" {
        n = split($0, C, "|")
        if (n < 3) next
        allow = C[2]
        if (allow ~ /^[[:space:]]*允许/) next          # 表头
        if (allow ~ /^[[:space:]]*---/) next           # 分隔行
        if (allow ~ /全部[[:space:]]*`internal\/\*`/) {   # cmd 段
            for (i=1; i<=np; i++) print pkg " internal/" P[i]
            next
        }
        for (i=1; i<=np; i++)
            if (allow ~ ("`internal/" P[i] "`")) print pkg " internal/" P[i]
    }' "$DOC_DIR/Contract/10-包依赖矩阵.md" | sort -u)

if [ -z "$MAP22" ] || [ -z "$MAP10" ]; then
    bad "白名单值级比对无法进行（10 号抽到 $(echo "$MAP10"|grep -c .||true) 条 / 22 号抽到 $(echo "$MAP22"|grep -c .||true) 条）—— 检查表格与 map 的格式是否被改动"
else
    WDIFF=$(comm -3 <(echo "$MAP10") <(echo "$MAP22"))
    if [ -z "$WDIFF" ]; then
        ok "白名单**值级**双向一致（$(echo "$MAP10" | grep -c .) 条 包→依赖 关系）"
    else
        W10=$(echo "$WDIFF" | grep -v $'^\t' | tr '\n' ';')
        W22=$(echo "$WDIFF" | grep $'^\t' | tr -d '\t' | tr '\n' ';')
        [ -n "$W10" ]  && bad "10 号允许但 22 号白名单没有（**实现会编译不过**）: $W10"
        [ -n "$W22" ] && bad "22 号白名单有但 10 号未允许（**护栏被放宽**）: $W22"
    fi
fi

# 4b-3：allowedExternal 必须存在且覆盖全部 8 个包（C10：外部 import 此前完全不受检）
EXT_PKGS=$(awk '
    /^var allowedExternal[[:space:]]*=/ {f=1; next}
    f && /^}/ {f=0}
    f && match($0, /"(internal|cmd)\/[a-z/]+"/) { print substr($0, RSTART+1, RLENGTH-2) }
' "$DOC_DIR/Engineering/22-架构守护规则.md" | sort -u)
EXT_N=$(echo "$EXT_PKGS" | grep -c . || true)
if [ "$EXT_N" = 17 ]; then
    ok "allowedExternal 覆盖全部 17 个包（外部 import 受检）"
else
    bad "allowedExternal 只覆盖 $EXT_N 个包（应 17）: $(echo "$EXT_PKGS"|tr '\n' ' ')"
fi
# 10 号 §2.x 的「标准库」列与 22 号 allowedExternal 的**标准库项**做双向 diff
STD_OK=0; STD_TRIED=0
for short in $PKGS cmd; do
    # 🔴 $PKGS 里是**裸名**（domain / config / ...），而 22 号 allowedExternal 的键是
    #    完整路径（"internal/domain"）。直接拿裸名当 key 会一条都匹配不到，
    #    然后 comm -3 把 10 号的全部条目误报成「不一致」—— 看起来像文档漂移，其实是脚本 bug。
    if [ "$short" = cmd ]; then pkg="cmd/deepseeksearch"; else pkg="internal/$short"; fi
    D10=$(awk -v want="$short" '
        /^### 2\.[0-9.]+ `/ { if (match($0, /`[^`]+`/)) p = substr($0, RSTART+1, RLENGTH-2); sub(/^internal\//,"",p); sub(/^cmd\/.*/,"cmd",p) }
        /^\|/ && p == want {
            n=split($0,C,"|"); if (n<3) next
            if (C[2] ~ /标准库/) {
                m=split(C[2],K,"`")
                for(i=2;i<=m;i+=2) if (K[i] !~ /^(internal|cmd)\// && K[i] !~ /\./) print K[i]
            }
        }' "$DOC_DIR/Contract/10-包依赖矩阵.md" | sort -u)
    # 🔴 匹配 MUST 容忍行首缩进：Go map 字面量的键是 `    "internal/domain": {`，
    #    用 ^"..." 锚定会一条都匹配不到，然后 comm -3 把 10 号的全部条目误报成「不一致」。
    # 🔴 MUST 先把扫描范围限定在 `var allowedExternal` 块内。
    #    直接用 index($0, "\"internal/domain\":") 会先命中上方 allowedImports 的同名行
    #    （那行是 `{...},` 单行，`/^    }/` 终止条件永不触发）⇒ f 一旦置 1 就再也不清零，
    #    抽出来的是**另一张表**的内容。这是本检查最容易写错的地方。
    D22=$(awk -v key="\"$pkg\"" '
        /^var allowedExternal[[:space:]]*=/ {inblk=1; next}
        inblk && /^}/ {inblk=0}
        !inblk {next}
        index($0, key ":") > 0 {f=1; next}
        f && /^    },?/ {f=0; next}
        f { m=split($0,Q,"\""); for(i=2;i<=m;i+=2) if (Q[i] !~ /^(internal|cmd)\// && Q[i] !~ /\./) print Q[i] }
    ' "$DOC_DIR/Engineering/22-架构守护规则.md" | sort -u)
    [ -z "$D10" ] && continue
    STD_TRIED=$((STD_TRIED+1))
    SD=$(comm -3 <(echo "$D10") <(echo "$D22") | tr -d '\t' | tr '\n' ' ')
    if [ -n "$SD" ]; then bad "$pkg 的标准库白名单两处不一致: $SD"; else STD_OK=$((STD_OK+1)); fi
done
# 🔴 原来的 OK 判据是「grep FAIL 数组」，而 FAIL 数组在此处可能尚未定义 ⇒ 恒真。
#    改为显式计数：必须**每个包都比过**（STD_OK == 实际比对的包数）才算通过。
if [ "$STD_OK" -gt 0 ] && [ "$STD_OK" = "$STD_TRIED" ]; then
    ok "各包标准库白名单两处一致（比对 $STD_OK 个包）"
elif [ "$STD_TRIED" -eq 0 ]; then
    bad "标准库白名单一个包都没比成（10 号的「标准库：」行或 22 号的 allowedExternal 格式被改动）"
fi

# ---------------------------------------------------------------- 检查 5
say ""
say "[5/8] 目录命名二元规则（GitHubSrc 内全小写，外层 PascalCase）"
CASED=$(grep -rnoP '`Internal/[a-z]' "$DOC_DIR" 2>/dev/null || true)
if [ -n "$CASED" ]; then
    while IFS= read -r line; do bad "残留 PascalCase 包路径: $line"; done <<< "$CASED"
else
    ok "无 PascalCase 包路径残留"
fi
LOWERTOP=$(grep -rnoP '`(bin|config|data|logs|doc|scripts|ecosystem)/' "$DOC_DIR" 2>/dev/null \
           | grep -v 'MUST NOT' || true)
if [ -n "$LOWERTOP" ]; then
    while IFS= read -r line; do
        f="${line%%:*}"; rest="${line#*:}"; ln="${rest%%:*}"
        ctx=$(sed -n "${ln}p" "$f" 2>/dev/null)
        case "$ctx" in *"MUST NOT"*) continue ;; esac
        bad "残留小写部署目录: $line"
    done <<< "$LOWERTOP"
fi
ok "小写部署目录检查完成（已排除 MUST NOT 反例行）"

# 版本号
say ""
say "[6/8] 项目版本号真源（首发 v0.1.0）"
# 只看「版本真源」语境：Makefile 的 VERSION 赋值行、以及「唯一真源/首发版本/当前版本」的表述
VBAD=$(grep -rnoP '(VERSION\s*:?=\s*v[0-9]+\.[0-9]+\.[0-9]+|唯一真源[^。\n]*v[0-9]+\.[0-9]+\.[0-9]+|首发[^。\n]*v[0-9]+\.[0-9]+\.[0-9]+)' "$DOC_DIR" 2>/dev/null \
        | grep -v 'v0\.1\.0' || true)
if [ -n "$VBAD" ]; then
    while IFS= read -r line; do bad "版本真源不是 v0.1.0: $line"; done <<< "$VBAD"
else
    ok "版本真源全部为 v0.1.0"
fi
N017=$(grep -c 'v0\.1\.0' "$DOC_DIR/Contract/17-版本与发布契约.md")
[ "$N017" -ge 3 ] && ok "17-版本契约 中出现 v0.1.0 x$N017" || bad "17-版本契约 中 v0.1.0 出现次数过少（$N017）"

# ---------------------------------------------------------------- 检查 6
say ""
say "[7/8] 代码存在时的交叉比对"
if [ -z "$SRC_DIR" ] || [ ! -d "$SRC_DIR/internal" ]; then
    say "  [SKIP] GitHubSrc/internal 不存在（尚未进入编码阶段）"
else
    # 🔴 `grep -c ... || echo -1` 是错的：grep 无匹配时**既退出 1 又打印 0**，
    #    于是 CNT 变成两行 "0\n-1"，后面的 `[ "$CNT" -eq ... ]` 报
    #    "integer expression expected" 并返回非 0 ⇒ 判定结果不可信（脚本无 -e，会继续跑）。
    # 🔴 文件还不存在时必须 SKIP，不能判 FAIL：`codes.go` 由 Task-02 A-01 创建，
    #    在此之前的每个窗口（Task-01 阶段）都会因为 `grep -c` 返回 0 而误报
    #    「常量数 0 ≠ 文档 41」。护栏在「还没写到那儿」时应该沉默，
    #    在「写了但写错」时才响 —— 否则执行窗口会学会无视它。
    if [ ! -f "$SRC_DIR/internal/domain/codes.go" ]; then
        say "  [SKIP] codes.go 尚未创建（Task-02 A-01 之前）"
    else
        CNT=$(grep -c $'^\tCode' "$SRC_DIR/internal/domain/codes.go" 2>/dev/null || true)
        # 🔴 必须用 $'\t'：BRE 里 '\t' 不解析为制表符（被当成字面 t），恒返回 0。
        CNT=${CNT:-0}
        [ "$CNT" -eq "$ACTUAL" ] && ok "codes.go 常量数 = $CNT" || bad "codes.go 常量数 $CNT ≠ 文档 $ACTUAL"
    fi

    # 🔴 键名正则 MUST 含 0-9：ttl_search_s / max_entry_kb / ttl_fetch_s 都带数字，
    #    `[a-z_]+` 会静默漏掉它们（漏检比误报更危险）。
    # 🔴 可选键（默认「不写 = null」）在模板里写作 `# OPTIONAL: key = 示例值`，
    #    MUST 一并计入文档键集合，否则代码里合法的 toml tag 会被判成「文档没有」。
    DKEYS=$(awk '/^```toml/{f=1;next} /^```/{f=0} f' "$DOC_DIR/Contract/13-配置契约.md" \
            | grep -oP '^\s*[a-z0-9_]+\s*(?==)' | tr -d ' =' | sort -u)
    DOPT=$(awk '/^```toml/{f=1;next} /^```/{f=0} f' "$DOC_DIR/Contract/13-配置契约.md" \
            | grep -oP '^\s*#\s*OPTIONAL:\s*\K[a-z0-9_]+(?=\s*=)' | sort -u)
    DKEYS=$(printf '%s\n%s\n' "$DKEYS" "$DOPT" | sort -u | grep .)
    CKEYS=$(grep -rhoP 'toml:"\K[a-z0-9_]+(?=")' "$SRC_DIR/internal/config/"*.go 2>/dev/null | sort -u)
    if [ -n "$CKEYS" ]; then
        # 🔴 MUST 用 comm -3 **双向**比对。原实现 comm -23 只报「文档有而代码无」，
        #    于是实现者悄悄加一个没写进 13 号的配置键（正是 H4 要防的）脚本照样全绿。
        DIFF=$(comm -3 <(echo "$DKEYS") <(echo "$CKEYS"))
        if [ -z "$DIFF" ]; then
            ok "配置键双向一致（各 $(echo "$DKEYS" | grep -c .) 个）"
        else
            ONLY_DOC=$(echo "$DIFF" | grep -v $'^\t' | tr -d ' ' | tr '\n' ' ')
            ONLY_CODE=$(echo "$DIFF" | grep $'^\t' | tr -d '\t ' | tr '\n' ' ')
            [ -n "$ONLY_DOC" ]  && bad "文档有而代码无的键（实现缺失）: $ONLY_DOC"
            [ -n "$ONLY_CODE" ] && bad "代码有而文档无的键（**未登记的配置键**，更危险）: $ONLY_CODE"
        fi
    else
        say "  [SKIP] 未找到 toml tag"
    fi

    # 🔴 工具数的真源是**规范工具名单**（本脚本检查 3 里的 `ds_search ds_fetch ds_health`），
    #    MUST NOT 拿 12 号 §四 的小节数当总数 —— 12 号 §四 是「对外工具**入参**」，
    #    只覆盖有上游参数的两个工具；`ds_health` 是纯本地诊断、没有上游参数，
    #    它在 Task-10 定义。用 12 号的小节数会得出 2，等代码注册了 3 个工具就误报。
    WANT_TOOLS=0
    for t in ds_search ds_fetch ds_health ds_research ds_extract; do WANT_TOOLS=$((WANT_TOOLS+1)); done
    # 🔴 排除 doc.go：它是 A-01 建的包注释占位，不是工具实现。
    #    不排除的话，从 Task-02 起每个窗口都会看到「工具数 0 ≠ 3」的假红。
    TOOL_FILES=$(ls "$SRC_DIR/internal/tools/"*.go 2>/dev/null | grep -v '_test\.go$' | grep -v '/doc\.go$' || true)
    if [ -z "$TOOL_FILES" ]; then
        say "  [SKIP] internal/tools/ 尚无实现文件（Task-08 之前）"
    else
        TCNT=$(grep -h 'func (.*) Name() string' $TOOL_FILES 2>/dev/null | wc -l)
        [ "$TCNT" -eq "$WANT_TOOLS" ] && ok "工具数 = $TCNT（规范名单 $WANT_TOOLS 个）" \
                                     || bad "工具数 $TCNT ≠ 规范名单 $WANT_TOOLS 个"
    fi
fi

# ---------------------------------------------------------------- 检查 8
say ""
say "[8/8] Task 清单与 AGENTS.md 单一真源"
# 8a：文档里提到的 Task-NN 必须有对应文件
TASKS_DIR="$DOC_DIR/Tasks"
if [ -d "$TASKS_DIR" ]; then
    MISSING=0
    for n in $(grep -rhoP 'Task-\d{2}' "$DOC_DIR" 2>/dev/null | sort -u); do
        if ! ls "$TASKS_DIR" 2>/dev/null | grep -q "^${n}-"; then
            bad "文档提到了 $n 但没有对应任务文件"
            MISSING=$((MISSING+1))
        fi
    done
    [ "$MISSING" -eq 0 ] && ok "全部 Task-NN 引用都有对应文件（$(ls "$TASKS_DIR" | wc -l) 个 Task）"
else
    say "  [SKIP] 无 Tasks 目录"
fi

# 8b：AGENTS.md 必须与 19 号文档的标记内内容一致
SRC19=""
for cand in "$DOC_DIR/Contract/19-AGENTS.md全文.md" "$SRC_DIR/docs/Contract/19-AGENTS.md全文.md"; do
    [ -f "$cand" ] && { SRC19="$cand"; break; }
done
# 🔴 MUST 检查**所有**存在的副本，MUST NOT 找到第一个就 break。
#    原实现只取第一个命中的文件：当「部署根 AGENTS.md」与「GitHubSrc/AGENTS.md」
#    同时存在时，后者（真正的交付物）会被完全跳过 —— 这正是「多份手写副本必然漂移」
#    里最危险的一种：守卫看起来在工作，实际只看了其中一半。
#    候选覆盖两种运行语境：
#      ① GitHubSrc/ 内执行：SRC_DIR=.  ⇒ ./AGENTS.md
#      ② 部署根执行：      SRC_DIR=.  ⇒ ./AGENTS.md 与 ./GitHubSrc/AGENTS.md
AGENTS_FILES=""
for cand in "$SRC_DIR/AGENTS.md" \
            "$(dirname "$DOC_DIR")/AGENTS.md" \
            "$(dirname "$DOC_DIR")/GitHubSrc/AGENTS.md"; do
    [ -f "$cand" ] && AGENTS_FILES="${AGENTS_FILES}${cand}"$'\n'
done
# 去重（两种语境下路径可能重叠），并丢掉空行
# 🔴 按 realpath 去重：从 GitHubSrc 执行时 `./AGENTS.md` 与 `../GitHubSrc/AGENTS.md`
#    指向同一个文件，仅按字符串排序去重会把它校验两遍、输出两行看起来像两份副本。
AGENTS_FILES="$(printf '%s' "$AGENTS_FILES" | sed '/^$/d' | while IFS= read -r f; do
    printf '%s\n' "$(realpath "$f")"
done | sort -u)"
if [ -z "$SRC19" ]; then
    say "  [SKIP] 找不到 19-AGENTS.md全文.md"
elif [ -z "$AGENTS_FILES" ]; then
    # 🔴 B7 守卫：曾经存在一个名为 `AGENTS.md `（**尾随空格**）的文件，内容是整份 19 号
    #    文档（含 BEGIN/END 标记）而非应交付的提取正文。后果三重：
    #      ① 本项检查静默 SKIP（精确名不存在）⇒ 单一真源守卫永久失效；
    #      ② `[ -f AGENTS.md ]`、glob、`git add` 全部漏掉它；
    #      ③ 内容还是错的。
    #    所以「找到 AGENTS.md* 但没有精确名」MUST 判 FAIL，MUST NOT 判 SKIP。
    AGENTS_DIR="$(dirname "$SRC_DIR")"
    # sort -u：SRC_DIR 与其父目录在「开发机语境（SRC_DIR=.）」下会重叠，不去重会重复报同一条
    ODDBALLS=$(find "$SRC_DIR" "$AGENTS_DIR" -maxdepth 1 -name 'AGENTS.md*' ! -name 'AGENTS.md' 2>/dev/null | sort -u || true)
    if [ -n "$ODDBALLS" ]; then
        while IFS= read -r f; do
            [ -z "$f" ] && continue
            # 用 <...> 包裹，让尾随空格/制表符肉眼可见（cat -A 的 $ 标记在管道里易被吃掉）
            bad "发现畸形副本 <$(printf '%s' "$f")> —— 精确名 AGENTS.md 不存在，守卫不能 SKIP（B7）"
        done <<< "$ODDBALLS"
    else
        say "  [SKIP] 尚未生成 AGENTS.md（Task-13 之前）"
    fi
else
    NB="$(grep -c 'BEGIN AGENTS.md' "$SRC19" || true)"
    if [ "$NB" -ne 1 ]; then
        bad "19 号文档里 BEGIN 标记出现 $NB 次（必须恰好 1 次），切片会从错误的行开始"
    fi
    t1="$(mktemp)"
    awk '/<!-- BEGIN AGENTS.md -->/{f=1;next} /<!-- END AGENTS.md -->/{f=0} f' "$SRC19" \
        | sed '/./,$!d' > "$t1"   # sed 删掉前导空行：标记后紧跟一个空行，直接输出会让文件以空行开头
    N_COPIES=0
    while IFS= read -r AGENTS_FILE; do
        [ -z "$AGENTS_FILE" ] && continue
        N_COPIES=$((N_COPIES+1))
        if cmp -s "$t1" "$AGENTS_FILE"; then
            ok "$AGENTS_FILE 与 19 号文档一致"
        else
            bad "$AGENTS_FILE 与 19 号文档不一致（改真源 19 号，别只改副本）"
            diff "$t1" "$AGENTS_FILE" | head -10 | sed 's/^/         /'
        fi
    done <<< "$AGENTS_FILES"
    [ "$N_COPIES" -ge 1 ] && ok "共校验 $N_COPIES 份 AGENTS.md 副本（全部 MUST 与 19 号逐字相同）"
    rm -f "$t1"
fi

# ---------------------------------------------------------------- 汇总
say ""
say "=============================================="
if [ "$FAIL" -eq 0 ]; then
    say " 结果: 全部通过，无文档漂移"
else
    say " 结果: 发现漂移，MUST 修复后才能合并"
fi
say "=============================================="
exit "$FAIL"
