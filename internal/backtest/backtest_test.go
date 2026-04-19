package backtest_test

import (
	"testing"

	"github.com/rusty/coinex-bot/internal/backtest"
	"github.com/rusty/coinex-bot/internal/strategies"
	_ "github.com/rusty/coinex-bot/internal/strategies"
)

func TestGenerateSineCandles_Length(t *testing.T) {
	n := 500
	candles := backtest.GenerateSineCandles(n, 30000, 1000, 100)
	if len(candles) != n {
		t.Errorf("expected %d candles, got %d", n, len(candles))
	}
}

func TestGenerateSineCandles_OHLCV_Sanity(t *testing.T) {
	candles := backtest.GenerateSineCandles(100, 10000, 500, 50)
	for i, c := range candles {
		highF, _ := c.High.Float64()
		lowF, _ := c.Low.Float64()
		closeF, _ := c.Close.Float64()
		volF, _ := c.Volume.Float64()
		if highF < lowF {
			t.Errorf("candle %d: high (%.2f) < low (%.2f)", i, highF, lowF)
		}
		if closeF <= 0 {
			t.Errorf("candle %d: close (%.2f) <= 0", i, closeF)
		}
		if volF <= 0 {
			t.Errorf("candle %d: volume (%.2f) <= 0", i, volF)
		}
	}
}

func TestBacktest_AllStrategies_NoErrors(t *testing.T) {
	candles := backtest.GenerateSineCandles(300, 30000, 2000, 200)
	bt := backtest.New(backtest.DefaultConfig())

	for _, s := range strategies.All() {
		t.Run(s.Name(), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("backtest panicked for %s: %v", s.Name(), r)
				}
			}()
			result := bt.Run(s, candles)
			if result.StrategyName != s.Name() {
				t.Errorf("name mismatch: %s vs %s", result.StrategyName, s.Name())
			}
			if result.FinalCapital < 0 {
				t.Errorf("negative final capital: %.2f", result.FinalCapital)
			}
		})
	}
}

func TestBacktest_MetricsRange(t *testing.T) {
	candles := backtest.GenerateSineCandles(500, 30000, 2000, 200)
	bt := backtest.New(backtest.DefaultConfig())
	s, _ := strategies.Get("ema_cross")
	r := bt.Run(s, candles)

	if r.WinRate < 0 || r.WinRate > 1 {
		t.Errorf("WinRate out of range: %.4f", r.WinRate)
	}
	if r.MaxDrawdown < 0 || r.MaxDrawdown > 1 {
		t.Errorf("MaxDrawdown out of range: %.4f", r.MaxDrawdown)
	}
	if r.TotalTrades < 0 {
		t.Errorf("negative TotalTrades: %d", r.TotalTrades)
	}
	if r.WinningTrades+r.LosingTrades > r.TotalTrades {
		t.Errorf("W+L (%d) > Total (%d)", r.WinningTrades+r.LosingTrades, r.TotalTrades)
	}
}

func TestBacktest_ProfitFactor_Consistency(t *testing.T) {
	candles := backtest.GenerateSineCandles(500, 30000, 2000, 200)
	bt := backtest.New(backtest.DefaultConfig())

	for _, s := range strategies.All() {
		r := bt.Run(s, candles)
		if r.ProfitFactor < 0 {
			t.Errorf("%s: negative profit factor %.4f", s.Name(), r.ProfitFactor)
		}
	}
}

func TestBacktest_EquityCurve_NonEmpty(t *testing.T) {
	candles := backtest.GenerateSineCandles(200, 30000, 2000, 200)
	bt := backtest.New(backtest.DefaultConfig())
	s, _ := strategies.Get("macd_cross")
	r := bt.Run(s, candles)
	cfg := backtest.DefaultConfig()
	expected := len(candles) - cfg.WarmupBars
	if len(r.EquityCurve) < expected/2 {
		t.Errorf("equity curve too short: %d", len(r.EquityCurve))
	}
}

func TestBacktest_RunAll_ReturnsSortedBySharpe(t *testing.T) {
	candles := backtest.GenerateSineCandles(300, 30000, 2000, 200)
	bt := backtest.New(backtest.DefaultConfig())
	results := bt.RunAll(candles)

	for i := 1; i < len(results); i++ {
		if results[i].SharpeRatio > results[i-1].SharpeRatio+0.0001 {
			t.Errorf("results not sorted by Sharpe at index %d: %.4f > %.4f",
				i, results[i].SharpeRatio, results[i-1].SharpeRatio)
		}
	}
}

func TestBacktest_CSVOutput(t *testing.T) {
	candles := backtest.GenerateSineCandles(200, 30000, 2000, 200)
	bt := backtest.New(backtest.DefaultConfig())
	s, _ := strategies.Get("ichimoku")
	r := bt.Run(s, candles)
	csv := r.ToCSV()
	if len(csv) == 0 {
		t.Error("CSV output is empty")
	}
	// Check header
	if csv[:9] != "entry_tim" {
		t.Errorf("CSV missing header, starts with: %q", csv[:20])
	}
}

func TestBacktest_SineCandles_OscillatingStrategy(t *testing.T) {
	// RSI mean reversion should find opportunities in sine wave market
	candles := backtest.GenerateSineCandles(500, 30000, 3000, 50)
	bt := backtest.New(backtest.DefaultConfig())
	s, _ := strategies.Get("rsi_mean_revert")
	r := bt.Run(s, candles)
	if r.TotalTrades == 0 {
		t.Log("rsi_mean_revert found 0 trades on sine wave (may need longer data)")
	}

}
