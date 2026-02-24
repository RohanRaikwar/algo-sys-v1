// Package portfolio tracks positions, P&L, and portfolio-level metrics.
//
// It maintains a real-time view of all open positions, calculates unrealized
// P&L from latest market prices, and provides exposure summaries.
package portfolio

import (
	"sync"

	"trading-systemv1/internal/model"
)

// Position represents a single instrument position.
type Position struct {
	Token    string `json:"token"`
	Exchange string `json:"exchange"`
	Qty      int64  `json:"qty"`       // positive = long, negative = short
	AvgPrice int64  `json:"avg_price"` // average entry price in paise
	LastLTP  int64  `json:"last_ltp"`  // last traded price in paise
}

// UnrealizedPnL returns the unrealized P&L in paise.
func (p *Position) UnrealizedPnL() int64 {
	return (p.LastLTP - p.AvgPrice) * p.Qty
}

// Portfolio tracks all open positions.
type Portfolio struct {
	mu        sync.RWMutex
	positions map[string]*Position // key = "exchange:token"
}

// New creates a new empty Portfolio.
func New() *Portfolio {
	return &Portfolio{
		positions: make(map[string]*Position),
	}
}

// UpdatePrice updates the last traded price for a position.
func (pf *Portfolio) UpdatePrice(candle model.Candle) {
	key := candle.Exchange + ":" + candle.Token
	pf.mu.Lock()
	defer pf.mu.Unlock()
	if pos, ok := pf.positions[key]; ok {
		pos.LastLTP = candle.Close
	}
}

// GetPositions returns a snapshot of all positions.
func (pf *Portfolio) GetPositions() []Position {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	result := make([]Position, 0, len(pf.positions))
	for _, p := range pf.positions {
		result = append(result, *p)
	}
	return result
}

// TotalUnrealizedPnL returns the total unrealized P&L across all positions.
func (pf *Portfolio) TotalUnrealizedPnL() int64 {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	var total int64
	for _, p := range pf.positions {
		total += p.UnrealizedPnL()
	}
	return total
}
