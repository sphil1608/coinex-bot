package indicators

import (
	"github.com/shopspring/decimal"
	"github.com/rusty/coinex-bot/internal/models"
)

// IchimokuResult holds all five Ichimoku components at a given index.
// Chikou (lagging span) is shifted back 26 periods so array indices align with
// the candle that generated them.
type IchimokuResult struct {
	Tenkan   decimal.Decimal // Conversion line (9)
	Kijun    decimal.Decimal // Base line (26)
	SenkouA  decimal.Decimal // Leading Span A  (plotted 26 forward)
	SenkouB  decimal.Decimal // Leading Span B  (plotted 26 forward)
	Chikou   decimal.Decimal // Lagging span close shifted back 26
}

// IchimokuSignal encodes the cloud signal at the latest candle.
type IchimokuSignal int

const (
	IchimokuBullish IchimokuSignal = 1
	IchimokuBearish IchimokuSignal = -1
	IchimokuNeutral IchimokuSignal = 0
)

// Ichimoku calculates the full Ichimoku cloud.
// Returns a slice aligned to candles, starting from index (kijunPeriod-1).
// Standard params: tenkan=9, kijun=26, senkou=52.
func Ichimoku(candles []models.Candle, tenkan, kijun, senkou int) []IchimokuResult {
	n := len(candles)
	if n < senkou {
		return nil
	}

	midpoint := func(start, end int) decimal.Decimal {
		hi := candles[start].High
		lo := candles[start].Low
		for i := start + 1; i <= end; i++ {
			if candles[i].High.GreaterThan(hi) {
				hi = candles[i].High
			}
			if candles[i].Low.LessThan(lo) {
				lo = candles[i].Low
			}
		}
		return hi.Add(lo).Div(decimal.NewFromInt(2))
	}

	// Align output to max lookback start = kijun-1
	start := kijun - 1
	out := make([]IchimokuResult, n-start)

	for i := start; i < n; i++ {
		res := &out[i-start]

		// Tenkan-sen
		if i >= tenkan-1 {
			res.Tenkan = midpoint(i-tenkan+1, i)
		}

		// Kijun-sen
		res.Kijun = midpoint(i-kijun+1, i)

		// Senkou Span A = avg(tenkan, kijun) plotted 26 ahead
		// We store at current index; caller renders 26 ahead
		if i >= tenkan-1 {
			res.SenkouA = res.Tenkan.Add(res.Kijun).Div(decimal.NewFromInt(2))
		}

		// Senkou Span B = midpoint of senkou period, plotted 26 ahead
		if i >= senkou-1 {
			res.SenkouB = midpoint(i-senkou+1, i)
		}

		// Chikou = close plotted 26 behind
		res.Chikou = candles[i].Close
	}

	return out
}

// IchimokuLatestSignal analyses the most recent Ichimoku result.
// Returns Bullish / Bearish / Neutral with a reason string.
func IchimokuLatestSignal(results []IchimokuResult, candles []models.Candle) (IchimokuSignal, string) {
	if len(results) < 2 {
		return IchimokuNeutral, "insufficient data"
	}
	cur := results[len(results)-1]
	prev := results[len(results)-2]
	price := candles[len(candles)-1].Close

	// 1. Price above/below cloud
	cloudTop := cur.SenkouA
	if cur.SenkouB.GreaterThan(cloudTop) {
		cloudTop = cur.SenkouB
	}
	cloudBot := cur.SenkouA
	if cur.SenkouB.LessThan(cloudBot) {
		cloudBot = cur.SenkouB
	}
	aboveCloud := price.GreaterThan(cloudTop)
	belowCloud := price.LessThan(cloudBot)

	// 2. TK cross
	tkCrossBull := cur.Tenkan.GreaterThan(cur.Kijun) && prev.Tenkan.LessThanOrEqual(prev.Kijun)
	tkCrossBear := cur.Tenkan.LessThan(cur.Kijun) && prev.Tenkan.GreaterThanOrEqual(prev.Kijun)

	// 3. Bullish scenario: price above cloud + TK cross bull
	if aboveCloud && tkCrossBull {
		return IchimokuBullish, "price above cloud + TK bullish cross"
	}
	// 4. Bearish scenario: price below cloud + TK cross bear
	if belowCloud && tkCrossBear {
		return IchimokuBearish, "price below cloud + TK bearish cross"
	}
	// 5. Softer signals
	if aboveCloud && cur.Tenkan.GreaterThan(cur.Kijun) {
		return IchimokuBullish, "price above cloud, tenkan > kijun"
	}
	if belowCloud && cur.Tenkan.LessThan(cur.Kijun) {
		return IchimokuBearish, "price below cloud, tenkan < kijun"
	}

	return IchimokuNeutral, "inside cloud or mixed signals"
}
