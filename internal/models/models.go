package models

import (
	"time"

	"github.com/shopspring/decimal"
)

// ────────────────────────────────────────────────────────────────────────────
// Market Data
// ────────────────────────────────────────────────────────────────────────────

type Candle struct {
	OpenTime  time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
	Timeframe string
}

type Tick struct {
	Market    string
	Price     decimal.Decimal
	Amount    decimal.Decimal
	Side      string // buy | sell
	Timestamp time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Order Book
// ────────────────────────────────────────────────────────────────────────────

type Level struct {
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

type OrderBook struct {
	Market    string
	Bids      []Level // sorted descending
	Asks      []Level // sorted ascending
	UpdatedAt time.Time
}

func (ob *OrderBook) BestBid() decimal.Decimal {
	if len(ob.Bids) > 0 {
		return ob.Bids[0].Price
	}
	return decimal.Zero
}

func (ob *OrderBook) BestAsk() decimal.Decimal {
	if len(ob.Asks) > 0 {
		return ob.Asks[0].Price
	}
	return decimal.Zero
}

func (ob *OrderBook) MidPrice() decimal.Decimal {
	bid := ob.BestBid()
	ask := ob.BestAsk()
	if bid.IsZero() || ask.IsZero() {
		return decimal.Zero
	}
	return bid.Add(ask).Div(decimal.NewFromInt(2))
}

func (ob *OrderBook) Spread() decimal.Decimal {
	if ob.BestBid().IsZero() || ob.BestAsk().IsZero() {
		return decimal.Zero
	}
	return ob.BestAsk().Sub(ob.BestBid())
}

// BidAskImbalance returns value in [-1, 1]; positive = bid pressure
func (ob *OrderBook) BidAskImbalance(depth int) decimal.Decimal {
	var bidVol, askVol decimal.Decimal
	for i := 0; i < depth && i < len(ob.Bids); i++ {
		bidVol = bidVol.Add(ob.Bids[i].Quantity)
	}
	for i := 0; i < depth && i < len(ob.Asks); i++ {
		askVol = askVol.Add(ob.Asks[i].Quantity)
	}
	total := bidVol.Add(askVol)
	if total.IsZero() {
		return decimal.Zero
	}
	return bidVol.Sub(askVol).Div(total)
}

// ────────────────────────────────────────────────────────────────────────────
// Orders & Positions
// ────────────────────────────────────────────────────────────────────────────

type OrderSide string

const (
	SideBuy  OrderSide = "buy"
	SideSell OrderSide = "sell"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "limit"
	OrderTypeMarket OrderType = "market"
)

type MarketType string

const (
	MarketSpot    MarketType = "SPOT"
	MarketFutures MarketType = "FUTURES"
)

type Order struct {
	ID         string
	ClientID   string
	Market     string
	MarketType MarketType
	Side       OrderSide
	Type       OrderType
	Price      decimal.Decimal
	Amount     decimal.Decimal
	FilledAmt  decimal.Decimal
	Status     string
	CreatedAt  time.Time
}

type Position struct {
	Market       string
	Side         OrderSide
	EntryPrice   decimal.Decimal
	Amount       decimal.Decimal
	Leverage     int
	Margin       decimal.Decimal
	UnrealizedPL decimal.Decimal
	OpenedAt     time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Strategy Signal
// ────────────────────────────────────────────────────────────────────────────

type SignalType string

const (
	SignalLong  SignalType = "LONG"
	SignalShort SignalType = "SHORT"
	SignalFlat  SignalType = "FLAT"
)

type Signal struct {
	Strategy   string
	Market     string
	Signal     SignalType
	Confidence float64 // 0.0 – 1.0
	Reason     string
	Timestamp  time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// Price helpers for SL/TP calculation
// ────────────────────────────────────────────────────────────────────────────

// OneMinusPct returns a multiplier for stop-loss: for a long buy it's (1-pct),
// for a short sell it's (1+pct).
func OneMinusPct(pct float64, side OrderSide) decimal.Decimal {
	if side == SideBuy {
		return decimal.NewFromFloat(1 - pct)
	}
	return decimal.NewFromFloat(1 + pct)
}

// OnePlusPct returns a multiplier for take-profit: for a long buy it's (1+pct),
// for a short sell it's (1-pct).
func OnePlusPct(pct float64, side OrderSide) decimal.Decimal {
	if side == SideBuy {
		return decimal.NewFromFloat(1 + pct)
	}
	return decimal.NewFromFloat(1 - pct)
}
