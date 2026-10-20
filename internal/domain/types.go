// 文件名: types.go
// 用途: 定义上游请求/响应与编译期版本快照的领域类型。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package domain

// BuildInfo 是编译期信息的不可变快照，供 serverInfo、/healthz 与 --version 共用。
type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
	GoVersion string
}

// SearchResponse 是上游 search 端点的解析结果；零结果时 Results 是空切片而非 nil。
type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

// FetchRequest 是 ds_fetch 发往上游的抓取目标。
type FetchRequest struct {
	Url string `json:"url"` //nolint:revive // 契约字段名，与 SearchResult.Url 保持一致
}

// FetchResponse 是上游 fetch 端点的解析结果；Content 是未反转义的原文。
type FetchResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}
