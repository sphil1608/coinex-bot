package strategies

// This file registers all 22 strategies. Each strategy is a self-contained
// struct implementing the Strategy interface.

import (
	"context"
	"math"

	"github.com/shopspring/decimal"

	ind "github.com/rusty/coinex-bot/internal/indicators"
	"github.com/rusty/coinex-bot/internal/models"
)

func init() {
	Register(&IchimokuStrategy{TenkanPeriod: 9, KijunPeriod: 26, SenkouPeriod: 52})
	Register(&RSIMeanRevert{Period: 14, Oversold: 30, Overbought: 70})
	Register(&MACDCross{Fast: 12, Slow: 26, Signal: 9})
	Register(&BollingerBandStrategy{Period: 20, StdMult: 2.0})
	Register(&EMACross{Fast: 9, Slow: 21})
	Register(&VWAPRevert{})
	Register(&Momentum{Period: 10})
	Register(&OrderBookScalper{Depth: 10, ImbalanceThresh: 0.4})
	Register(&GridBreaker{Levels: 10, SpacingPct: 0.005})
	Register(&TrendFollow{ATRPeriod: 14, ATRMult: 2.0})
	Register(&BreakoutStrategy{Lookback: 20})
	Register(&MeanRevertZ{ZThreshold: 2.0, Period: 20})
	Register(&DualThrust{K1: 0.7, K2: 0.7, Lookback: 5})
	Register(&SupertrendStrategy{ATRPeriod: 10, Factor: 3.0})
	Register(&WilliamsRStrategy{Period: 14, Oversold: -80, Overbought: -20})
	Register(&StochRSIStrategy{Period: 14, K: 3, D: 3})
	Register(&ADXDMIStrategy{Period: 14, ADXThresh: 25.0})
	Register(&ParabolicSARStrategy{Step: 0.02, Max: 0.2})
	Register(&HullMAStrategy{Period: 16})
	Register(&KeltnerChannelStrategy{Period: 20, ATRMult: 1.5})
	Register(&DonchianBreakout{Period: 20})
	Register(&SpreadArb{SpreadThresh: 0.001})
}

// ────────────────────────────────────────────────────────────────────────────
// 1. Ichimoku Cloud
// ────────────────────────────────────────────────────────────────────────────

type IchimokuStrategy struct {
	TenkanPeriod int
	KijunPeriod  int
	SenkouPeriod int
}

func (s *IchimokuStrategy) Name() string { return "ichimoku" }
func (s *IchimokuStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	market := candles[len(candles)-1].Timeframe
	results := ind.Ichimoku(candles, s.TenkanPeriod, s.KijunPeriod, s.SenkouPeriod)
	if results == nil {
		return flat(s.Name(), market)
	}
	sig, reason := ind.IchimokuLatestSignal(results, candles)
	switch sig {
	case ind.IchimokuBullish:
		return newSignal(s.Name(), market, models.SignalLong, 0.75, reason)
	case ind.IchimokuBearish:
		return newSignal(s.Name(), market, models.SignalShort, 0.75, reason)
	default:
		return flat(s.Name(), market)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// 2. RSI Mean Reversion
// ────────────────────────────────────────────────────────────────────────────

type RSIMeanRevert struct {
	Period     int
	Oversold   float64
	Overbought float64
}

func (s *RSIMeanRevert) Name() string { return "rsi_mean_revert" }
func (s *RSIMeanRevert) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	rsi := ind.RSI(candles, s.Period)
	if rsi == nil {
		return flat(s.Name(), mkt)
	}
	v := rsi[len(rsi)-1]
	if v <= s.Oversold {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.7, "RSI oversold")
	}
	if v >= s.Overbought {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.7, "RSI overbought")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 3. MACD Cross
// ────────────────────────────────────────────────────────────────────────────

type MACDCross struct{ Fast, Slow, Signal int }

func (s *MACDCross) Name() string { return "macd_cross" }
func (s *MACDCross) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	res := ind.MACD(candles, s.Fast, s.Slow, s.Signal)
	if len(res) < 2 {
		return flat(s.Name(), mkt)
	}
	cur, prev := res[len(res)-1], res[len(res)-2]
	if cur.Histogram.IsPositive() && prev.Histogram.IsNegative() {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.68, "MACD histogram crossover")
	}
	if cur.Histogram.IsNegative() && prev.Histogram.IsPositive() {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.68, "MACD histogram crossunder")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 4. Bollinger Band Squeeze
// ────────────────────────────────────────────────────────────────────────────

type BollingerBandStrategy struct {
	Period  int
	StdMult float64
}

func (s *BollingerBandStrategy) Name() string { return "bollinger_bands" }
func (s *BollingerBandStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	bb := ind.BollingerBands(candles, s.Period, s.StdMult)
	if len(bb) < 2 {
		return flat(s.Name(), mkt)
	}
	cur := bb[len(bb)-1]
	price := candles[len(candles)-1].Close
	if price.LessThan(cur.Lower) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.65, "price below lower band")
	}
	if price.GreaterThan(cur.Upper) {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.65, "price above upper band")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 5. EMA Cross
// ────────────────────────────────────────────────────────────────────────────

type EMACross struct{ Fast, Slow int }

func (s *EMACross) Name() string { return "ema_cross" }
func (s *EMACross) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	cl := make([]decimal.Decimal, len(candles))
	for i, c := range candles {
		cl[i] = c.Close
	}
	fast := ind.EMA(cl, s.Fast)
	slow := ind.EMA(cl, s.Slow)
	if len(fast) < 2 || len(slow) < 2 {
		return flat(s.Name(), mkt)
	}
	fOff := len(fast) - len(slow)
	curF, prevF := fast[len(fast)-1], fast[len(fast)-2]
	curS, prevS := slow[len(slow)-1], slow[len(slow)-2]
	_ = fOff
	if curF.GreaterThan(curS) && prevF.LessThanOrEqual(prevS) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.70, "EMA golden cross")
	}
	if curF.LessThan(curS) && prevF.GreaterThanOrEqual(prevS) {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.70, "EMA death cross")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 6. VWAP Reversion
// ────────────────────────────────────────────────────────────────────────────

type VWAPRevert struct{}

func (s *VWAPRevert) Name() string { return "vwap_revert" }
func (s *VWAPRevert) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	if len(candles) < 20 {
		return flat(s.Name(), mkt)
	}
	vwap := ind.VWAP(candles)
	price := candles[len(candles)-1].Close
	dev := price.Sub(vwap).Abs().Div(vwap)
	devF, _ := dev.Float64()
	if devF > 0.015 {
		if price.LessThan(vwap) {
			return newSignal(s.Name(), mkt, models.SignalLong, 0.60, "price 1.5% below VWAP")
		}
		return newSignal(s.Name(), mkt, models.SignalShort, 0.60, "price 1.5% above VWAP")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 7. Momentum
// ────────────────────────────────────────────────────────────────────────────

type Momentum struct{ Period int }

func (s *Momentum) Name() string { return "momentum" }
func (s *Momentum) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	if len(candles) <= s.Period {
		return flat(s.Name(), mkt)
	}
	now := candles[len(candles)-1].Close
	past := candles[len(candles)-1-s.Period].Close
	mom := now.Sub(past).Div(past)
	momF, _ := mom.Float64()
	if momF > 0.02 {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.62, "positive momentum")
	}
	if momF < -0.02 {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.62, "negative momentum")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 8. Order Book Scalper
// ────────────────────────────────────────────────────────────────────────────

type OrderBookScalper struct {
	Depth           int
	ImbalanceThresh float64
}

func (s *OrderBookScalper) Name() string { return "scalp_ob" }
func (s *OrderBookScalper) Evaluate(_ context.Context, _ []models.Candle, ob models.OrderBook) models.Signal {
	mkt := ob.Market
	imb := ob.BidAskImbalance(s.Depth)
	imbF, _ := imb.Float64()
	if imbF > s.ImbalanceThresh {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.55+imbF*0.2, "bid imbalance")
	}
	if imbF < -s.ImbalanceThresh {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.55+math.Abs(imbF)*0.2, "ask imbalance")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 9. Grid Breakout
// ────────────────────────────────────────────────────────────────────────────

type GridBreaker struct {
	Levels     int
	SpacingPct float64
}

func (s *GridBreaker) Name() string { return "grid" }
func (s *GridBreaker) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	if len(candles) < 2 {
		return flat(s.Name(), mkt)
	}
	// Simple grid: detect price crossing a grid boundary
	ref, _ := candles[0].Close.Float64()
	cur, _ := candles[len(candles)-1].Close.Float64()
	ratio := cur / ref
	crossings := int(math.Abs(math.Log(ratio) / math.Log(1+s.SpacingPct)))
	if crossings > 0 && cur > ref {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.58, "grid upward cross")
	}
	if crossings > 0 && cur < ref {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.58, "grid downward cross")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 10. Trend Following (ATR-based)
// ────────────────────────────────────────────────────────────────────────────

type TrendFollow struct {
	ATRPeriod int
	ATRMult   float64
}

func (s *TrendFollow) Name() string { return "trend_follow" }
func (s *TrendFollow) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	atr := ind.ATR(candles, s.ATRPeriod)
	if len(atr) < 2 {
		return flat(s.Name(), mkt)
	}
	price := candles[len(candles)-1].Close
	prevClose := candles[len(candles)-2].Close
	curATR := atr[len(atr)-1]
	band := curATR.Mul(decimal.NewFromFloat(s.ATRMult))
	if price.Sub(prevClose).GreaterThan(band) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.72, "ATR trend breakout up")
	}
	if prevClose.Sub(price).GreaterThan(band) {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.72, "ATR trend breakout down")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 11. Breakout (highest high / lowest low)
// ────────────────────────────────────────────────────────────────────────────

type BreakoutStrategy struct{ Lookback int }

func (s *BreakoutStrategy) Name() string { return "breakout" }
func (s *BreakoutStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	if len(candles) <= s.Lookback {
		return flat(s.Name(), mkt)
	}
	window := candles[len(candles)-1-s.Lookback : len(candles)-1]
	hiH, loL := window[0].High, window[0].Low
	for _, c := range window {
		if c.High.GreaterThan(hiH) {
			hiH = c.High
		}
		if c.Low.LessThan(loL) {
			loL = c.Low
		}
	}
	price := candles[len(candles)-1].Close
	if price.GreaterThan(hiH) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.73, "breakout above resistance")
	}
	if price.LessThan(loL) {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.73, "breakdown below support")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 12. Mean Reversion Z-Score
// ────────────────────────────────────────────────────────────────────────────

type MeanRevertZ struct {
	ZThreshold float64
	Period     int
}

func (s *MeanRevertZ) Name() string { return "mean_revert_z" }
func (s *MeanRevertZ) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	if len(candles) < s.Period {
		return flat(s.Name(), mkt)
	}
	window := candles[len(candles)-s.Period:]
	var sum float64
	for _, c := range window {
		f, _ := c.Close.Float64()
		sum += f
	}
	mean := sum / float64(s.Period)
	var variance float64
	for _, c := range window {
		f, _ := c.Close.Float64()
		variance += (f - mean) * (f - mean)
	}
	std := math.Sqrt(variance / float64(s.Period))
	if std == 0 {
		return flat(s.Name(), mkt)
	}
	price, _ := candles[len(candles)-1].Close.Float64()
	z := (price - mean) / std
	if z < -s.ZThreshold {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.66, "z-score oversold")
	}
	if z > s.ZThreshold {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.66, "z-score overbought")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 13. Dual Thrust
// ────────────────────────────────────────────────────────────────────────────

type DualThrust struct {
	K1, K2  float64
	Lookback int
}

func (s *DualThrust) Name() string { return "dual_thrust" }
func (s *DualThrust) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	if len(candles) <= s.Lookback {
		return flat(s.Name(), mkt)
	}
	window := candles[len(candles)-1-s.Lookback : len(candles)-1]
	hh, lc, hc, ll := window[0].High, window[0].Close, window[0].Close, window[0].Low
	for _, c := range window {
		if c.High.GreaterThan(hh) { hh = c.High }
		if c.Close.LessThan(lc) { lc = c.Close }
		if c.Close.GreaterThan(hc) { hc = c.Close }
		if c.Low.LessThan(ll) { ll = c.Low }
	}
	r1 := maxD(hh.Sub(lc), hc.Sub(ll))
	openPrice := candles[len(candles)-1].Open
	buyLine := openPrice.Add(decimal.NewFromFloat(s.K1).Mul(r1))
	sellLine := openPrice.Sub(decimal.NewFromFloat(s.K2).Mul(r1))
	price := candles[len(candles)-1].Close
	if price.GreaterThan(buyLine) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.71, "dual thrust buy")
	}
	if price.LessThan(sellLine) {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.71, "dual thrust sell")
	}
	return flat(s.Name(), mkt)
}

func maxD(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThan(b) { return a }
	return b
}

// ────────────────────────────────────────────────────────────────────────────
// 14. Supertrend
// ────────────────────────────────────────────────────────────────────────────

type SupertrendStrategy struct {
	ATRPeriod int
	Factor    float64
}

func (s *SupertrendStrategy) Name() string { return "supertrend" }
func (s *SupertrendStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	st := ind.Supertrend(candles, s.ATRPeriod, s.Factor)
	if len(st) < 2 {
		return flat(s.Name(), mkt)
	}
	cur, prev := st[len(st)-1], st[len(st)-2]
	if cur.IsLong && !prev.IsLong {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.74, "supertrend flip long")
	}
	if !cur.IsLong && prev.IsLong {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.74, "supertrend flip short")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 15. Williams %R
// ────────────────────────────────────────────────────────────────────────────

type WilliamsRStrategy struct {
	Period               int
	Oversold, Overbought float64
}

func (s *WilliamsRStrategy) Name() string { return "williams_r" }
func (s *WilliamsRStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	wr := ind.WilliamsR(candles, s.Period)
	if wr == nil {
		return flat(s.Name(), mkt)
	}
	v := wr[len(wr)-1]
	if v <= s.Oversold {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.65, "Williams%R oversold")
	}
	if v >= s.Overbought {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.65, "Williams%R overbought")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 16. Stochastic RSI
// ────────────────────────────────────────────────────────────────────────────

type StochRSIStrategy struct{ Period, K, D int }

func (s *StochRSIStrategy) Name() string { return "stoch_rsi" }
func (s *StochRSIStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	sr := ind.StochRSI(candles, s.Period, s.K, s.D)
	if len(sr) < 2 {
		return flat(s.Name(), mkt)
	}
	cur, prev := sr[len(sr)-1], sr[len(sr)-2]
	if cur.K > cur.D && prev.K <= prev.D && cur.K < 20 {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.67, "stochRSI cross up oversold")
	}
	if cur.K < cur.D && prev.K >= prev.D && cur.K > 80 {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.67, "stochRSI cross down overbought")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 17. ADX / DMI Trend Filter
// ────────────────────────────────────────────────────────────────────────────

type ADXDMIStrategy struct {
	Period    int
	ADXThresh float64
}

func (s *ADXDMIStrategy) Name() string { return "dmi_adx" }
func (s *ADXDMIStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	adx := ind.ADX(candles, s.Period)
	if len(adx) < 2 {
		return flat(s.Name(), mkt)
	}
	cur := adx[len(adx)-1]
	if cur.ADX < s.ADXThresh {
		return flat(s.Name(), mkt) // no trend
	}
	if cur.PlusDI > cur.MinusDI {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.68, "ADX strong +DI")
	}
	return newSignal(s.Name(), mkt, models.SignalShort, 0.68, "ADX strong -DI")
}

// ────────────────────────────────────────────────────────────────────────────
// 18. Parabolic SAR
// ────────────────────────────────────────────────────────────────────────────

type ParabolicSARStrategy struct{ Step, Max float64 }

func (s *ParabolicSARStrategy) Name() string { return "parabolic_sar" }
func (s *ParabolicSARStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	sar := ind.ParabolicSAR(candles, s.Step, s.Max)
	if len(sar) < 2 {
		return flat(s.Name(), mkt)
	}
	cur, prev := sar[len(sar)-1], sar[len(sar)-2]
	if cur.IsLong && !prev.IsLong {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.69, "SAR flip to long")
	}
	if !cur.IsLong && prev.IsLong {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.69, "SAR flip to short")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 19. Hull MA
// ────────────────────────────────────────────────────────────────────────────

type HullMAStrategy struct{ Period int }

func (s *HullMAStrategy) Name() string { return "hull_ma" }
func (s *HullMAStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	hma := ind.HullMA(candles, s.Period)
	if len(hma) < 2 {
		return flat(s.Name(), mkt)
	}
	cur, prev := hma[len(hma)-1], hma[len(hma)-2]
	if cur.GreaterThan(prev) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.63, "HullMA rising")
	}
	return newSignal(s.Name(), mkt, models.SignalShort, 0.63, "HullMA falling")
}

// ────────────────────────────────────────────────────────────────────────────
// 20. Keltner Channel Breakout
// ────────────────────────────────────────────────────────────────────────────

type KeltnerChannelStrategy struct {
	Period  int
	ATRMult float64
}

func (s *KeltnerChannelStrategy) Name() string { return "keltner_channel" }
func (s *KeltnerChannelStrategy) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	kc := ind.KeltnerChannel(candles, s.Period, s.ATRMult)
	if kc == nil {
		return flat(s.Name(), mkt)
	}
	cur := kc[len(kc)-1]
	price := candles[len(candles)-1].Close
	if price.GreaterThan(cur.Upper) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.67, "price above Keltner upper")
	}
	if price.LessThan(cur.Lower) {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.67, "price below Keltner lower")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 21. Donchian Channel Breakout
// ────────────────────────────────────────────────────────────────────────────

type DonchianBreakout struct{ Period int }

func (s *DonchianBreakout) Name() string { return "donchian" }
func (s *DonchianBreakout) Evaluate(_ context.Context, candles []models.Candle, _ models.OrderBook) models.Signal {
	mkt := ""
	dc := ind.DonchianChannel(candles, s.Period)
	if len(dc) < 2 {
		return flat(s.Name(), mkt)
	}
	prev := dc[len(dc)-2]
	price := candles[len(candles)-1].Close
	if price.GreaterThan(prev.Upper) {
		return newSignal(s.Name(), mkt, models.SignalLong, 0.70, "Donchian upper breakout")
	}
	if price.LessThan(prev.Lower) {
		return newSignal(s.Name(), mkt, models.SignalShort, 0.70, "Donchian lower breakdown")
	}
	return flat(s.Name(), mkt)
}

// ────────────────────────────────────────────────────────────────────────────
// 22. Spread / Order Book Arb
// ────────────────────────────────────────────────────────────────────────────

type SpreadArb struct{ SpreadThresh float64 }

func (s *SpreadArb) Name() string { return "arb_spread" }
func (s *SpreadArb) Evaluate(_ context.Context, _ []models.Candle, ob models.OrderBook) models.Signal {
	mkt := ob.Market
	mid := ob.MidPrice()
	if mid.IsZero() {
		return flat(s.Name(), mkt)
	}
	spread := ob.Spread()
	spreadPct, _ := spread.Div(mid).Float64()
	if spreadPct > s.SpreadThresh {
		// Wide spread: fade the ask (expect reversion)
		return newSignal(s.Name(), mkt, models.SignalLong, 0.55, "spread arb opportunity")
	}
	return flat(s.Name(), mkt)
}
