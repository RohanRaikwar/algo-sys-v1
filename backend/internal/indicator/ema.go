package indicator

import "trading-systemv1/internal/model"

// EMA calculates Exponential Moving Average.
// O(1) per update — no window storage needed.
type EMA struct {
	period     int
	multiplier float64
	current    float64
	count      int
	sum        float64
}

// NewEMA creates a new EMA indicator with the given period.
func NewEMA(period int) *EMA {
	return &EMA{
		period:     period,
		multiplier: 2.0 / float64(period+1),
	}
}

func (e *EMA) Name() string { return "EMA" }

func (e *EMA) Update(candle model.Candle) {
	price := float64(candle.Close)
	e.count++

	if e.count <= e.period {
		// Accumulate for initial SMA seed
		e.sum += price
		if e.count == e.period {
			e.current = e.sum / float64(e.period)
		}
		return
	}

	// EMA formula: EMA = (Price * multiplier) + (EMA_prev * (1 - multiplier))
	e.current = (price * e.multiplier) + (e.current * (1 - e.multiplier))
}

func (e *EMA) Value() float64 { return e.current }
func (e *EMA) Ready() bool    { return e.count >= e.period }

// Peek computes what Value() would be with an additional candle without mutating state.
func (e *EMA) Peek(closePaise int64) float64 {
	price := float64(closePaise)
	if e.count < e.period {
		// Not fully ready — return partial estimate using the price
		return price
	}
	return (price * e.multiplier) + (e.current * (1 - e.multiplier))
}

// Reset clears the EMA state for reuse.
func (e *EMA) Reset() {
	e.current = 0
	e.count = 0
	e.sum = 0
}

// Snapshot serializes the EMA state for checkpoint persistence.
func (e *EMA) Snapshot() IndicatorSnapshot {
	return IndicatorSnapshot{
		Type:       "EMA",
		Period:     e.period,
		Multiplier: e.multiplier,
		Current:    e.current,
		Count:      e.count,
		Sum:        e.sum,
	}
}

// RestoreFromSnapshot restores EMA state from a checkpoint.
func (e *EMA) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	e.period = snap.Period
	e.multiplier = snap.Multiplier
	e.current = snap.Current
	e.count = snap.Count
	e.sum = snap.Sum
	return nil
}
