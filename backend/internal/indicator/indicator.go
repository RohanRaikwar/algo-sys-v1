// Package indicator provides technical indicator calculations over candle data.
//
// All indicators implement the Indicator interface, receiving candles and
// producing float64 values. Indicators are designed to be composable.
package indicator

import "trading-systemv1/internal/model"

// Indicator is the interface for all technical indicators.
type Indicator interface {
	// Name returns the indicator name (e.g., "SMA_20", "EMA_9").
	Name() string

	// Update feeds a new candle and recalculates.
	Update(candle model.Candle)

	// Value returns the current calculated value. Returns 0 if not enough data.
	Value() float64

	// Ready returns true when enough data has been accumulated.
	Ready() bool

	// Peek computes what Value() would be if a candle with this close price
	// (in paise) were added next, WITHOUT mutating internal state.
	// Used for live/streaming updates from forming candles.
	Peek(closePaise int64) float64
}
