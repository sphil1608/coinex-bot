package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/rusty/coinex-bot/internal/api"
	"github.com/rusty/coinex-bot/internal/config"
	"github.com/rusty/coinex-bot/internal/engine"
	_ "github.com/rusty/coinex-bot/internal/strategies"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	// Log to bot.log AND stdout so errors are visible in screen session
	logFile, err := os.OpenFile("bot.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	} else {
		multi := zerolog.MultiLevelWriter(
			zerolog.ConsoleWriter{Out: os.Stdout},
			logFile,
		)
		log.Logger = zerolog.New(multi).With().Timestamp().Logger()
	}
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

	// Colored terminal dashboard — works great in screen over SSH
	eng.StartTerminalDash(eng.Journal, 3*time.Second)

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
