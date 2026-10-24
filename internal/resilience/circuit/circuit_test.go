// 文件名: circuit_test.go
// 用途: 验证连续失败阈值、open/half-open/closed 状态机、3030 与错误码白名单。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package circuit

import (
	"testing"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/resilience"
)

func TestCircuit(t *testing.T) {
	t.Parallel()

	failErr := domain.NewUpstreamError(500, "", "", nil)
	clientErr := domain.NewValidationError(1000, "count", "bad")

	t.Run("4次失败仍closed", func(t *testing.T) {
		t.Parallel()
		c := newTestCircuit(newFakeClock(limiterNow), nil)
		for i := 0; i < 4; i++ {
			c.Record("search", failErr)
		}
		if got := c.State("search"); got != stateClosed {
			t.Fatalf("State = %q, want closed", got)
		}
		if err := c.Allow("search"); err != nil {
			t.Fatalf("Allow() = %v", err)
		}
	})

	t.Run("5次失败open", func(t *testing.T) {
		t.Parallel()
		c := newTestCircuit(newFakeClock(limiterNow), nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		if got := c.State("search"); got != stateOpen {
			t.Fatalf("State = %q, want open", got)
		}
	})

	t.Run("open返回3030与retry_after", func(t *testing.T) {
		t.Parallel()
		c := newTestCircuit(newFakeClock(limiterNow), nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		err := c.Allow("search")
		if got := domain.CodeOf(err); got != domain.CodeLocCircuitOpen {
			t.Fatalf("Code = %d, want %d", got, domain.CodeLocCircuitOpen)
		}
		details := domain.ToPayload(err, "").Details
		if ms, ok := details["retry_after_ms"].(int64); !ok || ms <= 0 {
			t.Fatalf("retry_after_ms = %v, want >0", details["retry_after_ms"])
		}
	})

	t.Run("15s后half_open限额", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		c := newTestCircuit(clk, nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		clk.Advance(15 * time.Second)
		if got := c.State("search"); got != stateHalfOpen {
			t.Fatalf("State = %q, want half-open", got)
		}
		if err := c.Allow("search"); err != nil {
			t.Fatalf("第 1 个探测 = %v", err)
		}
		if err := c.Allow("search"); err != nil {
			t.Fatalf("第 2 个探测 = %v", err)
		}
		if err := c.Allow("search"); domain.CodeOf(err) != domain.CodeLocCircuitOpen {
			t.Fatalf("第 3 个探测应被拒，err=%v", err)
		}
	})

	t.Run("half_open超时回open而非永久卡死", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		c := newTestCircuit(clk, nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		clk.Advance(15 * time.Second)
		if err := c.Allow("search"); err != nil {
			t.Fatalf("探测 1 = %v", err)
		}
		if err := c.Allow("search"); err != nil {
			t.Fatalf("探测 2 = %v", err)
		}
		// 两个探测名额被消费但没有任何 Record：超过一个 open 窗口后应重新 open。
		clk.Advance(15 * time.Second)
		if got := c.State("search"); got != stateOpen {
			t.Fatalf("State = %q, want open", got)
		}
		clk.Advance(15 * time.Second)
		if err := c.Allow("search"); err != nil {
			t.Fatalf("重新进入 half-open 后 Allow() = %v", err)
		}
	})

	t.Run("half_open全成功closed", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		c := newTestCircuit(clk, nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		clk.Advance(15 * time.Second)
		if err := c.Allow("search"); err != nil {
			t.Fatalf("探测 1 = %v", err)
		}
		if err := c.Allow("search"); err != nil {
			t.Fatalf("探测 2 = %v", err)
		}
		c.Record("search", nil)
		c.Record("search", nil)
		if got := c.State("search"); got != stateClosed {
			t.Fatalf("State = %q, want closed", got)
		}
		if err := c.Allow("search"); err != nil {
			t.Fatalf("恢复后 Allow() = %v", err)
		}
	})

	t.Run("half_open失败回open", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		c := newTestCircuit(clk, nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		clk.Advance(15 * time.Second)
		if err := c.Allow("search"); err != nil {
			t.Fatalf("探测 = %v", err)
		}
		c.Record("search", failErr)
		if got := c.State("search"); got != stateOpen {
			t.Fatalf("State = %q, want open", got)
		}
		if err := c.Allow("search"); domain.CodeOf(err) != domain.CodeLocCircuitOpen {
			t.Fatalf("回 open 后 Allow() = %v", err)
		}
	})

	t.Run("成功清零连续失败", func(t *testing.T) {
		t.Parallel()
		c := newTestCircuit(newFakeClock(limiterNow), nil)
		for i := 0; i < 3; i++ {
			c.Record("search", failErr)
		}
		c.Record("search", nil)
		for i := 0; i < 3; i++ {
			c.Record("search", failErr)
		}
		if got := c.State("search"); got != stateClosed {
			t.Fatalf("成功后计数应清零，State = %q", got)
		}
		c.Record("search", failErr)
		c.Record("search", failErr)
		if got := c.State("search"); got != stateOpen {
			t.Fatalf("再 2 次失败应 open，State = %q", got)
		}
	})

	t.Run("search与fetch独立", func(t *testing.T) {
		t.Parallel()
		c := newTestCircuit(newFakeClock(limiterNow), nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		if got := c.State("fetch"); got != stateClosed {
			t.Fatalf("fetch State = %q, want closed", got)
		}
		if err := c.Allow("fetch"); err != nil {
			t.Fatalf("fetch Allow() = %v", err)
		}
	})

	t.Run("非上游错误不计入", func(t *testing.T) {
		t.Parallel()
		c := newTestCircuit(newFakeClock(limiterNow), nil)
		for i := 0; i < 4; i++ {
			c.Record("search", failErr)
		}
		c.Record("search", clientErr)
		for i := 0; i < 4; i++ {
			c.Record("search", failErr)
		}
		if got := c.State("search"); got != stateClosed {
			t.Fatalf("1xxx 不应计入，State = %q", got)
		}
		c.Record("search", failErr)
		if got := c.State("search"); got != stateOpen {
			t.Fatalf("第 5 次上游失败应 open，State = %q", got)
		}
	})

	t.Run("open后retry_after递减", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		c := newTestCircuit(clk, nil)
		for i := 0; i < 5; i++ {
			c.Record("search", failErr)
		}
		clk.Advance(5 * time.Second)
		err := c.Allow("search")
		details := domain.ToPayload(err, "").Details
		ms, ok := details["retry_after_ms"].(int64)
		if !ok || ms <= 0 || ms >= 15000 {
			t.Fatalf("retry_after_ms = %v, want 0<ms<15000", details["retry_after_ms"])
		}
	})

	t.Run("自定义阈值与探测数", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock(limiterNow)
		c := newTestCircuit(clk, func(cfg *resilience.Config) {
			cfg.CircuitFailThreshold = 2
			cfg.CircuitHalfOpenProbes = 1
		})
		c.Record("search", failErr)
		c.Record("search", failErr)
		if got := c.State("search"); got != stateOpen {
			t.Fatalf("State = %q, want open", got)
		}
		clk.Advance(15 * time.Second)
		if err := c.Allow("search"); err != nil {
			t.Fatalf("Allow() = %v", err)
		}
		c.Record("search", nil)
		if got := c.State("search"); got != stateClosed {
			t.Fatalf("1 个探测成功后应 closed，State = %q", got)
		}
	})

	t.Run("空配置默认值兜底", func(t *testing.T) {
		t.Parallel()
		c := NewCircuit(resilience.Config{}, newFakeClock(limiterNow))
		for i := 0; i < 4; i++ {
			c.Record("search", failErr)
		}
		if got := c.State("search"); got != stateClosed {
			t.Fatalf("4 次失败应仍 closed，State = %q", got)
		}
		c.Record("search", failErr)
		if got := c.State("search"); got != stateOpen {
			t.Fatalf("第 5 次失败应 open，State = %q", got)
		}
	})
}

func newTestCircuit(clk resilience.Clock, mutate func(*resilience.Config)) resilience.Circuit {
	cfg := resilience.Config{
		CircuitFailThreshold:  5,
		CircuitOpenMs:         15000,
		CircuitHalfOpenProbes: 2,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewCircuit(cfg, clk)
}

func TestCircuitDiagnostics(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(limiterNow)
	c := newTestCircuit(clk, nil)
	failErr := domain.NewUpstreamError(500, "", "", nil)
	for range 5 {
		c.Record("search", failErr)
	}
	breaker, ok := c.(*circuit)
	if !ok {
		t.Fatalf("熔断器类型 = %T", c)
	}
	diag := breaker.Diagnostics("search")
	if diag.State != stateOpen || diag.Failures != 5 || diag.Threshold != 5 {
		t.Fatalf("Diagnostics = %+v", diag)
	}
	if diag.RetryAfterMs <= 0 {
		t.Fatalf("RetryAfterMs = %d, want > 0", diag.RetryAfterMs)
	}
	if closed := breaker.Diagnostics("fetch"); closed.State != stateClosed {
		t.Fatalf("未失败 key State = %q", closed.State)
	}
}
