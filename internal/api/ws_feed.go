package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// WebSocket Feed
// ────────────────────────────────────────────────────────────────────────────

type FeedHandler func(event string, data json.RawMessage)

type WSFeed struct {
	url        string
	conn       *websocket.Conn
	mu         sync.Mutex
	handlers   []FeedHandler
	done       chan struct{}
	pingTicker *time.Ticker
}

func NewWSFeed(wsURL string) *WSFeed {
	return &WSFeed{url: wsURL, done: make(chan struct{})}
}

func (f *WSFeed) AddHandler(h FeedHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, h)
}

func (f *WSFeed) Connect(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, f.url, nil)
	if err != nil {
		return fmt.Errorf("ws dial %s: %w", f.url, err)
	}
	f.conn = conn
	f.pingTicker = time.NewTicker(20 * time.Second)

	go f.readLoop(ctx)
	go f.pingLoop(ctx)
	return nil
}

func (f *WSFeed) Subscribe(method string, params interface{}) error {
	msg := map[string]interface{}{
		"id":     time.Now().UnixNano(),
		"method": method,
		"params": params,
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.WriteJSON(msg)
}

// SubscribeDepth subscribes to order book depth updates
func (f *WSFeed) SubscribeDepth(market string, limit int) error {
	return f.Subscribe("depth.subscribe", map[string]interface{}{
		"market_list": []interface{}{
			[]interface{}{market, limit, "0", true},
		},
	})
}

// SubscribeTrades subscribes to live trade stream
func (f *WSFeed) SubscribeTrades(market string) error {
	return f.Subscribe("deals.subscribe", []string{market})
}

// SubscribeKLine subscribes to kline (OHLCV) stream
func (f *WSFeed) SubscribeKLine(market, period string) error {
	return f.Subscribe("kline.subscribe", map[string]interface{}{
		"market": market,
		"period": period,
	})
}

func (f *WSFeed) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_, msg, err := f.conn.ReadMessage()
		if err != nil {
			log.Error().Err(err).Msg("ws read error, reconnecting in 3s")
			time.Sleep(3 * time.Second)
			if err2 := f.Connect(ctx); err2 != nil {
				log.Error().Err(err2).Msg("ws reconnect failed")
			}
			return
		}

		var envelope struct {
			Method string          `json:"method"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			continue
		}

		f.mu.Lock()
		for _, h := range f.handlers {
			h(envelope.Method, envelope.Data)
		}
		f.mu.Unlock()
	}
}

func (f *WSFeed) pingLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.pingTicker.C:
			f.mu.Lock()
			f.conn.WriteJSON(map[string]interface{}{
				"id": time.Now().UnixNano(), "method": "server.ping", "params": map[string]interface{}{},
			})
			f.mu.Unlock()
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Live Order Book (maintained via WS depth events)
// ────────────────────────────────────────────────────────────────────────────

type LiveOrderBook struct {
	mu   sync.RWMutex
	book models.OrderBook
}

func NewLiveOrderBook(market string) *LiveOrderBook {
	return &LiveOrderBook{book: models.OrderBook{Market: market}}
}

func (lb *LiveOrderBook) Handle(event string, data json.RawMessage) {
	if event != "depth.update" {
		return
	}
	var raw struct {
		Market string `json:"market"`
		Depth  struct {
			Asks [][]string `json:"asks"`
			Bids [][]string `json:"bids"`
		} `json:"depth"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Full snapshot replaces; incremental merges (price qty="0" = remove)
	lb.book.Asks = mergeLevels(lb.book.Asks, raw.Depth.Asks, true)
	lb.book.Bids = mergeLevels(lb.book.Bids, raw.Depth.Bids, false)
	lb.book.UpdatedAt = time.Now()
}

func (lb *LiveOrderBook) Snapshot() models.OrderBook {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	snap := lb.book
	asks := make([]models.Level, len(lb.book.Asks))
	bids := make([]models.Level, len(lb.book.Bids))
	copy(asks, lb.book.Asks)
	copy(bids, lb.book.Bids)
	snap.Asks = asks
	snap.Bids = bids
	return snap
}

func mergeLevels(existing []models.Level, updates [][]string, ascending bool) []models.Level {
	m := make(map[string]decimal.Decimal, len(existing))
	for _, l := range existing {
		m[l.Price.String()] = l.Quantity
	}
	for _, u := range updates {
		if len(u) < 2 {
			continue
		}
		qty := decimal.RequireFromString(u[1])
		if qty.IsZero() {
			delete(m, u[0])
		} else {
			m[u[0]] = qty
		}
	}
	out := make([]models.Level, 0, len(m))
	for p, q := range m {
		out = append(out, models.Level{Price: decimal.RequireFromString(p), Quantity: q})
	}
	// sort
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			cmp := out[i].Price.Cmp(out[j].Price)
			if (ascending && cmp > 0) || (!ascending && cmp < 0) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
