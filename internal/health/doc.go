// 文件名: doc.go
// 用途: 提供 ds_health 诊断工具的包级文档。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package health 实现 ds_health 本地诊断：8 项检查、build/limits 段与不计费探活。
//
// 独立成包的原因：tools 包已触及 F-S5 的 3000 行上限（ADR-017 基线棘轮），
// 而本工具与 search/fetch 无共享编排逻辑，只有一组只读依赖。
// 本包 MUST NOT 导入 internal/tools（反向依赖会成环）。
package health
