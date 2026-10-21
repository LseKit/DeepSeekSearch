// 文件名: doc.go
// 用途: 定义 resilience 层的接口、共享配置与可注入时钟，并提供包级文档。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package resilience 定义限流、重试、预算、熔断、缓存与 singleflight 的接口与共享类型。
//
// 实现位于 ratelimit / retry / budget / circuit / cache 五个子包。本包 MUST NOT
// import 任何子包；子包只允许向下依赖本包与 internal/domain，子包之间 MUST NOT 互相 import。
package resilience
