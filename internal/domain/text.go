// 文件名: text.go
// 用途: rune 切片、Markdown 反转义、标题归一与不可信正文的 nonce 包裹。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// unescapeRe 覆盖实测的 32 种被转义字符。
// 字符类按实测 32 字符全集构造：早期示例正则漏了 ' 与 +，
// 按实测频次（\' 197 次、\+ 167 次）与 32 字符验收补回。
//
// MUST 单趟 ReplaceAllString：实测有 26 处嵌套转义（原文本身含 \.，
// 上游再转义一层得到 \\\.）。单趟只脱一层得到 \.；多趟会继续得到 .，
// 把原文改掉。字符类里的反引号无法写进 Go 反引号串，故用拼接。
var unescapeRe = regexp.MustCompile(`\\([/#.,:{}()\-!*\|=_;'+\[\]<>"%?&@` + "`" + `~$^\\])`)

// SliceByRune 按 rune 切片，返回切片结果与是否发生截断。
//
// 中文 1 字 3 字节，用 s[i:j] 会把 UTF-8 截成 U+FFFD，所以所有
// offset/limit 一律按 rune 计。
func SliceByRune(s string, offset, limit int) (string, bool) {
	r := []rune(s)
	if offset < 0 {
		offset = 0
	}
	if offset > len(r) {
		offset = len(r)
	}
	if limit < 0 {
		limit = 0
	}
	if limit > len(r)-offset {
		limit = len(r) - offset
	}
	return string(r[offset : offset+limit]), offset+limit < len(r)
}

// Unescape 只反转义 32 种 Markdown 字符，代码里的 \n \d \w \s 原样保留。
func Unescape(s string) string {
	return unescapeRe.ReplaceAllString(s, "$1")
}

// NormalizeTitle 把标题归一成可比较形式：去首尾空白、全角转半角、
// 转小写、连续空白压成一个。
func NormalizeTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(halfWidth(r))
	}
	return strings.Join(strings.Fields(strings.ToLower(b.String())), " ")
}

// untrustedTagRe 匹配任何试图伪造包裹定界符的标签，含大小写、
// 尾随空白与属性变体；只中和标签开头的 '<'，不动正文其他内容。
var untrustedTagRe = regexp.MustCompile(`(?i)</?\s*untrusted_content\b[^>]*>`)

// WrapUntrusted 把上游正文包进带每次调用随机 nonce 的定界块。
//
// 固定标签挡不住伪造：LLM 对标签大小写与尾随空白不敏感，攻击者写
// </UNTRUSTED_CONTENT> 或 </untrusted_content > 就能提前闭合。nonce 每次
// 重新生成（crypto/rand 6 字节 hex），body 内同一 nonce 串与标签状片段
// 都会被中和，闭合标签因此无法被构造。
func WrapUntrusted(source, body string) string {
	nonce, err := newNonce()
	if err != nil {
		return wrapUntrustedDegraded(source, body)
	}
	return wrapUntrusted(source, body, nonce)
}

// newNonce 生成 6 字节 hex 定界符。MUST 用 crypto/rand，math/rand 可预测。
func newNonce() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func wrapUntrusted(source, body, nonce string) string {
	return `<untrusted_content id="` + nonce + `" source="` + html.EscapeString(source) + `">` + "\n" +
		neutralizeBody(body, nonce) + "\n" +
		`</untrusted_content id="` + nonce + `">`
}

// neutralizeBody 先抹掉 body 内可能出现的同一 nonce，再中和所有
// untrusted_content 标签片段；两步都只改使定界失效所需的最小内容。
func neutralizeBody(body, nonce string) string {
	nonceRe := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(nonce))
	body = nonceRe.ReplaceAllString(body, "***")
	return untrustedTagRe.ReplaceAllStringFunc(body, func(tag string) string {
		return "&lt;" + tag[1:]
	})
}

// wrapUntrustedDegraded 是随机源失效时的退化路径。
// crypto/rand 失败无法恢复；此时退回固定 nonce 并剥离 body 内所有裸 '<'，
// 即使定界符可预测也无法伪造标签。
func wrapUntrustedDegraded(source, body string) string {
	const fixedNonce = "000000000000"
	body = strings.ReplaceAll(body, "<", "&lt;")
	return `<untrusted_content id="` + fixedNonce + `" source="` + html.EscapeString(source) + `">` + "\n" +
		body + "\n" +
		`</untrusted_content id="` + fixedNonce + `">`
}

// halfWidth 把全角 ASCII（U+FF01~U+FF5E）与全角空格转成半角。
func halfWidth(r rune) rune {
	switch {
	case r == '\u3000':
		return ' '
	case r >= '\uFF01' && r <= '\uFF5E':
		return r - 0xFEE0
	}
	return r
}

// TruncateRunes 按 limit 个 rune 截断；limit <= 0 表示不截断。
func TruncateRunes(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	out, _ := SliceByRune(s, 0, limit)
	return out
}

// TruncateUntrusted 按 limit 个 rune 截断 WrapUntrusted 的包裹结果，保留首尾标签。
// 直接切包裹后的字符串会切出未闭合的定界符，调用方无法区分正文结束与标签断裂。
func TruncateUntrusted(wrapped string, limit int) string {
	open := strings.Index(wrapped, ">\n")
	closeAt := strings.LastIndex(wrapped, "\n</untrusted_content")
	if open < 0 || closeAt < open {
		out, _ := SliceByRune(wrapped, 0, limit)
		return out
	}
	prefix := wrapped[:open+2]
	suffix := wrapped[closeAt:]
	avail := limit - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(suffix)
	if avail <= 0 {
		return prefix + suffix
	}
	body, _ := SliceByRune(wrapped[open+2:closeAt], 0, avail)
	return prefix + body + suffix
}

// ClipBytes 按 maxBytes 截断并在末尾追加 "(truncated)"；空输入返回 nil，
// 便于调用方用 nil 判断「该字段不应出现」。按字节截断是安全的：日志是字节流，
// 不面向人类排版；调用方保证只在报文日志里使用。
func ClipBytes(body []byte, maxBytes int) []byte {
	if len(body) == 0 || maxBytes <= 0 {
		return nil
	}
	if len(body) <= maxBytes {
		return body
	}
	out := make([]byte, 0, maxBytes+16)
	out = append(out, body[:maxBytes]...)
	return append(out, "...(truncated)"...)
}
