package strategies

import (
	"context"
	"time"

	"github.com/rusty/coinex-bot/internal/ml"
	"github.com/rusty/coinex-bot/internal/models"
)

// MLEnsembleStrategy wraps the ML ensemble as a Strategy.
// It must be constructed with a pre-created Ensemble instance
// since training happens outside the Evaluate loop.
type MLEnsembleStrategy struct {
	ensemble *ml.Ensemble
}

func NewMLEnsembleStrategy(ensemble *ml.Ensemble) *MLEnsembleStrategy {
	s := &MLEnsembleStrategy{ensemble: ensemble}
	Register(s)
	return s
}

func (s *MLEnsembleStrategy) Name() string { return "ml_ensemble" }

func (s *MLEnsembleStrategy) Evaluate(ctx context.Context, candles []models.Candle, ob models.OrderBook) models.Signal {
	mkt := ""
	if len(candles) > 0 {
		mkt = candles[0].Timeframe
	}

	feats, ok := ml.ExtractFeatures(candles, ob)
	if !ok {
		return flat(s.Name(), mkt)
	}

	x := feats.ToSlice()
	sig, conf := s.ensemble.Predict(x)

	return models.Signal{
		Strategy:   s.Name(),
		Market:     mkt,
		Signal:     sig,
		Confidence: conf,
		Reason:     "ML ensemble prediction",
		Timestamp:  time.Now(),
	}
}
