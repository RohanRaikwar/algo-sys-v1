// Package closedetector detects the market closing price by observing
// post-15:30 tick price stability. When the price stops changing for
// StableFor duration, it considers the closing price captured.
package closedetector

import (
	"log"
	"time"
)

// Detector observes ticks after market close time and determines
// when the closing price has been captured (price becomes constant).
type Detector struct {
	lastPrice   int64
	stableSince time.Time
	closeTime   time.Time // 15:30 IST

	// StableFor is how long the price must remain constant to be considered
	// the closing price. Default: 30 seconds.
	StableFor time.Duration

	// MaxGrace is the hard deadline after closeTime. If price hasn't stabilized
	// by closeTime + MaxGrace, disconnect anyway. Default: 5 minutes.
	MaxGrace time.Duration
}

// New creates a Detector for the given close time.
func New(closeTime time.Time) *Detector {
	return &Detector{
		closeTime: closeTime,
		StableFor: 30 * time.Second,
		MaxGrace:  5 * time.Minute,
	}
}

// IsPostClose returns true if now is after the market close time.
func (d *Detector) IsPostClose(now time.Time) bool {
	return now.After(d.closeTime)
}

// Observe records a tick price and returns true if the session should
// disconnect (price has stabilized or hard deadline reached).
func (d *Detector) Observe(tickPrice int64, now time.Time) bool {
	// Hard deadline: always disconnect after MaxGrace
	if now.After(d.closeTime.Add(d.MaxGrace)) {
		log.Printf("[closedetector] hard deadline %v reached — disconnecting", d.MaxGrace)
		return true
	}

	// Only start observing after close time
	if !d.IsPostClose(now) {
		d.lastPrice = tickPrice
		return false
	}

	// Price changed — reset stability timer
	if tickPrice != d.lastPrice {
		d.lastPrice = tickPrice
		d.stableSince = now
		return false
	}

	// Price unchanged — check if stable long enough
	if d.stableSince.IsZero() {
		d.stableSince = now
		return false
	}

	if now.Sub(d.stableSince) >= d.StableFor {
		log.Printf("[closedetector] price %d stable for %v after close — closing price captured",
			d.lastPrice, d.StableFor)
		return true
	}

	return false
}

// ClosingPrice returns the last observed price (the closing price).
func (d *Detector) ClosingPrice() int64 {
	return d.lastPrice
}
