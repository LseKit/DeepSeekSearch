// 文件名: ssrf_test.go
// 用途: 验证内网、保留地址与自定义黑名单的 SSRF 判定。
// 创建日期: 2026-10-24
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

import "testing"

func TestSSRFBlocked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host    string
		blocked bool
	}{
		{host: "localhost", blocked: true},
		{host: "127.0.0.1", blocked: true},
		{host: "10.1.2.3", blocked: true},
		{host: "172.16.0.1", blocked: true},
		{host: "192.168.1.1", blocked: true},
		{host: "192.0.0.1", blocked: true},
		{host: "198.18.0.1", blocked: true},
		{host: "::", blocked: true},
		{host: "::1", blocked: true},
		{host: "fc00::1", blocked: true},
		{host: "example.internal", blocked: true},
		{host: "8.8.8.8", blocked: false},
		{host: "example.com", blocked: false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			if _, got := SSRFBlocked(tt.host, nil); got != tt.blocked {
				t.Fatalf("SSRFBlocked(%q) = %v, want %v", tt.host, got, tt.blocked)
			}
		})
	}
	if _, got := SSRFBlocked("metadata.google.internal", []string{"metadata.google.internal"}); !got {
		t.Fatal("自定义黑名单未生效")
	}
}
