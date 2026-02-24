// Package ringbuf provides a lock-free, single-producer single-consumer (SPSC)
// ring buffer for model.Candle. It uses atomic operations and cache-line padding
// to achieve minimal latency with zero contention.
package ringbuf

import (
	"sync/atomic"
	"trading-systemv1/internal/model"
)

// cacheLine is the typical x86-64 cache line size used for padding.
const cacheLine = 64

// Ring is a lock-free SPSC ring buffer for Candle values.
// Size must be a power of two for fast bitwise modulo.
type Ring struct {
	buf  []model.Candle
	mask uint64

	// Separate cache lines to prevent false sharing between producer and consumer.
	_pad0 [cacheLine]byte
	head  atomic.Uint64 // written by producer
	_pad1 [cacheLine]byte
	tail  atomic.Uint64 // written by consumer
	_pad2 [cacheLine]byte

	// Overflow counter (atomic, for metrics)
	overflow atomic.Uint64
}

// New creates a ring buffer. capacity is rounded up to the next power of two.
// Minimum capacity is 2.
func New(capacity int) *Ring {
	cap := nextPow2(capacity)
	if cap < 2 {
		cap = 2
	}
	return &Ring{
		buf:  make([]model.Candle, cap),
		mask: uint64(cap - 1),
	}
}

// Push appends a candle to the ring buffer. Returns false if the buffer is full
// (the candle is NOT written in that case). Non-blocking.
func (r *Ring) Push(c model.Candle) bool {
	head := r.head.Load()
	tail := r.tail.Load()

	if head-tail >= uint64(len(r.buf)) {
		// Buffer full
		r.overflow.Add(1)
		return false
	}

	r.buf[head&r.mask] = c
	r.head.Store(head + 1)
	return true
}

// Pop retrieves the next candle from the ring buffer.
// Returns false if the buffer is empty. Non-blocking.
func (r *Ring) Pop() (model.Candle, bool) {
	tail := r.tail.Load()
	head := r.head.Load()

	if tail >= head {
		// Buffer empty
		return model.Candle{}, false
	}

	c := r.buf[tail&r.mask]
	r.tail.Store(tail + 1)
	return c, true
}

// Len returns the current number of items in the buffer.
func (r *Ring) Len() int {
	return int(r.head.Load() - r.tail.Load())
}

// Cap returns the buffer capacity.
func (r *Ring) Cap() int {
	return len(r.buf)
}

// Overflow returns the total number of dropped pushes due to full buffer.
func (r *Ring) Overflow() uint64 {
	return r.overflow.Load()
}

// nextPow2 returns the smallest power of 2 >= n.
func nextPow2(n int) int {
	if n <= 0 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	return n + 1
}
