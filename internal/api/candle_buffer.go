package api

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"github.com/rs/zerolog/log"

	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// LiveCandleBuffer
// Maintains a rolling buffer of candles updated via WebSocket kline events.
// ────────────────────────────────────────────────────────────────────────────

// LiveCandleBuffer keeps an ordered ring of OHLCV candles, updated in real
// time from "kline.update" WebSocket events. The buffer caps at MaxLen entries
// (oldest evicted first). Safe for concurrent use.
type LiveCandleBuffer struct {
	mu        sync.RWMutex
	candles   []models.Candle
	maxLen    int
	market    string
	timeframe string
}

// NewLiveCandleBuffer creates a buffer for a given market and timeframe.
// maxLen is the maximum number of completed candles to keep.
func NewLiveCandleBuffer(market, timeframe string, maxLen int) *LiveCandleBuffer {
	return &LiveCandleBuffer{
		market:    market,
		timeframe: timeframe,
		maxLen:    maxLen,
	}
}

// Handle satisfies FeedHandler – wired into WSFeed.AddHandler.
func (b *LiveCandleBuffer) Handle(event string, data json.RawMessage) {
	if event != "kline.update" {
		return
	}

	// CoinEx kline.update payload shape:
	// { "market": "BTCUSDT", "kline": [[ts, open, close, high, low, volume, ...]] }
	var raw struct {
		Market string          `json:"market"`
		Kline  [][]interface{} `json:"kline"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Debug().Err(err).Msg("kline.update unmarshal failed")
		return
	}
	if raw.Market != b.market {
		return
	}

	for _, bar := range raw.Kline {
		c, ok := parseKlineBar(bar, b.timeframe)
		if !ok {
			continue
		}
		b.upsert(c)
	}
}

// upsert inserts or updates the candle at its timestamp position.
func (b *LiveCandleBuffer) upsert(c models.Candle) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Update existing candle (same open-time = in-progress bar update)
	for i := len(b.candles) - 1; i >= 0; i-- {
		if b.candles[i].OpenTime.Equal(c.OpenTime) {
			b.candles[i] = c
			return
		}
	}

	// Append new candle
	b.candles = append(b.candles, c)

	// Evict oldest if over capacity
	if len(b.candles) > b.maxLen {
		b.candles = b.candles[len(b.candles)-b.maxLen:]
	}
}

// Snapshot returns a copy of the current candle slice (oldest → newest).
func (b *LiveCandleBuffer) Snapshot() []models.Candle {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]models.Candle, len(b.candles))
	copy(out, b.candles)
	return out
}

// Len returns the current number of candles in the buffer.
func (b *LiveCandleBuffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.candles)
}

// Seed pre-populates the buffer with historical candles (e.g. on startup).
func (b *LiveCandleBuffer) Seed(candles []models.Candle) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(candles) > b.maxLen {
		candles = candles[len(candles)-b.maxLen:]
	}
	b.candles = make([]models.Candle, len(candles))
	copy(b.candles, candles)
}

// ────────────────────────────────────────────────────────────────────────────
// Parsing helper
// ────────────────────────────────────────────────────────────────────────────

// parseKlineBar converts a raw kline bar (array of interface{}) to a Candle.
// CoinEx kline bar format: [timestamp_ms, open, close, high, low, volume, ...]
func parseKlineBar(bar []interface{}, timeframe string) (models.Candle, bool) {
	if len(bar) < 6 {
		return models.Candle{}, false
	}

	toFloat := func(v interface{}) (float64, bool) {
		switch t := v.(type) {
		case float64:
			return t, true
		case string:
			d, err := decimal.NewFromString(t)
			if err != nil {
				return 0, false
			}
			f, _ := d.Float64()
			return f, true
		case json.Number:
			f, err := t.Float64()
			return f, err == nil
		}
		return 0, false
	}

	tsF, ok := toFloat(bar[0])
	if !ok {
		return models.Candle{}, false
	}
	openF, ok := toFloat(bar[1])
	if !ok {
		return models.Candle{}, false
	}
	closeF, ok := toFloat(bar[2])
	if !ok {
		return models.Candle{}, false
	}
	highF, ok := toFloat(bar[3])
	if !ok {
		return models.Candle{}, false
	}
	lowF, ok := toFloat(bar[4])
	if !ok {
		return models.Candle{}, false
	}
	volF, ok := toFloat(bar[5])
	if !ok {
		return models.Candle{}, false
	}

	return models.Candle{
		OpenTime:  time.UnixMilli(int64(tsF)),
		Open:      decimal.NewFromFloat(openF),
		High:      decimal.NewFromFloat(highF),
		Low:       decimal.NewFromFloat(lowF),
		Close:     decimal.NewFromFloat(closeF),
		Volume:    decimal.NewFromFloat(volF),
		Timeframe: timeframe,
	}, true
}
