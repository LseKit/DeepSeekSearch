// 文件名: ssrf.go
// 用途: 判定主机是否指向内网或保留地址，供 ds_fetch 的 URL 校验使用。
// 创建日期: 2026-10-24
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

import (
	"net/netip"
	"strconv"
	"strings"
)

// blockedNets 是 SSRF 黑名单。定位是纵深防御：抓取由上游执行，我方不直连目标 URL，
// 但 base_url 改成自建代理时它就是第一道防线。
var blockedNets = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

var blockedSuffixes = []string{".internal", ".local", ".localhost"}

// SSRFBlocked 返回命中的规则名；第二个返回值表示是否应拦截。
func SSRFBlocked(host string, blockedHosts []string) (string, bool) {
	if host == "localhost" {
		return "localhost", true
	}
	for _, suffix := range blockedSuffixes {
		if strings.HasSuffix(host, suffix) {
			return suffix, true
		}
	}
	for _, blocked := range blockedHosts {
		if host == strings.ToLower(strings.TrimSuffix(blocked, ".")) {
			return blocked, true
		}
	}
	if addr, ok := parseHostIP(host); ok {
		return addrBlockedPrefix(addr)
	}
	return "", false
}

func addrBlockedPrefix(addr netip.Addr) (string, bool) {
	// WithZone("") 不可省：netip.Prefix.Contains 对带 zone 的地址恒返回 false，
	// 于是 http://[fe80::1%25eth0]/ 能穿过整张黑名单（实测放行到上游才被拒）。
	// zone 只是本机网卡名，与「这个地址是不是内网」无关。
	addr = addr.Unmap().WithZone("")
	if addr.IsUnspecified() {
		return "unspecified", true
	}
	for _, prefix := range blockedNets {
		if prefix.Contains(addr) {
			return prefix.String(), true
		}
	}
	return "", false
}

// parseHostIP 处理十进制/八进制/十六进制 IP 与 inet_aton 省略写法。
// netip.ParseAddr("2130706433") 会直接失败；若就此放过，它会被当成普通域名。
func parseHostIP(host string) (netip.Addr, bool) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr, true
	}
	if strings.Contains(host, ":") {
		return netip.Addr{}, false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 1 || len(labels) > 4 {
		return netip.Addr{}, false
	}
	values := make([]uint64, len(labels))
	for i, label := range labels {
		if label == "" {
			return netip.Addr{}, false
		}
		value, err := strconv.ParseUint(label, 0, 64)
		if err != nil {
			return netip.Addr{}, false
		}
		values[i] = value
	}
	number, ok := CombineIPv4(values)
	if !ok {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte{
		byte(number >> 24), byte(number >> 16), byte(number >> 8), byte(number), //nolint:gosec // 已逐段校验范围
	}), true
}
