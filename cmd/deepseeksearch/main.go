// 文件名: main.go
// 用途: 进程入口。只负责信号桥接与 os.Exit，全部逻辑在 run()。
// 创建日期: 2026-10-20
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>
package main

import (
	"os"
	"os/signal"
	"syscall"
)

// main 把 SIGINT/SIGTERM 桥接给 run()，并保证第二次信号立即退出。
// 第一次信号触发优雅关闭；关闭卡住时用户通常会再按一次 Ctrl-C，
// 若没有第二次信号的处理，进程看起来会「按了没反应」。
func main() {
	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		close(stop)
		<-sigCh
		os.Exit(1)
	}()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, stop))
}
