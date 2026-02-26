package closedetector

import (
	"testing"
	"time"
)

func TestDetector_PriceStabilization(t *testing.T) {
	closeTime := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC) // 15:30 IST
	d := New(closeTime)
	d.StableFor = 3 * time.Second // quick for test

	// Before close: should never disconnect
	if d.Observe(50000, closeTime.Add(-1*time.Minute)) {
		t.Error("should not disconnect before close")
	}

	// After close: changing prices should not trigger disconnect
	if d.Observe(50100, closeTime.Add(1*time.Second)) {
		t.Error("should not disconnect when price is changing")
	}
	if d.Observe(50200, closeTime.Add(2*time.Second)) {
		t.Error("should not disconnect when price is changing")
	}

	// Stable price but not long enough
	if d.Observe(50200, closeTime.Add(3*time.Second)) {
		t.Error("should not disconnect yet, only 1s stable")
	}

	// Stable for StableFor (3s)
	if !d.Observe(50200, closeTime.Add(5*time.Second)) {
		t.Error("should disconnect — price stable for 3s")
	}

	if d.ClosingPrice() != 50200 {
		t.Errorf("expected closing price 50200, got %d", d.ClosingPrice())
	}
}

func TestDetector_HardDeadline(t *testing.T) {
	closeTime := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)
	d := New(closeTime)
	d.MaxGrace = 2 * time.Minute

	// Price keeps changing but we're past the hard deadline
	if d.Observe(50100, closeTime.Add(1*time.Minute)) {
		t.Error("should not disconnect before hard deadline")
	}

	// Past hard deadline — should disconnect even though price changed
	if !d.Observe(50200, closeTime.Add(3*time.Minute)) {
		t.Error("should disconnect — past hard deadline")
	}
}

func TestDetector_PriceChangeResetsStability(t *testing.T) {
	closeTime := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)
	d := New(closeTime)
	d.StableFor = 2 * time.Second

	// Start stable
	d.Observe(50000, closeTime.Add(1*time.Second))
	d.Observe(50000, closeTime.Add(2*time.Second))

	// Price changes — resets stability
	d.Observe(50100, closeTime.Add(2500*time.Millisecond))

	// 1s after change — not stable long enough
	if d.Observe(50100, closeTime.Add(3*time.Second)) {
		t.Error("should not disconnect — only 0.5s since price change")
	}

	// 2s after change — now stable long enough
	if !d.Observe(50100, closeTime.Add(4500*time.Millisecond)) {
		t.Error("should disconnect — 2s stable after the price change")
	}
}
