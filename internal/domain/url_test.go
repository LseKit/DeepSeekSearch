// 文件名: url_test.go
// 用途: 校验 URL 规范化的 10 步、追踪参数注入语义与 FNV-1a 64 哈希。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

import (
	"hash/fnv"
	"testing"
)

func TestNormalizeUrlKeepsMalformedQuery(t *testing.T) {
	t.Parallel()

	broken, err := NormalizeUrl("https://example.com/?a=1&b=%zz", nil)
	if err != nil {
		t.Fatalf("NormalizeUrl() = %v", err)
	}
	clean, err := NormalizeUrl("https://example.com/?a=1", nil)
	if err != nil {
		t.Fatalf("NormalizeUrl() = %v", err)
	}
	if broken == clean {
		t.Fatalf("非法 query 被静默丢弃，两个 URL 撞成同一个 key: %q", broken)
	}
}

func TestNormalizeUrl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		tracking []string
		want     string
		wantErr  bool
	}{
		{
			name: "根路径归一为空",
			raw:  "https://example.com/",
			want: "https://example.com",
		},
		{
			name:     "大写主机默认端口追踪参数与排序",
			raw:      "https://EXAMPLE.com:443/x?utm_source=a&b=2&a=1",
			tracking: []string{"utm_source"},
			want:     "https://example.com/x?a=1&b=2",
		},
		{
			name: "http scheme 保留",
			raw:  "http://example.com/",
			want: "http://example.com",
		},
		{ //nolint:gosec // 测试用例：验证含凭据的 URL 被拒绝，不是真实凭据
			name:    "凭据拒绝",
			raw:     "https://u:p@example.com/",
			wantErr: true,
		},
		{
			name:    "仅用户名也拒绝",
			raw:     "https://u@example.com/",
			wantErr: true,
		},
		{
			name:    "非 http scheme 拒绝",
			raw:     "ftp://x.com/",
			wantErr: true,
		},
		{
			name: "非根尾斜杠保留",
			raw:  "https://example.com/a/",
			want: "https://example.com/a/",
		},
		{
			name: "非根无尾斜杠保留",
			raw:  "https://example.com/a",
			want: "https://example.com/a",
		},
		{
			name: "tracking 为 nil 时不剥离",
			raw:  "https://x.com/a?utm_source=a",
			want: "https://x.com/a?utm_source=a",
		},
		{
			name:     "注入 table 才剥离 utm_source",
			raw:      "https://x.com/?utm_source=a",
			tracking: []string{"utm_source"},
			want:     "https://x.com",
		},
		{
			name:     "键名大小写不敏感",
			raw:      "https://x.com/a?UTM_SOURCE=x&keep=1",
			tracking: []string{"utm_source"},
			want:     "https://x.com/a?keep=1",
		},
		{
			name:     "精确匹配不误伤前缀",
			raw:      "https://x.com/a?utm_source_x=1&utm_source=2",
			tracking: []string{"utm_source"},
			want:     "https://x.com/a?utm_source_x=1",
		},
		{
			name: "fragment 丢弃",
			raw:  "https://example.com/p#sec",
			want: "https://example.com/p",
		},
		{
			name: "http 默认端口 80 丢弃",
			raw:  "http://example.com:80/",
			want: "http://example.com",
		},
		{
			name: "https 的 80 端口保留",
			raw:  "https://example.com:80/",
			want: "https://example.com:80",
		},
		{
			name: "https 默认端口 443 丢弃",
			raw:  "https://example.com:443/p",
			want: "https://example.com/p",
		},
		{
			name: "非默认端口保留",
			raw:  "https://example.com:8443/",
			want: "https://example.com:8443",
		},
		{
			name: "query 键与同名值排序",
			raw:  "https://x.com/a?b=2&a=3&a=1",
			want: "https://x.com/a?a=1&a=3&b=2",
		},
		{
			name: "裸问号不输出",
			raw:  "https://x.com?",
			want: "https://x.com",
		},
		{
			name: "根斜杠加裸问号也归一",
			raw:  "https://x.com/?",
			want: "https://x.com",
		},
		{
			name: "scheme 大写归一",
			raw:  "HTTPS://Example.com/Path",
			want: "https://example.com/Path",
		},
		{
			name: "IPv6 默认端口与根路径",
			raw:  "https://[::1]:443/",
			want: "https://[::1]",
		},
		{
			name:    "缺 host 拒绝",
			raw:     "https:///path",
			wantErr: true,
		},
		{
			name: "路径大小写保留",
			raw:  "https://example.com/A/B",
			want: "https://example.com/A/B",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeUrl(tt.raw, tt.tracking)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeUrl(%q) = %q, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeUrl(%q) error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeUrl(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNormalizeUrlTrackingParamsAreInjected(t *testing.T) {
	t.Parallel()

	raw := "https://x.com/a?one=1&two=2&three=3"

	got, err := NormalizeUrl(raw, []string{"two"})
	if err != nil {
		t.Fatalf("NormalizeUrl: %v", err)
	}
	if want := "https://x.com/a?one=1&three=3"; got != want {
		t.Errorf("注入 two 后 = %q, want %q", got, want)
	}

	got, err = NormalizeUrl(raw, nil)
	if err != nil {
		t.Fatalf("NormalizeUrl: %v", err)
	}
	if want := "https://x.com/a?one=1&three=3&two=2"; got != want {
		t.Errorf("tracking=nil 时 = %q, want %q（一个都不剥，仅按契约排序）", got, want)
	}
}

func TestHashUrl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want uint64
	}{
		{"空串", "", 14695981039346656037},
		{"单字符", "a", 0xaf63dc4c8601ec8c},
		{"foobar", "foobar", 0x85944171f73967e8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := HashUrl(tt.in); got != tt.want {
				t.Errorf("HashUrl(%q) = %#x, want %#x", tt.in, got, tt.want)
			}
		})
	}

	h := fnv.New64a()
	if _, err := h.Write([]byte("https://example.com")); err != nil {
		t.Fatalf("fnv.Write: %v", err)
	}
	if got := HashUrl("https://example.com"); got != h.Sum64() {
		t.Errorf("HashUrl 与 hash/fnv 不一致: %#x vs %#x", got, h.Sum64())
	}
}
