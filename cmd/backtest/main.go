package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/rusty/coinex-bot/internal/api"
	"github.com/rusty/coinex-bot/internal/backtest"
	"github.com/rusty/coinex-bot/internal/config"
	"github.com/rusty/coinex-bot/internal/models"
	"github.com/rusty/coinex-bot/internal/strategies"
)

func main() {
	cfgPath   := flag.String("config",    "configs/config.yaml", "config file")
	market    := flag.String("market",    "BTCUSDT",             "market symbol")
	tf        := flag.String("tf",        "1hour",               "timeframe")
	limit     := flag.Int("limit",        500,                   "number of candles")
	strategy  := flag.String("strategy",  "all",                 "strategy name or 'all'")
	outDir    := flag.String("out",       "",                    "directory to write CSV results")
	synthetic := flag.Bool("synthetic",   false,                 "use synthetic sine candles (no API key needed)")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	zerolog.SetGlobalLevel(zerolog.WarnLevel)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal().Err(err).Msg("config load failed")
	}

	bt := backtest.New(backtest.DefaultConfig())

	var candles []models.Candle

	if *synthetic {
		fmt.Println("Using synthetic sine-wave candles (2000 bars)…")
		candles = backtest.GenerateSineCandles(2000, 30000, 2000, 200)
	} else {
		client := api.NewClient(cfg.CoinEx.AccessID, cfg.CoinEx.SecretKey, cfg.CoinEx.BaseURL, true)
		ctx := context.Background()
		mt := cfg.Bot.MarketType
		if mt == "" {
			mt = "spot"
		}
		fmt.Printf("Fetching %d %s candles for %s…\n", *limit, *tf, *market)
		candles, err = client.GetKLines(ctx, *market, mt, *tf, *limit)
		if err != nil {
			log.Fatal().Err(err).Msg("candle fetch failed")
		}
	}

	fmt.Printf("Loaded %d candles\n\n", len(candles))

	if *strategy == "all" {
		results := bt.RunAll(candles)
		fmt.Printf("%-22s %8s %8s %8s %8s %7s\n",
			"Strategy", "Return%", "Sharpe", "MaxDD%", "WinRate", "Trades")
		fmt.Println("─────────────────────────────────────────────────────────────────")
		for _, r := range results {
			fmt.Printf("%-22s %7.2f%% %8.3f %7.2f%% %6.1f%% %7d\n",
				r.StrategyName,
				r.TotalReturn*100,
				r.SharpeRatio,
				r.MaxDrawdown*100,
				r.WinRate*100,
				r.TotalTrades,
			)
			if *outDir != "" {
				_ = os.MkdirAll(*outDir, 0755)
				path := *outDir + "/" + r.StrategyName + "_trades.csv"
				_ = os.WriteFile(path, []byte(r.ToCSV()), 0644)
			}
		}
	} else {
		s, ok := strategies.Get(*strategy)
		if !ok {
			log.Fatal().Str("strategy", *strategy).Msg("unknown strategy")
		}
		r := bt.Run(s, candles)
		r.Print()
		if *outDir != "" {
			_ = os.MkdirAll(*outDir, 0755)
			path := *outDir + "/" + r.StrategyName + "_trades.csv"
			_ = os.WriteFile(path, []byte(r.ToCSV()), 0644)
			fmt.Println("Trades written to", path)
		}
		fmt.Println("\nEquity curve (every 50 bars):")
		for i, eq := range r.EquityCurve {
			if i%50 == 0 {
				filled := int(eq / backtest.DefaultConfig().InitialCapital * 20)
				if filled > 40 {
					filled = 40
				}
				if filled < 0 {
					filled = 0
				}
				bar := ""
				for j := 0; j < filled; j++ { bar += "█" }
				for j := filled; j < 40; j++ { bar += "░" }
				fmt.Printf("  [%4d] $%8.2f |%s\n", i, eq, bar)
			}
		}
	}
}
