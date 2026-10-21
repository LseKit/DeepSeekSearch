// 文件名: doc.go
// 用途: 提供工具层共用的 MCP 请求/结果包装，避免每个包各写一份。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package mcputil 提供 MCP 工具层共用的薄封装：解析入参、构造子调用请求、
// 取出文本结果。零业务逻辑、零项目内依赖。
package mcputil
