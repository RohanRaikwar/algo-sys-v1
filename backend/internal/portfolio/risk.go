package portfolio

import (
	"log"
	"sync"
)

// RiskLimits defines configurable risk management thresholds.
type RiskLimits struct {
	MaxPositionSize  int64   `json:"max_position_size"`  // max qty per instrument
	MaxDailyLoss     int64   `json:"max_daily_loss"`     // max daily loss in paise
	MaxOpenPositions int     `json:"max_open_positions"` // max number of concurrent positions
	MaxExposure      int64   `json:"max_exposure"`       // max total exposure in paise
	MaxDrawdownPct   float64 `json:"max_drawdown_pct"`   // max drawdown percentage (0-100)
}

// DefaultRiskLimits returns conservative default limits.
func DefaultRiskLimits() RiskLimits {
	return RiskLimits{
		MaxPositionSize:  100,
		MaxDailyLoss:     500000, // ₹5,000
		MaxOpenPositions: 5,
		MaxExposure:      10000000, // ₹1,00,000
		MaxDrawdownPct:   5.0,
	}
}

// RiskManager validates trades against risk limits and tracks equity.
type RiskManager struct {
	mu        sync.RWMutex
	limits    RiskLimits
	portfolio *Portfolio

	dailyPnL   int64
	equity     int64
	peakEquity int64
}

// NewRiskManager creates a RiskManager with the given limits, portfolio, and starting equity.
func NewRiskManager(limits RiskLimits, pf *Portfolio, initialEquity int64) *RiskManager {
	return &RiskManager{
		limits:     limits,
		portfolio:  pf,
		equity:     initialEquity,
		peakEquity: initialEquity,
	}
}

// CanTrade checks if a new trade would violate any risk limits.
// Returns true if the trade is allowed, false with a reason if not.
func (rm *RiskManager) CanTrade(token, exchange string, qty int64) (bool, string) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	positions := rm.portfolio.GetPositions()

	// Check max open positions
	key := exchange + ":" + token
	isNew := true
	for _, pos := range positions {
		if pos.Exchange+":"+pos.Token == key {
			isNew = false
			break
		}
	}
	if isNew && len(positions) >= rm.limits.MaxOpenPositions {
		return false, "max open positions reached"
	}

	// Check position size
	if qty > rm.limits.MaxPositionSize || qty < -rm.limits.MaxPositionSize {
		return false, "position size exceeds limit"
	}

	// Check daily loss
	if rm.dailyPnL < -rm.limits.MaxDailyLoss {
		return false, "max daily loss reached"
	}

	// Check drawdown
	if rm.peakEquity > 0 {
		drawdown := float64(rm.peakEquity-rm.equity) / float64(rm.peakEquity) * 100
		if drawdown > rm.limits.MaxDrawdownPct {
			return false, "max drawdown exceeded"
		}
	}

	return true, ""
}

// RecordPnL updates daily P&L and equity tracking.
func (rm *RiskManager) RecordPnL(pnl int64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.dailyPnL += pnl
	rm.equity += pnl
	if rm.equity > rm.peakEquity {
		rm.peakEquity = rm.equity
	}

	log.Printf("[risk] daily P&L: %d, equity: %d, peak: %d", rm.dailyPnL, rm.equity, rm.peakEquity)
}

// ResetDaily resets the daily P&L counter (call at market open).
func (rm *RiskManager) ResetDaily() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.dailyPnL = 0
}

// GetStatus returns current risk status.
func (rm *RiskManager) GetStatus() map[string]interface{} {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	drawdown := 0.0
	if rm.peakEquity > 0 {
		drawdown = float64(rm.peakEquity-rm.equity) / float64(rm.peakEquity) * 100
	}

	return map[string]interface{}{
		"daily_pnl":    rm.dailyPnL,
		"equity":       rm.equity,
		"peak_equity":  rm.peakEquity,
		"drawdown_pct": drawdown,
		"limits":       rm.limits,
	}
}
