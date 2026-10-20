// 文件名: redact.go
// 用途: 日志用的 URL query 脱敏（键名大小写不敏感、精确匹配）。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"net/url"
	"strings"
)

// RedactURL 把 query 中命中 keys 的参数值替换为 ***。
//
// keys 由调用方从 cfg.Log.RedactQueryKeys 注入；键名大小写不敏感、精确匹配
// （sig 不能命中 sig2）。解析失败或无 query 时原样返回，绝不 panic。
//
// 放在 domain 而不是 logging：deepseek/tools/transport 三处都要用，而前两者
// 按包依赖矩阵不允许 import logging；这是纯字符串变换，属 domain 的职责。
func RedactURL(raw string, keys []string) string {
	if len(keys) == 0 {
		return raw
	}

	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}

	sensitive := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		sensitive[strings.ToLower(k)] = struct{}{}
	}

	// 不用 url.Values.Encode：它会按字典序重排，破坏日志里原本的参数顺序。
	parts := strings.Split(u.RawQuery, "&")
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

	if !changed {
		return raw
	}
	u.RawQuery = strings.Join(parts, "&")
	return u.String()
}
