// 文件名: url.go
// 用途: URL 规范化（缓存 key 与去重）与 FNV-1a 64 哈希。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

// NormalizeUrl 把 URL 归一成缓存 key 与去重用的稳定形式。
//
// 结果 MUST NOT 发给上游：归一可能改变语义。
// trackingParams 由调用方从配置注入，传 nil 表示不剥离任何参数；
// 在 domain 硬编码这张表会让 [dedupe].tracking_params 变成假配置。
func NormalizeUrl(raw string, trackingParams []string) (string, error) { //nolint:revive // 对外函数名固定为 NormalizeUrl/HashUrl
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	if u.User != nil {
		return "", errors.New("url must not contain credentials")
	}

	host, err := normalizeHost(u, scheme)
	if err != nil {
		return "", err
	}

	u.Scheme = scheme
	u.Host = host
	u.Fragment = ""
	u.RawFragment = ""
	u.ForceQuery = false
	// 非法转义的 query 不能交给 url.Query：它会静默丢弃解析失败的部分，
	// 让两个不同的 URL 归一成同一个缓存 key。解析失败时保留原串。
	//
	// 🔴 解析失败最常见的原因是 query 里带 `;`（Go 1.17 起 ParseQuery 直接报错）。
	// 此时 MUST NOT 整段跳过剥离：同一个页面会因一个分号变成不同缓存 key，白花一次计费
	// （实测 `?utm_source=a;b` 与 `?` 是两条缓存记录）。退化为「只删追踪片段、其余原样保留」。
	if values, err := url.ParseQuery(u.RawQuery); err == nil {
		u.RawQuery = filterQuery(values, trackingParams)
	} else {
		u.RawQuery = stripTrackingLoose(u.RawQuery, trackingParams)
	}

	// 根路径与无路径必须归一成同一个 key；非根路径的尾斜杠有语义，MUST NOT 删。
	if u.Path == "/" {
		u.Path = ""
		u.RawPath = ""
	}

	return u.String(), nil
}

// HashUrl 返回字符串的 FNV-1a 64 位哈希，用于 URL 去重。
//
// 手写循环而不是 hash/fnv：后者的 Write 返回一个永远为 nil 的 error，
// 在 errcheck 的 check-blank 下会被迫写一条不可达的错误分支。
func HashUrl(s string) uint64 { //nolint:revive // 对外函数名固定为 NormalizeUrl/HashUrl
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)

	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

// normalizeHost 统一主机大小写、去掉默认端口，并给 IPv6 补回方括号。
func normalizeHost(u *url.URL, scheme string) (string, error) {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("url must have a host")
	}

	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(host, port), nil
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]", nil
	}
	return host, nil
}

// filterQuery 剥离追踪参数，并按字典序重排剩余键与同名值。
// 键名比较大小写不敏感、精确匹配；前缀相同不算命中。
// stripTrackingLoose 用于 url.ParseQuery 失败的 query：只删掉键名命中 trackingParams
// 的 `&` 片段，其余原样保留（不重新编码、不排序）。
//
// 为什么不复用 filterQuery：那条路会 Encode()，而解析失败的 query 本来就没法用
// url.Values 安全表示；重编码反而可能把两个语义不同的 URL 归一成同一个缓存 key。
// 降级目标只有一个 —— 让追踪参数不再击穿缓存，其余字节一律不动。
func stripTrackingLoose(raw string, trackingParams []string) string {
	if raw == "" || len(trackingParams) == 0 {
		return raw
	}

	drop := make(map[string]struct{}, len(trackingParams))
	for _, p := range trackingParams {
		drop[strings.ToLower(p)] = struct{}{}
	}

	parts := strings.Split(raw, "&")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil {
			decoded = key
		}
		if _, ok := drop[strings.ToLower(decoded)]; ok {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&")
}

func filterQuery(values url.Values, trackingParams []string) string {
	if len(values) == 0 {
		return ""
	}

	drop := make(map[string]struct{}, len(trackingParams))
	for _, p := range trackingParams {
		drop[strings.ToLower(p)] = struct{}{}
	}
	for key := range values {
		if _, ok := drop[strings.ToLower(key)]; ok {
			delete(values, key)
		}
	}

	// Values.Encode 只排序键名，不排序同名键的值；契约要求值也按字典序。
	for _, vals := range values {
		sort.Strings(vals)
	}

	return values.Encode()
}
