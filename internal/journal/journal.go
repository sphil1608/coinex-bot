// Package journal provides a persistent, append-only trade log written as
// newline-delimited JSON (NDJSON). Each closed trade is a single line.
// The journal also maintains running P&L statistics in memory.
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// Trade record
// ────────────────────────────────────────────────────────────────────────────

type TradeRecord struct {
	ID           string           `json:"id"`
	Market       string           `json:"market"`
	MarketType   models.MarketType `json:"market_type"`
	Side         models.OrderSide  `json:"side"`
	Strategy     string           `json:"strategy"`
	EntryPrice   decimal.Decimal  `json:"entry_price"`
	ExitPrice    decimal.Decimal  `json:"exit_price"`
	Quantity     decimal.Decimal  `json:"quantity"`
	PnL          decimal.Decimal  `json:"pnl"`
	PnLPct       float64          `json:"pnl_pct"`
	Fee          decimal.Decimal  `json:"fee"`
	ExitReason   string           `json:"exit_reason"` // tp | sl | signal | manual
	EntryOrderID string           `json:"entry_order_id"`
	ExitOrderID  string           `json:"exit_order_id"`
	EntryTime    time.Time        `json:"entry_time"`
	ExitTime     time.Time        `json:"exit_time"`
	Duration     string           `json:"duration"`
	Paper        bool             `json:"paper"`
}

// ────────────────────────────────────────────────────────────────────────────
// Statistics
// ────────────────────────────────────────────────────────────────────────────

type Stats struct {
	TotalTrades    int             `json:"total_trades"`
	Winners        int             `json:"winners"`
	Losers         int             `json:"losers"`
	WinRate        float64         `json:"win_rate"`
	TotalPnL       decimal.Decimal `json:"total_pnl"`
	GrossProfit    decimal.Decimal `json:"gross_profit"`
	GrossLoss      decimal.Decimal `json:"gross_loss"`
	ProfitFactor   float64         `json:"profit_factor"`
	AvgWin         decimal.Decimal `json:"avg_win"`
	AvgLoss        decimal.Decimal `json:"avg_loss"`
	LargestWin     decimal.Decimal `json:"largest_win"`
	LargestLoss    decimal.Decimal `json:"largest_loss"`
	MaxConsecWins  int             `json:"max_consec_wins"`
	MaxConsecLoss  int             `json:"max_consec_loss"`
	AvgHoldTime    string          `json:"avg_hold_time"`
	StrategyBreakdown map[string]*StrategyStats `json:"strategy_breakdown"`
}

type StrategyStats struct {
	Trades   int             `json:"trades"`
	Winners  int             `json:"winners"`
	TotalPnL decimal.Decimal `json:"total_pnl"`
	WinRate  float64         `json:"win_rate"`
}

// ────────────────────────────────────────────────────────────────────────────
// Journal
// ────────────────────────────────────────────────────────────────────────────

type Journal struct {
	mu       sync.RWMutex
	path     string
	file     *os.File
	writer   *bufio.Writer
	trades   []TradeRecord
	stats    Stats
}

// Open opens (or creates) the journal at path and replays existing entries.
func Open(path string) (*Journal, error) {
	j := &Journal{
		path: path,
		stats: Stats{
			StrategyBreakdown: make(map[string]*StrategyStats),
		},
	}

	// Replay existing entries to rebuild stats
	if err := j.replay(); err != nil {
		return nil, fmt.Errorf("journal replay: %w", err)
	}

	// Open append-only
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	j.file = f
	j.writer = bufio.NewWriter(f)

	return j, nil
}

// Record writes a completed trade to disk and updates in-memory stats.
func (j *Journal) Record(t TradeRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	t.Duration = t.ExitTime.Sub(t.EntryTime).Round(time.Second).String()

	b, err := json.Marshal(t)
	if err != nil {
		return err
	}

	if _, err := j.writer.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := j.writer.Flush(); err != nil {
		return err
	}

	j.trades = append(j.trades, t)
	j.updateStats(t)
	return nil
}

// Stats returns a snapshot of current statistics.
func (j *Journal) Stats() Stats {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.stats
}

// RecentTrades returns the last n trades.
func (j *Journal) RecentTrades(n int) []TradeRecord {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if n > len(j.trades) {
		n = len(j.trades)
	}
	out := make([]TradeRecord, n)
	copy(out, j.trades[len(j.trades)-n:])
	return out
}

// AllTrades returns a copy of the full trade slice.
func (j *Journal) AllTrades() []TradeRecord {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]TradeRecord, len(j.trades))
	copy(out, j.trades)
	return out
}

// Close flushes and closes the underlying file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.writer != nil {
		_ = j.writer.Flush()
	}
	if j.file != nil {
		return j.file.Close()
	}
	return nil
}

// PrintSummary prints a formatted summary to stdout.
func (j *Journal) PrintSummary() {
	s := j.Stats()
	fmt.Println("\n╔══════════════════════════════════════════════╗")
	fmt.Println("║            TRADE JOURNAL SUMMARY             ║")
	fmt.Println("╠══════════════════════════════════════════════╣")
	fmt.Printf("║  Total Trades   : %-26d║\n", s.TotalTrades)
	fmt.Printf("║  Win / Loss     : %-4d / %-21d║\n", s.Winners, s.Losers)
	fmt.Printf("║  Win Rate       : %-26s║\n", fmt.Sprintf("%.1f%%", s.WinRate*100))
	fmt.Printf("║  Total P&L      : %-26s║\n", s.TotalPnL.StringFixed(4))
	fmt.Printf("║  Profit Factor  : %-26s║\n", fmt.Sprintf("%.3f", s.ProfitFactor))
	fmt.Printf("║  Avg Win        : %-26s║\n", s.AvgWin.StringFixed(4))
	fmt.Printf("║  Avg Loss       : %-26s║\n", s.AvgLoss.StringFixed(4))
	fmt.Printf("║  Largest Win    : %-26s║\n", s.LargestWin.StringFixed(4))
	fmt.Printf("║  Largest Loss   : %-26s║\n", s.LargestLoss.StringFixed(4))
	fmt.Printf("║  Max Consec W/L : %-4d / %-21d║\n", s.MaxConsecWins, s.MaxConsecLoss)
	fmt.Printf("║  Avg Hold Time  : %-26s║\n", s.AvgHoldTime)
	fmt.Println("╠══════════════════════════════════════════════╣")
	fmt.Println("║  Strategy Breakdown                          ║")
	for name, ss := range s.StrategyBreakdown {
		line := fmt.Sprintf("  %-16s %3d trades  %5.1f%% win  %s PnL",
			name, ss.Trades, ss.WinRate*100, ss.TotalPnL.StringFixed(2))
		fmt.Printf("║  %-44s║\n", line)
	}
	fmt.Println("╚══════════════════════════════════════════════╝")
}

// ────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ────────────────────────────────────────────────────────────────────────────

func (j *Journal) replay() error {
	f, err := os.Open(j.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var t TradeRecord
		if err := json.Unmarshal(sc.Bytes(), &t); err != nil {
			continue // skip corrupted lines
		}
		j.trades = append(j.trades, t)
		j.updateStats(t)
	}
	return sc.Err()
}

func (j *Journal) updateStats(t TradeRecord) {
	s := &j.stats
	s.TotalTrades++
	s.TotalPnL = s.TotalPnL.Add(t.PnL)

	if t.PnL.IsPositive() {
		s.Winners++
		s.GrossProfit = s.GrossProfit.Add(t.PnL)
		if t.PnL.GreaterThan(s.LargestWin) {
			s.LargestWin = t.PnL
		}
	} else {
		s.Losers++
		s.GrossLoss = s.GrossLoss.Add(t.PnL.Abs())
		if t.PnL.LessThan(s.LargestLoss) {
			s.LargestLoss = t.PnL
		}
	}

	if s.TotalTrades > 0 {
		s.WinRate = float64(s.Winners) / float64(s.TotalTrades)
	}
	if s.Winners > 0 {
		s.AvgWin = s.GrossProfit.Div(decimal.NewFromInt(int64(s.Winners)))
	}
	if s.Losers > 0 {
		s.AvgLoss = s.GrossLoss.Div(decimal.NewFromInt(int64(s.Losers))).Neg()
	}
	grossLossF, _ := s.GrossLoss.Float64()
	grossProfitF, _ := s.GrossProfit.Float64()
	if grossLossF > 0 {
		s.ProfitFactor = grossProfitF / grossLossF
	}

	// Consecutive wins/losses
	consecW, consecL, maxW, maxL := 0, 0, 0, 0
	for _, tr := range j.trades {
		if tr.PnL.IsPositive() {
			consecW++
			consecL = 0
		} else {
			consecL++
			consecW = 0
		}
		if consecW > maxW { maxW = consecW }
		if consecL > maxL { maxL = consecL }
	}
	s.MaxConsecWins = maxW
	s.MaxConsecLoss = maxL

	// Average hold time
	var totalNs float64
	for _, tr := range j.trades {
		totalNs += float64(tr.ExitTime.Sub(tr.EntryTime))
	}
	if s.TotalTrades > 0 {
		avg := time.Duration(totalNs / float64(s.TotalTrades))
		s.AvgHoldTime = avg.Round(time.Second).String()
	}

	// Strategy breakdown
	if _, ok := s.StrategyBreakdown[t.Strategy]; !ok {
		s.StrategyBreakdown[t.Strategy] = &StrategyStats{}
	}
	ss := s.StrategyBreakdown[t.Strategy]
	ss.Trades++
	ss.TotalPnL = ss.TotalPnL.Add(t.PnL)
	if t.PnL.IsPositive() {
		ss.Winners++
	}
	if ss.Trades > 0 {
		ss.WinRate = float64(ss.Winners) / float64(ss.Trades)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// CSV export
// ────────────────────────────────────────────────────────────────────────────

func (j *Journal) ExportCSV(path string) error {
	j.mu.RLock()
	defer j.mu.RUnlock()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "id,market,market_type,side,strategy,entry_price,exit_price,quantity,pnl,pnl_pct,fee,exit_reason,entry_time,exit_time,duration,paper")
	for _, t := range j.trades {
		fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%.4f,%s,%s,%s,%s,%s,%v\n",
			t.ID, t.Market, t.MarketType, t.Side, t.Strategy,
			t.EntryPrice.StringFixed(8), t.ExitPrice.StringFixed(8),
			t.Quantity.StringFixed(8), t.PnL.StringFixed(8),
			t.PnLPct, t.Fee.StringFixed(8), t.ExitReason,
			t.EntryTime.Format(time.RFC3339), t.ExitTime.Format(time.RFC3339),
			t.Duration, t.Paper,
		)
	}
	return w.Flush()
}

// DailyPnL returns a map of date → total PnL for that day.
func (j *Journal) DailyPnL() map[string]decimal.Decimal {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make(map[string]decimal.Decimal)
	for _, t := range j.trades {
		day := t.ExitTime.Format("2006-01-02")
		out[day] = out[day].Add(t.PnL)
	}
	return out
}

// DrawdownSeries computes the running drawdown series from equity curve.
func (j *Journal) DrawdownSeries(initialCapital decimal.Decimal) []float64 {
	j.mu.RLock()
	defer j.mu.RUnlock()

	equity := initialCapital
	peak := equity
	out := make([]float64, len(j.trades))

	for i, t := range j.trades {
		equity = equity.Add(t.PnL)
		if equity.GreaterThan(peak) {
			peak = equity
		}
		peakF, _ := peak.Float64()
		eqF, _ := equity.Float64()
		if peakF > 0 {
			out[i] = (peakF - eqF) / peakF
		}
	}
	return out
}

// MaxDrawdown returns the maximum peak-to-trough drawdown fraction.
func (j *Journal) MaxDrawdown(initialCapital decimal.Decimal) float64 {
	dd := j.DrawdownSeries(initialCapital)
	max := 0.0
	for _, v := range dd {
		if v > max {
			max = v
		}
	}
	return max
}

// SharpeRatio computes annualised Sharpe from daily P&L (rf=0).
func (j *Journal) SharpeRatio(initialCapital decimal.Decimal) float64 {
	dailyMap := j.DailyPnL()
	if len(dailyMap) < 2 {
		return 0
	}
	cap, _ := initialCapital.Float64()
	returns := make([]float64, 0, len(dailyMap))
	for _, pnl := range dailyMap {
		f, _ := pnl.Float64()
		returns = append(returns, f/cap)
	}
	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))
	var variance float64
	for _, r := range returns {
		d := r - mean
		variance += d * d
	}
	std := math.Sqrt(variance / float64(len(returns)))
	if std == 0 {
		return 0
	}
	return mean / std * math.Sqrt(252) // annualise daily
}
