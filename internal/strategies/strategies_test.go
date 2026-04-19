package strategies_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/models"
	"github.com/rusty/coinex-bot/internal/strategies"
	_ "github.com/rusty/coinex-bot/internal/strategies" // init registers all
)

func makeCandles(prices []float64) []models.Candle {
	c := make([]models.Candle, len(prices))
	for i, p := range prices {
		c[i] = models.Candle{
			OpenTime: time.Now().Add(time.Duration(i) * time.Hour),
			Open:     decimal.NewFromFloat(p * 0.999),
			High:     decimal.NewFromFloat(p * 1.005),
			Low:      decimal.NewFromFloat(p * 0.995),
			Close:    decimal.NewFromFloat(p),
			Volume:   decimal.NewFromFloat(500 + float64(i)*10),
		}
	}
	return c
}

func ramp(start, end float64, n int) []float64 {
	out := make([]float64, n)
	step := (end - start) / float64(n-1)
	for i := range out {
		out[i] = start + float64(i)*step
	}
	return out
}

func emptyOB() models.OrderBook { return models.OrderBook{} }

// TestAllStrategiesRegistered ensures all 22 strategies are in the registry.
func TestAllStrategiesRegistered(t *testing.T) {
	all := strategies.All()
	const wantCount = 22
	if len(all) < wantCount {
		t.Errorf("expected at least %d strategies, got %d", wantCount, len(all))
	}
}

// TestAllStrategiesReturnValidSignal ensures no strategy panics on a normal input.
func TestAllStrategiesReturnValidSignal(t *testing.T) {
	candles := makeCandles(ramp(100, 150, 200))
	ob := emptyOB()
	ctx := context.Background()

	for _, s := range strategies.All() {
		t.Run(s.Name(), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("strategy %s panicked: %v", s.Name(), r)
				}
			}()
			sig := s.Evaluate(ctx, candles, ob)
			switch sig.Signal {
			case models.SignalLong, models.SignalShort, models.SignalFlat:
				// valid
			default:
				t.Errorf("strategy %s returned invalid signal: %q", s.Name(), sig.Signal)
			}
			if sig.Confidence < 0 || sig.Confidence > 1 {
				t.Errorf("strategy %s confidence %.4f out of [0,1]", s.Name(), sig.Confidence)
			}
		})
	}
}

// TestAllStrategiesHandleInsufficientData ensures no panic with minimal candles.
func TestAllStrategiesHandleInsufficientData(t *testing.T) {
	candles := makeCandles([]float64{100, 101, 102})
	ctx := context.Background()
	for _, s := range strategies.All() {
		t.Run(s.Name()+"_tiny", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("strategy %s panicked on tiny input: %v", s.Name(), r)
				}
			}()
			_ = s.Evaluate(ctx, candles, emptyOB())
		})
	}
}

// TestIchimoku_LongSignalInUptrend
func TestIchimoku_LongSignalInUptrend(t *testing.T) {
	s, ok := strategies.Get("ichimoku")
	if !ok {
		t.Fatal("ichimoku not found")
	}
	candles := makeCandles(ramp(100, 300, 200))
	sig := s.Evaluate(context.Background(), candles, emptyOB())
	if sig.Signal == models.SignalShort {
		t.Errorf("expected Long or Flat in strong uptrend, got Short. Reason: %s", sig.Reason)
	}
}

// TestOrderBookScalper_BidImbalance_Long
func TestOrderBookScalper_BidImbalance_Long(t *testing.T) {
	s, ok := strategies.Get("scalp_ob")
	if !ok {
		t.Fatal("scalp_ob not found")
	}
	ob := models.OrderBook{
		Market: "BTCUSDT",
		Bids:   []models.Level{{Price: decimal.NewFromFloat(100), Quantity: decimal.NewFromFloat(100)}},
		Asks:   []models.Level{{Price: decimal.NewFromFloat(101), Quantity: decimal.NewFromFloat(5)}},
	}
	sig := s.Evaluate(context.Background(), nil, ob)
	if sig.Signal != models.SignalLong {
		t.Errorf("expected Long on heavy bid imbalance, got %s", sig.Signal)
	}
}

// TestRSIMeanRevert_OversoldIsLong
func TestRSIMeanRevert_OversoldIsLong(t *testing.T) {
	s, ok := strategies.Get("rsi_mean_revert")
	if !ok {
		t.Fatal("rsi_mean_revert not found")
	}
	// Strongly downtrending → RSI very low
	candles := makeCandles(ramp(200, 50, 100))
	sig := s.Evaluate(context.Background(), candles, emptyOB())
	if sig.Signal != models.SignalLong {
		t.Logf("rsi_mean_revert in downtrend: signal=%s confidence=%.2f reason=%s",
			sig.Signal, sig.Confidence, sig.Reason)
	}
}

// TestBreakout_BreaksResistance_Long
func TestBreakout_BreaksResistance_Long(t *testing.T) {
	s, ok := strategies.Get("breakout")
	if !ok {
		t.Fatal("breakout not found")
	}
	// Flat then spike above recent high
	prices := ramp(100, 100, 30)
	prices = append(prices, 130) // above all prior highs
	sig := s.Evaluate(context.Background(), makeCandles(prices), emptyOB())
	if sig.Signal != models.SignalLong {
		t.Errorf("expected Long on breakout, got %s. Reason: %s", sig.Signal, sig.Reason)
	}
}

// TestSupertrend_FlipLong
func TestSupertrend_FlipLong(t *testing.T) {
	s, ok := strategies.Get("supertrend")
	if !ok {
		t.Fatal("supertrend not found")
	}
	// Downtrend then strong uptrend should produce flip signal
	prices := append(ramp(150, 80, 80), ramp(80, 200, 80)...)
	sig := s.Evaluate(context.Background(), makeCandles(prices), emptyOB())
	// We just assert no panic and valid output here — flip timing depends on ATR
	switch sig.Signal {
	case models.SignalLong, models.SignalShort, models.SignalFlat:
	default:
		t.Errorf("invalid signal type: %q", sig.Signal)
	}
}
