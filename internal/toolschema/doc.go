// 文件名: doc.go
// 用途: 提供 MCP 工具 JSON Schema 字面量包，并把包级文档集中在此文件。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package toolschema 保存 ds_search / ds_fetch 的 InputSchema 字面量。
//
// 独立成叶子包的原因：tools 包的非测试代码已触及 F-S5 的 3000 行上限
// （ADR-017 基线棘轮只减不增），而 schema 是纯数据、零依赖，天然可外置。
// 本包 MUST NOT 导入本项目其它包，也 MUST NOT 引入任何逻辑。
package toolschema
