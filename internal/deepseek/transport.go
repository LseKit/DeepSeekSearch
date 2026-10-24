// 文件名: transport.go
// 用途: 按 Config 的分层超时构造 http.Transport。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"net"
	"net/http"
	"time"
)

// idleConnTimeout 与 Go 默认值一致；显式写出来是因为下面两个上限一旦调大，
// 空闲连接会长期占着 fd，必须有回收时间。
const idleConnTimeout = 90 * time.Second

// newTransport 把三个分层超时映射到 Transport；单次尝试总闸门由 do() 的 ctx 负责。
// 不走环境代理：上游是固定 API 地址，代理支持不在当前契约里，且会让 DNS 失败测试失真。
func newTransport(cfg *Config) *http.Transport {
	dialer := &net.Dialer{Timeout: cfg.Dial}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   cfg.TLSHandshake,
		ResponseHeaderTimeout: cfg.ResponseHeader,
		ForceAttemptHTTP2:     true,
		// 每主机空闲连接默认只有 2 个，而组合工具一批要并发抓 10 个 URL：
		// 不放开就会每批重新做 TLS 握手（实测冷启动 fetch 4.7s，握手占大头）。
		// 只放开空闲连接数，不设 MaxConnsPerHost —— 并发上限是限流器的职责，
		// 在这里再设一层会让 3000/3075 的归因变成两处。
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     idleConnTimeout,
	}
}
