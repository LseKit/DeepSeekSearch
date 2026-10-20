// 文件名: doc.go
// 用途: 提供 logging 包级文档：结构化日志出口与按自然日轮转。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package logging 提供结构化日志器与按自然日轮转的文件写入器。
//
// 日志只写文件与 stderr，MUST NOT 写 stdout：stdio 传输下 stdout 是 MCP 协议通道，
// 混入一个字节就会让客户端 JSON-RPC 解析失败且没有任何错误提示。
package logging
