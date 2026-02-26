package gateway

import (
	"math"
	"sort"
	"sync"
)

// LatencyTracker records end-to-end latency samples in a circular buffer
// and computes percentiles (p50, p95, p99). Thread-safe.
type LatencyTracker struct {
	mu      sync.Mutex
	samples []float64 // circular buffer of latency values (ms)
	pos     int
	count   int
	cap     int
}

// NewLatencyTracker creates a tracker that holds the last `capacity` samples.
func NewLatencyTracker(capacity int) *LatencyTracker {
	if capacity <= 0 {
		capacity = 10000
	}
	return &LatencyTracker{
		samples: make([]float64, capacity),
		cap:     capacity,
	}
}

// Record adds a latency sample in milliseconds.
func (lt *LatencyTracker) Record(latencyMs float64) {
	lt.mu.Lock()
	lt.samples[lt.pos] = latencyMs
	lt.pos = (lt.pos + 1) % lt.cap
	if lt.count < lt.cap {
		lt.count++
	}
	lt.mu.Unlock()
}

// Percentiles returns p50, p95, p99 latency in milliseconds.
// Returns (0, 0, 0) if no samples have been recorded.
func (lt *LatencyTracker) Percentiles() (p50, p95, p99 float64) {
	lt.mu.Lock()
	n := lt.count
	if n == 0 {
		lt.mu.Unlock()
		return 0, 0, 0
	}

	// Copy samples for sorting
	sorted := make([]float64, n)
	if n == lt.cap {
		// Buffer is full; copy from pos (oldest) to end, then start to pos
		copy(sorted, lt.samples[lt.pos:])
		copy(sorted[lt.cap-lt.pos:], lt.samples[:lt.pos])
	} else {
		copy(sorted, lt.samples[:n])
	}
	lt.mu.Unlock()

	sort.Float64s(sorted)

	p50 = percentile(sorted, 0.50)
	p95 = percentile(sorted, 0.95)
	p99 = percentile(sorted, 0.99)
	return
}

// Count returns the number of samples recorded (up to capacity).
func (lt *LatencyTracker) Count() int {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return lt.count
}

// percentile computes the p-th percentile (0.0–1.0) of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p * float64(n-1)
	lower := int(math.Floor(rank))
	upper := lower + 1
	if upper >= n {
		return sorted[n-1]
	}
	frac := rank - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
