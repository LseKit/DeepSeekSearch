// 文件名: lease_poison_test.go
// 用途: 验证带错误码的租约行过期后不再毒化该 key（未过期仍快速失败）。
// 创建日期: 2026-10-25
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package cache

import (
	"context"
	"testing"
	"time"
)

// staleErrorLeases 模拟「上一个 owner 失败后没被清掉的租约行」：
// 标记与删除不是原子的，进程被 SIGKILL 就会留下这种行。
type staleErrorLeases struct {
	row       leaseRow
	preempted bool
}

func (s *staleErrorLeases) acquireLease(context.Context, string, string, int64) (bool, error) {
	return false, nil
}

func (s *staleErrorLeases) preemptLease(context.Context, string, string, int64, int64) (bool, error) {
	s.preempted = true
	return true, nil
}

func (s *staleErrorLeases) readLease(context.Context, string) (leaseRow, bool, error) {
	return s.row, true, nil
}

func (s *staleErrorLeases) markLeaseError(context.Context, string, string, int) error { return nil }

func (s *staleErrorLeases) deleteLease(context.Context, string, string) error { return nil }

func (s *staleErrorLeases) releaseAllLeases(context.Context, string) (int64, error) { return 0, nil }

func TestReadLeaseStateTreatsExpiredErrorAsPreemptable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	fn := func(context.Context) ([]byte, error) { return []byte("fresh"), nil }
	noop := func() {}

	t.Run("过期的错误行交回抢占路径", func(t *testing.T) {
		t.Parallel()
		leases := &staleErrorLeases{row: leaseRow{
			Owner: "dead-owner", ExpiresAt: now.Unix() - 60, LastErrorCode: 2998,
		}}
		s := &singleflight{leases: leases, clk: newFakeClock(now), owner: "o", logger: newRecordingLogger()}
		current := &call{done: make(chan struct{})}

		lease, found, done := s.readLeaseState(context.Background(), "v1:fetch:a", "a", current, fn, nil, noop)
		if done {
			t.Fatalf("过期错误行仍被判为终止状态 ⇒ 该 key 被永久毒化，重启也不恢复: err=%v", current.err)
		}
		if !found || lease.LastErrorCode == 0 || lease.ExpiresAt != now.Unix()-60 {
			t.Fatalf("应把错误行原样交给 tryPreempt: %+v found=%v", lease, found)
		}
		if current.err != nil {
			t.Fatalf("尚未抢占就写了错误: %v", current.err)
		}
	})

	t.Run("未过期的错误行仍快速失败", func(t *testing.T) {
		t.Parallel()
		leases := &staleErrorLeases{row: leaseRow{
			Owner: "live-owner", ExpiresAt: now.Unix() + 60, LastErrorCode: 2998,
		}}
		s := &singleflight{leases: leases, clk: newFakeClock(now), owner: "o", logger: newRecordingLogger()}
		current := &call{done: make(chan struct{})}

		if _, _, done := s.readLeaseState(context.Background(), "v1:fetch:b", "b", current, fn, nil, noop); !done {
			t.Fatal("未过期的错误行 MUST 立刻返回错误，否则等待者会对同一个失败 key 反复重打上游")
		}
		if current.err == nil {
			t.Fatal("应带上 owner 失败的错误")
		}
		if leases.preempted {
			t.Fatal("未过期时 MUST NOT 抢占")
		}
	})
}
