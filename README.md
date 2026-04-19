# CoinEx Trade Bot

A production-grade algorithmic trading bot for [CoinEx](https://www.coinex.com/) written in Go.  
Supports **Spot** and **Futures** trading with **22 strategies**, full **Ichimoku Cloud**, a live **Order Book**, an optional **Machine Learning ensemble**, and a built-in **backtester**.

---

## Features

| Area | Details |
|---|---|
| Exchange | CoinEx API v2 (REST + WebSocket) |
| Markets | Spot & Futures |
| Strategies | 22 (see table below) |
| Indicators | EMA, SMA, RSI, MACD, Bollinger, ATR, VWAP, ADX/DMI, StochRSI, Williams %R, Parabolic SAR, Hull MA, Keltner, Donchian, Supertrend, Ichimoku Cloud |
| Order Book | Live WebSocket depth feed with incremental merge |
| ML | Feature engineering → Logistic Regression + Gradient Boosted Stumps ensemble (pure Go, no CGO) |
| Backtester | Full replay engine with Sharpe, CAGR, max drawdown, win rate, profit factor, CSV export |
| Risk Mgmt | Per-trade stop-loss / take-profit, max open orders, position sizing |
| Safety | Paper trading mode (default), graceful shutdown, rate limiter |
| Dashboard | REST API on port 8080 (`/signals`, `/orders`, `/orderbook`, `/health`) |

---

## Project Structure

```
coinex-bot/
├── cmd/
│   ├── bot/main.go           # Live/paper trading entrypoint
│   └── backtest/main.go      # Backtest CLI
├── configs/
│   └── config.yaml           # All configuration
├── internal/
│   ├── api/
│   │   ├── client.go         # CoinEx REST client (HMAC-SHA256 auth)
│   │   ├── ws_feed.go        # WebSocket feed + live order book
│   │   ├── candle_buffer.go  # Live candle ring buffer (WS kline updates)
│   │   └── rate_limiter.go   # Token-bucket rate limiter
│   ├── config/config.go      # Viper config loader
│   ├── models/models.go      # Candle, OrderBook, Order, Signal, Position
│   ├── indicators/
│   │   ├── indicators.go     # All technical indicators
│   │   └── ichimoku.go       # Ichimoku Cloud
│   ├── strategies/
│   │   ├── strategy.go       # Interface + registry
│   │   ├── all_strategies.go # 22 strategies
│   │   └── ml_strategy.go    # ML ensemble strategy
│   ├── ml/ensemble.go        # Feature extractor + LR + GBT + ensemble
│   ├── backtest/backtest.go  # Backtesting engine
│   └── engine/
│       ├── engine.go         # Core trading engine
│       └── dashboard.go      # REST monitoring dashboard
└── Makefile
```

---

## Quick Start

### 1. Prerequisites

```bash
go 1.22+
```

### 2. Clone & configure

```bash
git clone https://github.com/youruser/coinex-bot
cd coinex-bot
cp configs/config.yaml configs/config.local.yaml
```

Edit `configs/config.local.yaml`:
```yaml
coinex:
  access_id: "YOUR_ACCESS_ID"
  secret_key: "YOUR_SECRET_KEY"

bot:
  mode: "paper"        # start with paper!
  market_type: "spot"
  market: "BTCUSDT"
  strategy: "ichimoku"
  base_qty: "0.001"
```

Get your API keys from: https://www.coinex.com/en/apikey

### 3. Install dependencies

```bash
go mod tidy
```

### 4. Run in paper mode (safe default)

```bash
make run
# or
go run ./cmd/bot --config configs/config.yaml
```

### 5. Run the backtester

```bash
# All strategies on synthetic sine-wave data (no API key needed)
go run ./cmd/backtest --synthetic

# Single strategy with real data
go run ./cmd/backtest --strategy ichimoku --market BTCUSDT --limit 500

# All strategies, export CSVs
go run ./cmd/backtest --synthetic --out ./results
```

### 6. Run tests

```bash
make test
# or
go test ./... -v -race
```

---

## The 22 Strategies

| # | Name | Key | Type | Signal Logic |
|---|------|-----|------|---|
| 1 | **Ichimoku Cloud** | `ichimoku` | Trend | TK cross + price vs cloud |
| 2 | RSI Mean Reversion | `rsi_mean_revert` | Oscillator | RSI < 30 / > 70 |
| 3 | MACD Cross | `macd_cross` | Momentum | Histogram sign flip |
| 4 | Bollinger Bands | `bollinger_bands` | Mean reversion | Price outside 2σ band |
| 5 | EMA Cross | `ema_cross` | Trend | 9/21 EMA golden/death cross |
| 6 | VWAP Reversion | `vwap_revert` | Mean reversion | Price >1.5% from VWAP |
| 7 | Momentum | `momentum` | Momentum | N-bar price return > ±2% |
| 8 | Order Book Scalper | `scalp_ob` | Microstructure | Bid/ask volume imbalance |
| 9 | Grid Breakout | `grid` | Range | Price crosses grid boundary |
| 10 | ATR Trend Follow | `trend_follow` | Trend | Close-to-close move > ATR×mult |
| 11 | Breakout | `breakout` | Breakout | Price breaks N-bar HH/LL |
| 12 | Mean Revert Z | `mean_revert_z` | Statistical | Z-score > ±2 |
| 13 | Dual Thrust | `dual_thrust` | Breakout | Open ± K×range from lookback |
| 14 | Supertrend | `supertrend` | Trend | ATR-based trailing band flip |
| 15 | Williams %R | `williams_r` | Oscillator | %R < -80 / > -20 |
| 16 | Stochastic RSI | `stoch_rsi` | Oscillator | K/D cross in oversold/overbought |
| 17 | ADX / DMI | `dmi_adx` | Trend filter | ADX > 25, +DI vs -DI |
| 18 | Parabolic SAR | `parabolic_sar` | Reversal | SAR side flip |
| 19 | Hull MA | `hull_ma` | Trend | HMA slope direction |
| 20 | Keltner Channel | `keltner_channel` | Breakout | Price outside ATR bands |
| 21 | Donchian Channel | `donchian` | Breakout | Price breaks N-period channel |
| 22 | Spread / OB Arb | `arb_spread` | Microstructure | Wide spread fade |
| +1 | **ML Ensemble** | `ml_ensemble` | ML | LR + GBT on 20 features |

### Signal aggregation

All enabled strategies run in parallel every tick. Signals are combined via **confidence-weighted majority vote**:

```
longScore  = Σ confidence(s) for all Long signals
shortScore = Σ confidence(s) for all Short signals
consensus  = winner of {longScore, shortScore}
finalConf  = winner / (longScore + shortScore)
```

Only strategies with `enabled: true` in `config.yaml` participate.

---

## Machine Learning Module

Located in `internal/ml/ensemble.go`.

### Feature vector (20 features)

| Feature | Source |
|---|---|
| RSI(14) | RSI oscillator |
| MACD histogram | MACD(12,26,9) |
| Bollinger width | (upper-lower)/middle |
| EMA(9), EMA(21) | Exponential MA |
| ATR(14) | Average True Range |
| VWAP | Session VWAP |
| Price | Current close |
| BidAskImbalance | Order book depth-10 |
| Return(1), Return(5), Return(10) | N-bar log returns |
| Volume, VolumeRatio | vs 20-bar average |
| Ichimoku TK diff | Tenkan − Kijun |
| Cloud direction | +1/0/-1 above/inside/below |
| Stochastic K | StochRSI(14,3,3) |
| ADX | ADX(14) |
| Williams %R | WR(14) |
| SAR direction | 1=long, 0=short |

### Models

**Logistic Regression** — gradient descent, configurable learning rate and epochs.

**Gradient Boosted Stumps** — additive model of 50 decision stumps, each fit to pseudo-residuals (log-loss gradient). Pure Go, no external libraries.

### Training

The ensemble trains on labelled trade outcomes (win/loss). Call `ensemble.AddSample(features, profitable)` after each closed trade. `MaybeRetrain()` is called automatically on the configured interval (default 24h).

To enable:
```yaml
ml:
  enabled: true
  min_confidence: 0.65
  retrain_interval: "24h"
```

---

## Configuration Reference

```yaml
coinex:
  access_id: ""          # from coinex.com/apikey
  secret_key: ""
  base_url: "https://api.coinex.com/v2"

bot:
  mode: "paper"          # paper | live
  market_type: "spot"    # spot | futures
  market: "BTCUSDT"
  base_qty: "0.001"      # order size
  max_open_orders: 5
  stop_loss_pct: 0.015   # 1.5%
  take_profit_pct: 0.030 # 3.0%
  leverage: 10           # futures only

strategies:
  ichimoku: { enabled: true }
  rsi_mean_revert: { enabled: true, rsi_period: 14, oversold: 30, overbought: 70 }
  # ... (see configs/config.yaml for full list)

ml:
  enabled: false
  min_confidence: 0.65
  retrain_interval: "24h"

dashboard:
  enabled: true
  port: 8080
```

---

## Dashboard API

| Endpoint | Description |
|---|---|
| `GET /health` | Liveness check |
| `GET /signals` | Recent strategy signals (JSON) |
| `GET /orders` | Open orders (JSON) |
| `GET /orderbook` | Live order book snapshot (JSON) |

---

## Rate Limits

The bot respects CoinEx API v2 limits via a token-bucket rate limiter:

- Market data endpoints: **30 req/s**
- Trading endpoints: **10 req/s**

---

## ⚠️ Disclaimer

This software is for educational and research purposes. Cryptocurrency trading involves substantial risk of loss. **Always test in paper mode first.** The authors take no responsibility for financial losses.

---

## Makefile Targets

```bash
make build     # compile binary to bin/
make run       # build + run (paper mode)
make test      # run all tests with -race
make tidy      # go mod tidy
make clean     # remove bin/
```
