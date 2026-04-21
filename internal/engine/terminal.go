package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/journal"
	"github.com/rusty/coinex-bot/internal/models"
)

// ── ANSI codes ────────────────────────────────────────────────────────────────

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiCyan    = "\033[36m"
	ansiMagenta = "\033[35m"
	ansiWhite   = "\033[97m"
	ansiClear   = "\033[2J\033[H"
)

// StartTerminalDash starts a goroutine that redraws the terminal every interval.
// Logs should be directed to a file before calling this so they don't clobber the UI.
func (e *Engine) StartTerminalDash(j *journal.Journal, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		// Draw immediately on start
		e.renderDash(j)
		for {
			select {
			case <-t.C:
				e.renderDash(j)
			case <-e.stopCh:
				return
			}
		}
	}()
}

func (e *Engine) renderDash(j *journal.Journal) {
	stats := j.Stats()
	trades := j.RecentTrades(20)
	unrealized := e.UnrealizedPnL()
	now := time.Now()

	mode := strings.ToUpper(e.cfg.Bot.Mode)
	modeColor := ansiGreen
	if mode == "LIVE" {
		modeColor = ansiRed + ansiBold
	}

	uptime := now.Sub(e.startedAt).Truncate(time.Second)

	var sb strings.Builder
	sb.WriteString(ansiClear)

	// ── Header ────────────────────────────────────────────────────────────────
	title := fmt.Sprintf("  CoinEx Bot  │  %s%-5s%s  │  %s%-12s%s│  %s  │  up %s",
		modeColor, mode, ansiCyan+ansiBold,
		ansiWhite+ansiBold, e.cfg.Bot.Market, ansiCyan+ansiBold,
		now.Format("02-Jan 15:04:05"),
		formatDuration(uptime),
	)
	titlePlain := fmt.Sprintf("  CoinEx Bot  │  %-5s  │  %-12s│  %s  │  up %s",
		mode, e.cfg.Bot.Market,
		now.Format("02-Jan 15:04:05"),
		formatDuration(uptime),
	)
	width := len(titlePlain) + 2
	border := strings.Repeat("═", width)

	sb.WriteString(ansiCyan + ansiBold)
	sb.WriteString("╔" + border + "╗\n")
	sb.WriteString("║" + title + ansiCyan + ansiBold + "  ║\n")
	sb.WriteString("╚" + border + "╝\n")
	sb.WriteString(ansiReset)

	// ── PnL summary ──────────────────────────────────────────────────────────
	realColor, realSym := pnlColorSym(stats.TotalPnL)
	unrColor, unrSym := pnlColorSym(unrealized)

	winRate := 0.0
	if stats.TotalTrades > 0 {
		winRate = float64(stats.Winners) / float64(stats.TotalTrades) * 100
	}
	wrColor := ansiYellow
	if winRate >= 55 {
		wrColor = ansiGreen
	} else if winRate < 45 && stats.TotalTrades > 0 {
		wrColor = ansiRed
	}

	sb.WriteString(fmt.Sprintf(
		"\n  Realized PnL: %s%s%s%+.2f%s   Unrealized: %s%s%s%+.2f%s   Win Rate: %s%.0f%%%s  (%dW / %dL)\n\n",
		realColor, ansiBold, realSym, decFloat(stats.TotalPnL), ansiReset,
		unrColor, ansiBold, unrSym, decFloat(unrealized), ansiReset,
		wrColor+ansiBold, winRate, ansiReset,
		stats.Winners, stats.Losers,
	))

	// ── Trade table ──────────────────────────────────────────────────────────
	colFmt := "  %-9s  %-12s  %-5s  %-12s  %-12s  %-11s  %s\n"
	sb.WriteString(ansiCyan + ansiBold)
	sb.WriteString(fmt.Sprintf(colFmt, "TIME", "MARKET", "SIDE", "ENTRY", "EXIT", "PNL", "REASON"))
	sb.WriteString(ansiDim + "  " + strings.Repeat("─", 78) + "\n" + ansiReset)

	if len(trades) == 0 {
		sb.WriteString(ansiDim + "  No closed trades yet.\n" + ansiReset)
	} else {
		for _, t := range trades {
			sideColor := ansiGreen
			sideLabel := "BUY"
			if t.Side == models.SideSell {
				sideColor = ansiRed
				sideLabel = "SELL"
			}
			pc, sym := pnlColorSym(t.PnL)
			reasonColor := ansiDim
			switch t.ExitReason {
			case "tp":
				reasonColor = ansiGreen
			case "sl":
				reasonColor = ansiRed
			}
			row := fmt.Sprintf(colFmt,
				t.ExitTime.Format("15:04:05"),
				t.Market,
				sideColor+ansiBold+sideLabel+ansiReset,
				t.EntryPrice.StringFixed(2),
				t.ExitPrice.StringFixed(2),
				fmt.Sprintf("%s%s%s%+.2f%s", pc, ansiBold, sym, decFloat(t.PnL), ansiReset),
				reasonColor+t.ExitReason+ansiReset,
			)
			sb.WriteString(row)
		}
	}

	// ── Open positions footer ─────────────────────────────────────────────────
	e.mu.RLock()
	openCount := len(e.openOrders)
	e.mu.RUnlock()

	openColor := ansiDim
	if openCount > 0 {
		openColor = ansiYellow + ansiBold
	}
	sb.WriteString(fmt.Sprintf(
		"\n  %sOpen positions: %d%s   %sProfit factor: %.2f%s\n",
		openColor, openCount, ansiReset,
		ansiDim, stats.ProfitFactor, ansiReset,
	))

	fmt.Print(sb.String())
}

// UnrealizedPnL sums notional PnL across all open orders using the live mid price.
func (e *Engine) UnrealizedPnL() decimal.Decimal {
	ob := e.lob.Snapshot()
	mid := ob.MidPrice()
	if mid.IsZero() {
		return decimal.Zero
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	total := decimal.Zero
	for _, o := range e.openOrders {
		if o.Price.IsZero() || o.Amount.IsZero() {
			continue
		}
		var diff decimal.Decimal
		if o.Side == models.SideBuy {
			diff = mid.Sub(o.Price)
		} else {
			diff = o.Price.Sub(mid)
		}
		total = total.Add(diff.Mul(o.Amount))
	}
	return total
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func pnlColorSym(v decimal.Decimal) (color, sym string) {
	f := decFloat(v)
	switch {
	case f > 0:
		return ansiGreen, "▲ $"
	case f < 0:
		return ansiRed, "▼ $"
	default:
		return ansiDim, "  $"
	}
}

func decFloat(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}
