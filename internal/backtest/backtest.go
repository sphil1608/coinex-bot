// Package backtest provides a candle-replay engine that runs any registered
// strategy against historical data and produces a detailed performance report.
package backtest

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/models"
	"github.com/rusty/coinex-bot/internal/strategies"
)

// ────────────────────────────────────────────────────────────────────────────
// Config
// ────────────────────────────────────────────────────────────────────────────

type Config struct {
	InitialCapital float64
	FeeRate        float64 // e.g. 0.001 = 0.1%
	SlippagePct    float64 // e.g. 0.0005 = 0.05%
	StopLossPct    float64
	TakeProfitPct  float64
	PositionSizePct float64 // fraction of capital per trade e.g. 0.02
	WarmupBars     int     // bars consumed before trading starts
}

func DefaultConfig() Config {
	return Config{
		InitialCapital:  10000,
		FeeRate:         0.001,
		SlippagePct:     0.0005,
		StopLossPct:     0.015,
		TakeProfitPct:   0.030,
		PositionSizePct: 0.02,
		WarmupBars:      60,
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Trade record
// ────────────────────────────────────────────────────────────────────────────

type Trade struct {
	EntryTime  time.Time
	ExitTime   time.Time
	Side       models.SignalType
	EntryPrice float64
	ExitPrice  float64
	Qty        float64
	PnL        float64
	PnLPct     float64
	ExitReason string // "tp" | "sl" | "signal_flip" | "end"
}

// ────────────────────────────────────────────────────────────────────────────
// Result
// ────────────────────────────────────────────────────────────────────────────

type Result struct {
	StrategyName    string
	Trades          []Trade
	FinalCapital    float64
	TotalReturn     float64 // fractional
	CAGR            float64
	MaxDrawdown     float64
	SharpeRatio     float64
	WinRate         float64
	ProfitFactor    float64
	TotalTrades     int
	WinningTrades   int
	LosingTrades    int
	AvgWin          float64
	AvgLoss         float64
	LargestWin      float64
	LargestLoss     float64
	EquityCurve     []float64
}

func (r *Result) Print() {
	fmt.Printf("\n══════════════════════════════════════════════\n")
	fmt.Printf("  Backtest: %-30s\n", r.StrategyName)
	fmt.Printf("══════════════════════════════════════════════\n")
	fmt.Printf("  Total Return    : %+.2f%%\n", r.TotalReturn*100)
	fmt.Printf("  CAGR            : %+.2f%%\n", r.CAGR*100)
	fmt.Printf("  Sharpe Ratio    : %.3f\n", r.SharpeRatio)
	fmt.Printf("  Max Drawdown    : %.2f%%\n", r.MaxDrawdown*100)
	fmt.Printf("  Win Rate        : %.1f%%\n", r.WinRate*100)
	fmt.Printf("  Profit Factor   : %.3f\n", r.ProfitFactor)
	fmt.Printf("  Total Trades    : %d (W:%d L:%d)\n", r.TotalTrades, r.WinningTrades, r.LosingTrades)
	fmt.Printf("  Avg Win / Loss  : %+.4f / %+.4f\n", r.AvgWin, r.AvgLoss)
	fmt.Printf("  Largest Win     : %+.4f\n", r.LargestWin)
	fmt.Printf("  Largest Loss    : %+.4f\n", r.LargestLoss)
	fmt.Printf("  Final Capital   : $%.2f\n", r.FinalCapital)
	fmt.Printf("══════════════════════════════════════════════\n\n")
}

// ────────────────────────────────────────────────────────────────────────────
// Backtester
// ────────────────────────────────────────────────────────────────────────────

type Backtester struct {
	cfg Config
}

func New(cfg Config) *Backtester {
	return &Backtester{cfg: cfg}
}

// Run replays candles through a single strategy and returns a Result.
func (bt *Backtester) Run(strategy strategies.Strategy, candles []models.Candle) Result {
	cfg := bt.cfg
	capital := cfg.InitialCapital
	equity := capital

	var trades []Trade
	var equityCurve []float64

	type Position struct {
		side       models.SignalType
		entryPrice float64
		qty        float64
		entryTime  time.Time
		entryIdx   int
	}
	var pos *Position

	for i := cfg.WarmupBars; i < len(candles); i++ {
		window := candles[:i+1]
		emptyOB := models.OrderBook{}

		sig := strategy.Evaluate(nil, window, emptyOB)
		price, _ := candles[i].Close.Float64()

		// Check SL/TP on open position
		if pos != nil {
			var pnlPct float64
			if pos.side == models.SignalLong {
				pnlPct = (price - pos.entryPrice) / pos.entryPrice
			} else {
				pnlPct = (pos.entryPrice - price) / pos.entryPrice
			}

			exitReason := ""
			if pnlPct <= -cfg.StopLossPct {
				exitReason = "sl"
			} else if pnlPct >= cfg.TakeProfitPct {
				exitReason = "tp"
			} else if sig.Signal != models.SignalFlat && sig.Signal != pos.side {
				exitReason = "signal_flip"
			}

			if exitReason != "" {
				exitPrice := price * (1 - cfg.SlippagePct)
				if pos.side == models.SignalShort {
					exitPrice = price * (1 + cfg.SlippagePct)
				}
				var pnl float64
				if pos.side == models.SignalLong {
					pnl = (exitPrice - pos.entryPrice) * pos.qty
				} else {
					pnl = (pos.entryPrice - exitPrice) * pos.qty
				}
				fee := exitPrice * pos.qty * cfg.FeeRate
				pnl -= fee
				equity += pnl

				trades = append(trades, Trade{
					EntryTime:  pos.entryTime,
					ExitTime:   candles[i].OpenTime,
					Side:       pos.side,
					EntryPrice: pos.entryPrice,
					ExitPrice:  exitPrice,
					Qty:        pos.qty,
					PnL:        pnl,
					PnLPct:     pnlPct,
					ExitReason: exitReason,
				})
				pos = nil
			}
		}

		// Open new position on signal
		if pos == nil && sig.Signal != models.SignalFlat && equity > 0 {
			entryPrice := price * (1 + cfg.SlippagePct)
			if sig.Signal == models.SignalShort {
				entryPrice = price * (1 - cfg.SlippagePct)
			}
			tradeCapital := equity * cfg.PositionSizePct
			qty := tradeCapital / entryPrice
			fee := entryPrice * qty * cfg.FeeRate
			equity -= fee

			pos = &Position{
				side:       sig.Signal,
				entryPrice: entryPrice,
				qty:        qty,
				entryTime:  candles[i].OpenTime,
				entryIdx:   i,
			}
		}

		equityCurve = append(equityCurve, equity)
	}

	// Close any open position at end
	if pos != nil && len(candles) > 0 {
		price, _ := candles[len(candles)-1].Close.Float64()
		exitPrice := price
		var pnl float64
		if pos.side == models.SignalLong {
			pnl = (exitPrice - pos.entryPrice) * pos.qty
		} else {
			pnl = (pos.entryPrice - exitPrice) * pos.qty
		}
		fee := exitPrice * pos.qty * cfg.FeeRate
		pnl -= fee
		equity += pnl
		pnlPct := pnl / (pos.entryPrice * pos.qty)
		trades = append(trades, Trade{
			EntryTime:  pos.entryTime,
			ExitTime:   candles[len(candles)-1].OpenTime,
			Side:       pos.side,
			EntryPrice: pos.entryPrice,
			ExitPrice:  exitPrice,
			Qty:        pos.qty,
			PnL:        pnl,
			PnLPct:     pnlPct,
			ExitReason: "end",
		})
	}

	return bt.buildResult(strategy.Name(), trades, equityCurve, cfg.InitialCapital, equity, candles)
}

// RunAll runs all registered strategies and returns sorted results.
func (bt *Backtester) RunAll(candles []models.Candle) []Result {
	all := strategies.All()
	results := make([]Result, 0, len(all))
	for _, s := range all {
		r := bt.Run(s, candles)
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].SharpeRatio > results[j].SharpeRatio
	})
	return results
}

// ────────────────────────────────────────────────────────────────────────────
// Metrics
// ────────────────────────────────────────────────────────────────────────────

func (bt *Backtester) buildResult(
	name string,
	trades []Trade,
	equityCurve []float64,
	initialCapital, finalCapital float64,
	candles []models.Candle,
) Result {
	r := Result{
		StrategyName: name,
		Trades:       trades,
		FinalCapital: finalCapital,
		EquityCurve:  equityCurve,
		TotalReturn:  (finalCapital - initialCapital) / initialCapital,
	}

	if len(trades) == 0 {
		return r
	}

	// CAGR
	if len(candles) > 1 {
		years := candles[len(candles)-1].OpenTime.Sub(candles[0].OpenTime).Hours() / 8760.0
		if years > 0 {
			r.CAGR = math.Pow(finalCapital/initialCapital, 1.0/years) - 1
		}
	}

	// Win/loss stats
	var grossProfit, grossLoss float64
	for _, t := range trades {
		r.TotalTrades++
		if t.PnL > 0 {
			r.WinningTrades++
			grossProfit += t.PnL
			if t.PnL > r.LargestWin {
				r.LargestWin = t.PnL
			}
		} else {
			r.LosingTrades++
			grossLoss += math.Abs(t.PnL)
			if t.PnL < r.LargestLoss {
				r.LargestLoss = t.PnL
			}
		}
	}
	if r.TotalTrades > 0 {
		r.WinRate = float64(r.WinningTrades) / float64(r.TotalTrades)
	}
	if r.WinningTrades > 0 {
		r.AvgWin = grossProfit / float64(r.WinningTrades)
	}
	if r.LosingTrades > 0 {
		r.AvgLoss = -grossLoss / float64(r.LosingTrades)
	}
	if grossLoss > 0 {
		r.ProfitFactor = grossProfit / grossLoss
	}

	// Max drawdown
	peak := equityCurve[0]
	for _, eq := range equityCurve {
		if eq > peak {
			peak = eq
		}
		dd := (peak - eq) / peak
		if dd > r.MaxDrawdown {
			r.MaxDrawdown = dd
		}
	}

	// Sharpe ratio (annualised, assuming hourly bars, rf=0)
	if len(equityCurve) > 1 {
		returns := make([]float64, len(equityCurve)-1)
		for i := 1; i < len(equityCurve); i++ {
			if equityCurve[i-1] != 0 {
				returns[i-1] = (equityCurve[i] - equityCurve[i-1]) / equityCurve[i-1]
			}
		}
		mean, std := meanStd(returns)
		if std > 0 {
			r.SharpeRatio = mean / std * math.Sqrt(8760) // annualise hourly
		}
	}

	return r
}

func meanStd(vals []float64) (float64, float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	var variance float64
	for _, v := range vals {
		d := v - mean
		variance += d * d
	}
	return mean, math.Sqrt(variance / float64(len(vals)))
}

// ────────────────────────────────────────────────────────────────────────────
// CSV export
// ────────────────────────────────────────────────────────────────────────────

func (r *Result) ToCSV() string {
	out := "entry_time,exit_time,side,entry_price,exit_price,qty,pnl,pnl_pct,exit_reason\n"
	for _, t := range r.Trades {
		out += fmt.Sprintf("%s,%s,%s,%.6f,%.6f,%.6f,%.6f,%.4f,%s\n",
			t.EntryTime.Format(time.RFC3339),
			t.ExitTime.Format(time.RFC3339),
			string(t.Side),
			t.EntryPrice,
			t.ExitPrice,
			t.Qty,
			t.PnL,
			t.PnLPct,
			t.ExitReason,
		)
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Synthetic candle generator (for unit tests / demos)
// ────────────────────────────────────────────────────────────────────────────

// GenerateSineCandles produces N synthetic hourly candles following a sine wave
// with added Gaussian noise – useful for deterministic tests.
func GenerateSineCandles(n int, basePrice, amplitude, noiseStd float64) []models.Candle {
	candles := make([]models.Candle, n)
	t := time.Now().Add(-time.Duration(n) * time.Hour)
	price := basePrice
	for i := 0; i < n; i++ {
		sine := amplitude * math.Sin(float64(i)*2*math.Pi/48) // 48-bar cycle
		noise := noiseStd * (pseudoRand(i) - 0.5)
		price = basePrice + sine + noise
		if price < 1 {
			price = 1
		}
		open := price * (1 + (pseudoRand(i+1)-0.5)*0.002)
		high := price * (1 + pseudoRand(i+2)*0.005)
		low := price * (1 - pseudoRand(i+3)*0.005)
		vol := 100 + pseudoRand(i+4)*500

		candles[i] = models.Candle{
			OpenTime: t.Add(time.Duration(i) * time.Hour),
			Open:     decimal.NewFromFloat(open),
			High:     decimal.NewFromFloat(high),
			Low:      decimal.NewFromFloat(low),
			Close:    decimal.NewFromFloat(price),
			Volume:   decimal.NewFromFloat(vol),
		}
	}
	return candles
}

// Deterministic "random" based on index to keep tests reproducible.
func pseudoRand(seed int) float64 {
	x := float64((seed*1103515245 + 12345) & 0x7fffffff)
	return x / float64(0x7fffffff)
}
