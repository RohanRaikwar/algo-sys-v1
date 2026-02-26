package gateway

import "sync"

// replayEntry holds a single broadcasted message for replay.
type replayEntry struct {
	Seq  int64
	Data []byte // pre-built envelope JSON
}

// ReplayBuffer is a fixed-size circular buffer of recent WS envelopes
// per channel. Supports Range queries for client gap backfill.
//
// Thread-safe for concurrent writes and reads.
type ReplayBuffer struct {
	mu   sync.RWMutex
	buf  []replayEntry
	cap  int
	pos  int // next write position
	full bool
}

// NewReplayBuffer creates a replay buffer with the given capacity.
func NewReplayBuffer(capacity int) *ReplayBuffer {
	if capacity <= 0 {
		capacity = 500
	}
	return &ReplayBuffer{
		buf: make([]replayEntry, capacity),
		cap: capacity,
	}
}

// Push appends an envelope to the buffer. Overwrites oldest entry when full.
func (rb *ReplayBuffer) Push(seq int64, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Copy data to avoid holding onto the caller's slice
	cp := make([]byte, len(data))
	copy(cp, data)

	rb.buf[rb.pos] = replayEntry{Seq: seq, Data: cp}
	rb.pos = (rb.pos + 1) % rb.cap
	if rb.pos == 0 && !rb.full {
		rb.full = true
	}
}

// Range returns all entries with seq in [fromSeq, toSeq] (inclusive).
// Returns entries in seq order.
func (rb *ReplayBuffer) Range(fromSeq, toSeq int64) []replayEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	var result []replayEntry
	count := rb.len()

	for i := 0; i < count; i++ {
		idx := rb.index(i)
		e := rb.buf[idx]
		if e.Seq >= fromSeq && e.Seq <= toSeq {
			result = append(result, e)
		}
	}
	return result
}

// Len returns the number of entries currently in the buffer.
func (rb *ReplayBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.len()
}

func (rb *ReplayBuffer) len() int {
	if rb.full {
		return rb.cap
	}
	return rb.pos
}

// index converts a logical index (0 = oldest) to a physical buffer index.
func (rb *ReplayBuffer) index(logical int) int {
	if rb.full {
		return (rb.pos + logical) % rb.cap
	}
	return logical
}
