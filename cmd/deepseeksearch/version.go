// 文件名: version.go
// 用途: 版本变量的定义与 EffectiveVersion 兜底逻辑。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

// 这三个变量只能由链接器注入，代码里的值是「未注入时的开发版标识」。
// MUST NOT 从 config.toml 或环境变量读取 —— 版本号必须描述「这个二进制是什么」，
// 而不是「它读到了什么配置」。注入方式见 Makefile 的 LDFLAGS。
var (
	version   = "v0.0.0-dev" // -X main.version=v0.1.0
	commit    = "none"       // -X main.commit=a1b2c3d
	buildTime = "unknown"    // -X main.buildTime=2026-10-20T13:05:12Z
)

// EffectiveVersion 返回实际对外展示的版本号。
// 注入值有效时忽略配置里的兜底值，保证协议层版本号永远描述二进制本身。
func EffectiveVersion(injected, fallback string) string {
	if injected != "" && injected != "v0.0.0-dev" {
		return injected
	}
	if fallback != "" {
		return fallback
	}
	return "v0.0.0-dev"
}
