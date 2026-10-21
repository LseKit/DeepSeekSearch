// 文件名: doc.go
// 用途: 提供 cache 子包的包级文档。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

// Package cache 实现 SQLite 持久化缓存与进程内/跨进程 singleflight。
//
// 两者同包是因为它们共享 storage key、oversize 标记与 inflight 租约表；
// 拆成两个包会把 leaseBackend 的 6 个内部方法提升为导出 API。
package cache
