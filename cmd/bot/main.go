package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/rusty/coinex-bot/internal/api"
	"github.com/rusty/coinex-bot/internal/config"
	"github.com/rusty/coinex-bot/internal/engine"
	_ "github.com/rusty/coinex-bot/internal/strategies" // registers all strategies
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	paper := cfg.Bot.Mode == "paper"
	if paper {
		log.Warn().Msg("⚠️  PAPER TRADING MODE – no real orders will be placed")
	}

	client := api.NewClient(
		cfg.CoinEx.AccessID,
		cfg.CoinEx.SecretKey,
		cfg.CoinEx.BaseURL,
		paper,
	)

	eng := engine.New(cfg, client)

	if cfg.Dashboard.Enabled {
		go eng.StartDashboard(cfg.Dashboard.Port)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info().Msg("shutting down…")
		cancel()
	}()

	if err := eng.Run(ctx); err != nil && err != context.Canceled {
		log.Error().Err(err).Msg("engine stopped with error")
		os.Exit(1)
	}

	log.Info().Msg("bot stopped cleanly")
}
