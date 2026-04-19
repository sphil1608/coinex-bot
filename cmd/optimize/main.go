package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/rusty/coinex-bot/internal/backtest"
	"github.com/rusty/coinex-bot/internal/optimizer"
	"github.com/rusty/coinex-bot/internal/strategies"
	_ "github.com/rusty/coinex-bot/internal/strategies"
)

func main() {
	strategy  := flag.String("strategy",  "rsi",      "strategy to optimise: rsi | ema | breakout")
	metric    := flag.String("metric",    "sharpe",   "sharpe | return | profit_factor")
	inBars    := flag.Int("in",           200,        "in-sample bars")
	outBars   := flag.Int("out",          50,         "out-of-sample bars")
	nCandles  := flag.Int("candles",      1000,       "synthetic candle count")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	zerolog.SetGlobalLevel(zerolog.WarnLevel)

	fmt.Printf("Generating %d synthetic candles…\n", *nCandles)
	candles := backtest.GenerateSineCandles(*nCandles, 30000, 3000, 300)

	optCfg := optimizer.DefaultConfig()
	optCfg.Metric = *metric
	optCfg.InSampleBars = *inBars
	optCfg.OutSampleBars = *outBars

	opt := optimizer.New(optCfg, backtest.DefaultConfig())
	ctx := context.Background()

	var factory optimizer.StrategyFactory
	var paramRanges []optimizer.ParamRange

	switch *strategy {
	case "ema":
		factory = optimizer.EMACrossFactory
		paramRanges = []optimizer.ParamRange{
			{Name: "fast", Start: 5,  End: 20, Step: 5},
			{Name: "slow", Start: 15, End: 50, Step: 5},
		}
	case "breakout":
		factory = func(ps optimizer.ParamSet) strategies.Strategy {
			lb := 20
			if v, ok := ps["lookback"]; ok {
				lb = v
			}
			return &strategies.BreakoutStrategyExported{Lookback: lb}
		}
		paramRanges = []optimizer.ParamRange{
			{Name: "lookback", Start: 10, End: 50, Step: 5},
		}
	default: // rsi
		factory = optimizer.RSIMeanRevertFactory
		paramRanges = []optimizer.ParamRange{
			{Name: "period",     Start: 7,  End: 21, Step: 7},
			{Name: "oversold",   Start: 20, End: 40, Step: 5},
			{Name: "overbought", Start: 60, End: 80, Step: 5},
		}
	}

	results, summary, err := opt.Run(ctx, candles, paramRanges, factory)
	if err != nil {
		log.Fatal().Err(err).Msg("optimiser failed")
	}

	fmt.Printf("\nTop 5 out-of-sample windows:\n")
	fmt.Printf("%-8s %-12s %-12s  Params\n", "Window", "In-metric", "Out-metric")
	fmt.Println("────────────────────────────────────────────────────────")
	for i, r := range results {
		if i >= 5 {
			break
		}
		fmt.Printf("%-8d %11.4f  %11.4f   %v\n",
			r.Window.InSampleStart, r.InSampleMetric, r.OutSampleMetric, r.Params)
	}
	summary.Print()
}
