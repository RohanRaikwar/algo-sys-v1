package indicator

import "trading-systemv1/internal/model"

// SMA calculates Simple Moving Average over a rolling window.
// Uses a preallocated circular buffer for zero-allocation hot path.
type SMA struct {
	period  int
	buf     []float64 // preallocated circular buffer
	idx     int       // current write position
	count   int       // total values received
	sum     float64
	current float64
}

// NewSMA creates a new SMA indicator with the given period.
func NewSMA(period int) *SMA {
	return &SMA{
		period: period,
		buf:    make([]float64, period),
	}
}

func (s *SMA) Name() string { return "SMA" }

func (s *SMA) Update(candle model.Candle) {
	price := float64(candle.Close) / 100.0 // paise → rupees

	if s.count >= s.period {
		// Subtract the oldest value being overwritten
		s.sum -= s.buf[s.idx]
	}

	s.buf[s.idx] = price
	s.sum += price
	s.idx = (s.idx + 1) % s.period
	s.count++

	if s.count >= s.period {
		s.current = s.sum / float64(s.period)
	}
}

func (s *SMA) Value() float64 { return s.current }
func (s *SMA) Ready() bool    { return s.count >= s.period }

// Peek computes what Value() would be with an additional candle without mutating state.
func (s *SMA) Peek(closePaise int64) float64 {
	price := float64(closePaise) / 100.0
	if s.count < s.period {
		// Not fully ready — return partial average including this price
		return (s.sum + price) / float64(s.count+1)
	}
	// Preview: replace the oldest value (at idx) with new price
	return (s.sum - s.buf[s.idx] + price) / float64(s.period)
}

// Reset clears the SMA state for reuse.
func (s *SMA) Reset() {
	s.idx = 0
	s.count = 0
	s.sum = 0
	s.current = 0
	for i := range s.buf {
		s.buf[i] = 0
	}
}

// Snapshot serializes the SMA state for checkpoint persistence.
func (s *SMA) Snapshot() IndicatorSnapshot {
	bufCopy := make([]float64, len(s.buf))
	copy(bufCopy, s.buf)
	return IndicatorSnapshot{
		Type:    "SMA",
		Period:  s.period,
		Buf:     bufCopy,
		Idx:     s.idx,
		Count:   s.count,
		Sum:     s.sum,
		Current: s.current,
	}
}

// RestoreFromSnapshot restores SMA state from a checkpoint.
func (s *SMA) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	s.period = snap.Period
	s.idx = snap.Idx
	s.count = snap.Count
	s.sum = snap.Sum
	s.current = snap.Current
	if len(snap.Buf) > 0 {
		s.buf = make([]float64, len(snap.Buf))
		copy(s.buf, snap.Buf)
	} else {
		s.buf = make([]float64, snap.Period)
	}
	return nil
}
