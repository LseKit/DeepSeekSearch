// 文件名: tomlscan.go
// 用途: 对 config.toml 原文做密钥检测、未知键行号定位与已移除段拒绝。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package config

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// 精确全等而非子串：auth_token_env 这类描述用途的键名必须放行（13 号 §1.1.1）。
var secretKeyNames = map[string]bool{
	"api_key": true, "apikey": true, "api_secret": true,
	"token": true, "secret": true, "password": true, "passwd": true,
	"credential": true, "credentials": true, "private_key": true,
}

var secretValuePattern = regexp.MustCompile(`sk-[A-Za-z0-9]{16,}`)

func checkTomlSecrets(data []byte) error {
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return configErr(domain.CodeLocConfigInvalid, "解析 config.toml 失败: %v", err)
	}

	lines := tomlLineIndex(data)
	var errs []error
	walkTomlValues("", raw, func(path string, value any) {
		last := path
		if i := strings.LastIndex(path, "."); i >= 0 {
			last = path[i+1:]
		}
		if secretKeyNames[strings.ToLower(last)] {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid,
				"禁止在 config.toml 放密钥：键名 %q%s", path, lineHint(lines[path])))
			return
		}
		if text, ok := value.(string); ok && isSecretValue(text) {
			errs = append(errs, configErr(domain.CodeLocConfigInvalid,
				"禁止在 config.toml 放密钥：值形态疑似密钥，键 %q%s", path, lineHint(lines[path])))
		}
	})
	return errors.Join(errs...)
}

// walkTomlValues 递归访问所有叶子；数组元素沿用数组键路径，便于报出所在键。
func walkTomlValues(prefix string, value any, visit func(string, any)) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			walkTomlValues(joinKeyPath(prefix, key), child, visit)
		}
	case []map[string]any:
		walkSlice(prefix, len(v), func(i int) any { return v[i] }, visit)
	case []any:
		walkSlice(prefix, len(v), func(i int) any { return v[i] }, visit)
	case []string:
		for _, child := range v {
			visit(prefix, child)
		}
	default:
		visitLeaf(prefix, value, visit)
	}
}

func joinKeyPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func walkSlice(prefix string, n int, at func(int) any, visit func(string, any)) {
	for i := 0; i < n; i++ {
		walkTomlValues(prefix, at(i), visit)
	}
}

func visitLeaf(prefix string, value any, visit func(string, any)) {
	if prefix != "" {
		visit(prefix, value)
	}
}

func isSecretValue(s string) bool {
	if secretValuePattern.MatchString(s) {
		return true
	}
	return utf8.RuneCountInString(s) >= 32 && shannonEntropy(s) >= 4.0
}

func shannonEntropy(s string) float64 {
	counts := make(map[rune]int)
	total := 0
	for _, r := range s {
		counts[r]++
		total++
	}
	if total == 0 {
		return 0
	}
	var entropy float64
	for _, count := range counts {
		p := float64(count) / float64(total)
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// rejectHedgeTable 显式拒绝已移除的 [hedge]：空表不会出现在 Undecoded 里，必须单独扫段名。
func rejectHedgeTable(data []byte) error {
	lines := tomlLineIndex(data)
	paths := make([]string, 0, len(lines))
	for path := range lines {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if path == "hedge" || strings.HasPrefix(path, "hedge.") {
			return configErr(domain.CodeLocConfigInvalid,
				"未知键 %q%s：[hedge] 已从阶段一移除（DEBT-007）", path, lineHint(lines[path]))
		}
	}
	return nil
}

func unknownKeysError(md *toml.MetaData, data []byte) error {
	undecoded := md.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	lines := tomlLineIndex(data)
	errs := make([]error, 0, len(undecoded))
	for _, key := range undecoded {
		path := key.String()
		errs = append(errs, configErr(domain.CodeLocConfigInvalid,
			"未知键 %q%s", path, lineHint(lines[path])))
	}
	return errors.Join(errs...)
}

// tomlLineIndex 把点分键路径映射到首次出现的行号。
// BurntSushi 的 MetaData 不暴露位置，而「拼错的参数名必须报第几行」是硬要求。
func tomlLineIndex(data []byte) map[string]int {
	lines := make(map[string]int)
	section := ""
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripTomlComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = recordTomlSection(line, i+1, lines)
			continue
		}
		recordTomlKey(line, section, i+1, lines)
	}
	return lines
}

// recordTomlSection 记录 [section] 行号并返回当前段名；畸形段名返回空串。
func recordTomlSection(line string, ln int, lines map[string]int) string {
	end := strings.Index(line, "]")
	if end < 0 {
		return ""
	}
	section := strings.TrimSpace(line[1:end])
	if _, seen := lines[section]; !seen {
		lines[section] = ln
	}
	return section
}

// recordTomlKey 记录 key = value 的行号；section 非空时补全为 section.key。
func recordTomlKey(line, section string, ln int, lines map[string]int) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return
	}
	key := normalizeKeyPath(strings.TrimSpace(line[:eq]))
	if key == "" {
		return
	}
	if section != "" {
		key = section + "." + key
	}
	if _, seen := lines[key]; !seen {
		lines[key] = ln
	}
}

// stripTomlComment 只切引号外的 #，避免把值里的 # 当成注释。
func stripTomlComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote && (i == 0 || line[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '#':
			return line[:i]
		}
	}
	return line
}

func normalizeKeyPath(key string) string {
	parts := strings.Split(key, ".")
	for i, part := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(part), `"'`)
	}
	return strings.Join(parts, ".")
}

func lineHint(line int) string {
	if line <= 0 {
		return ""
	}
	return fmt.Sprintf("（第 %d 行）", line)
}
