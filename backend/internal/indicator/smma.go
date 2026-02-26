package indicator

import "trading-systemv1/internal/model"

// SMMA calculates Smoothed Moving Average (Wilder-style smoothing).
// First value is SMA(period), then SMMA = (prev*(period-1) + price) / period.
type SMMA struct {
	period  int
	count   int
	sum     float64
	current float64
}

// NewSMMA creates a new SMMA indicator with the given period.
func NewSMMA(period int) *SMMA {
	return &SMMA{period: period}
}

func (s *SMMA) Name() string { return "SMMA" }

func (s *SMMA) Update(candle model.Candle) {
	price := float64(candle.Close)
	s.count++

	if s.count <= s.period {
		// Accumulate for initial SMA seed
		s.sum += price
		if s.count == s.period {
			s.current = s.sum / float64(s.period)
		}
		return
	}

	// Wilder-style smoothing
	s.current = (s.current*float64(s.period-1) + price) / float64(s.period)
}

func (s *SMMA) Value() float64 { return s.current }
func (s *SMMA) Ready() bool    { return s.count >= s.period }

// Peek computes what Value() would be with an additional candle without mutating state.
func (s *SMMA) Peek(closePaise int64) float64 {
	price := float64(closePaise)
	if s.count < s.period {
		return (s.sum + price) / float64(s.count+1)
	}
	return (s.current*float64(s.period-1) + price) / float64(s.period)
}

// Reset clears the SMMA state for reuse.
func (s *SMMA) Reset() {
	s.count = 0
	s.sum = 0
	s.current = 0
}

// Snapshot serializes the SMMA state for checkpoint persistence.
func (s *SMMA) Snapshot() IndicatorSnapshot {
	return IndicatorSnapshot{
		Type:    "SMMA",
		Period:  s.period,
		Count:   s.count,
		Sum:     s.sum,
		Current: s.current,
	}
}

// RestoreFromSnapshot restores SMMA state from a checkpoint.
func (s *SMMA) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	s.period = snap.Period
	s.count = snap.Count
	s.sum = snap.Sum
	s.current = snap.Current
	return nil
}
