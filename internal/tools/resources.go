// 文件名: resources.go
// 用途: 注册三个只读 MCP Resource，并提供按 URI 的 ttlMs/cacheScope 分派与用量统计。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

const (
	resourceEffectiveConfig = "ds://effective-config"
	resourceErrorCodes      = "ds://error-codes"
	resourceUsage           = "ds://usage"
)

type resourceDef struct {
	URI         string
	Name        string
	Title       string
	Description string
	MIMEType    string
	read        func(context.Context, *Deps) ([]byte, error)
}

// resourceDefinitions 按 URI 字典序返回三个 Resource。
// 顺序即注册顺序，保证 resources/list 连续 20 次完全一致。
func resourceDefinitions() []resourceDef {
	return []resourceDef{
		{
			URI:         resourceEffectiveConfig,
			Name:        "生效配置",
			Title:       "生效配置与来源",
			Description: "排查「为什么参数没生效」时读取：脱敏后的完整配置、每个键的来源层级与 config_sha256。",
			MIMEType:    "application/json",
			read:        effectiveConfigJSON,
		},
		{
			URI:         resourceErrorCodes,
			Name:        "错误码表",
			Title:       "错误码全表",
			Description: "需要解释错误码含义或判断是否可重试时读取：42 个码的 code/name/message/hint/retryable。",
			MIMEType:    "application/json",
			read:        func(context.Context, *Deps) ([]byte, error) { return errorCodesDocument() },
		},
		{
			URI:         resourceUsage,
			Name:        "调用统计",
			Title:       "本进程调用统计",
			Description: "想了解各工具调用量、计费次数、缓存命中与错误数时读取。仅本进程内存，重启清零。",
			MIMEType:    "application/json",
			read:        usageJSON,
		},
	}
}

// registerResources 注册三个只读 Resource。MIME 三个都是 application/json。
func registerResources(srv *mcp.Server, deps Deps) error { //nolint:gocritic // 签名与 MCP 资源注册接口保持一致
	for _, def := range resourceDefinitions() {
		definition := def
		resource := &mcp.Resource{
			URI:         definition.URI,
			Name:        definition.Name,
			Title:       definition.Title,
			Description: definition.Description,
			MIMEType:    definition.MIMEType,
		}
		srv.AddResource(resource, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("read resource canceled: %w", err)
			}
			data, err := definition.read(ctx, &deps)
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      definition.URI,
					MIMEType: definition.MIMEType,
					Text:     string(data),
				}},
			}, nil
		})
	}
	return nil
}

// CacheableFor 返回 Resource 的 ttlMs/cacheScope；未知 URI 回落到 (1000,"private")。
// 0 与「SetCacheable 回调没接上」不可区分，所以任何取值都 MUST > 0。
func CacheableFor(uri string) (ttlMs int64, scope string) {
	switch uri {
	case resourceEffectiveConfig:
		return 300000, "private"
	case resourceErrorCodes:
		return 300000, "public"
	case resourceUsage:
		return 1000, "private"
	default:
		return 1000, "private"
	}
}

// CacheableForRequest 从 resources/read 的 Params.URI 取键。
// go-sdk v1.8.0 的 mcp.Request 没有 URI()；其它请求（list/server-discover）走默认值。
func CacheableForRequest(req mcp.Request) (ttlMs int64, scope string) {
	if read, ok := req.(*mcp.ServerRequest[*mcp.ReadResourceParams]); ok && read.Params != nil {
		return CacheableFor(read.Params.URI)
	}
	return 1000, "private"
}

type errorCodeEntry struct {
	Code      int    `json:"code"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	Hint      string `json:"hint"`
	Retryable bool   `json:"retryable"`
}

func errorCodesDocument() ([]byte, error) {
	return errorCodesDocumentFrom(domain.CodeRegistry)
}

// errorCodesDocumentFrom 单独接收码表，测试可注入改过的副本验证「不是手写 JSON」。
func errorCodesDocumentFrom(registry map[int]domain.CodeInfo) ([]byte, error) {
	codes := make([]int, 0, len(registry))
	for code := range registry {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	entries := make([]errorCodeEntry, 0, len(codes))
	for _, code := range codes {
		info := registry[code]
		entries = append(entries, errorCodeEntry{
			Code: code, Name: info.Name, Message: info.Message,
			Hint: info.Hint, Retryable: info.Retryable,
		})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal error codes: %w", err)
	}
	return raw, nil
}

type effectiveConfigDocument struct {
	ApiKey       string            `json:"api_key"`    //nolint:revive // 输出字段名与配置契约一致
	HttpToken    string            `json:"http_token"` //nolint:revive // 输出字段名与配置契约一致
	Config       json.RawMessage   `json:"config"`
	Sources      map[string]string `json:"source"`
	ConfigSha256 string            `json:"config_sha256"`
}

func configSourceKeys() []string {
	return []string{
		"server.transport",
		"upstream.base_url",
		"ratelimit.max_inflight",
		"ratelimit.search.rps",
		"ratelimit.fetch.rps",
		"timeout.search.total_timeout_ms",
		"timeout.fetch.total_timeout_ms",
		"cache.enabled",
		"cache.db_path",
		"cache.ttl_search_s",
		"cache.ttl_fetch_s",
		"limits.max_total_chars",
		"limits.inline_max_chars",
		"limits.outline_max_sections",
		"log.level",
		"log.timezone",
		"log.slow_ms",
		"dedupe.enabled",
	}
}

func effectiveConfigJSON(_ context.Context, deps *Deps) ([]byte, error) {
	redacted := deps.Cfg.Redacted()
	// Config 的每个字段都带 json tag，键名与 config.toml 完全一致 ——
	// 这样本资源的 config 段与 source 的点路径能互相对照。
	configRaw, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("marshal redacted config: %w", err)
	}
	keys := configSourceKeys()
	sources := make(map[string]string, len(keys))
	for _, key := range keys {
		sources[key] = string(deps.Cfg.Source(key))
	}
	doc := effectiveConfigDocument{
		ApiKey:       "***",
		HttpToken:    "***",
		Config:       configRaw,
		Sources:      sources,
		ConfigSha256: configFingerprint(&deps.Cfg),
	}
	raw, err := json.Marshal(doc) //nolint:gosec // 这两个字段的值固定为 ***，仅展示脱敏结果
	if err != nil {
		return nil, fmt.Errorf("marshal effective config: %w", err)
	}
	return raw, nil
}

// usageJSON 渲染 ds://usage 资源；统计器缺位时由 health 侧给全零骨架。
func usageJSON(_ context.Context, deps *Deps) ([]byte, error) {
	raw, err := deps.Usage.JSON()
	if err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}
	return raw, nil
}

// configFingerprint 返回脱敏配置的 sha256 前 12 位；与 health 包内的同名实现
// 必须给出相同结果（同一份 Redacted() 输入），两处都改才不漂移。
func configFingerprint(cfg *config.Config) string {
	raw, err := json.Marshal(cfg.Redacted())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}
