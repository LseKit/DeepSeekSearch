// 文件名: main.go
// 用途: 进程入口，解析 --version 并按 Contract/17 §3.1 打印编译期注入的版本信息（传输装配在 Task-12 落地）。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

import (
	"flag"
	"fmt"
	"runtime"
)

// main 是 A-02 阶段的入口：只处理 --version 并打印版本，不做任何配置加载与传输装配。
// 真正的 19 步装配在 Task-12 的 run() 里实现，此处 MUST NOT 提前写。
func main() {
	showVersion := flag.Bool("version", false, "打印版本号后立即退出，不读任何配置")
	flag.Parse()

	// --version MUST 在 flag.Parse() 之后立即返回，MUST NOT 进入配置加载路径。
	if *showVersion {
		printVersion()
		return
	}

	// A-02 阶段尚无传输可起，未带 --version 时同样只打印版本占位。
	printVersion()
}

// printVersion 把版本信息按 Contract/17 §3.1 的格式打到 stdout（单行）。
// go 版本用 runtime.Version() 运行时取，MUST NOT 走 -ldflags 注入 —— LDFLAGS 里
// 恰好三条 -X main.（version/commit/buildTime），加第四条会破坏 A-02 req2 的断言。
func printVersion() {
	fmt.Printf("DeepSeekSearch %s (commit %s, built %s, %s)\n",
		EffectiveVersion(version, ""), commit, buildTime, runtime.Version())
}
