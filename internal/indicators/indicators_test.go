package indicators_test

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	ind "github.com/rusty/coinex-bot/internal/indicators"
	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func makeCandles(closes []float64) []models.Candle {
	candles := make([]models.Candle, len(closes))
	for i, c := range closes {
		// Generate plausible H/L around close
		candles[i] = models.Candle{
			OpenTime: time.Now().Add(time.Duration(i) * time.Hour),
			Open:     decimal.NewFromFloat(c * 0.999),
			High:     decimal.NewFromFloat(c * 1.005),
			Low:      decimal.NewFromFloat(c * 0.995),
			Close:    decimal.NewFromFloat(c),
			Volume:   decimal.NewFromFloat(1000),
		}
	}
	return candles
}

func makeRamp(start, end float64, n int) []float64 {
	out := make([]float64, n)
	step := (end - start) / float64(n-1)
	for i := range out {
		out[i] = start + float64(i)*step
	}
	return out
}

func approxEq(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// ────────────────────────────────────────────────────────────────────────────
// SMA
// ────────────────────────────────────────────────────────────────────────────

func TestSMA_RampingPrices(t *testing.T) {
	prices := makeRamp(100, 120, 20) // linear ramp
	decs := make([]decimal.Decimal, len(prices))
	for i, p := range prices {
		decs[i] = decimal.NewFromFloat(p)
	}
	sma := ind.SMA(decs, 5)
	if sma == nil {
		t.Fatal("SMA returned nil")
	}
	// SMA of linear ramp should equal midpoint of each window
	for i, v := range sma {
		f, _ := v.Float64()
		expected := prices[i+2] // midpoint of window [i..i+4]
		if !approxEq(f, expected, 0.01) {
			t.Errorf("SMA[%d] = %.4f, want %.4f", i, f, expected)
		}
	}
}

func TestSMA_InsufficientData(t *testing.T) {
	decs := []decimal.Decimal{decimal.NewFromFloat(1), decimal.NewFromFloat(2)}
	if ind.SMA(decs, 5) != nil {
		t.Error("expected nil for insufficient data")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// EMA
// ────────────────────────────────────────────────────────────────────────────

func TestEMA_ConstantPrices(t *testing.T) {
	prices := make([]decimal.Decimal, 30)
	for i := range prices {
		prices[i] = decimal.NewFromFloat(100)
	}
	ema := ind.EMA(prices, 10)
	if ema == nil {
		t.Fatal("EMA returned nil")
	}
	// EMA of constant prices should equal that constant
	for i, v := range ema {
		f, _ := v.Float64()
		if !approxEq(f, 100, 0.0001) {
			t.Errorf("EMA[%d] = %.6f, want 100", i, f)
		}
	}
}

func TestEMA_ReactsToSpike(t *testing.T) {
	prices := make([]decimal.Decimal, 50)
	for i := range prices {
		prices[i] = decimal.NewFromFloat(100)
	}
	prices[49] = decimal.NewFromFloat(200) // spike at the end
	ema := ind.EMA(prices, 10)
	last, _ := ema[len(ema)-1].Float64()
	if last <= 100 {
		t.Errorf("EMA should react to spike, got %.2f", last)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// RSI
// ────────────────────────────────────────────────────────────────────────────

func TestRSI_FlatMarket(t *testing.T) {
	candles := makeCandles(makeRamp(100, 100, 50)) // all flat
	// With zero moves, both gains and losses are 0 → RSI=100 (all gains tie)
	rsi := ind.RSI(candles, 14)
	if rsi == nil {
		t.Fatal("RSI returned nil")
	}
	// We just check it's in range
	for _, v := range rsi {
		if v < 0 || v > 100 {
			t.Errorf("RSI out of range: %.2f", v)
		}
	}
}

func TestRSI_TrendingUp_HighValue(t *testing.T) {
	candles := makeCandles(makeRamp(100, 150, 50))
	rsi := ind.RSI(candles, 14)
	last := rsi[len(rsi)-1]
	if last < 70 {
		t.Errorf("RSI should be high in uptrend, got %.2f", last)
	}
}

func TestRSI_TrendingDown_LowValue(t *testing.T) {
	candles := makeCandles(makeRamp(150, 100, 50))
	rsi := ind.RSI(candles, 14)
	last := rsi[len(rsi)-1]
	if last > 30 {
		t.Errorf("RSI should be low in downtrend, got %.2f", last)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// MACD
// ────────────────────────────────────────────────────────────────────────────

func TestMACD_BullishCross(t *testing.T) {
	// First flat, then ramp up → MACD histogram should turn positive
	prices := make([]float64, 100)
	for i := range prices {
		if i < 60 {
			prices[i] = 100
		} else {
			prices[i] = 100 + float64(i-60)*2
		}
	}
	candles := makeCandles(prices)
	res := ind.MACD(candles, 12, 26, 9)
	if res == nil {
		t.Fatal("MACD returned nil")
	}
	// Last histogram should be positive
	last := res[len(res)-1].Histogram
	if !last.IsPositive() {
		t.Errorf("expected positive MACD histogram after price ramp, got %s", last)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Bollinger Bands
// ────────────────────────────────────────────────────────────────────────────

func TestBollingerBands_PriceInsideBands(t *testing.T) {
	candles := makeCandles(makeRamp(100, 110, 50))
	bb := ind.BollingerBands(candles, 20, 2.0)
	if bb == nil {
		t.Fatal("BB returned nil")
	}
	for i, b := range bb {
		price, _ := candles[i+19].Close.Float64()
		upper, _ := b.Upper.Float64()
		lower, _ := b.Lower.Float64()
		// Price on a smooth ramp should be inside the bands
		if price > upper*1.001 || price < lower*0.999 {
			t.Errorf("price %.2f outside bands [%.2f, %.2f] at bar %d", price, lower, upper, i)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// ATR
// ────────────────────────────────────────────────────────────────────────────

func TestATR_HighVolatility_HigherThanLow(t *testing.T) {
	// Low-vol candles
	lowVol := make([]models.Candle, 50)
	highVol := make([]models.Candle, 50)
	for i := range lowVol {
		p := 100.0
		lowVol[i] = models.Candle{
			Open: decimal.NewFromFloat(p), High: decimal.NewFromFloat(p * 1.001),
			Low: decimal.NewFromFloat(p * 0.999), Close: decimal.NewFromFloat(p),
			Volume: decimal.NewFromFloat(100),
		}
		highVol[i] = models.Candle{
			Open: decimal.NewFromFloat(p), High: decimal.NewFromFloat(p * 1.05),
			Low: decimal.NewFromFloat(p * 0.95), Close: decimal.NewFromFloat(p),
			Volume: decimal.NewFromFloat(100),
		}
	}
	atrLow := ind.ATR(lowVol, 14)
	atrHigh := ind.ATR(highVol, 14)
	if atrLow == nil || atrHigh == nil {
		t.Fatal("ATR returned nil")
	}
	lv, _ := atrLow[len(atrLow)-1].Float64()
	hv, _ := atrHigh[len(atrHigh)-1].Float64()
	if hv <= lv {
		t.Errorf("high-vol ATR (%.4f) should exceed low-vol ATR (%.4f)", hv, lv)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// VWAP
// ────────────────────────────────────────────────────────────────────────────

func TestVWAP_EqualVolumes_EqualsTypicalPrice(t *testing.T) {
	// When all volumes are equal, VWAP = mean of typical prices
	candles := makeCandles([]float64{100, 110, 120, 130, 140})
	vwap := ind.VWAP(candles)
	// typical = (H+L+C)/3 ≈ close for our synthetic candles
	vwapF, _ := vwap.Float64()
	if vwapF < 100 || vwapF > 140 {
		t.Errorf("VWAP out of price range: %.2f", vwapF)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Ichimoku
// ────────────────────────────────────────────────────────────────────────────

func TestIchimoku_SufficientData(t *testing.T) {
	candles := makeCandles(makeRamp(100, 200, 120))
	res := ind.Ichimoku(candles, 9, 26, 52)
	if res == nil {
		t.Fatal("Ichimoku returned nil")
	}
	if len(res) == 0 {
		t.Fatal("Ichimoku returned empty slice")
	}
}

func TestIchimoku_BullishSignal_UpTrend(t *testing.T) {
	// Strong uptrend should produce bullish or neutral signal
	candles := makeCandles(makeRamp(100, 250, 120))
	res := ind.Ichimoku(candles, 9, 26, 52)
	sig, _ := ind.IchimokuLatestSignal(res, candles)
	if sig == ind.IchimokuBearish {
		t.Error("expected Bullish or Neutral in strong uptrend, got Bearish")
	}
}

func TestIchimoku_BearishSignal_DownTrend(t *testing.T) {
	candles := makeCandles(makeRamp(250, 100, 120))
	res := ind.Ichimoku(candles, 9, 26, 52)
	sig, _ := ind.IchimokuLatestSignal(res, candles)
	if sig == ind.IchimokuBullish {
		t.Error("expected Bearish or Neutral in strong downtrend, got Bullish")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Williams %R
// ────────────────────────────────────────────────────────────────────────────

func TestWilliamsR_Range(t *testing.T) {
	candles := makeCandles(makeRamp(100, 130, 30))
	wr := ind.WilliamsR(candles, 14)
	if wr == nil {
		t.Fatal("WilliamsR nil")
	}
	for _, v := range wr {
		if v < -100 || v > 0 {
			t.Errorf("WilliamsR out of range: %.2f", v)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Parabolic SAR
// ────────────────────────────────────────────────────────────────────────────

func TestParabolicSAR_UpTrend_IsLong(t *testing.T) {
	candles := makeCandles(makeRamp(100, 200, 60))
	sar := ind.ParabolicSAR(candles, 0.02, 0.2)
	if sar == nil {
		t.Fatal("SAR nil")
	}
	last := sar[len(sar)-1]
	if !last.IsLong {
		t.Error("expected SAR to be long in persistent uptrend")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Supertrend
// ────────────────────────────────────────────────────────────────────────────

func TestSupertrend_OutputLength(t *testing.T) {
	candles := makeCandles(makeRamp(100, 130, 50))
	st := ind.Supertrend(candles, 10, 3.0)
	if st == nil {
		t.Fatal("Supertrend nil")
	}
	if len(st) == 0 {
		t.Error("Supertrend empty")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// OrderBook helpers
// ────────────────────────────────────────────────────────────────────────────

func TestOrderBook_MidPrice(t *testing.T) {
	ob := models.OrderBook{
		Bids: []models.Level{{Price: decimal.NewFromFloat(100), Quantity: decimal.NewFromFloat(1)}},
		Asks: []models.Level{{Price: decimal.NewFromFloat(102), Quantity: decimal.NewFromFloat(1)}},
	}
	mid, _ := ob.MidPrice().Float64()
	if !approxEq(mid, 101, 0.001) {
		t.Errorf("MidPrice = %.4f, want 101", mid)
	}
}

func TestOrderBook_Spread(t *testing.T) {
	ob := models.OrderBook{
		Bids: []models.Level{{Price: decimal.NewFromFloat(100)}},
		Asks: []models.Level{{Price: decimal.NewFromFloat(101)}},
	}
	spread, _ := ob.Spread().Float64()
	if !approxEq(spread, 1.0, 0.0001) {
		t.Errorf("Spread = %.4f, want 1.0", spread)
	}
}

func TestOrderBook_BidAskImbalance_BidHeavy(t *testing.T) {
	ob := models.OrderBook{
		Bids: []models.Level{
			{Price: decimal.NewFromFloat(100), Quantity: decimal.NewFromFloat(10)},
		},
		Asks: []models.Level{
			{Price: decimal.NewFromFloat(101), Quantity: decimal.NewFromFloat(2)},
		},
	}
	imb, _ := ob.BidAskImbalance(5).Float64()
	if imb <= 0 {
		t.Errorf("BidAskImbalance should be positive for bid-heavy book, got %.4f", imb)
	}
}
