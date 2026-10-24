// 文件名: redact.go
// 用途: 日志用的 URL query 与上游报文脱敏（键名大小写不敏感、精确匹配）。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"net/url"
	"regexp"
	"strings"
)

// 上游错误 message 可能回显 Key 后 4 位或整段 sk- Key。解析层与日志层
// MUST 共用这两条正则，避免两处写法漂移。
var (
	secretKVRe = regexp.MustCompile(`(?i)(api[_ ]?key|token|secret|authorization)\s*[:=]?\s*\S*`)
	secretSkRe = regexp.MustCompile(`sk-[A-Za-z0-9]{8,}`)
)

// RedactSecrets 抹掉报文中的凭据片段；可重复调用（幂等）。
func RedactSecrets(message string) string {
	message = secretKVRe.ReplaceAllString(message, "[REDACTED]")
	return secretSkRe.ReplaceAllString(message, "sk-***")
}

// RedactURL 抹掉 URL 里的凭据与敏感 query 参数，供日志使用。
//
// userinfo（user:pass@）无条件剥掉：ds_fetch 的 URL 由调用方给，带密码的 URL
// 会在参数校验之前就进 call.begin，而密码对排查毫无用处。
// keys 由调用方从 cfg.Log.RedactQueryKeys 注入；键名大小写不敏感、精确匹配
// （sig 不能命中 sig2）。解析失败时原样返回，绝不 panic。
//
// 放在 domain 而不是 logging：deepseek/tools/transport 三处都要用，而前两者
// 按包依赖矩阵不允许 import logging；这是纯字符串变换，属 domain 的职责。
func RedactURL(raw string, keys []string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	changed := u.User != nil
	u.User = nil

	sensitive := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		sensitive[strings.ToLower(k)] = struct{}{}
	}
	if query, ok := redactQuery(u.RawQuery, sensitive); ok {
		u.RawQuery = query
		changed = true
	}

	if !changed {
		return raw
	}
	return u.String()
}

// redactQuery 把命中 sensitive 的参数值替换为 ***，第二个返回值表示是否改动过。
func redactQuery(rawQuery string, sensitive map[string]struct{}) (string, bool) {
	if rawQuery == "" || len(sensitive) == 0 {
		return rawQuery, false
	}

	// 不用 url.Values.Encode：它会按字典序重排，破坏日志里原本的参数顺序。
	parts := strings.Split(rawQuery, "&")
	changed := false
	for i, part := range parts {
		if part == "" {
			continue
		}
		key, _, _ := strings.Cut(part, "=")
		decoded, decodeErr := url.QueryUnescape(key)
		if decodeErr != nil {
			decoded = key
		}
		if _, ok := sensitive[strings.ToLower(decoded)]; !ok {
			continue
		}
		parts[i] = key + "=***"
		changed = true
	}
	return strings.Join(parts, "&"), changed
}
