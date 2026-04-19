// Package ml provides a machine-learning layer that can augment or replace
// classic technical strategies. It exposes an Ensemble type that:
//   - Engineers features from raw candle + orderbook data
//   - Trains a logistic regression AND a simple gradient-boosted tree
//   - Combines predictions into a confidence-weighted signal
//
// Pure-Go implementation – no CGO / Python / ONNX runtime required.
// The gradient boost uses a hand-rolled additive model with decision stumps,
// keeping the dependency footprint minimal.
//
// To plug in a production model (XGBoost, TensorFlow Lite, etc.) simply
// replace the Predictor interface implementations below.
package ml

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"

	ind "github.com/rusty/coinex-bot/internal/indicators"
	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// Feature vector
// ────────────────────────────────────────────────────────────────────────────

// Features extracted at each candle bar.
type Features struct {
	RSI14        float64
	MACDHist     float64
	BBWidth      float64 // (upper-lower)/middle
	EMA9         float64
	EMA21        float64
	ATR14        float64
	VWAP         float64
	Price        float64
	BidAskImb    float64 // order-book imbalance
	ReturnN1     float64 // 1-bar return
	ReturnN5     float64 // 5-bar return
	ReturnN10    float64 // 10-bar return
	Volume       float64
	VolumeRatio  float64 // volume / 20-bar avg volume
	TKDiff       float64 // ichimoku tenkan-kijun diff
	CloudDir     float64 // +1 above cloud, -1 below, 0 inside
	StochK       float64
	ADX          float64
	WilliamsR    float64
	SARLong      float64 // 1 if SAR is long, 0 short
}

func (f Features) ToSlice() []float64 {
	return []float64{
		f.RSI14, f.MACDHist, f.BBWidth, f.EMA9, f.EMA21,
		f.ATR14, f.VWAP, f.Price, f.BidAskImb,
		f.ReturnN1, f.ReturnN5, f.ReturnN10,
		f.Volume, f.VolumeRatio, f.TKDiff, f.CloudDir,
		f.StochK, f.ADX, f.WilliamsR, f.SARLong,
	}
}

// ExtractFeatures builds a feature vector from the tail of candles + live order book.
func ExtractFeatures(candles []models.Candle, ob models.OrderBook) (Features, bool) {
	if len(candles) < 60 {
		return Features{}, false
	}

	f := Features{}
	price, _ := candles[len(candles)-1].Close.Float64()
	f.Price = price

	// RSI
	rsi := ind.RSI(candles, 14)
	if len(rsi) > 0 {
		f.RSI14 = rsi[len(rsi)-1]
	}

	// MACD histogram
	macdRes := ind.MACD(candles, 12, 26, 9)
	if len(macdRes) > 0 {
		f.MACDHist, _ = macdRes[len(macdRes)-1].Histogram.Float64()
	}

	// Bollinger width
	bb := ind.BollingerBands(candles, 20, 2.0)
	if len(bb) > 0 {
		cur := bb[len(bb)-1]
		midF, _ := cur.Middle.Float64()
		if midF > 0 {
			upF, _ := cur.Upper.Float64()
			loF, _ := cur.Lower.Float64()
			f.BBWidth = (upF - loF) / midF
		}
	}

	// EMA 9 / 21
	cl := make([]decimal.Decimal, len(candles))
	for i, c := range candles {
		cl[i] = c.Close
	}
	ema9 := ind.EMA(cl, 9)
	ema21 := ind.EMA(cl, 21)
	if len(ema9) > 0 {
		f.EMA9, _ = ema9[len(ema9)-1].Float64()
	}
	if len(ema21) > 0 {
		f.EMA21, _ = ema21[len(ema21)-1].Float64()
	}

	// ATR
	atr := ind.ATR(candles, 14)
	if len(atr) > 0 {
		f.ATR14, _ = atr[len(atr)-1].Float64()
	}

	// VWAP
	vwap := ind.VWAP(candles)
	f.VWAP, _ = vwap.Float64()

	// Order book imbalance
	f.BidAskImb, _ = ob.BidAskImbalance(10).Float64()

	// Returns
	if len(candles) > 1 {
		p1, _ := candles[len(candles)-2].Close.Float64()
		if p1 != 0 { f.ReturnN1 = (price - p1) / p1 }
	}
	if len(candles) > 5 {
		p5, _ := candles[len(candles)-6].Close.Float64()
		if p5 != 0 { f.ReturnN5 = (price - p5) / p5 }
	}
	if len(candles) > 10 {
		p10, _ := candles[len(candles)-11].Close.Float64()
		if p10 != 0 { f.ReturnN10 = (price - p10) / p10 }
	}

	// Volume ratio
	var volSum float64
	window := 20
	for i := len(candles) - window; i < len(candles); i++ {
		v, _ := candles[i].Volume.Float64()
		volSum += v
	}
	avgVol := volSum / float64(window)
	curVol, _ := candles[len(candles)-1].Volume.Float64()
	f.Volume = curVol
	if avgVol > 0 {
		f.VolumeRatio = curVol / avgVol
	}

	// Ichimoku TK diff and cloud direction
	ichi := ind.Ichimoku(candles, 9, 26, 52)
	if len(ichi) > 0 {
		cur := ichi[len(ichi)-1]
		tkDiff := cur.Tenkan.Sub(cur.Kijun)
		f.TKDiff, _ = tkDiff.Float64()
		cloudTop := cur.SenkouA
		if cur.SenkouB.GreaterThan(cloudTop) { cloudTop = cur.SenkouB }
		cloudBot := cur.SenkouA
		if cur.SenkouB.LessThan(cloudBot) { cloudBot = cur.SenkouB }
		priceD := candles[len(candles)-1].Close
		if priceD.GreaterThan(cloudTop) { f.CloudDir = 1 } else if priceD.LessThan(cloudBot) { f.CloudDir = -1 }
	}

	// Stoch K
	stoch := ind.StochRSI(candles, 14, 3, 3)
	if len(stoch) > 0 { f.StochK = stoch[len(stoch)-1].K }

	// ADX
	adx := ind.ADX(candles, 14)
	if len(adx) > 0 { f.ADX = adx[len(adx)-1].ADX }

	// Williams R
	wr := ind.WilliamsR(candles, 14)
	if len(wr) > 0 { f.WilliamsR = wr[len(wr)-1] }

	// Parabolic SAR
	sar := ind.ParabolicSAR(candles, 0.02, 0.2)
	if len(sar) > 0 && sar[len(sar)-1].IsLong { f.SARLong = 1 }

	return f, true
}

// ────────────────────────────────────────────────────────────────────────────
// Predictor interface
// ────────────────────────────────────────────────────────────────────────────

type Predictor interface {
	Fit(X [][]float64, y []float64)
	Predict(x []float64) float64 // probability of going long [0,1]
	Name() string
}

// ────────────────────────────────────────────────────────────────────────────
// Logistic Regression (gradient descent)
// ────────────────────────────────────────────────────────────────────────────

type LogisticRegression struct {
	weights []float64
	bias    float64
	LR      float64
	Epochs  int
}

func NewLogisticRegression() *LogisticRegression {
	return &LogisticRegression{LR: 0.01, Epochs: 500}
}

func (lr *LogisticRegression) Name() string { return "logistic" }

func sigmoid(z float64) float64 {
	return 1.0 / (1.0 + math.Exp(-z))
}

func (lr *LogisticRegression) Fit(X [][]float64, y []float64) {
	if len(X) == 0 {
		return
	}
	d := len(X[0])
	if lr.weights == nil {
		lr.weights = make([]float64, d)
	}
	n := len(X)
	for epoch := 0; epoch < lr.Epochs; epoch++ {
		dw := make([]float64, d)
		var db float64
		for i, xi := range X {
			z := lr.bias
			for j, xij := range xi {
				z += lr.weights[j] * xij
			}
			pred := sigmoid(z)
			err := pred - y[i]
			for j := range dw {
				dw[j] += err * xi[j]
			}
			db += err
		}
		for j := range lr.weights {
			lr.weights[j] -= lr.LR * dw[j] / float64(n)
		}
		lr.bias -= lr.LR * db / float64(n)
	}
}

func (lr *LogisticRegression) Predict(x []float64) float64 {
	if lr.weights == nil {
		return 0.5
	}
	z := lr.bias
	for j, xj := range x {
		if j < len(lr.weights) {
			z += lr.weights[j] * xj
		}
	}
	return sigmoid(z)
}

// ────────────────────────────────────────────────────────────────────────────
// Gradient Boosted Stumps
// ────────────────────────────────────────────────────────────────────────────

type stump struct {
	feature   int
	threshold float64
	leftVal   float64
	rightVal  float64
}

func (s *stump) predict(x []float64) float64 {
	if x[s.feature] <= s.threshold {
		return s.leftVal
	}
	return s.rightVal
}

type GradientBoost struct {
	trees      []stump
	lr         float64
	nEstimators int
}

func NewGradientBoost() *GradientBoost {
	return &GradientBoost{lr: 0.1, nEstimators: 50}
}

func (gb *GradientBoost) Name() string { return "gradient_boost" }

func (gb *GradientBoost) Fit(X [][]float64, y []float64) {
	n := len(X)
	if n == 0 {
		return
	}
	// init predictions at 0.5
	preds := make([]float64, n)
	for i := range preds {
		preds[i] = 0.5
	}
	gb.trees = nil

	for t := 0; t < gb.nEstimators; t++ {
		// pseudo-residuals (log-loss gradient)
		residuals := make([]float64, n)
		for i := range residuals {
			residuals[i] = y[i] - preds[i]
		}
		// fit best stump to residuals
		best := gb.bestStump(X, residuals)
		gb.trees = append(gb.trees, best)
		// update predictions
		for i, xi := range X {
			preds[i] += gb.lr * best.predict(xi)
			// clamp
			if preds[i] > 1 { preds[i] = 1 }
			if preds[i] < 0 { preds[i] = 0 }
		}
	}
}

func (gb *GradientBoost) bestStump(X [][]float64, residuals []float64) stump {
	if len(X) == 0 {
		return stump{}
	}
	d := len(X[0])
	bestGain := math.Inf(-1)
	best := stump{}

	for f := 0; f < d; f++ {
		// try a few thresholds
		var vals []float64
		for _, xi := range X {
			vals = append(vals, xi[f])
		}
		// use mean as threshold
		var sum float64
		for _, v := range vals {
			sum += v
		}
		thresh := sum / float64(len(vals))

		var leftSum, rightSum float64
		var leftCount, rightCount int
		for i, xi := range X {
			if xi[f] <= thresh {
				leftSum += residuals[i]
				leftCount++
			} else {
				rightSum += residuals[i]
				rightCount++
			}
		}
		var lv, rv float64
		if leftCount > 0 { lv = leftSum / float64(leftCount) }
		if rightCount > 0 { rv = rightSum / float64(rightCount) }

		// gain = sum of squared reductions
		var gain float64
		for i, xi := range X {
			var pred float64
			if xi[f] <= thresh { pred = lv } else { pred = rv }
			r := residuals[i] - pred
			gain -= r * r
		}
		if gain > bestGain {
			bestGain = gain
			best = stump{feature: f, threshold: thresh, leftVal: lv, rightVal: rv}
		}
	}
	return best
}

func (gb *GradientBoost) Predict(x []float64) float64 {
	pred := 0.5
	for _, t := range gb.trees {
		pred += gb.lr * t.predict(x)
	}
	if pred > 1 { return 1 }
	if pred < 0 { return 0 }
	return pred
}

// ────────────────────────────────────────────────────────────────────────────
// Ensemble
// ────────────────────────────────────────────────────────────────────────────

type Ensemble struct {
	mu             sync.RWMutex
	models         []Predictor
	trained        bool
	TrainingBuffer []featureSample
	MinSamples     int
	MinConfidence  float64
	lastRetrain    time.Time
	retrainInterval time.Duration
}

type featureSample struct {
	x []float64
	y float64 // 1 = long was profitable, 0 = not
}

func NewEnsemble(minConf float64, retrainInterval time.Duration) *Ensemble {
	return &Ensemble{
		models:          []Predictor{NewLogisticRegression(), NewGradientBoost()},
		MinSamples:      200,
		MinConfidence:   minConf,
		retrainInterval: retrainInterval,
	}
}

// AddSample adds a labelled training sample (called after trade outcome is known).
func (e *Ensemble) AddSample(x []float64, profitable bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	y := 0.0
	if profitable { y = 1.0 }
	e.TrainingBuffer = append(e.TrainingBuffer, featureSample{x: x, y: y})
	// rolling window
	if len(e.TrainingBuffer) > 5000 {
		e.TrainingBuffer = e.TrainingBuffer[len(e.TrainingBuffer)-5000:]
	}
}

// MaybeRetrain retrains models if enough data exists and interval has elapsed.
func (e *Ensemble) MaybeRetrain(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.TrainingBuffer) < e.MinSamples { return }
	if time.Since(e.lastRetrain) < e.retrainInterval { return }
	log.Info().Msg("ML ensemble: retraining models")
	X := make([][]float64, len(e.TrainingBuffer))
	y := make([]float64, len(e.TrainingBuffer))
	for i, s := range e.TrainingBuffer {
		X[i] = s.x
		y[i] = s.y
	}
	for _, m := range e.models {
		m.Fit(X, y)
	}
	e.trained = true
	e.lastRetrain = time.Now()
	log.Info().Msg("ML ensemble: retrain complete")
}

// Predict returns (SignalType, confidence). Returns SignalFlat if untrained or low confidence.
func (e *Ensemble) Predict(x []float64) (models.SignalType, float64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.trained {
		return models.SignalFlat, 0
	}
	var sum float64
	for _, m := range e.models {
		sum += m.Predict(x)
	}
	prob := sum / float64(len(e.models))

	if prob > e.MinConfidence {
		return models.SignalLong, prob
	}
	if prob < 1-e.MinConfidence {
		return models.SignalShort, 1 - prob
	}
	return models.SignalFlat, 0
}
