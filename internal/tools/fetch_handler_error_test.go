// 文件名: fetch_handler_error_test.go
// 用途: 验证 ds_fetch 的 504、快照过期、FNV 碰撞复核与 SSRF 日志脱敏。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

func TestHandleFetchErrors(t *testing.T) {
	t.Parallel()

	t.Run("上游504", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{fetchFn: func(context.Context, domain.FetchRequest) (domain.FetchResponse, error) {
			return domain.FetchResponse{}, domain.NewUpstreamError(504, "req-504", "gateway timeout", nil)
		}}
		deps, _ := newTestDeps(t, client, nil)
		res := invokeFetch(t, &deps, &fetchInput{URL: strPtr("https://example.com/504")})
		payload := decodeError(t, res)
		if payload.Code != domain.CodeUpGatewayTimeout || !payload.Retryable {
			t.Fatalf("payload = %+v", payload)
		}
		if strings.Contains(resultText(t, res), "truncated_by") {
			t.Fatal("504 不应包含 truncated_by")
		}
	})

	t.Run("cache_id不存在", func(t *testing.T) {
		t.Parallel()
		deps, _ := newTestDeps(t, &fakeClient{}, nil)
		payload := decodeError(t, invokeFetch(t, &deps, &fetchInput{
			CacheID: strPtr("snap_0000000000000000"),
		}))
		if payload.Code != domain.CodeLocSnapshotGone {
			t.Fatalf("code = %d, want 3080", payload.Code)
		}
	})

	t.Run("FNV碰撞复核失败不返回错误内容", func(t *testing.T) {
		t.Parallel()
		url := "https://example.com/collision"
		wrong, err := json.Marshal(cachedFetchEntry{
			CacheID:    "snap_0000000000000001",
			URL:        "https://other.example.com/",
			URLSHA256:  sha256Hex("https://other.example.com/"),
			CapturedAt: 1,
			Title:      "WRONG",
			Content:    strings.Repeat("W", 300),
		})
		if err != nil {
			t.Fatalf("marshal wrong entry = %v", err)
		}
		client := fetchFixture("T", strings.Repeat("C", 300))
		deps, buf := newTestDeps(t, client, nil)
		normalized, normErr := domain.NormalizeUrl(url, deps.Cfg.Dedupe.TrackingParams)
		if normErr != nil {
			t.Fatalf("NormalizeUrl() = %v", normErr)
		}
		if putErr := deps.Cache.Put(context.Background(), fetchEntryKey(normalized), wrong, time.Hour); putErr != nil {
			t.Fatalf("写入碰撞条目 = %v", putErr)
		}
		out := decodeFetchSuccess(t, invokeFetch(t, &deps, &fetchInput{URL: strPtr(url)}))
		if strings.Contains(out.Content, "WWW") {
			t.Fatal("碰撞时返回了另一个 URL 的正文")
		}
		if !strings.Contains(out.Content, strings.Repeat("C", 100)) {
			t.Fatal("碰撞后未重新抓取正确内容")
		}
		if client.fetchCount() != 1 {
			t.Fatalf("上游 fetch 次数 = %d, want 1", client.fetchCount())
		}
		if !hasEvent(buf, "warn.cache_key_collision") {
			t.Fatal("缺少 warn.cache_key_collision 日志")
		}
	})

	t.Run("SSRF日志脱敏", func(t *testing.T) {
		t.Parallel()
		client := &fakeClient{}
		deps, buf := newTestDeps(t, client, nil)
		res := invokeFetch(t, &deps, &fetchInput{URL: strPtr("http://169.254.169.254/?token=SECRET")})
		payload := decodeError(t, res)
		if payload.Code != domain.CodeValSSRFBlocked {
			t.Fatalf("code = %d, want 1070", payload.Code)
		}
		if client.fetchCount() != 0 {
			t.Fatal("SSRF 拦截后不应发上游")
		}
		logs := buf.String()
		if strings.Contains(logs, "SECRET") {
			t.Fatalf("日志泄露了 token: %s", logs)
		}
		if !strings.Contains(logs, "token=***") {
			t.Fatalf("日志未按预期脱敏: %s", logs)
		}
		if !hasEvent(buf, "ssrf.blocked") {
			t.Fatal("缺少 ssrf.blocked 日志")
		}
	})
}

func hasEvent(buf *syncBuffer, event string) bool {
	for _, record := range logRecordsNoFail(buf) {
		if record["msg"] == event {
			return true
		}
	}
	return false
}

func logRecordsNoFail(buf *syncBuffer) []map[string]any {
	text := strings.TrimSpace(buf.String())
	if text == "" {
		return nil
	}
	records := make([]map[string]any, 0, 4)
	for _, line := range strings.Split(text, "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil {
			records = append(records, record)
		}
	}
	return records
}
