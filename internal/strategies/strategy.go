package strategies

import (
	"context"
	"time"

	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// Strategy interface
// ────────────────────────────────────────────────────────────────────────────

// Strategy is the core interface every trading strategy must implement.
type Strategy interface {
	// Name returns the unique strategy ID.
	Name() string
	// Evaluate takes the latest candles + order book and returns a Signal.
	Evaluate(ctx context.Context, candles []models.Candle, ob models.OrderBook) models.Signal
}

// ────────────────────────────────────────────────────────────────────────────
// Registry
// ────────────────────────────────────────────────────────────────────────────

var registry = map[string]Strategy{}

func Register(s Strategy) {
	registry[s.Name()] = s
}

func Get(name string) (Strategy, bool) {
	s, ok := registry[name]
	return s, ok
}

func All() []Strategy {
	out := make([]Strategy, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func newSignal(strategy, market string, sig models.SignalType, conf float64, reason string) models.Signal {
	return models.Signal{
		Strategy:   strategy,
		Market:     market,
		Signal:     sig,
		Confidence: conf,
		Reason:     reason,
		Timestamp:  time.Now(),
	}
}

func flat(strategy, market string) models.Signal {
	return newSignal(strategy, market, models.SignalFlat, 0, "no signal")
}
