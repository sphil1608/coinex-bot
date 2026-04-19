// Package health provides a watchdog/circuit-breaker for the trading engine.
// It monitors error rates, consecutive failures, API latency, and can halt
// trading when the system is in a degraded state.
package health

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// ────────────────────────────────────────────────────────────────────────────
// Circuit Breaker
// ────────────────────────────────────────────────────────────────────────────

type CircuitState int32

const (
	CircuitClosed   CircuitState = 0 // normal – trading allowed
	CircuitOpen     CircuitState = 1 // tripped – trading halted
	CircuitHalfOpen CircuitState = 2 // testing recovery
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "CLOSED"
	case CircuitOpen:
		return "OPEN"
	case CircuitHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreaker halts execution after consecutive failures.
type CircuitBreaker struct {
	state          atomic.Int32
	failureCount   atomic.Int32
	successCount   atomic.Int32
	lastFailure    atomic.Int64 // unix nano
	threshold      int32
	resetTimeout   time.Duration
	halfOpenProbes int32 // successes needed to close from half-open
}

func NewCircuitBreaker(threshold int, resetTimeout time.Duration) *CircuitBreaker {
	cb := &CircuitBreaker{
		threshold:      int32(threshold),
		resetTimeout:   resetTimeout,
		halfOpenProbes: 3,
	}
	cb.state.Store(int32(CircuitClosed))
	return cb
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(cb.state.Load())
}

// Allow returns true if a request should be allowed through.
func (cb *CircuitBreaker) Allow() bool {
	state := cb.State()
	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Check if reset timeout has elapsed → try half-open
		lastF := time.Unix(0, cb.lastFailure.Load())
		if time.Since(lastF) >= cb.resetTimeout {
			cb.state.CompareAndSwap(int32(CircuitOpen), int32(CircuitHalfOpen))
			cb.successCount.Store(0)
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	}
	return false
}

// RecordSuccess records a successful operation.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.failureCount.Store(0)
	state := cb.State()
	if state == CircuitHalfOpen {
		n := cb.successCount.Add(1)
		if n >= cb.halfOpenProbes {
			cb.state.Store(int32(CircuitClosed))
			log.Info().Msg("circuit breaker: closed (recovered)")
		}
	}
}

// RecordFailure records a failed operation and may trip the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.lastFailure.Store(time.Now().UnixNano())
	n := cb.failureCount.Add(1)
	if n >= cb.threshold {
		prev := cb.state.Swap(int32(CircuitOpen))
		if CircuitState(prev) != CircuitOpen {
			log.Warn().Int32("failures", n).Msg("circuit breaker: OPEN (too many failures)")
		}
	}
}

// Reset manually closes the circuit (e.g. after operator intervention).
func (cb *CircuitBreaker) Reset() {
	cb.state.Store(int32(CircuitClosed))
	cb.failureCount.Store(0)
	cb.successCount.Store(0)
	log.Info().Msg("circuit breaker: manually reset")
}

// ────────────────────────────────────────────────────────────────────────────
// Error Rate Tracker
// ────────────────────────────────────────────────────────────────────────────

type errorEvent struct {
	ts  time.Time
	msg string
}

type ErrorTracker struct {
	mu     sync.Mutex
	events []errorEvent
	window time.Duration
}

func NewErrorTracker(window time.Duration) *ErrorTracker {
	return &ErrorTracker{window: window}
}

func (et *ErrorTracker) Record(msg string) {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.events = append(et.events, errorEvent{ts: time.Now(), msg: msg})
	et.prune()
}

func (et *ErrorTracker) Rate() float64 {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.prune()
	return float64(len(et.events)) / et.window.Seconds()
}

func (et *ErrorTracker) Count() int {
	et.mu.Lock()
	defer et.mu.Unlock()
	et.prune()
	return len(et.events)
}

func (et *ErrorTracker) prune() {
	cutoff := time.Now().Add(-et.window)
	i := 0
	for i < len(et.events) && et.events[i].ts.Before(cutoff) {
		i++
	}
	et.events = et.events[i:]
}

// ────────────────────────────────────────────────────────────────────────────
// Latency Tracker
// ────────────────────────────────────────────────────────────────────────────

type LatencyTracker struct {
	mu      sync.Mutex
	samples []time.Duration
	maxLen  int
}

func NewLatencyTracker(maxLen int) *LatencyTracker {
	return &LatencyTracker{maxLen: maxLen}
}

func (lt *LatencyTracker) Record(d time.Duration) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.samples = append(lt.samples, d)
	if len(lt.samples) > lt.maxLen {
		lt.samples = lt.samples[1:]
	}
}

func (lt *LatencyTracker) P50() time.Duration { return lt.percentile(0.50) }
func (lt *LatencyTracker) P95() time.Duration { return lt.percentile(0.95) }
func (lt *LatencyTracker) P99() time.Duration { return lt.percentile(0.99) }

func (lt *LatencyTracker) percentile(p float64) time.Duration {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if len(lt.samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(lt.samples))
	copy(sorted, lt.samples)
	// simple insertion sort (samples are small)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// ────────────────────────────────────────────────────────────────────────────
// Monitor
// ────────────────────────────────────────────────────────────────────────────

type Config struct {
	// Circuit breaker trips after this many consecutive failures
	CBThreshold    int           `mapstructure:"cb_threshold"`
	CBResetTimeout time.Duration `mapstructure:"cb_reset_timeout"`

	// Max error rate (errors/second) before alerting
	MaxErrorRate float64 `mapstructure:"max_error_rate"`
	ErrorWindow  time.Duration `mapstructure:"error_window"`

	// Watchdog: if no tick within this duration, restart
	TickTimeout time.Duration `mapstructure:"tick_timeout"`

	// Max API latency before warning
	LatencyWarnThreshold time.Duration `mapstructure:"latency_warn_threshold"`
}

func DefaultConfig() Config {
	return Config{
		CBThreshold:          5,
		CBResetTimeout:       60 * time.Second,
		MaxErrorRate:         0.5,
		ErrorWindow:          60 * time.Second,
		TickTimeout:          5 * time.Minute,
		LatencyWarnThreshold: 2 * time.Second,
	}
}

type Monitor struct {
	cfg        Config
	Circuit    *CircuitBreaker
	Errors     *ErrorTracker
	Latency    *LatencyTracker
	lastTick   atomic.Int64 // unix nano
	alertFn    func(string)
}

func NewMonitor(cfg Config, alertFn func(string)) *Monitor {
	return &Monitor{
		cfg:     cfg,
		Circuit: NewCircuitBreaker(cfg.CBThreshold, cfg.CBResetTimeout),
		Errors:  NewErrorTracker(cfg.ErrorWindow),
		Latency: NewLatencyTracker(200),
		alertFn: alertFn,
	}
}

// RecordTick marks that the engine completed a tick successfully.
func (m *Monitor) RecordTick() {
	m.lastTick.Store(time.Now().UnixNano())
	m.Circuit.RecordSuccess()
}

// RecordError records an engine error.
func (m *Monitor) RecordError(err error) {
	msg := err.Error()
	m.Errors.Record(msg)
	m.Circuit.RecordFailure()
	log.Error().Err(err).Msg("engine error recorded")

	if m.Errors.Rate() > m.cfg.MaxErrorRate {
		if m.alertFn != nil {
			m.alertFn(fmt.Sprintf("high error rate: %.2f/s", m.Errors.Rate()))
		}
	}
}

// RecordAPILatency records the round-trip time of an API call.
func (m *Monitor) RecordAPILatency(d time.Duration) {
	m.Latency.Record(d)
	if d > m.cfg.LatencyWarnThreshold {
		log.Warn().Dur("latency", d).Msg("high API latency")
	}
}

// Run starts the watchdog loop that checks for stalled ticks.
func (m *Monitor) Run(ctx context.Context) {
	m.lastTick.Store(time.Now().UnixNano())
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last := time.Unix(0, m.lastTick.Load())
			if time.Since(last) > m.cfg.TickTimeout {
				msg := fmt.Sprintf("watchdog: no tick for %.0fs", time.Since(last).Seconds())
				log.Error().Msg(msg)
				if m.alertFn != nil {
					m.alertFn(msg)
				}
			}

			state := m.Circuit.State()
			if state != CircuitClosed {
				log.Warn().Str("state", state.String()).Msg("circuit breaker state")
			}
		}
	}
}

// Status returns a human-readable status summary.
func (m *Monitor) Status() string {
	last := time.Unix(0, m.lastTick.Load())
	return fmt.Sprintf(
		"circuit=%s errors/min=%.1f latency_p95=%s last_tick=%s ago",
		m.Circuit.State(),
		m.Errors.Rate()*60,
		m.Latency.P95(),
		time.Since(last).Round(time.Second),
	)
}
