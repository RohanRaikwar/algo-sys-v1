package redis

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"trading-systemv1/internal/model"
)

// pendingWrite represents a write that was buffered during circuit-open state.
type pendingWrite struct {
	WriteType string // "candle_1s", "tf_candle"
	Data      []byte // JSON-encoded payload
}

// BufferedWriter wraps a Redis Writer with a circuit breaker.
// During circuit-open state, writes are buffered locally and flushed
// when the circuit closes again.
type BufferedWriter struct {
	writer *Writer
	cb     *CircuitBreaker
	ctx    context.Context

	mu     sync.Mutex
	buffer []pendingWrite
	maxBuf int // max buffered writes before dropping oldest (default: 10000)

	// Callbacks
	OnBuffer func()          // called when a write is buffered (for metrics)
	OnFlush  func(count int) // called after flushing buffered writes
}

// NewBufferedWriter creates a BufferedWriter wrapping the given Writer.
func NewBufferedWriter(ctx context.Context, w *Writer, cb *CircuitBreaker, maxBufferSize int) *BufferedWriter {
	if maxBufferSize <= 0 {
		maxBufferSize = 10000
	}
	bw := &BufferedWriter{
		writer: w,
		cb:     cb,
		ctx:    ctx,
		buffer: make([]pendingWrite, 0, 256),
		maxBuf: maxBufferSize,
	}

	// Register flush on circuit close
	prevCallback := cb.OnStateChange
	cb.OnStateChange = func(from, to State) {
		if prevCallback != nil {
			prevCallback(from, to)
		}
		if to == StateClosed {
			go bw.flush()
		}
	}

	return bw
}

// WriteTFCandle writes a TF candle through the circuit breaker.
// If the circuit is open, the write is buffered locally.
func (bw *BufferedWriter) WriteTFCandle(tfc model.TFCandle) error {
	err := bw.cb.Execute(func() error {
		bw.writer.writeTFCandle(bw.ctx, tfc)
		return nil // writeTFCandle logs errors internally
	})
	if err == ErrCircuitOpen {
		bw.bufferWrite("tf_candle", tfc)
		return nil // buffered, not lost
	}
	return err
}

// WriteCandle writes a 1s candle through the circuit breaker.
func (bw *BufferedWriter) WriteCandle(c model.Candle) error {
	err := bw.cb.Execute(func() error {
		bw.writer.writeCandle(bw.ctx, c)
		return nil
	})
	if err == ErrCircuitOpen {
		bw.bufferWrite("candle_1s", c)
		return nil
	}
	return err
}

func (bw *BufferedWriter) bufferWrite(writeType string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[buffered-writer] marshal error: %v", err)
		return
	}

	bw.mu.Lock()
	defer bw.mu.Unlock()

	if len(bw.buffer) >= bw.maxBuf {
		// Buffer full — drop oldest
		bw.buffer = bw.buffer[1:]
	}
	bw.buffer = append(bw.buffer, pendingWrite{WriteType: writeType, Data: data})

	if bw.OnBuffer != nil {
		bw.OnBuffer()
	}
}

// flush replays all buffered writes through the underlying writer.
func (bw *BufferedWriter) flush() {
	bw.mu.Lock()
	if len(bw.buffer) == 0 {
		bw.mu.Unlock()
		return
	}
	// Take ownership of the buffer
	toFlush := bw.buffer
	bw.buffer = make([]pendingWrite, 0, 256)
	bw.mu.Unlock()

	flushed := 0
	for _, pw := range toFlush {
		switch pw.WriteType {
		case "tf_candle":
			var tfc model.TFCandle
			if json.Unmarshal(pw.Data, &tfc) == nil {
				bw.writer.writeTFCandle(bw.ctx, tfc)
			}
		case "candle_1s":
			var c model.Candle
			if json.Unmarshal(pw.Data, &c) == nil {
				bw.writer.writeCandle(bw.ctx, c)
			}
		}
		flushed++
	}

	log.Printf("[buffered-writer] flushed %d buffered writes", flushed)
	if bw.OnFlush != nil {
		bw.OnFlush(flushed)
	}
}

// PendingCount returns the number of buffered writes waiting to be flushed.
func (bw *BufferedWriter) PendingCount() int {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	return len(bw.buffer)
}

// Writer returns the underlying Redis writer for direct access.
func (bw *BufferedWriter) Underlying() *Writer {
	return bw.writer
}
