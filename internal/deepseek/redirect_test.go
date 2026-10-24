// 文件名: redirect_test.go
// 用途: 验证上游客户端不跟随重定向（跟随会把 Bearer 送给跳转目标，并把一次调用放大成多次计费）。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func TestClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	target := "http://169.254.169.254/latest/meta-data/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer srv.Close()

	client := New(Config{
		BaseUrl:          srv.URL,
		ApiKey:           "test-key",
		PerAttempt:       5 * time.Second,
		Dial:             time.Second,
		TLSHandshake:     time.Second,
		ResponseHeader:   2 * time.Second,
		MaxResponseBytes: 1 << 20,
	})

	_, err := client.Search(context.Background(), domain.SearchRequest{Query: "q"})
	if err == nil {
		t.Fatal("3xx MUST 被当成失败返回，不能跟随跳转")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("上游被打了 %d 次：跟随重定向会把一次调用放大成多次计费", got)
	}
	if code := domain.CodeOf(err); code == 0 {
		t.Fatalf("错误 MUST 带错误码，实际 %v", err)
	}
}
