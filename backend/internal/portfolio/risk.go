package portfolio

// RiskLimits defines configurable risk management thresholds.
type RiskLimits struct {
	MaxPositionSize  int64 `json:"max_position_size"`  // max qty per instrument
	MaxDailyLoss     int64 `json:"max_daily_loss"`     // max daily loss in paise
	MaxOpenPositions int   `json:"max_open_positions"` // max number of concurrent positions
	MaxExposure      int64 `json:"max_exposure"`       // max total exposure in paise
}

// DefaultRiskLimits returns conservative default limits.
func DefaultRiskLimits() RiskLimits {
	return RiskLimits{
		MaxPositionSize:  100,
		MaxDailyLoss:     500000, // ₹5,000
		MaxOpenPositions: 5,
		MaxExposure:      10000000, // ₹1,00,000
	}
}

// RiskManager validates trades against risk limits.
type RiskManager struct {
	limits    RiskLimits
	portfolio *Portfolio
}

// NewRiskManager creates a RiskManager with the given limits and portfolio.
func NewRiskManager(limits RiskLimits, pf *Portfolio) *RiskManager {
	return &RiskManager{limits: limits, portfolio: pf}
}

// CanTrade checks if a new trade would violate any risk limits.
// Returns true if the trade is allowed, false with a reason if not.
func (rm *RiskManager) CanTrade(token, exchange string, qty int64) (bool, string) {
	positions := rm.portfolio.GetPositions()

	// Check max open positions
	if len(positions) >= rm.limits.MaxOpenPositions {
		return false, "max open positions reached"
	}

	// Check position size
	if qty > rm.limits.MaxPositionSize || qty < -rm.limits.MaxPositionSize {
		return false, "position size exceeds limit"
	}

	// Check daily loss
	if rm.portfolio.TotalUnrealizedPnL() < -rm.limits.MaxDailyLoss {
		return false, "max daily loss reached"
	}

	return true, ""
}
