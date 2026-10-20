// 文件名: parse.go
// 用途: 把上游 200 响应原文解析成 domain 类型，空响应与非法 JSON 统一归 2999。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

// parseSearchResponse 解析 search 端点；results 缺失或为 null 时归一为空切片。
func parseSearchResponse(raw []byte) (domain.SearchResponse, error) {
	if len(raw) == 0 {
		return domain.SearchResponse{}, fmt.Errorf("deepseek: 解析响应失败: %w",
			domain.NewUpstreamParseError(0, errors.New("响应体为空")))
	}
	var resp domain.SearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return domain.SearchResponse{}, fmt.Errorf("deepseek: 解析响应失败: %w",
			domain.NewUpstreamParseError(len(raw), err))
	}
	if resp.Results == nil {
		resp.Results = []domain.SearchResult{}
	}
	return resp, nil
}

// parseFetchResponse 解析 fetch 端点；Content 原样保留，不做反转义。
func parseFetchResponse(raw []byte) (domain.FetchResponse, error) {
	if len(raw) == 0 {
		return domain.FetchResponse{}, fmt.Errorf("deepseek: 解析响应失败: %w",
			domain.NewUpstreamParseError(0, errors.New("响应体为空")))
	}
	var resp domain.FetchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return domain.FetchResponse{}, fmt.Errorf("deepseek: 解析响应失败: %w",
			domain.NewUpstreamParseError(len(raw), err))
	}
	return resp, nil
}
