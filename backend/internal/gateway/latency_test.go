package gateway

import (
	"math"
	"testing"
)

func TestLatencyTracker_Empty(t *testing.T) {
	lt := NewLatencyTracker(100)
	p50, p95, p99 := lt.Percentiles()
	if p50 != 0 || p95 != 0 || p99 != 0 {
		t.Errorf("empty tracker: expected (0,0,0), got (%f,%f,%f)", p50, p95, p99)
	}
}

func TestLatencyTracker_SingleSample(t *testing.T) {
	lt := NewLatencyTracker(100)
	lt.Record(42.5)

	p50, p95, p99 := lt.Percentiles()
	if p50 != 42.5 {
		t.Errorf("p50: got %f, want 42.5", p50)
	}
	if p95 != 42.5 {
		t.Errorf("p95: got %f, want 42.5", p95)
	}
	if p99 != 42.5 {
		t.Errorf("p99: got %f, want 42.5", p99)
	}
}

func TestLatencyTracker_Percentiles(t *testing.T) {
	lt := NewLatencyTracker(10000)

	// Record 100 samples: 1.0, 2.0, 3.0, ..., 100.0
	for i := 1; i <= 100; i++ {
		lt.Record(float64(i))
	}

	p50, p95, p99 := lt.Percentiles()

	// p50 should be around 50.5 (median of 1..100)
	if math.Abs(p50-50.5) > 1.0 {
		t.Errorf("p50: got %f, expected ~50.5", p50)
	}
	// p95 should be around 95.05
	if math.Abs(p95-95.05) > 1.0 {
		t.Errorf("p95: got %f, expected ~95.05", p95)
	}
	// p99 should be around 99.01
	if math.Abs(p99-99.01) > 1.0 {
		t.Errorf("p99: got %f, expected ~99.01", p99)
	}
}

func TestLatencyTracker_Wraparound(t *testing.T) {
	lt := NewLatencyTracker(10) // tiny capacity

	// Record 20 samples — first 10 should be evicted
	for i := 1; i <= 20; i++ {
		lt.Record(float64(i))
	}

	if lt.Count() != 10 {
		t.Fatalf("Count() = %d, want 10", lt.Count())
	}

	p50, _, _ := lt.Percentiles()

	// After wraparound, buffer contains 11..20
	// p50 of 11..20 = 15.5
	if math.Abs(p50-15.5) > 1.0 {
		t.Errorf("p50 after wraparound: got %f, expected ~15.5", p50)
	}
}

func TestLatencyTracker_Count(t *testing.T) {
	lt := NewLatencyTracker(100)

	if lt.Count() != 0 {
		t.Errorf("initial count: got %d, want 0", lt.Count())
	}

	for i := 0; i < 5; i++ {
		lt.Record(float64(i))
	}
	if lt.Count() != 5 {
		t.Errorf("after 5 records: got %d, want 5", lt.Count())
	}
}
