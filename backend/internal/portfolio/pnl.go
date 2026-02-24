package portfolio

import (
	"sync"
	"time"
)

// Trade represents a completed trade for P&L calculation.
type Trade struct {
	Token     string    `json:"token"`
	Exchange  string    `json:"exchange"`
	Action    string    `json:"action"` // BUY or SELL
	Qty       int64     `json:"qty"`
	Price     int64     `json:"price"` // in paise
	Timestamp time.Time `json:"timestamp"`
}

// PnLTracker tracks realized and unrealized P&L.
type PnLTracker struct {
	mu     sync.RWMutex
	trades []Trade

	// Realized P&L from closed positions (in paise)
	realizedPnL int64

	// Per-token cost basis for P&L calculation
	costBasis map[string]costEntry
}

type costEntry struct {
	Qty      int64
	AvgPrice int64 // in paise
}

// NewPnLTracker creates a new P&L tracker.
func NewPnLTracker() *PnLTracker {
	return &PnLTracker{
		trades:    make([]Trade, 0, 500),
		costBasis: make(map[string]costEntry),
	}
}

// RecordTrade records a trade and updates realized P&L.
func (p *PnLTracker) RecordTrade(trade Trade) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.trades = append(p.trades, trade)
	key := trade.Exchange + ":" + trade.Token
	entry := p.costBasis[key]

	var realizedPnL int64

	if trade.Action == "BUY" {
		// Increase position
		if entry.Qty == 0 {
			entry.Qty = trade.Qty
			entry.AvgPrice = trade.Price
		} else {
			// Weighted average price
			totalCost := entry.AvgPrice*entry.Qty + trade.Price*trade.Qty
			entry.Qty += trade.Qty
			if entry.Qty > 0 {
				entry.AvgPrice = totalCost / entry.Qty
			}
		}
	} else {
		// Reduce position — calculate realized P&L
		sellQty := trade.Qty
		if sellQty > entry.Qty {
			sellQty = entry.Qty
		}
		realizedPnL = (trade.Price - entry.AvgPrice) * sellQty
		entry.Qty -= sellQty
		if entry.Qty <= 0 {
			entry.Qty = 0
			entry.AvgPrice = 0
		}
		p.realizedPnL += realizedPnL
	}

	p.costBasis[key] = entry
	return realizedPnL
}

// GetRealizedPnL returns total realized P&L in paise.
func (p *PnLTracker) GetRealizedPnL() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.realizedPnL
}

// GetUnrealizedPnL calculates unrealized P&L from current prices.
// currentPrices maps "exchange:token" -> latest price in paise.
func (p *PnLTracker) GetUnrealizedPnL(currentPrices map[string]int64) int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var unrealized int64
	for key, entry := range p.costBasis {
		if entry.Qty <= 0 {
			continue
		}
		if price, ok := currentPrices[key]; ok {
			unrealized += (price - entry.AvgPrice) * entry.Qty
		}
	}
	return unrealized
}

// GetTrades returns a snapshot of all trades.
func (p *PnLTracker) GetTrades() []Trade {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cp := make([]Trade, len(p.trades))
	copy(cp, p.trades)
	return cp
}

// Summary returns a P&L summary.
type PnLSummary struct {
	RealizedPnL   int64 `json:"realized_pnl"`
	UnrealizedPnL int64 `json:"unrealized_pnl"`
	TotalPnL      int64 `json:"total_pnl"`
	TotalTrades   int   `json:"total_trades"`
	OpenPositions int   `json:"open_positions"`
}

// GetSummary returns the current P&L summary.
func (p *PnLTracker) GetSummary(currentPrices map[string]int64) PnLSummary {
	p.mu.RLock()
	defer p.mu.RUnlock()

	unrealized := int64(0)
	openPositions := 0
	for key, entry := range p.costBasis {
		if entry.Qty <= 0 {
			continue
		}
		openPositions++
		if price, ok := currentPrices[key]; ok {
			unrealized += (price - entry.AvgPrice) * entry.Qty
		}
	}

	return PnLSummary{
		RealizedPnL:   p.realizedPnL,
		UnrealizedPnL: unrealized,
		TotalPnL:      p.realizedPnL + unrealized,
		TotalTrades:   len(p.trades),
		OpenPositions: openPositions,
	}
}
