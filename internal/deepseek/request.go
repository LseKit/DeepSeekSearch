// 文件名: request.go
// 用途: 构造发往上游 search/fetch 的严格请求体。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package deepseek

import "github.com/LseKit/DeepSeekSearch/internal/domain"

// searchRequest 是 POST /v1/tools/search 的请求体。
// 字段顺序固定；MUST NOT 用 map，防止拼错键名被上游静默忽略。
type searchRequest struct {
	Query        string  `json:"query"`
	Count        *int    `json:"count,omitempty"`
	OriQuestion  *string `json:"ori_question,omitempty"`
	Localization string  `json:"localization,omitempty"`
	NeedContent  *bool   `json:"need_content,omitempty"`
}

type fetchRequest struct {
	Url string `json:"url"` //nolint:revive // 契约字段名，与 domain.FetchRequest.Url 一致
}

// toSearchBody 落实三条实测规则：ori_question 空串置 nil、localization 空串省略、
// count 为 0 视为不传；need_content 的显式 false 是合法值，原样透传。
func toSearchBody(req *domain.SearchRequest) searchRequest {
	body := searchRequest{
		Query:        req.Query,
		Localization: req.Localization,
		NeedContent:  req.NeedContent,
	}
	if req.Count != nil && *req.Count > 0 {
		count := *req.Count
		body.Count = &count
	}
	if req.OriQuestion != nil && *req.OriQuestion != "" {
		question := *req.OriQuestion
		body.OriQuestion = &question
	}
	return body
}

func toFetchBody(req domain.FetchRequest) fetchRequest {
	return fetchRequest{Url: req.Url}
}
