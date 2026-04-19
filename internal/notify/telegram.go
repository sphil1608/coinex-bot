// Package notify provides a Telegram notification client for trading events.
// Uses only the stdlib net/http – no external bot library required.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rusty/coinex-bot/internal/journal"
	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// Telegram client
// ────────────────────────────────────────────────────────────────────────────

type TelegramConfig struct {
	BotToken string `mapstructure:"bot_token"`
	ChatID   string `mapstructure:"chat_id"`
	Enabled  bool   `mapstructure:"enabled"`
}

type Telegram struct {
	cfg    TelegramConfig
	client *http.Client
	queue  chan string
	done   chan struct{}
}

func NewTelegram(cfg TelegramConfig) *Telegram {
	t := &Telegram{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
		queue:  make(chan string, 100),
		done:   make(chan struct{}),
	}
	if cfg.Enabled {
		go t.worker()
	}
	return t
}

// worker drains the queue sequentially to avoid Telegram rate limits.
func (t *Telegram) worker() {
	for {
		select {
		case msg := <-t.queue:
			_ = t.send(msg)
			time.Sleep(100 * time.Millisecond) // ~10 msg/s max
		case <-t.done:
			return
		}
	}
}

func (t *Telegram) Stop() { close(t.done) }

// Enqueue adds a message to the send queue (non-blocking, drops if full).
func (t *Telegram) Enqueue(msg string) {
	if !t.cfg.Enabled {
		return
	}
	select {
	case t.queue <- msg:
	default:
		// queue full – drop rather than block
	}
}

func (t *Telegram) send(text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.cfg.BotToken)
	body, _ := json.Marshal(map[string]string{
		"chat_id":    t.cfg.ChatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Typed notification helpers
// ────────────────────────────────────────────────────────────────────────────

func (t *Telegram) NotifySignal(sig models.Signal) {
	emoji := "🟢"
	if sig.Signal == models.SignalShort {
		emoji = "🔴"
	}
	msg := fmt.Sprintf(
		"%s <b>%s Signal</b>\nStrategy: <code>%s</code>\nMarket: <code>%s</code>\nConfidence: <code>%.1f%%</code>\nReason: %s\nTime: %s",
		emoji, sig.Signal, sig.Strategy, sig.Market,
		sig.Confidence*100, sig.Reason,
		sig.Timestamp.Format("15:04:05"),
	)
	t.Enqueue(msg)
}

func (t *Telegram) NotifyOrderFilled(order *models.Order) {
	emoji := "📗"
	if order.Side == models.SideSell {
		emoji = "📕"
	}
	msg := fmt.Sprintf(
		"%s <b>Order Filled</b>\nMarket: <code>%s</code>\nSide: <code>%s</code>\nPrice: <code>%s</code>\nQty: <code>%s</code>\nID: <code>%s</code>",
		emoji, order.Market, order.Side,
		order.Price.StringFixed(4), order.FilledAmt.StringFixed(6),
		order.ID,
	)
	t.Enqueue(msg)
}

func (t *Telegram) NotifyTradeClosed(trade journal.TradeRecord) {
	emoji := "✅"
	sign := "+"
	if trade.PnL.IsNegative() {
		emoji = "❌"
		sign = ""
	}
	msg := fmt.Sprintf(
		"%s <b>Trade Closed</b>\nMarket: <code>%s</code>\nStrategy: <code>%s</code>\nSide: <code>%s</code>\nEntry: <code>%s</code> → Exit: <code>%s</code>\nP&amp;L: <code>%s%s</code> (<code>%.2f%%</code>)\nReason: <code>%s</code>\nDuration: %s",
		emoji, trade.Market, trade.Strategy, trade.Side,
		trade.EntryPrice.StringFixed(4), trade.ExitPrice.StringFixed(4),
		sign, trade.PnL.StringFixed(4), trade.PnLPct*100,
		trade.ExitReason, trade.Duration,
	)
	t.Enqueue(msg)
}

func (t *Telegram) NotifyError(context, err string) {
	msg := fmt.Sprintf("⚠️ <b>Error</b>\nContext: <code>%s</code>\nError: <code>%s</code>", context, err)
	t.Enqueue(msg)
}

func (t *Telegram) NotifyStartup(market, mode string, strategies []string) {
	strats := ""
	for _, s := range strategies {
		strats += "  • " + s + "\n"
	}
	msg := fmt.Sprintf(
		"🤖 <b>Bot Started</b>\nMarket: <code>%s</code>\nMode: <code>%s</code>\nStrategies (%d):\n%s",
		market, mode, len(strategies), strats,
	)
	t.Enqueue(msg)
}

func (t *Telegram) NotifyShutdown() {
	t.Enqueue("🛑 <b>Bot Stopped</b>")
}

func (t *Telegram) NotifyDailySummary(stats journal.Stats, date string) {
	winLoss := fmt.Sprintf("%d / %d", stats.Winners, stats.Losers)
	msg := fmt.Sprintf(
		"📊 <b>Daily Summary — %s</b>\nTrades: <code>%d</code>  W/L: <code>%s</code>\nWin Rate: <code>%.1f%%</code>\nTotal P&amp;L: <code>%s</code>\nProfit Factor: <code>%.3f</code>",
		date, stats.TotalTrades, winLoss,
		stats.WinRate*100, stats.TotalPnL.StringFixed(4),
		stats.ProfitFactor,
	)
	t.Enqueue(msg)
}

func (t *Telegram) NotifyDrawdownAlert(drawdown float64, threshold float64) {
	msg := fmt.Sprintf(
		"🚨 <b>Drawdown Alert</b>\nCurrent DD: <code>%.2f%%</code>\nThreshold: <code>%.2f%%</code>",
		drawdown*100, threshold*100,
	)
	t.Enqueue(msg)
}
