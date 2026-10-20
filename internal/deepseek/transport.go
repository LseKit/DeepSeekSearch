// 文件名: transport.go
// 用途: 按 Config 的分层超时构造 http.Transport。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"net"
	"net/http"
)

// newTransport 把三个分层超时映射到 Transport；单次尝试总闸门由 do() 的 ctx 负责。
// 不走环境代理：上游是固定 API 地址，代理支持不在当前契约里，且会让 DNS 失败测试失真。
func newTransport(cfg *Config) *http.Transport {
	dialer := &net.Dialer{Timeout: cfg.Dial}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   cfg.TLSHandshake,
		ResponseHeaderTimeout: cfg.ResponseHeader,
		ForceAttemptHTTP2:     true,
	}
}
