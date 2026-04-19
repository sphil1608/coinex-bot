package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/models"
)

const defaultBaseURL = "https://api.coinex.com/v2"

// ────────────────────────────────────────────────────────────────────────────
// Client
// ────────────────────────────────────────────────────────────────────────────

type Client struct {
	accessID  string
	secretKey string
	baseURL   string
	http      *http.Client
	paper     bool // paper-trading mode – read-only
}

func NewClient(accessID, secretKey, baseURL string, paper bool) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		accessID:  accessID,
		secretKey: secretKey,
		baseURL:   baseURL,
		http:      &http.Client{Timeout: 10 * time.Second},
		paper:     paper,
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Auth helpers
// ────────────────────────────────────────────────────────────────────────────

func (c *Client) sign(method, path, body string) (string, string) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	prepared := method + path + body + ts
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(prepared))
	sig := hex.EncodeToString(mac.Sum(nil))
	return sig, ts
}

func (c *Client) do(ctx context.Context, method, endpoint string, params url.Values, body interface{}) ([]byte, error) {
	var bodyStr string
	var bodyReader io.Reader

	if body != nil {
		b, _ := json.Marshal(body)
		bodyStr = string(b)
		bodyReader = bytes.NewReader(b)
	}

	path := "/v2" + endpoint
	if params != nil && len(params) > 0 {
		path += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, bodyReader)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = params.Encode()

	sig, ts := c.sign(method, path, bodyStr)
	req.Header.Set("X-COINEX-KEY", c.accessID)
	req.Header.Set("X-COINEX-SIGN", sig)
	req.Header.Set("X-COINEX-TIMESTAMP", ts)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("coinex error %d: %s", envelope.Code, envelope.Message)
	}
	return envelope.Data, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Market Data – Spot & Futures
// ────────────────────────────────────────────────────────────────────────────

type KLineResp struct {
	CreatedAt int64    `json:"created_at"`
	Open      string   `json:"open"`
	Close     string   `json:"close"`
	High      string   `json:"high"`
	Low       string   `json:"low"`
	Volume    string   `json:"volume"`
}

// GetKLines fetches OHLCV candles.
// marketType: "spot" | "futures"
// period: "1min" | "5min" | "15min" | "30min" | "1hour" | "4hour" | "1day"
func (c *Client) GetKLines(ctx context.Context, market, marketType, period string, limit int) ([]models.Candle, error) {
	endpoint := fmt.Sprintf("/%s/kline", strings.ToLower(marketType))
	p := url.Values{}
	p.Set("market", market)
	p.Set("period", period)
	p.Set("limit", strconv.Itoa(limit))

	data, err := c.do(ctx, "GET", endpoint, p, nil)
	if err != nil {
		return nil, err
	}

	var raw []KLineResp
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	candles := make([]models.Candle, len(raw))
	for i, r := range raw {
		candles[i] = models.Candle{
			OpenTime:  time.UnixMilli(r.CreatedAt),
			Open:      decimal.RequireFromString(r.Open),
			High:      decimal.RequireFromString(r.High),
			Low:       decimal.RequireFromString(r.Low),
			Close:     decimal.RequireFromString(r.Close),
			Volume:    decimal.RequireFromString(r.Volume),
			Timeframe: period,
		}
	}
	return candles, nil
}

// GetOrderBook fetches the current order book.
func (c *Client) GetOrderBook(ctx context.Context, market, marketType string, depth int) (*models.OrderBook, error) {
	endpoint := fmt.Sprintf("/%s/depth", strings.ToLower(marketType))
	p := url.Values{}
	p.Set("market", market)
	p.Set("limit", strconv.Itoa(depth))
	p.Set("merge", "0")

	data, err := c.do(ctx, "GET", endpoint, p, nil)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Depth struct {
			Asks [][]string `json:"asks"`
			Bids [][]string `json:"bids"`
		} `json:"depth"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	ob := &models.OrderBook{Market: market, UpdatedAt: time.Now()}
	for _, a := range raw.Depth.Asks {
		if len(a) < 2 {
			continue
		}
		ob.Asks = append(ob.Asks, models.Level{
			Price:    decimal.RequireFromString(a[0]),
			Quantity: decimal.RequireFromString(a[1]),
		})
	}
	for _, b := range raw.Depth.Bids {
		if len(b) < 2 {
			continue
		}
		ob.Bids = append(ob.Bids, models.Level{
			Price:    decimal.RequireFromString(b[0]),
			Quantity: decimal.RequireFromString(b[1]),
		})
	}
	return ob, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Trading – Spot
// ────────────────────────────────────────────────────────────────────────────

type PlaceOrderReq struct {
	Market     string `json:"market"`
	MarketType string `json:"market_type"`
	Side       string `json:"side"`
	Type       string `json:"type"`
	Amount     string `json:"amount"`
	Price      string `json:"price,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
}

func (c *Client) PlaceSpotOrder(ctx context.Context, req PlaceOrderReq) (*models.Order, error) {
	if c.paper {
		log.Info().Str("market", req.Market).Str("side", req.Side).Str("price", req.Price).Msg("[PAPER] place spot order")
		return &models.Order{ID: "paper-" + strconv.FormatInt(time.Now().UnixNano(), 36), Market: req.Market}, nil
	}
	data, err := c.do(ctx, "POST", "/spot/order", nil, req)
	if err != nil {
		return nil, err
	}
	return parseOrder(data)
}

func (c *Client) CancelSpotOrder(ctx context.Context, market, orderID string) error {
	if c.paper {
		log.Info().Str("order_id", orderID).Msg("[PAPER] cancel spot order")
		return nil
	}
	body := map[string]string{"market": market, "market_type": "SPOT", "order_id": orderID}
	_, err := c.do(ctx, "DELETE", "/spot/order", nil, body)
	return err
}

// ────────────────────────────────────────────────────────────────────────────
// Trading – Futures
// ────────────────────────────────────────────────────────────────────────────

func (c *Client) PlaceFuturesOrder(ctx context.Context, req PlaceOrderReq) (*models.Order, error) {
	req.MarketType = "FUTURES"
	if c.paper {
		log.Info().Str("market", req.Market).Str("side", req.Side).Msg("[PAPER] place futures order")
		return &models.Order{ID: "paper-" + strconv.FormatInt(time.Now().UnixNano(), 36), Market: req.Market}, nil
	}
	data, err := c.do(ctx, "POST", "/futures/order", nil, req)
	if err != nil {
		return nil, err
	}
	return parseOrder(data)
}

func (c *Client) SetFuturesLeverage(ctx context.Context, market string, leverage int, side string) error {
	if c.paper {
		return nil
	}
	body := map[string]interface{}{"market": market, "market_type": "FUTURES", "leverage": leverage, "position_type": side}
	_, err := c.do(ctx, "POST", "/futures/adjust-position-leverage", nil, body)
	return err
}

func (c *Client) GetPositions(ctx context.Context, market string) ([]models.Position, error) {
	p := url.Values{}
	p.Set("market", market)
	p.Set("market_type", "FUTURES")
	data, err := c.do(ctx, "GET", "/futures/pending-position", p, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Market     string `json:"market"`
		Side       string `json:"side"`
		EntryPrice string `json:"avg_entry_price"`
		Amount     string `json:"amount"`
		Leverage   int    `json:"leverage"`
		Margin     string `json:"margin"`
		UnrealPL   string `json:"unrealized_pnl"`
		CreatedAt  int64  `json:"created_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]models.Position, len(raw))
	for i, r := range raw {
		out[i] = models.Position{
			Market:       r.Market,
			Side:         models.OrderSide(r.Side),
			EntryPrice:   decimal.RequireFromString(r.EntryPrice),
			Amount:       decimal.RequireFromString(r.Amount),
			Leverage:     r.Leverage,
			Margin:       decimal.RequireFromString(r.Margin),
			UnrealizedPL: decimal.RequireFromString(r.UnrealPL),
			OpenedAt:     time.UnixMilli(r.CreatedAt),
		}
	}
	return out, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Account Balance
// ────────────────────────────────────────────────────────────────────────────

type Balance struct {
	Asset     string
	Available decimal.Decimal
	Frozen    decimal.Decimal
}

func (c *Client) GetSpotBalances(ctx context.Context) (map[string]Balance, error) {
	data, err := c.do(ctx, "GET", "/assets/spot/balance", nil, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Ccy       string `json:"ccy"`
		Available string `json:"available"`
		Frozen    string `json:"frozen"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]Balance, len(raw))
	for _, r := range raw {
		out[r.Ccy] = Balance{
			Asset:     r.Ccy,
			Available: decimal.RequireFromString(r.Available),
			Frozen:    decimal.RequireFromString(r.Frozen),
		}
	}
	return out, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func parseOrder(data []byte) (*models.Order, error) {
	var r struct {
		OrderID   json.Number `json:"order_id"`
		Market    string      `json:"market"`
		Side      string      `json:"side"`
		Type      string      `json:"type"`
		Price     string      `json:"price"`
		Amount    string      `json:"amount"`
		FilledAmt string      `json:"filled_amount"`
		Status    string      `json:"status"`
		CreatedAt int64       `json:"created_at"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	price := decimal.Zero
	if r.Price != "" {
		price = decimal.RequireFromString(r.Price)
	}
	return &models.Order{
		ID:        r.OrderID.String(),
		Market:    r.Market,
		Side:      models.OrderSide(r.Side),
		Type:      models.OrderType(r.Type),
		Price:     price,
		Amount:    decimal.RequireFromString(r.Amount),
		FilledAmt: decimal.RequireFromString(r.FilledAmt),
		Status:    r.Status,
		CreatedAt: time.UnixMilli(r.CreatedAt),
	}, nil
}
