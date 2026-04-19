package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/api"
	"github.com/rusty/coinex-bot/internal/config"
	"github.com/rusty/coinex-bot/internal/ml"
	"github.com/rusty/coinex-bot/internal/models"
	"github.com/rusty/coinex-bot/internal/strategies"
)

// ────────────────────────────────────────────────────────────────────────────
// Engine
// ────────────────────────────────────────────────────────────────────────────

type Engine struct {
	cfg         *config.Config
	client      *api.Client
	strategies  []strategies.Strategy
	ensemble    *ml.Ensemble
	lob         *api.LiveOrderBook
	wsFeed      *api.WSFeed
	candles     []models.Candle
	mu          sync.RWMutex
	openOrders  map[string]*models.Order
	stopCh      chan struct{}
	SignalLog   []models.Signal
}

func New(cfg *config.Config, client *api.Client) *Engine {
	e := &Engine{
		cfg:        cfg,
		client:     client,
		openOrders: make(map[string]*models.Order),
		stopCh:     make(chan struct{}),
	}

	// Build strategy list from config
	for _, s := range strategies.All() {
		if scfg, ok := cfg.Strategies[s.Name()]; ok {
			if enabled, ok := scfg["enabled"].(bool); ok && !enabled {
				continue
			}
		}
		e.strategies = append(e.strategies, s)
	}

	// ML ensemble
	if cfg.ML.Enabled {
		interval, err := time.ParseDuration(cfg.ML.RetrainInterval)
		if err != nil {
			interval = 24 * time.Hour
		}
		e.ensemble = ml.NewEnsemble(cfg.ML.MinConfidence, interval)
		strategies.NewMLEnsembleStrategy(e.ensemble)
	}

	// Live order book + WS feed
	wsURL := cfg.CoinEx.WSSpotURL
	if cfg.Bot.MarketType == "futures" {
		wsURL = cfg.CoinEx.WSFuturesURL
	}
	e.lob = api.NewLiveOrderBook(cfg.Bot.Market)
	e.wsFeed = api.NewWSFeed(wsURL)
	e.wsFeed.AddHandler(e.lob.Handle)

	return e
}

// ────────────────────────────────────────────────────────────────────────────
// Run
// ────────────────────────────────────────────────────────────────────────────

func (e *Engine) Run(ctx context.Context) error {
	log.Info().
		Str("market", e.cfg.Bot.Market).
		Str("type", e.cfg.Bot.MarketType).
		Str("mode", e.cfg.Bot.Mode).
		Int("strategies", len(e.strategies)).
		Msg("engine starting")

	// Connect WebSocket
	if err := e.wsFeed.Connect(ctx); err != nil {
		return fmt.Errorf("ws connect: %w", err)
	}
	if err := e.wsFeed.SubscribeDepth(e.cfg.Bot.Market, 20); err != nil {
		log.Warn().Err(err).Msg("depth subscribe failed")
	}

	// Seed initial candles
	if err := e.refreshCandles(ctx); err != nil {
		return fmt.Errorf("initial candle fetch: %w", err)
	}

	// Set futures leverage
	if e.cfg.Bot.MarketType == "futures" {
		_ = e.client.SetFuturesLeverage(ctx, e.cfg.Bot.Market, e.cfg.Bot.Leverage, "both")
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.stopCh:
			return nil
		case <-ticker.C:
			if err := e.tick(ctx); err != nil {
				log.Error().Err(err).Msg("tick error")
			}
			if e.ensemble != nil {
				e.ensemble.MaybeRetrain(ctx)
			}
		}
	}
}

func (e *Engine) Stop() { close(e.stopCh) }

// ────────────────────────────────────────────────────────────────────────────
// Tick
// ────────────────────────────────────────────────────────────────────────────

func (e *Engine) tick(ctx context.Context) error {
	if err := e.refreshCandles(ctx); err != nil {
		return err
	}

	e.mu.RLock()
	candles := make([]models.Candle, len(e.candles))
	copy(candles, e.candles)
	e.mu.RUnlock()

	ob := e.lob.Snapshot()

	signals := e.gatherSignals(ctx, candles, ob)
	if len(signals) == 0 {
		return nil
	}

	consensus := e.aggregateSignals(signals)
	if consensus.Signal == models.SignalFlat {
		return nil
	}

	log.Info().
		Str("strategy", consensus.Strategy).
		Str("signal", string(consensus.Signal)).
		Float64("confidence", consensus.Confidence).
		Str("reason", consensus.Reason).
		Msg("consensus signal")

	e.mu.Lock()
	e.SignalLog = append(e.SignalLog, consensus)
	e.mu.Unlock()

	return e.executeSignal(ctx, consensus, ob)
}

// ────────────────────────────────────────────────────────────────────────────
// Signal gathering & aggregation
// ────────────────────────────────────────────────────────────────────────────

func (e *Engine) gatherSignals(ctx context.Context, candles []models.Candle, ob models.OrderBook) []models.Signal {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []models.Signal
	)
	for _, s := range e.strategies {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			sig := s.Evaluate(ctx, candles, ob)
			if sig.Signal != models.SignalFlat {
				mu.Lock()
				out = append(out, sig)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// aggregateSignals uses a confidence-weighted majority vote.
func (e *Engine) aggregateSignals(signals []models.Signal) models.Signal {
	var longConf, shortConf float64
	for _, s := range signals {
		switch s.Signal {
		case models.SignalLong:
			longConf += s.Confidence
		case models.SignalShort:
			shortConf += s.Confidence
		}
	}

	if longConf == 0 && shortConf == 0 {
		return models.Signal{Signal: models.SignalFlat}
	}

	if longConf > shortConf {
		conf := longConf / (longConf + shortConf)
		return models.Signal{
			Strategy:   "consensus",
			Signal:     models.SignalLong,
			Confidence: conf,
			Reason:     fmt.Sprintf("%.0f%% long confidence across %d strategies", conf*100, len(signals)),
			Timestamp:  time.Now(),
		}
	}
	conf := shortConf / (longConf + shortConf)
	return models.Signal{
		Strategy:   "consensus",
		Signal:     models.SignalShort,
		Confidence: conf,
		Reason:     fmt.Sprintf("%.0f%% short confidence across %d strategies", conf*100, len(signals)),
		Timestamp:  time.Now(),
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Order execution with risk management
// ────────────────────────────────────────────────────────────────────────────

func (e *Engine) executeSignal(ctx context.Context, sig models.Signal, ob models.OrderBook) error {
	if len(e.openOrders) >= e.cfg.Bot.MaxOpenOrders {
		log.Warn().Msg("max open orders reached, skipping")
		return nil
	}

	price := ob.MidPrice()
	if price.IsZero() {
		return nil
	}

	// Position sizing: use fixed qty from config
	qty, err := decimal.NewFromString(e.cfg.Bot.BaseQty)
	if err != nil {
		return fmt.Errorf("invalid base_qty: %w", err)
	}

	side := string(models.SideBuy)
	if sig.Signal == models.SignalShort {
		side = string(models.SideSell)
	}

	// Limit order at mid price (taker-friendly)
	req := api.PlaceOrderReq{
		Market:     e.cfg.Bot.Market,
		MarketType: e.cfg.Bot.MarketType,
		Side:       side,
		Type:       string(models.OrderTypeLimit),
		Amount:     qty.String(),
		Price:      price.StringFixed(4),
		ClientID:   fmt.Sprintf("bot-%d", time.Now().UnixMilli()),
	}

	var order *models.Order
	if e.cfg.Bot.MarketType == "futures" {
		order, err = e.client.PlaceFuturesOrder(ctx, req)
	} else {
		order, err = e.client.PlaceSpotOrder(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("place order: %w", err)
	}

	e.mu.Lock()
	e.openOrders[order.ID] = order
	e.mu.Unlock()

	// Schedule SL / TP cancellation
	go e.manageTrade(ctx, order, price, sig.Signal)
	return nil
}

// manageTrade waits and cancels the order or closes at SL/TP.
func (e *Engine) manageTrade(ctx context.Context, order *models.Order, entryPrice decimal.Decimal, sig models.SignalType) {
	sl := decimal.NewFromFloat(e.cfg.Bot.StopLossPct)
	tp := decimal.NewFromFloat(e.cfg.Bot.TakeProfitPct)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(4 * time.Hour)
	defer timeout.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout.C:
			_ = e.client.CancelSpotOrder(ctx, order.Market, order.ID)
			e.removeOrder(order.ID)
			return
		case <-ticker.C:
			ob := e.lob.Snapshot()
			cur := ob.MidPrice()
			if cur.IsZero() {
				continue
			}
			var pct decimal.Decimal
			if sig == models.SignalLong {
				pct = cur.Sub(entryPrice).Div(entryPrice)
			} else {
				pct = entryPrice.Sub(cur).Div(entryPrice)
			}
			pctF, _ := pct.Float64()
			if pctF <= -sl.InexactFloat64() {
				log.Info().Str("order", order.ID).Float64("pct", pctF).Msg("stop-loss hit")
				_ = e.client.CancelSpotOrder(ctx, order.Market, order.ID)
				e.removeOrder(order.ID)
				return
			}
			if pctF >= tp.InexactFloat64() {
				log.Info().Str("order", order.ID).Float64("pct", pctF).Msg("take-profit hit")
				_ = e.client.CancelSpotOrder(ctx, order.Market, order.ID)
				e.removeOrder(order.ID)
				return
			}
		}
	}
}

func (e *Engine) removeOrder(id string) {
	e.mu.Lock()
	delete(e.openOrders, id)
	e.mu.Unlock()
}

// ────────────────────────────────────────────────────────────────────────────
// Candle management
// ────────────────────────────────────────────────────────────────────────────

func (e *Engine) refreshCandles(ctx context.Context) error {
	mt := "spot"
	if e.cfg.Bot.MarketType == "futures" {
		mt = "futures"
	}
	candles, err := e.client.GetKLines(ctx, e.cfg.Bot.Market, mt, "1hour", 200)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.candles = candles
	e.mu.Unlock()
	return nil
}

// GetSignalLog returns recent signals for the dashboard.
func (e *Engine) GetSignalLog() []models.Signal {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]models.Signal, len(e.SignalLog))
	copy(out, e.SignalLog)
	return out
}

// GetOpenOrders returns a copy of open orders.
func (e *Engine) GetOpenOrders() []*models.Order {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*models.Order, 0, len(e.openOrders))
	for _, o := range e.openOrders {
		out = append(out, o)
	}
	return out
}
