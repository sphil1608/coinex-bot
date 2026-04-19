// Package position provides an in-memory position tracker.
// It prevents re-entering an already-open position on the same market/side,
// tracks unrealised P&L in real time, and enforces max portfolio exposure.
package position

import (
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rusty/coinex-bot/internal/models"
)

// ────────────────────────────────────────────────────────────────────────────
// OpenPosition
// ────────────────────────────────────────────────────────────────────────────

type OpenPosition struct {
	ID          string
	Market      string
	MarketType  models.MarketType
	Side        models.OrderSide
	Strategy    string
	EntryPrice  decimal.Decimal
	Quantity    decimal.Decimal
	StopLoss    decimal.Decimal
	TakeProfit  decimal.Decimal
	EntryOrderID string
	OpenedAt    time.Time

	// Updated in real-time
	CurrentPrice decimal.Decimal
	UnrealizedPL decimal.Decimal
	UnrealPLPct  float64
}

func (p *OpenPosition) Key() string {
	return p.Market + ":" + string(p.Side)
}

func (p *OpenPosition) UpdatePrice(price decimal.Decimal) {
	p.CurrentPrice = price
	if p.EntryPrice.IsZero() {
		return
	}
	if p.Side == models.SideBuy {
		p.UnrealizedPL = price.Sub(p.EntryPrice).Mul(p.Quantity)
	} else {
		p.UnrealizedPL = p.EntryPrice.Sub(price).Mul(p.Quantity)
	}
	entryF, _ := p.EntryPrice.Float64()
	if entryF > 0 {
		pnlF, _ := p.UnrealizedPL.Float64()
		qtyF, _ := p.Quantity.Float64()
		p.UnrealPLPct = pnlF / (entryF * qtyF)
	}
}

func (p *OpenPosition) ShouldStopLoss() bool {
	if p.StopLoss.IsZero() || p.CurrentPrice.IsZero() {
		return false
	}
	if p.Side == models.SideBuy {
		return p.CurrentPrice.LessThanOrEqual(p.StopLoss)
	}
	return p.CurrentPrice.GreaterThanOrEqual(p.StopLoss)
}

func (p *OpenPosition) ShouldTakeProfit() bool {
	if p.TakeProfit.IsZero() || p.CurrentPrice.IsZero() {
		return false
	}
	if p.Side == models.SideBuy {
		return p.CurrentPrice.GreaterThanOrEqual(p.TakeProfit)
	}
	return p.CurrentPrice.LessThanOrEqual(p.TakeProfit)
}

// ────────────────────────────────────────────────────────────────────────────
// Manager
// ────────────────────────────────────────────────────────────────────────────

type Manager struct {
	mu          sync.RWMutex
	positions   map[string]*OpenPosition // key = market:side
	maxPositions int
	maxExposurePct float64 // max fraction of capital in open positions
}

func NewManager(maxPositions int, maxExposurePct float64) *Manager {
	return &Manager{
		positions:      make(map[string]*OpenPosition),
		maxPositions:   maxPositions,
		maxExposurePct: maxExposurePct,
	}
}

// CanOpen returns true if a new position can be opened.
func (m *Manager) CanOpen(market string, side models.OrderSide) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := market + ":" + string(side)
	if _, exists := m.positions[key]; exists {
		return false, fmt.Sprintf("position already open for %s %s", market, side)
	}
	if len(m.positions) >= m.maxPositions {
		return false, fmt.Sprintf("max open positions (%d) reached", m.maxPositions)
	}
	return true, ""
}

// Open registers a new position. Returns error if CanOpen would fail.
func (m *Manager) Open(p *OpenPosition) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := p.Key()
	if _, exists := m.positions[key]; exists {
		return fmt.Errorf("position already open: %s", key)
	}
	if len(m.positions) >= m.maxPositions {
		return fmt.Errorf("max positions reached")
	}
	m.positions[key] = p
	return nil
}

// Close removes the position and returns it. Returns nil if not found.
func (m *Manager) Close(market string, side models.OrderSide) *OpenPosition {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := market + ":" + string(side)
	p, ok := m.positions[key]
	if !ok {
		return nil
	}
	delete(m.positions, key)
	return p
}

// Get returns the open position for market+side, or nil.
func (m *Manager) Get(market string, side models.OrderSide) *OpenPosition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.positions[market+":"+string(side)]
}

// All returns a snapshot of all open positions.
func (m *Manager) All() []*OpenPosition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*OpenPosition, 0, len(m.positions))
	for _, p := range m.positions {
		cp := *p
		out = append(out, &cp)
	}
	return out
}

// Count returns the number of open positions.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.positions)
}

// UpdatePrices updates the current price for all positions on a given market.
func (m *Manager) UpdatePrices(market string, price decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.positions {
		if p.Market == market {
			p.UpdatePrice(price)
		}
	}
}

// TriggeredExits returns all positions that have hit SL or TP.
func (m *Manager) TriggeredExits() ([]*OpenPosition, []*OpenPosition) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var slHit, tpHit []*OpenPosition
	for _, p := range m.positions {
		if p.ShouldStopLoss() {
			cp := *p
			slHit = append(slHit, &cp)
		} else if p.ShouldTakeProfit() {
			cp := *p
			tpHit = append(tpHit, &cp)
		}
	}
	return slHit, tpHit
}

// TotalUnrealizedPL sums unrealized P&L across all open positions.
func (m *Manager) TotalUnrealizedPL() decimal.Decimal {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := decimal.Zero
	for _, p := range m.positions {
		total = total.Add(p.UnrealizedPL)
	}
	return total
}

// TotalExposure returns the total notional value of open positions.
func (m *Manager) TotalExposure() decimal.Decimal {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := decimal.Zero
	for _, p := range m.positions {
		total = total.Add(p.EntryPrice.Mul(p.Quantity))
	}
	return total
}

// ExceedsExposure returns true if adding newExposure would breach the cap.
func (m *Manager) ExceedsExposure(newExposure, totalCapital decimal.Decimal) bool {
	if m.maxExposurePct <= 0 || totalCapital.IsZero() {
		return false
	}
	current := m.TotalExposure()
	newTotal := current.Add(newExposure)
	capF, _ := totalCapital.Float64()
	newF, _ := newTotal.Float64()
	return newF/capF > m.maxExposurePct
}
