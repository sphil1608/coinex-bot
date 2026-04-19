package strategies

// Exported strategy types for use by the optimizer and external packages.
// These are type aliases pointing to the private structs in all_strategies.go.

// RSIMeanRevertExported is the public-facing RSI mean reversion strategy.
type RSIMeanRevertExported = RSIMeanRevert

// EMACrossExported is the public-facing EMA cross strategy.
type EMACrossExported = EMACross

// IchimokuStrategyExported is the public-facing Ichimoku strategy.
type IchimokuStrategyExported = IchimokuStrategy

// BreakoutStrategyExported is the public-facing breakout strategy.
type BreakoutStrategyExported = BreakoutStrategy

// MACDCrossExported is the public-facing MACD cross strategy.
type MACDCrossExported = MACDCross
