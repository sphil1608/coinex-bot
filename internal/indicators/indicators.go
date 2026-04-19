// Package indicators provides stateless technical indicator calculations
// over []models.Candle slices. All functions return decimal.Decimal values.
package indicators

import (
	"math"

	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func closes(candles []models.Candle) []decimal.Decimal {
	v := make([]decimal.Decimal, len(candles))
	for i, c := range candles {
		v[i] = c.Close
	}
	return v
}

func highs(candles []models.Candle) []decimal.Decimal {
	v := make([]decimal.Decimal, len(candles))
	for i, c := range candles {
		v[i] = c.High
	}
	return v
}

func lows(candles []models.Candle) []decimal.Decimal {
	v := make([]decimal.Decimal, len(candles))
	for i, c := range candles {
		v[i] = c.Low
	}
	return v
}

func maxDec(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}
	return b
}

func minDec(a, b decimal.Decimal) decimal.Decimal {
	if a.LessThan(b) {
		return a
	}
	return b
}

func absF64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// ────────────────────────────────────────────────────────────────────────────
// SMA / EMA
// ────────────────────────────────────────────────────────────────────────────

func SMA(vals []decimal.Decimal, period int) []decimal.Decimal {
	if len(vals) < period {
		return nil
	}
	result := make([]decimal.Decimal, len(vals)-period+1)
	sum := decimal.Zero
	for i := 0; i < period; i++ {
		sum = sum.Add(vals[i])
	}
	result[0] = sum.Div(decimal.NewFromInt(int64(period)))
	for i := period; i < len(vals); i++ {
		sum = sum.Add(vals[i]).Sub(vals[i-period])
		result[i-period+1] = sum.Div(decimal.NewFromInt(int64(period)))
	}
	return result
}

func EMA(vals []decimal.Decimal, period int) []decimal.Decimal {
	if len(vals) < period {
		return nil
	}
	k := decimal.NewFromFloat(2.0 / float64(period+1))
	result := make([]decimal.Decimal, len(vals)-period+1)
	// seed with SMA
	sum := decimal.Zero
	for i := 0; i < period; i++ {
		sum = sum.Add(vals[i])
	}
	result[0] = sum.Div(decimal.NewFromInt(int64(period)))
	for i := 1; i < len(result); i++ {
		idx := period - 1 + i
		result[i] = vals[idx].Mul(k).Add(result[i-1].Mul(decimal.NewFromInt(1).Sub(k)))
	}
	return result
}

// ────────────────────────────────────────────────────────────────────────────
// RSI
// ────────────────────────────────────────────────────────────────────────────

func RSI(candles []models.Candle, period int) []float64 {
	cl := closes(candles)
	if len(cl) < period+1 {
		return nil
	}
	gains := make([]float64, len(cl)-1)
	losses := make([]float64, len(cl)-1)
	for i := 1; i < len(cl); i++ {
		diff, _ := cl[i].Sub(cl[i-1]).Float64()
		if diff > 0 {
			gains[i-1] = diff
		} else {
			losses[i-1] = absF64(diff)
		}
	}
	var avgGain, avgLoss float64
	for i := 0; i < period; i++ {
		avgGain += gains[i]
		avgLoss += losses[i]
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	result := make([]float64, len(gains)-period+1)
	if avgLoss == 0 {
		result[0] = 100
	} else {
		rs := avgGain / avgLoss
		result[0] = 100 - 100/(1+rs)
	}
	for i := period; i < len(gains); i++ {
		avgGain = (avgGain*float64(period-1) + gains[i]) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + losses[i]) / float64(period)
		if avgLoss == 0 {
			result[i-period+1] = 100
		} else {
			result[i-period+1] = 100 - 100/(1+avgGain/avgLoss)
		}
	}
	return result
}

// ────────────────────────────────────────────────────────────────────────────
// MACD
// ────────────────────────────────────────────────────────────────────────────

type MACDResult struct {
	MACD      decimal.Decimal
	Signal    decimal.Decimal
	Histogram decimal.Decimal
}

func MACD(candles []models.Candle, fast, slow, signal int) []MACDResult {
	cl := closes(candles)
	fastEMA := EMA(cl, fast)
	slowEMA := EMA(cl, slow)
	if fastEMA == nil || slowEMA == nil {
		return nil
	}
	// align: fastEMA is longer; trim to slowEMA length
	offset := len(fastEMA) - len(slowEMA)
	macdLine := make([]decimal.Decimal, len(slowEMA))
	for i := range slowEMA {
		macdLine[i] = fastEMA[i+offset].Sub(slowEMA[i])
	}
	sigEMA := EMA(macdLine, signal)
	if sigEMA == nil {
		return nil
	}
	trimOffset := len(macdLine) - len(sigEMA)
	out := make([]MACDResult, len(sigEMA))
	for i := range sigEMA {
		m := macdLine[i+trimOffset]
		s := sigEMA[i]
		out[i] = MACDResult{MACD: m, Signal: s, Histogram: m.Sub(s)}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Bollinger Bands
// ────────────────────────────────────────────────────────────────────────────

type BollingerResult struct {
	Upper  decimal.Decimal
	Middle decimal.Decimal
	Lower  decimal.Decimal
}

func BollingerBands(candles []models.Candle, period int, stdMult float64) []BollingerResult {
	cl := closes(candles)
	sma := SMA(cl, period)
	if sma == nil {
		return nil
	}
	out := make([]BollingerResult, len(sma))
	for i, mid := range sma {
		// variance over period
		var variance float64
		for j := i; j < i+period; j++ {
			diff, _ := cl[j].Sub(mid).Float64()
			variance += diff * diff
		}
		stdDev := math.Sqrt(variance / float64(period))
		d := decimal.NewFromFloat(stdDev * stdMult)
		out[i] = BollingerResult{Upper: mid.Add(d), Middle: mid, Lower: mid.Sub(d)}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// ATR
// ────────────────────────────────────────────────────────────────────────────

func ATR(candles []models.Candle, period int) []decimal.Decimal {
	if len(candles) < period+1 {
		return nil
	}
	tr := make([]decimal.Decimal, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		hl := candles[i].High.Sub(candles[i].Low)
		hc := maxDec(candles[i].High.Sub(candles[i-1].Close), candles[i-1].Close.Sub(candles[i].Low))
		tr[i-1] = maxDec(hl, hc)
	}
	return EMA(tr, period)
}

// ────────────────────────────────────────────────────────────────────────────
// VWAP (session)
// ────────────────────────────────────────────────────────────────────────────

func VWAP(candles []models.Candle) decimal.Decimal {
	var cumPV, cumVol decimal.Decimal
	for _, c := range candles {
		typical := c.High.Add(c.Low).Add(c.Close).Div(decimal.NewFromInt(3))
		cumPV = cumPV.Add(typical.Mul(c.Volume))
		cumVol = cumVol.Add(c.Volume)
	}
	if cumVol.IsZero() {
		return decimal.Zero
	}
	return cumPV.Div(cumVol)
}

// ────────────────────────────────────────────────────────────────────────────
// ADX / DMI
// ────────────────────────────────────────────────────────────────────────────

type ADXResult struct {
	ADX  float64
	PlusDI  float64
	MinusDI float64
}

func ADX(candles []models.Candle, period int) []ADXResult {
	if len(candles) < period*2 {
		return nil
	}
	hi := highs(candles)
	lo := lows(candles)
	cl := closes(candles)

	dmPlus := make([]float64, len(candles)-1)
	dmMinus := make([]float64, len(candles)-1)
	trArr := make([]float64, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		upMove, _ := hi[i].Sub(hi[i-1]).Float64()
		downMove, _ := lo[i-1].Sub(lo[i]).Float64()
		if upMove > downMove && upMove > 0 {
			dmPlus[i-1] = upMove
		}
		if downMove > upMove && downMove > 0 {
			dmMinus[i-1] = downMove
		}
		hl, _ := hi[i].Sub(lo[i]).Float64()
		hc, _ := hi[i].Sub(cl[i-1]).Float64()
		lc, _ := cl[i-1].Sub(lo[i]).Float64()
		t := hl
		if absF64(hc) > t {
			t = absF64(hc)
		}
		if absF64(lc) > t {
			t = absF64(lc)
		}
		trArr[i-1] = t
	}

	smooth := func(arr []float64) []float64 {
		out := make([]float64, len(arr)-period+1)
		sum := 0.0
		for i := 0; i < period; i++ {
			sum += arr[i]
		}
		out[0] = sum
		for i := period; i < len(arr); i++ {
			sum = sum - sum/float64(period) + arr[i]
			out[i-period+1] = sum
		}
		return out
	}

	str := smooth(trArr)
	sdp := smooth(dmPlus)
	sdm := smooth(dmMinus)
	n := len(str)
	if n == 0 {
		return nil
	}

	dx := make([]float64, n)
	plus := make([]float64, n)
	minus := make([]float64, n)
	for i := 0; i < n; i++ {
		if str[i] == 0 {
			continue
		}
		plus[i] = 100 * sdp[i] / str[i]
		minus[i] = 100 * sdm[i] / str[i]
		diSum := plus[i] + minus[i]
		if diSum == 0 {
			continue
		}
		dx[i] = 100 * absF64(plus[i]-minus[i]) / diSum
	}

	// Smooth DX → ADX
	adxArr := make([]float64, len(dx)-period+1)
	adxSum := 0.0
	for i := 0; i < period; i++ {
		adxSum += dx[i]
	}
	adxArr[0] = adxSum / float64(period)
	for i := period; i < len(dx); i++ {
		adxArr[i-period+1] = (adxArr[i-period]*float64(period-1) + dx[i]) / float64(period)
	}

	out := make([]ADXResult, len(adxArr))
	padP := len(plus) - len(adxArr)
	for i := range adxArr {
		out[i] = ADXResult{ADX: adxArr[i], PlusDI: plus[i+padP], MinusDI: minus[i+padP]}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Stochastic RSI
// ────────────────────────────────────────────────────────────────────────────

type StochRSIResult struct {
	K float64
	D float64
}

func StochRSI(candles []models.Candle, rsiPeriod, kPeriod, dPeriod int) []StochRSIResult {
	rsi := RSI(candles, rsiPeriod)
	if len(rsi) < kPeriod {
		return nil
	}
	k := make([]float64, len(rsi)-kPeriod+1)
	for i := range k {
		window := rsi[i : i+kPeriod]
		lo, hi := window[0], window[0]
		for _, v := range window {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		if hi-lo == 0 {
			k[i] = 0
		} else {
			k[i] = (rsi[i+kPeriod-1] - lo) / (hi - lo) * 100
		}
	}
	// D = SMA(K, dPeriod)
	if len(k) < dPeriod {
		return nil
	}
	out := make([]StochRSIResult, len(k)-dPeriod+1)
	for i := range out {
		sum := 0.0
		for j := i; j < i+dPeriod; j++ {
			sum += k[j]
		}
		out[i] = StochRSIResult{K: k[i+dPeriod-1], D: sum / float64(dPeriod)}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Williams %R
// ────────────────────────────────────────────────────────────────────────────

func WilliamsR(candles []models.Candle, period int) []float64 {
	if len(candles) < period {
		return nil
	}
	out := make([]float64, len(candles)-period+1)
	for i := range out {
		window := candles[i : i+period]
		hi, lo := window[0].High, window[0].Low
		for _, c := range window {
			if c.High.GreaterThan(hi) {
				hi = c.High
			}
			if c.Low.LessThan(lo) {
				lo = c.Low
			}
		}
		cl := candles[i+period-1].Close
		hiF, _ := hi.Float64()
		loF, _ := lo.Float64()
		clF, _ := cl.Float64()
		if hiF-loF == 0 {
			out[i] = -50
		} else {
			out[i] = (hiF - clF) / (hiF - loF) * -100
		}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Parabolic SAR
// ────────────────────────────────────────────────────────────────────────────

type SARPoint struct {
	SAR     decimal.Decimal
	IsLong  bool
}

func ParabolicSAR(candles []models.Candle, step, maxStep float64) []SARPoint {
	if len(candles) < 2 {
		return nil
	}
	out := make([]SARPoint, len(candles))
	isLong := candles[1].Close.GreaterThan(candles[0].Close)
	af := step
	ep := candles[0].High
	sar := candles[0].Low
	if isLong {
		ep = candles[0].High
		sar = candles[0].Low
	} else {
		ep = candles[0].Low
		sar = candles[0].High
	}
	out[0] = SARPoint{SAR: sar, IsLong: isLong}

	for i := 1; i < len(candles); i++ {
		afD := decimal.NewFromFloat(af)
		sar = sar.Add(afD.Mul(ep.Sub(sar)))

		if isLong {
			if candles[i].High.GreaterThan(ep) {
				ep = candles[i].High
				af = math.Min(af+step, maxStep)
			}
			if candles[i].Low.LessThan(sar) {
				isLong = false
				sar = ep
				ep = candles[i].Low
				af = step
			}
		} else {
			if candles[i].Low.LessThan(ep) {
				ep = candles[i].Low
				af = math.Min(af+step, maxStep)
			}
			if candles[i].High.GreaterThan(sar) {
				isLong = true
				sar = ep
				ep = candles[i].High
				af = step
			}
		}
		out[i] = SARPoint{SAR: sar, IsLong: isLong}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Hull Moving Average  HMA(n) = EMA(2*EMA(n/2) - EMA(n), sqrt(n))
// ────────────────────────────────────────────────────────────────────────────

func HullMA(candles []models.Candle, period int) []decimal.Decimal {
	cl := closes(candles)
	half := period / 2
	sqrtP := int(math.Round(math.Sqrt(float64(period))))

	ema1 := EMA(cl, half)
	ema2 := EMA(cl, period)
	if ema1 == nil || ema2 == nil {
		return nil
	}
	offset := len(ema1) - len(ema2)
	raw := make([]decimal.Decimal, len(ema2))
	for i := range ema2 {
		raw[i] = ema1[i+offset].Mul(decimal.NewFromInt(2)).Sub(ema2[i])
	}
	return EMA(raw, sqrtP)
}

// ────────────────────────────────────────────────────────────────────────────
// Keltner Channel
// ────────────────────────────────────────────────────────────────────────────

type KeltnerResult struct {
	Upper  decimal.Decimal
	Middle decimal.Decimal
	Lower  decimal.Decimal
}

func KeltnerChannel(candles []models.Candle, emaPeriod int, atrMult float64) []KeltnerResult {
	cl := closes(candles)
	ema := EMA(cl, emaPeriod)
	atr := ATR(candles, emaPeriod)
	if ema == nil || atr == nil {
		return nil
	}
	minLen := len(ema)
	if len(atr) < minLen {
		minLen = len(atr)
	}
	out := make([]KeltnerResult, minLen)
	eOff := len(ema) - minLen
	aOff := len(atr) - minLen
	mult := decimal.NewFromFloat(atrMult)
	for i := 0; i < minLen; i++ {
		mid := ema[i+eOff]
		band := atr[i+aOff].Mul(mult)
		out[i] = KeltnerResult{Upper: mid.Add(band), Middle: mid, Lower: mid.Sub(band)}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Donchian Channel
// ────────────────────────────────────────────────────────────────────────────

type DonchianResult struct {
	Upper  decimal.Decimal
	Middle decimal.Decimal
	Lower  decimal.Decimal
}

func DonchianChannel(candles []models.Candle, period int) []DonchianResult {
	if len(candles) < period {
		return nil
	}
	out := make([]DonchianResult, len(candles)-period+1)
	for i := range out {
		window := candles[i : i+period]
		hi, lo := window[0].High, window[0].Low
		for _, c := range window {
			if c.High.GreaterThan(hi) {
				hi = c.High
			}
			if c.Low.LessThan(lo) {
				lo = c.Low
			}
		}
		mid := hi.Add(lo).Div(decimal.NewFromInt(2))
		out[i] = DonchianResult{Upper: hi, Middle: mid, Lower: lo}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Supertrend
// ────────────────────────────────────────────────────────────────────────────

type SupertrendResult struct {
	Value  decimal.Decimal
	IsLong bool
}

func Supertrend(candles []models.Candle, atrPeriod int, factor float64) []SupertrendResult {
	atr := ATR(candles, atrPeriod)
	if atr == nil {
		return nil
	}
	offset := len(candles) - len(atr)
	out := make([]SupertrendResult, len(atr))
	isLong := true

	factorD := decimal.NewFromFloat(factor)
	var prevUB, prevLB decimal.Decimal

	for i, a := range atr {
		ci := i + offset
		hl2 := candles[ci].High.Add(candles[ci].Low).Div(decimal.NewFromInt(2))
		ub := hl2.Add(factorD.Mul(a))
		lb := hl2.Sub(factorD.Mul(a))

		if i == 0 {
			prevUB = ub
			prevLB = lb
			out[i] = SupertrendResult{Value: lb, IsLong: true}
			continue
		}
		prevClose := candles[ci-1].Close
		if ub.LessThan(prevUB) || prevClose.GreaterThan(prevUB) {
			prevUB = ub
		}
		if lb.GreaterThan(prevLB) || prevClose.LessThan(prevLB) {
			prevLB = lb
		}
		if candles[ci].Close.GreaterThan(prevUB) {
			isLong = true
		} else if candles[ci].Close.LessThan(prevLB) {
			isLong = false
		}
		val := prevLB
		if !isLong {
			val = prevUB
		}
		out[i] = SupertrendResult{Value: val, IsLong: isLong}
	}
	return out
}
