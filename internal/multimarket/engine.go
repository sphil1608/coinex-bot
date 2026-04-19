// Package multimarket runs independent engine instances for multiple markets
// concurrently. Each market gets its own candle buffer, order book, and
// strategy evaluation loop. A shared journal and notifier aggregate all
// activity into one view.
package multimarket

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/rusty/coinex-bot/internal/api"
	"github.com/rusty/coinex-bot/internal/config"
	"github.com/rusty/coinex-bot/internal/health"
	"github.com/rusty/coinex-bot/internal/journal"
	"github.com/rusty/coinex-bot/internal/models"
	"github.com/rusty/coinex-bot/internal/notify"
	"github.com/rusty/coinex-bot/internal/position"
	"github.com/rusty/coinex-bot/internal/strategies"
)

// ────────────────────────────────────────────────────────────────────────────
// MarketConfig
// ────────────────────────────────────────────────────────────────────────────

type MarketConfig struct {
	Market        string
	MarketType    models.MarketType
	Timeframe     string  // e.g. "1hour"
	BaseQty       string
	StopLossPct   float64
	TakeProfitPct float64
	Leverage      int     // futures only
	Strategies    []string // strategy names to enable (empty = all)
}

// ────────────────────────────────────────────────────────────────────────────
// MarketWorker – one per market
// ────────────────────────────────────────────────────────────────────────────

type MarketWorker struct {
	cfg        MarketConfig
	client     *api.Client
	candleBuf  *api.LiveCandleBuffer
	lob        *api.LiveOrderBook
	wsFeed     *api.WSFeed
	strats     []strategies.Strategy
	posMgr     *position.Manager
	journal    *journal.Journal
	notifier   *notify.Telegram
	monitor    *health.Monitor
	paper      bool

	mu         sync.RWMutex
	signalLog  []models.Signal
	stopCh     chan struct{}
}

func newMarketWorker(
	mc MarketConfig,
	client *api.Client,
	wsURL string,
	posMgr *position.Manager,
	jrn *journal.Journal,
	notifier *notify.Telegram,
	monitor *health.Monitor,
	paper bool,
	enabledStrats []string,
) *MarketWorker {

	// Filter strategies
	var strats []strategies.Strategy
	if len(enabledStrats) == 0 || (len(enabledStrats) == 1 && enabledStrats[0] == "all") {
		strats = strategies.All()
	} else {
		for _, name := range enabledStrats {
			if s, ok := strategies.Get(name); ok {
				strats = append(strats, s)
			}
		}
	}

	candleBuf := api.NewLiveCandleBuffer(mc.Market, mc.Timeframe, 300)
	lob := api.NewLiveOrderBook(mc.Market)
	feed := api.NewWSFeed(wsURL)
	feed.AddHandler(lob.Handle)
	feed.AddHandler(candleBuf.Handle)

	mon := monitor
	if mon == nil {
		mon = health.NewMonitor(health.DefaultConfig(), func(msg string) {
			log.Warn().Str("market", mc.Market).Str("alert", msg).Msg("health alert")
		})
	}

	return &MarketWorker{
		cfg:       mc,
		client:    client,
		candleBuf: candleBuf,
		lob:       lob,
		wsFeed:    feed,
		strats:    strats,
		posMgr:    posMgr,
		journal:   jrn,
		notifier:  notifier,
		monitor:   mon,
		paper:     paper,
		stopCh:    make(chan struct{}),
	}
}

func (w *MarketWorker) Start(ctx context.Context) error {
	// Connect WS
	if err := w.wsFeed.Connect(ctx); err != nil {
		return fmt.Errorf("[%s] ws connect: %w", w.cfg.Market, err)
	}
	_ = w.wsFeed.SubscribeDepth(w.cfg.Market, 20)
	_ = w.wsFeed.SubscribeKLine(w.cfg.Market, w.cfg.Timeframe)

	// Seed candles from REST
	mt := string(w.cfg.MarketType)
	candles, err := w.client.GetKLines(ctx, w.cfg.Market, mt, w.cfg.Timeframe, 300)
	if err != nil {
		log.Warn().Err(err).Str("market", w.cfg.Market).Msg("candle seed failed, will rely on WS")
	} else {
		w.candleBuf.Seed(candles)
	}

	log.Info().
		Str("market", w.cfg.Market).
		Int("strategies", len(w.strats)).
		Msg("market worker started")

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.stopCh:
			return nil
		case <-ticker.C:
			if err := w.tick(ctx); err != nil {
				w.monitor.RecordError(err)
				log.Error().Err(err).Str("market", w.cfg.Market).Msg("tick error")
			} else {
				w.monitor.RecordTick()
			}
		}
	}
}

func (w *MarketWorker) Stop() { close(w.stopCh) }

func (w *MarketWorker) tick(ctx context.Context) error {
	candles := w.candleBuf.Snapshot()
	if len(candles) < 60 {
		return nil
	}
	ob := w.lob.Snapshot()

	// Check circuit breaker
	if !w.monitor.Circuit.Allow() {
		log.Warn().Str("market", w.cfg.Market).Msg("circuit open, skipping tick")
		return nil
	}

	// Check SL/TP on open positions
	price := ob.MidPrice()
	if !price.IsZero() {
		w.posMgr.UpdatePrices(w.cfg.Market, price)
		slHit, tpHit := w.posMgr.TriggeredExits()
		for _, p := range slHit {
			if p.Market == w.cfg.Market {
				w.closePosition(ctx, p, "sl")
			}
		}
		for _, p := range tpHit {
			if p.Market == w.cfg.Market {
				w.closePosition(ctx, p, "tp")
			}
		}
	}

	// Gather signals
	signals := w.gatherSignals(ctx, candles, ob)
	if len(signals) == 0 {
		return nil
	}
	consensus := aggregateSignals(signals)
	if consensus.Signal == models.SignalFlat {
		return nil
	}

	w.mu.Lock()
	w.signalLog = append(w.signalLog, consensus)
	if len(w.signalLog) > 500 {
		w.signalLog = w.signalLog[len(w.signalLog)-500:]
	}
	w.mu.Unlock()

	w.notifier.NotifySignal(consensus)

	return w.executeSignal(ctx, consensus, ob)
}

func (w *MarketWorker) gatherSignals(ctx context.Context, candles []models.Candle, ob models.OrderBook) []models.Signal {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var out []models.Signal

	for _, s := range w.strats {
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

func aggregateSignals(signals []models.Signal) models.Signal {
	var longConf, shortConf float64
	for _, s := range signals {
		if s.Signal == models.SignalLong {
			longConf += s.Confidence
		} else if s.Signal == models.SignalShort {
			shortConf += s.Confidence
		}
	}
	if longConf == 0 && shortConf == 0 {
		return models.Signal{Signal: models.SignalFlat}
	}
	if longConf > shortConf {
		conf := longConf / (longConf + shortConf)
		return models.Signal{Signal: models.SignalLong, Confidence: conf,
			Strategy: "consensus", Timestamp: time.Now(),
			Reason: fmt.Sprintf("%.0f%% long, %d strategies", conf*100, len(signals))}
	}
	conf := shortConf / (longConf + shortConf)
	return models.Signal{Signal: models.SignalShort, Confidence: conf,
		Strategy: "consensus", Timestamp: time.Now(),
		Reason: fmt.Sprintf("%.0f%% short, %d strategies", conf*100, len(signals))}
}

func (w *MarketWorker) executeSignal(ctx context.Context, sig models.Signal, ob models.OrderBook) error {
	side := models.SideBuy
	if sig.Signal == models.SignalShort {
		side = models.SideSell
	}

	ok, reason := w.posMgr.CanOpen(w.cfg.Market, side)
	if !ok {
		log.Debug().Str("market", w.cfg.Market).Str("reason", reason).Msg("cannot open position")
		return nil
	}

	price := ob.MidPrice()
	if price.IsZero() {
		return nil
	}

	// Place order
	req := api.PlaceOrderReq{
		Market:     w.cfg.Market,
		MarketType: string(w.cfg.MarketType),
		Side:       string(side),
		Type:       "limit",
		Amount:     w.cfg.BaseQty,
		Price:      price.StringFixed(4),
	}

	var (
		order *models.Order
		err   error
	)
	if w.cfg.MarketType == models.MarketFutures {
		order, err = w.client.PlaceFuturesOrder(ctx, req)
	} else {
		order, err = w.client.PlaceSpotOrder(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("place order: %w", err)
	}

	w.notifier.NotifyOrderFilled(order)

	// Register with position manager
	slPrice := price.Mul(models.OneMinusPct(w.cfg.StopLossPct, side))
	tpPrice := price.Mul(models.OnePlusPct(w.cfg.TakeProfitPct, side))

	_ = w.posMgr.Open(&position.OpenPosition{
		ID:           order.ID,
		Market:       w.cfg.Market,
		MarketType:   w.cfg.MarketType,
		Side:         side,
		Strategy:     sig.Strategy,
		EntryPrice:   price,
		Quantity:     order.Amount,
		StopLoss:     slPrice,
		TakeProfit:   tpPrice,
		EntryOrderID: order.ID,
		OpenedAt:     time.Now(),
	})

	return nil
}

func (w *MarketWorker) closePosition(ctx context.Context, p *position.OpenPosition, reason string) {
	closed := w.posMgr.Close(p.Market, p.Side)
	if closed == nil {
		return
	}
	log.Info().Str("market", p.Market).Str("reason", reason).Msg("closing position")

	// Cancel the open order (best effort)
	_ = w.client.CancelSpotOrder(ctx, p.Market, p.EntryOrderID)
}

func (w *MarketWorker) Signals() []models.Signal {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]models.Signal, len(w.signalLog))
	copy(out, w.signalLog)
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// MultiEngine – manages multiple MarketWorkers
// ────────────────────────────────────────────────────────────────────────────

type MultiEngine struct {
	workers []*MarketWorker
	journal *journal.Journal
}

func NewMultiEngine(
	markets []MarketConfig,
	coinexCfg config.CoinExConfig,
	paper bool,
	journalPath string,
	telegramCfg notify.TelegramConfig,
) (*MultiEngine, error) {

	client := api.NewClient(coinexCfg.AccessID, coinexCfg.SecretKey, coinexCfg.BaseURL, paper)
	jrn, err := journal.Open(journalPath)
	if err != nil {
		return nil, fmt.Errorf("journal open: %w", err)
	}

	notifier := notify.NewTelegram(telegramCfg)
	posMgr := position.NewManager(20, 0.5) // max 20 positions, 50% capital exposure
	monitor := health.NewMonitor(health.DefaultConfig(), func(msg string) {
		notifier.NotifyError("health", msg)
	})

	me := &MultiEngine{journal: jrn}

	for _, mc := range markets {
		wsURL := coinexCfg.WSSpotURL
		if mc.MarketType == models.MarketFutures {
			wsURL = coinexCfg.WSFuturesURL
		}
		w := newMarketWorker(mc, client, wsURL, posMgr, jrn, notifier, monitor, paper, mc.Strategies)
		me.workers = append(me.workers, w)
	}

	return me, nil
}

func (me *MultiEngine) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(me.workers))

	for _, w := range me.workers {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Start(ctx); err != nil && err != context.Canceled {
				errCh <- fmt.Errorf("[%s]: %w", w.cfg.Market, err)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (me *MultiEngine) Stop() {
	for _, w := range me.workers {
		w.Stop()
	}
}

func (me *MultiEngine) Journal() *journal.Journal { return me.journal }
