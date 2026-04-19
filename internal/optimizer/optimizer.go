// Package optimizer provides walk-forward parameter optimization.
// It splits the candle history into in-sample (training) and out-of-sample
// (validation) windows, exhaustively searches a parameter grid on in-sample,
// then evaluates the best params on out-of-sample — giving a realistic view of
// how well a strategy will generalise.
package optimizer

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/rusty/coinex-bot/internal/backtest"
	"github.com/rusty/coinex-bot/internal/models"
	"github.com/rusty/coinex-bot/internal/strategies"
)

// ────────────────────────────────────────────────────────────────────────────
// Parameter grid
// ────────────────────────────────────────────────────────────────────────────

// ParamRange defines a sweep for one integer parameter.
type ParamRange struct {
	Name  string
	Start int
	End   int
	Step  int
}

func (r ParamRange) Values() []int {
	var out []int
	for v := r.Start; v <= r.End; v += r.Step {
		out = append(out, v)
	}
	return out
}

// ParamSet is a concrete set of integer parameters for one trial.
type ParamSet map[string]int

// Grid generates the Cartesian product of all ParamRanges.
func Grid(ranges []ParamRange) []ParamSet {
	if len(ranges) == 0 {
		return []ParamSet{{}}
	}
	tail := Grid(ranges[1:])
	var out []ParamSet
	for _, v := range ranges[0].Values() {
		for _, rest := range tail {
			ps := ParamSet{ranges[0].Name: v}
			for k, val := range rest {
				ps[k] = val
			}
			out = append(out, ps)
		}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Strategy factory
// ────────────────────────────────────────────────────────────────────────────

// StrategyFactory creates a Strategy from a ParamSet.
type StrategyFactory func(params ParamSet) strategies.Strategy

// ────────────────────────────────────────────────────────────────────────────
// Walk-forward window
// ────────────────────────────────────────────────────────────────────────────

type WalkForwardWindow struct {
	InSampleStart  int
	InSampleEnd    int
	OutSampleStart int
	OutSampleEnd   int
}

func buildWindows(total, inSample, outSample, step int) []WalkForwardWindow {
	var windows []WalkForwardWindow
	for start := 0; start+inSample+outSample <= total; start += step {
		windows = append(windows, WalkForwardWindow{
			InSampleStart:  start,
			InSampleEnd:    start + inSample,
			OutSampleStart: start + inSample,
			OutSampleEnd:   start + inSample + outSample,
		})
	}
	return windows
}

// ────────────────────────────────────────────────────────────────────────────
// Trial result
// ────────────────────────────────────────────────────────────────────────────

type TrialResult struct {
	Params        ParamSet
	InSampleMetric  float64 // Sharpe on in-sample
	OutSampleMetric float64 // Sharpe on out-of-sample
	Window        WalkForwardWindow
}

// ────────────────────────────────────────────────────────────────────────────
// Optimizer
// ────────────────────────────────────────────────────────────────────────────

type Config struct {
	InSampleBars  int // bars in training window
	OutSampleBars int // bars in validation window
	StepBars      int // roll step
	Workers       int // parallel workers
	Metric        string // "sharpe" | "return" | "profit_factor"
}

func DefaultConfig() Config {
	return Config{
		InSampleBars:  200,
		OutSampleBars: 50,
		StepBars:      50,
		Workers:       4,
		Metric:        "sharpe",
	}
}

type Optimizer struct {
	cfg     Config
	btCfg   backtest.Config
}

func New(cfg Config, btCfg backtest.Config) *Optimizer {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	return &Optimizer{cfg: cfg, btCfg: btCfg}
}

// Run performs walk-forward optimisation.
// factory is called with each ParamSet to instantiate the strategy.
// Returns per-window trial results sorted by out-of-sample metric descending.
func (o *Optimizer) Run(
	ctx context.Context,
	candles []models.Candle,
	paramRanges []ParamRange,
	factory StrategyFactory,
) ([]TrialResult, Summary, error) {

	windows := buildWindows(len(candles), o.cfg.InSampleBars, o.cfg.OutSampleBars, o.cfg.StepBars)
	if len(windows) == 0 {
		return nil, Summary{}, fmt.Errorf("insufficient candles for walk-forward (have %d, need %d)",
			len(candles), o.cfg.InSampleBars+o.cfg.OutSampleBars)
	}

	grid := Grid(paramRanges)
	fmt.Printf("Walk-forward: %d windows × %d param combos = %d trials\n",
		len(windows), len(grid), len(windows)*len(grid))

	var (
		mu      sync.Mutex
		results []TrialResult
		sem     = make(chan struct{}, o.cfg.Workers)
		wg      sync.WaitGroup
	)

	for _, win := range windows {
		win := win
		inCandles := candles[win.InSampleStart:win.InSampleEnd]
		outCandles := candles[win.OutSampleStart:win.OutSampleEnd]

		// Find best params on in-sample
		bestPS, bestInMetric := o.bestParams(ctx, inCandles, grid, factory, sem, &wg)

		// Evaluate best params on out-of-sample
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			s := factory(bestPS)
			bt := backtest.New(o.btCfg)
			r := bt.Run(s, outCandles)
			outMetric := o.metric(r)

			mu.Lock()
			results = append(results, TrialResult{
				Params:          bestPS,
				InSampleMetric:  bestInMetric,
				OutSampleMetric: outMetric,
				Window:          win,
			})
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].OutSampleMetric > results[j].OutSampleMetric
	})

	return results, buildSummary(results), nil
}

func (o *Optimizer) bestParams(
	ctx context.Context,
	candles []models.Candle,
	grid []ParamSet,
	factory StrategyFactory,
	sem chan struct{},
	outerWG *sync.WaitGroup,
) (ParamSet, float64) {

	type scored struct {
		ps     ParamSet
		metric float64
	}
	ch := make(chan scored, len(grid))
	var wg sync.WaitGroup

	for _, ps := range grid {
		ps := ps
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			s := factory(ps)
			bt := backtest.New(o.btCfg)
			r := bt.Run(s, candles)
			ch <- scored{ps: ps, metric: o.metric(r)}
		}()
	}
	wg.Wait()
	close(ch)

	best := scored{metric: -1e18}
	for s := range ch {
		if s.metric > best.metric {
			best = s
		}
	}
	return best.ps, best.metric
}

func (o *Optimizer) metric(r backtest.Result) float64 {
	switch o.cfg.Metric {
	case "return":
		return r.TotalReturn
	case "profit_factor":
		return r.ProfitFactor
	default: // sharpe
		return r.SharpeRatio
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Summary
// ────────────────────────────────────────────────────────────────────────────

type Summary struct {
	TotalWindows       int
	AvgInSampleMetric  float64
	AvgOutSampleMetric float64
	EfficiencyRatio    float64 // out/in – closer to 1 = better generalisation
	BestParams         ParamSet
	BestOutMetric      float64
}

func buildSummary(results []TrialResult) Summary {
	if len(results) == 0 {
		return Summary{}
	}
	var sumIn, sumOut float64
	for _, r := range results {
		sumIn += r.InSampleMetric
		sumOut += r.OutSampleMetric
	}
	avgIn := sumIn / float64(len(results))
	avgOut := sumOut / float64(len(results))
	var er float64
	if avgIn != 0 {
		er = avgOut / avgIn
	}
	return Summary{
		TotalWindows:       len(results),
		AvgInSampleMetric:  avgIn,
		AvgOutSampleMetric: avgOut,
		EfficiencyRatio:    er,
		BestParams:         results[0].Params,
		BestOutMetric:      results[0].OutSampleMetric,
	}
}

func (s Summary) Print() {
	fmt.Println("\n╔══════════════════════════════════════════════╗")
	fmt.Println("║        WALK-FORWARD OPTIMIZER SUMMARY        ║")
	fmt.Println("╠══════════════════════════════════════════════╣")
	fmt.Printf("║  Windows tested      : %-22d║\n", s.TotalWindows)
	fmt.Printf("║  Avg in-sample       : %-22.4f║\n", s.AvgInSampleMetric)
	fmt.Printf("║  Avg out-of-sample   : %-22.4f║\n", s.AvgOutSampleMetric)
	fmt.Printf("║  Efficiency ratio    : %-22.4f║\n", s.EfficiencyRatio)
	fmt.Printf("║  Best out metric     : %-22.4f║\n", s.BestOutMetric)
	fmt.Println("╠══════════════════════════════════════════════╣")
	fmt.Println("║  Best Parameters                             ║")
	for k, v := range s.BestParams {
		fmt.Printf("║    %-16s = %-24d║\n", k, v)
	}
	fmt.Println("╚══════════════════════════════════════════════╝")
}

// ────────────────────────────────────────────────────────────────────────────
// Built-in factory examples
// ────────────────────────────────────────────────────────────────────────────

// RSIMeanRevertFactory builds an RSIMeanRevert strategy from params.
// Params used: "period", "oversold", "overbought"
func RSIMeanRevertFactory(params ParamSet) strategies.Strategy {
	period := 14
	oversold := 30
	overbought := 70
	if v, ok := params["period"]; ok {
		period = v
	}
	if v, ok := params["oversold"]; ok {
		oversold = v
	}
	if v, ok := params["overbought"]; ok {
		overbought = v
	}
	return &strategies.RSIMeanRevertExported{
		Period:     period,
		Oversold:   float64(oversold),
		Overbought: float64(overbought),
	}
}

// EMACrossFactory builds an EMACross strategy from params.
// Params used: "fast", "slow"
func EMACrossFactory(params ParamSet) strategies.Strategy {
	fast, slow := 9, 21
	if v, ok := params["fast"]; ok {
		fast = v
	}
	if v, ok := params["slow"]; ok {
		slow = v
	}
	return &strategies.EMACrossExported{Fast: fast, Slow: slow}
}
