package indicator

import "trading-systemv1/internal/model"

// RSI calculates the Relative Strength Index using Wilder's smoothing method.
// Update is O(1) per candle — no history scans.
type RSI struct {
	period    int
	count     int
	prevClose float64
	avgGain   float64
	avgLoss   float64
	current   float64
}

// NewRSI creates a new RSI indicator with the given period (typically 14).
func NewRSI(period int) *RSI {
	return &RSI{period: period}
}

func (r *RSI) Name() string { return "RSI" }

func (r *RSI) Update(candle model.Candle) {
	price := float64(candle.Close) / 100.0 // paise → rupees
	r.count++

	if r.count == 1 {
		// First candle — just record price, no delta yet
		r.prevClose = price
		return
	}

	delta := price - r.prevClose
	r.prevClose = price

	gain := 0.0
	loss := 0.0
	if delta > 0 {
		gain = delta
	} else {
		loss = -delta
	}

	if r.count <= r.period+1 {
		// Accumulation phase: build initial averages
		r.avgGain += gain
		r.avgLoss += loss

		if r.count == r.period+1 {
			// First RSI value using SMA seed
			r.avgGain /= float64(r.period)
			r.avgLoss /= float64(r.period)
			if r.avgLoss == 0 {
				r.current = 100.0
			} else {
				rs := r.avgGain / r.avgLoss
				r.current = 100.0 - (100.0 / (1.0 + rs))
			}
		}
		return
	}

	// Wilder's smoothing: avgGain = (prevAvgGain * (period-1) + gain) / period
	p := float64(r.period)
	r.avgGain = (r.avgGain*(p-1) + gain) / p
	r.avgLoss = (r.avgLoss*(p-1) + loss) / p

	if r.avgLoss == 0 {
		r.current = 100.0
	} else {
		rs := r.avgGain / r.avgLoss
		r.current = 100.0 - (100.0 / (1.0 + rs))
	}
}

func (r *RSI) Value() float64 { return r.current }
func (r *RSI) Ready() bool    { return r.count > r.period }

// Peek computes what RSI would be with an additional candle without mutating state.
func (r *RSI) Peek(closePaise int64) float64 {
	if r.count <= r.period {
		return r.current
	}
	price := float64(closePaise) / 100.0
	delta := price - r.prevClose
	gain, loss := 0.0, 0.0
	if delta > 0 {
		gain = delta
	} else {
		loss = -delta
	}
	p := float64(r.period)
	ag := (r.avgGain*(p-1) + gain) / p
	al := (r.avgLoss*(p-1) + loss) / p
	if al == 0 {
		return 100.0
	}
	rs := ag / al
	return 100.0 - (100.0 / (1.0 + rs))
}

// Snapshot serializes the RSI state for checkpoint persistence.
func (r *RSI) Snapshot() IndicatorSnapshot {
	return IndicatorSnapshot{
		Type:      "RSI",
		Period:    r.period,
		Count:     r.count,
		PrevClose: r.prevClose,
		AvgGain:   r.avgGain,
		AvgLoss:   r.avgLoss,
		Current:   r.current,
	}
}

// RestoreFromSnapshot restores RSI state from a checkpoint.
func (r *RSI) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	r.period = snap.Period
	r.count = snap.Count
	r.prevClose = snap.PrevClose
	r.avgGain = snap.AvgGain
	r.avgLoss = snap.AvgLoss
	r.current = snap.Current
	return nil
}
