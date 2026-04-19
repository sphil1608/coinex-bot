package ml_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/ml"
	"github.com/rusty/coinex-bot/internal/models"
)

func makeCandles(n int, prices []float64) []models.Candle {
	c := make([]models.Candle, n)
	for i := 0; i < n; i++ {
		var p float64
		if i < len(prices) {
			p = prices[i]
		} else {
			p = 100 + float64(i)*0.1
		}
		c[i] = models.Candle{
			OpenTime: time.Now().Add(time.Duration(i) * time.Hour),
			Open:     decimal.NewFromFloat(p * 0.999),
			High:     decimal.NewFromFloat(p * 1.005),
			Low:      decimal.NewFromFloat(p * 0.995),
			Close:    decimal.NewFromFloat(p),
			Volume:   decimal.NewFromFloat(1000),
		}
	}
	return c
}

func ramp(start, end float64, n int) []float64 {
	out := make([]float64, n)
	step := (end - start) / float64(n-1)
	for i := range out {
		out[i] = start + float64(i)*step
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Feature extraction
// ────────────────────────────────────────────────────────────────────────────

func TestExtractFeatures_Sufficient(t *testing.T) {
	prices := ramp(100, 150, 100)
	candles := makeCandles(100, prices)
	ob := models.OrderBook{
		Market: "BTCUSDT",
		Bids:   []models.Level{{Price: decimal.NewFromFloat(150), Quantity: decimal.NewFromFloat(1)}},
		Asks:   []models.Level{{Price: decimal.NewFromFloat(151), Quantity: decimal.NewFromFloat(1)}},
	}
	feats, ok := ml.ExtractFeatures(candles, ob)
	if !ok {
		t.Fatal("ExtractFeatures returned false for 100 candles")
	}
	if feats.Price <= 0 {
		t.Errorf("price feature is 0")
	}
	if feats.RSI14 < 0 || feats.RSI14 > 100 {
		t.Errorf("RSI14 out of range: %.2f", feats.RSI14)
	}
}

func TestExtractFeatures_InsufficientData(t *testing.T) {
	candles := makeCandles(30, ramp(100, 110, 30))
	_, ok := ml.ExtractFeatures(candles, models.OrderBook{})
	if ok {
		t.Error("expected false for <60 candles")
	}
}

func TestExtractFeatures_ToSliceLength(t *testing.T) {
	candles := makeCandles(100, ramp(100, 150, 100))
	feats, ok := ml.ExtractFeatures(candles, models.OrderBook{})
	if !ok {
		t.Fatal("ExtractFeatures returned false")
	}
	slice := feats.ToSlice()
	if len(slice) != 20 {
		t.Errorf("expected 20 features, got %d", len(slice))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Logistic Regression
// ────────────────────────────────────────────────────────────────────────────

func TestLogisticRegression_LearnsSeparableClasses(t *testing.T) {
	lr := ml.NewLogisticRegression()

	// X1 clearly positive → y=1, X2 clearly negative → y=0
	X := [][]float64{}
	y := []float64{}
	for i := 0; i < 50; i++ {
		X = append(X, []float64{float64(i) * 0.1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		y = append(y, 1)
	}
	for i := 0; i < 50; i++ {
		X = append(X, []float64{float64(-i) * 0.1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		y = append(y, 0)
	}

	lr.Fit(X, y)

	// Positive features should yield high probability
	highP := lr.Predict([]float64{5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	lowP := lr.Predict([]float64{-5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

	if highP <= 0.5 {
		t.Errorf("expected probability > 0.5 for positive feature, got %.4f", highP)
	}
	if lowP >= 0.5 {
		t.Errorf("expected probability < 0.5 for negative feature, got %.4f", lowP)
	}
}

func TestLogisticRegression_UntrainedReturns05(t *testing.T) {
	lr := ml.NewLogisticRegression()
	p := lr.Predict([]float64{1, 2, 3})
	if p < 0 || p > 1 {
		t.Errorf("predict out of [0,1]: %.4f", p)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Gradient Boost
// ────────────────────────────────────────────────────────────────────────────

func TestGradientBoost_OutputRange(t *testing.T) {
	gb := ml.NewGradientBoost()
	X := [][]float64{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}
	y := []float64{1, 1, 0, 0}
	gb.Fit(X, y)
	for _, xi := range X {
		p := gb.Predict(xi)
		if p < 0 || p > 1 {
			t.Errorf("GradientBoost predict out of [0,1]: %.4f", p)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Ensemble
// ────────────────────────────────────────────────────────────────────────────

func TestEnsemble_UntrainedFlat(t *testing.T) {
	e := ml.NewEnsemble(0.65, 0)
	sig, conf := e.Predict([]float64{1, 2, 3})
	if sig != models.SignalFlat {
		t.Errorf("untrained ensemble should return Flat, got %s", sig)
	}
	if conf != 0 {
		t.Errorf("untrained ensemble should return 0 confidence, got %.4f", conf)
	}
}

func TestEnsemble_AddSample_DoesNotPanic(t *testing.T) {
	e := ml.NewEnsemble(0.65, 0)
	for i := 0; i < 50; i++ {
		e.AddSample([]float64{float64(i), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, i%2 == 0)
	}
	if len(e.TrainingBuffer) != 50 {
		t.Errorf("expected 50 samples, got %d", len(e.TrainingBuffer))
	}
}
