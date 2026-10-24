// 文件名: doc.go
// 用途: 提供工具层复用的纯校验函数（语言码、日期、短语、IP 混淆写法、SSRF 主机判定）。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package toolvalidate 存放与协议无关的纯校验函数，包括 SSRF 主机判定。
//
// 独立成包的原因：tools 已到 F-S5 行数上限，而这些函数不依赖任何工具状态。
// 本包 MUST NOT 导入 internal/tools。
package toolvalidate
