// 文件名: client.go
// 用途: DeepSeek 上游客户端：一次 HTTP 往返 + 严格解析，不重试、不限流、不碰缓存。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// Client 是上游能力的唯一入口。
// 实现者 MUST 保证：单次方法调用至多产生一次上游 HTTP 请求（重试由 Resilience 包裹在外部）。
type Client interface {
	// Search 调用 POST /v1/tools/search。
	// 返回 nil error 时，Results 可能为空切片（上游零结果），调用方 MUST 区分「空结果」与「出错」。
	Search(ctx context.Context, req domain.SearchRequest) (domain.SearchResponse, error)
	// Fetch 调用 POST /v1/tools/fetch。
	// 返回的 Content 是上游原文（含 Markdown 反斜杠转义），反转义由 Tools 层负责。
	Fetch(ctx context.Context, req domain.FetchRequest) (domain.FetchResponse, error)
	// Ping 发 GET {base_url}/v1/tools/search，把 HTTP 405 判定为健康。
	Ping(ctx context.Context) error
}

// Config 是 Client 自己的依赖参数，不是全局 Config。
type Config struct {
	BaseUrl   string //nolint:revive // 对外字段名固定为 BaseUrl/ApiKey
	ApiKey    string //nolint:revive // 对外字段名固定为 BaseUrl/ApiKey
	UserAgent string

	// PerAttempt 是一次 HTTP 往返的硬上限，由本包用 context.WithTimeout 执行。
	PerAttempt time.Duration

	// 分层超时只保护连接建立各阶段；与 PerAttempt 职责不同，二者都要。
	Dial           time.Duration
	TLSHandshake   time.Duration
	ResponseHeader time.Duration

	MaxResponseBytes int64
	BuildInfo        domain.BuildInfo
}

type client struct {
	cfg     Config
	hc      *http.Client
	initErr error
}

// New 构造 Client。base_url 非法时不在构造期 panic，而是让每个方法返回 3090。
func New(cfg Config) Client { //nolint:gocritic // 签名与上游客户端接口保持一致
	parsed, err := url.Parse(cfg.BaseUrl)
	if err == nil && (parsed == nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "") {
		err = errors.New("必须是 http/https 且包含 host")
	}
	return &client{
		cfg: cfg,
		hc: &http.Client{
			Transport: newTransport(&cfg),
			// 上游只有 POST /v1/tools/{search,fetch} 两个端点，重定向没有正当用途。
			// 默认跟随最多 10 跳的后果：同域跳转会把 Bearer 一起带过去（Go 只在
			// 跨域时剥 Authorization），base_url 指向自建代理时一次误配的 302
			// 就能把请求送进内网，还会把 1 次调用放大成最多 10 次计费。
			// 收到 3xx 时按「非 200」走 parseUpstreamError，不追。
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		initErr: err,
	}
}

// Search 调用 POST /v1/tools/search。
func (c *client) Search(ctx context.Context, req domain.SearchRequest) (domain.SearchResponse, error) { //nolint:gocritic // 签名与上游客户端接口保持一致
	if err := c.ready(); err != nil {
		return domain.SearchResponse{}, err
	}
	raw, status, err := c.do(ctx, http.MethodPost, "/v1/tools/search", toSearchBody(&req))
	if err != nil {
		return domain.SearchResponse{}, err
	}
	if status != http.StatusOK {
		return domain.SearchResponse{}, parseUpstreamError(status, raw, "ds_search")
	}
	return parseSearchResponse(raw)
}

// Fetch 调用 POST /v1/tools/fetch。
func (c *client) Fetch(ctx context.Context, req domain.FetchRequest) (domain.FetchResponse, error) {
	if err := c.ready(); err != nil {
		return domain.FetchResponse{}, err
	}
	raw, status, err := c.do(ctx, http.MethodPost, "/v1/tools/fetch", toFetchBody(req))
	if err != nil {
		return domain.FetchResponse{}, err
	}
	if status != http.StatusOK {
		return domain.FetchResponse{}, parseUpstreamError(status, raw, "ds_fetch")
	}
	return parseFetchResponse(raw)
}

// Ping 用 GET /v1/tools/search 期望 405 探活；405 不计费。
func (c *client) Ping(ctx context.Context) error {
	if err := c.ready(); err != nil {
		return err
	}
	raw, status, err := c.do(ctx, http.MethodGet, "/v1/tools/search", nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusMethodNotAllowed:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("ping: 上游返回 404，请检查 [upstream].base_url 是否拼写正确并指向 /v1 路由: %w",
			parseUpstreamError(status, raw, "ds_search"))
	case http.StatusUnauthorized:
		return fmt.Errorf("ping: 上游返回 401，API Key 失效: %w",
			parseUpstreamError(status, raw, "ds_health"))
	default:
		return fmt.Errorf("ping: 上游返回 %d，预期 405: %w", status,
			parseUpstreamError(status, raw, "ds_health"))
	}
}

// do 是唯一发请求的地方；网络层失败当场包成 2998，MUST NOT 让裸 net.Error 逃出去。
func (c *client) do(ctx context.Context, method, path string, body any) (raw []byte, status int, err error) {
	attempt := domain.AttemptFromContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, c.cfg.PerAttempt)
	defer cancel()

	var reader io.Reader
	var payload []byte
	if body != nil {
		data, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return nil, 0, fmt.Errorf("deepseek: 序列化请求体失败: %w", marshalErr)
		}
		payload = data
		reader = bytes.NewReader(data)
	}
	hook := ExchangeHookFrom(ctx)
	fire := func(respBody []byte) {
		if hook != nil {
			hook(ctx, payload, respBody)
		}
	}

	req, reqErr := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseUrl, "/")+path, reader)
	if reqErr != nil {
		return nil, 0, fmt.Errorf("deepseek: 构造请求失败: %w", domain.NewLocalError(
			domain.CodeLocConfigInvalid, fmt.Sprintf("deepseek: 构造请求失败: %v", reqErr), nil))
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.ApiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())

	resp, doErr := c.hc.Do(req)
	if doErr != nil {
		fire(nil)
		return nil, 0, fmt.Errorf("deepseek: 网络请求失败: %w", domain.NewNetworkError(classifyNetErr(ctx, doErr), attempt, doErr))
	}
	defer func() {
		closeErr := resp.Body.Close()
		// 正文已完整读到时，Close 失败（HTTP/2 RST_STREAM 之类）MUST NOT 把成功
		// 响应变成错误：上层只看 err，一次 Close 抖动会让已经拿到、已经计费的内容
		// 被丢弃并触发重试 —— 那是双重计费。只在本来就要返回错误时并入。
		if closeErr == nil || err == nil {
			return
		}
		err = errors.Join(err, fmt.Errorf("deepseek: 关闭响应体失败: %w", closeErr))
	}()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxResponseBytes+1))
	if readErr != nil {
		fire(nil)
		return nil, resp.StatusCode, fmt.Errorf("deepseek: 读取响应体失败: %w", domain.NewNetworkError("response_reset", attempt, readErr))
	}
	if int64(len(raw)) > c.cfg.MaxResponseBytes {
		fire(raw)
		return nil, resp.StatusCode, newResponseTooLargeError(resp.StatusCode, raw, c.cfg.MaxResponseBytes)
	}
	fire(raw)
	return raw, resp.StatusCode, nil
}

func (c *client) ready() error {
	if c.initErr == nil {
		return nil
	}
	return fmt.Errorf("deepseek: base_url 无效: %w", domain.NewLocalError(
		domain.CodeLocConfigInvalid, "deepseek: base_url 无效: "+c.initErr.Error(), nil))
}

func (c *client) userAgent() string {
	if c.cfg.UserAgent != "" {
		return c.cfg.UserAgent
	}
	version := c.cfg.BuildInfo.Version
	if version == "" {
		version = "dev"
	}
	return "DeepSeekSearch/" + version
}
