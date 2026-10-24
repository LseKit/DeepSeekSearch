// 文件名: ssrf_zone_test.go
// 用途: 验证带 zone 的 IPv6 主机名同样命中 SSRF 黑名单（netip 的 Contains 对 zone 恒 false）。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

import "testing"

func TestSSRFBlockedIPv6Zone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
	}{
		{name: "链路本地带 zone", host: "fe80::1%eth0"},
		{name: "唯一本地带 zone", host: "fd00::dead%eth0"},
		{name: "回环带 zone", host: "::1%lo0"},
		{name: "IPv4 映射回环带 zone", host: "::ffff:127.0.0.1%eth0"},
		{name: "公网带 zone 不拦", host: "2606:2800:220:1:248:1893:25c8:1946%eth0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantBlocked := tt.name != "公网带 zone 不拦"
			rule, blocked := SSRFBlocked(tt.host, nil)
			if blocked != wantBlocked {
				t.Fatalf("SSRFBlocked(%q) = %q, %v; want blocked=%v", tt.host, rule, blocked, wantBlocked)
			}
			if blocked && rule == "" {
				t.Fatalf("命中拦截时 MUST 给出规则名，host=%q", tt.host)
			}
		})
	}
}
