// 文件名: cache.go
// 用途: 定义持久化缓存与跨进程 singleflight 接口；实现见子包 cache。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"context"
	"time"
)

// Cache 是持久化键值缓存。阶段一唯一实现是 SQLite 版本，理由见 ADR-006。
type Cache interface {
	// Get 命中返回 (值, true)。过期视为未命中并删除。
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Put 写入并设置 TTL。ttl <= 0 表示立即过期（等价于不缓存）。
	Put(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// Stats 返回当前条目数与总字节数，供日志与 /healthz 使用。
	Stats() (entries int, bytes int64, degraded bool)
	// Prune 主动清理过期条目。启动时一次 + 每 prune_interval_s 一次。
	Prune(ctx context.Context) (removed int, err error)
	// Close 停止 prune 调度并关闭连接；可重复调用。
	Close() error
}

// Singleflight 保证「同一 key 在同一时刻只有一次上游请求」，跨 goroutine 与跨进程同时生效。
// 内存层用 map+mutex，跨进程层用 SQLite inflight 表。
type Singleflight interface {
	// Do 执行 fn，或等待他人执行完共享结果。
	// 返回 shared=true 表示本次没有真正执行 fn（结果来自他人）。
	// 等待超过 waitMs 返回 *domain.LocalError（3075）。
	//
	// 🔴 commit MUST 是 Do 的参数，不能由调用方在 Do 返回后再自己 Put：
	//    owner 的顺序必须是 fn 成功 → commit(val) 成功 → 才 DELETE 租约。
	//    否则「租约消失」与「值可读」之间存在窗口，跨进程等待者在这个窗口里
	//    既读不到值也看不到租约，只能重新发起上游请求 ⇒ 同一次调用被计费两次。
	//
	// 🔴 返回的 val 是共享的底层数组，所有等待者只读，MUST NOT 原地修改。
	Do(ctx context.Context, key string, waitMs int,
		fn func(context.Context) ([]byte, error),
		commit func(context.Context, []byte) error) (val []byte, shared bool, err error)

	// ReleaseAll 清理属于 ownerID 的全部租约。MUST 在 cache.Close 之前调用。
	// ownerID 传空串表示本实例：owner 由 NewSingleflight 内部生成，不暴露给装配层。
	ReleaseAll(ctx context.Context, ownerID string) (released int, err error)
}
