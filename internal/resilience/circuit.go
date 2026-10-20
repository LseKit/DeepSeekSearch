// 文件名: circuit.go
// 用途: per-key 连续失败熔断器，open/half-open/closed 状态机与 3030 错误。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package resilience

import (
	"fmt"
	"sync"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/domain"
)

const (
	stateClosed   = "closed"
	stateOpen     = "open"
	stateHalfOpen = "half-open"
)

// Circuit 是按 key 隔离的熔断器。
type Circuit interface {
	// Allow 返回是否放行；Open 时返回 3030 并带 retry_after_ms。
	Allow(key string) error
	// Record 记录一次调用结果；只有白名单错误推进连续失败计数。
	Record(key string, err error)
	// State 返回 "closed" | "open" | "half-open"。
	State(key string) string
}

type circuit struct {
	clk    Clock
	cfg    Config
	mu     sync.Mutex
	states map[string]*circuitState
}

type circuitState struct {
	status    string
	failures  int
	openedAt  time.Time
	probes    int
	successes int
}

// breakerCodes 是计入熔断失败的码白名单，与 14 号 §6.1 逐字一致。
var breakerCodes = map[int]bool{
	domain.CodeUpTimeout:        true, // 2408
	domain.CodeUpRateLimited:    true, // 2429
	domain.CodeUpServer:         true, // 2500
	domain.CodeUpUnavailable:    true, // 2503
	domain.CodeUpGatewayTimeout: true, // 2504
	domain.CodeUpNetwork:        true, // 2998
	domain.CodeUpParse:          true, // 2999
}

// NewCircuit 构造熔断器；配置缺失时用文档默认值兜底。
func NewCircuit(cfg Config, clk Clock) Circuit { //nolint:gocritic // 签名由 11 号 §四 固定
	if clk == nil {
		clk = SystemClock()
	}
	if cfg.CircuitFailThreshold < 1 {
		cfg.CircuitFailThreshold = 5
	}
	if cfg.CircuitOpenMs < 1 {
		cfg.CircuitOpenMs = 15000
	}
	if cfg.CircuitHalfOpenProbes < 1 {
		cfg.CircuitHalfOpenProbes = 2
	}
	return &circuit{clk: clk, cfg: cfg, states: make(map[string]*circuitState)}
}

func (c *circuit) Allow(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.stateLocked(key)
	now := c.clk.Now()
	c.refreshLocked(state, now)

	switch state.status {
	case stateOpen:
		openFor := time.Duration(c.cfg.CircuitOpenMs) * time.Millisecond
		return circuitOpenError(openFor - now.Sub(state.openedAt))
	case stateHalfOpen:
		if state.probes >= c.cfg.CircuitHalfOpenProbes {
			return circuitOpenError(0)
		}
		state.probes++
		return nil
	default:
		return nil
	}
}

func (c *circuit) Record(key string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.stateLocked(key)
	now := c.clk.Now()
	c.refreshLocked(state, now)

	failed := err != nil && breakerCodes[domain.CodeOf(err)]
	switch state.status {
	case stateHalfOpen:
		if failed {
			c.openLocked(state, now)
			return
		}
		if state.probes > 0 {
			state.probes--
		}
		state.successes++
		if state.successes >= c.cfg.CircuitHalfOpenProbes {
			state.status = stateClosed
			state.failures = 0
			state.probes = 0
			state.successes = 0
		}
	case stateClosed:
		if failed {
			state.failures++
			if state.failures >= c.cfg.CircuitFailThreshold {
				c.openLocked(state, now)
			}
			return
		}
		state.failures = 0
	}
}

func (c *circuit) State(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.stateLocked(key)
	c.refreshLocked(state, c.clk.Now())
	return state.status
}

// CircuitDiagnostics 是健康诊断用的 per-key 熔断运行态。
type CircuitDiagnostics struct {
	State        string
	Failures     int
	Threshold    int
	RetryAfterMs int64
}

// Diagnostics 返回指定 key 的熔断状态、连续失败数与 Open 剩余时长。
func (c *circuit) Diagnostics(key string) CircuitDiagnostics {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.stateLocked(key)
	now := c.clk.Now()
	c.refreshLocked(state, now)
	out := CircuitDiagnostics{
		State:     state.status,
		Failures:  state.failures,
		Threshold: c.cfg.CircuitFailThreshold,
	}
	if state.status == stateOpen {
		remaining := time.Duration(c.cfg.CircuitOpenMs)*time.Millisecond - now.Sub(state.openedAt)
		if remaining > 0 {
			out.RetryAfterMs = remaining.Milliseconds()
		}
	}
	return out
}

func (c *circuit) stateLocked(key string) *circuitState {
	state, ok := c.states[key]
	if !ok {
		state = &circuitState{status: stateClosed}
		c.states[key] = state
	}
	return state
}

func (c *circuit) refreshLocked(state *circuitState, now time.Time) {
	if state.status != stateOpen {
		return
	}
	openFor := time.Duration(c.cfg.CircuitOpenMs) * time.Millisecond
	if now.Sub(state.openedAt) >= openFor {
		state.status = stateHalfOpen
		state.probes = 0
		state.successes = 0
	}
}

func (c *circuit) openLocked(state *circuitState, now time.Time) {
	state.status = stateOpen
	state.openedAt = now
	// 保留失败计数：Open 期间诊断仍要看得到「是几次失败触发的」，清零会误导排查。
	state.probes = 0
	state.successes = 0
}

func circuitOpenError(retryAfter time.Duration) error {
	ms := retryAfter.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return fmt.Errorf("resilience: %w", domain.NewLocalError(domain.CodeLocCircuitOpen, "熔断器打开", map[string]any{
		"retry_after_ms": ms,
	}))
}
