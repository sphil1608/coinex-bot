// Package api rate_limiter.go — token-bucket rate limiter that ensures
// the bot never exceeds CoinEx API v2 per-second limits.
package api

import (
	"context"
	"sync"
	"time"
)

// RateLimiter is a simple token-bucket implementation.
// CoinEx API v2 limits: 30 req/s for market data, 10 req/s for trading.
type RateLimiter struct {
	mu       sync.Mutex
	tokens   float64
	maxTokens float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

func NewRateLimiter(rps float64) *RateLimiter {
	return &RateLimiter{
		tokens:     rps,
		maxTokens:  rps,
		refillRate: rps,
		lastRefill: time.Now(),
	}
}

// Wait blocks until a token is available, or ctx is cancelled.
func (r *RateLimiter) Wait(ctx context.Context) error {
	for {
		r.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(r.lastRefill).Seconds()
		r.tokens += elapsed * r.refillRate
		if r.tokens > r.maxTokens {
			r.tokens = r.maxTokens
		}
		r.lastRefill = now

		if r.tokens >= 1.0 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}
		// Calculate sleep time needed for next token
		needed := (1.0 - r.tokens) / r.refillRate
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(needed * float64(time.Second))):
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Pre-configured limiters matching CoinEx v2 limits
// ────────────────────────────────────────────────────────────────────────────

var (
	// MarketDataLimiter: 30 req/s for public endpoints (klines, depth, etc.)
	MarketDataLimiter = NewRateLimiter(30)
	// TradingLimiter: 10 req/s for authenticated trading endpoints
	TradingLimiter = NewRateLimiter(10)
)
