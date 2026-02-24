package bus

import (
	"context"
	"log"
	"sync"

	"trading-systemv1/internal/model"
)

// FanOut broadcasts candles from a single input channel to N output channels.
// If an output channel is full, the candle is dropped for that consumer to
// prevent a slow consumer from blocking the pipeline.
type FanOut struct {
	mu      sync.RWMutex
	outputs []chan model.Candle
	bufSize int

	// OnDrop is called when a candle is dropped for a subscriber.
	// subscriberIdx is the 0-based index of the slow consumer.
	OnDrop func(subscriberIdx int)
}

// New creates a FanOut with the given buffer size for output channels.
func New(outputBufferSize int) *FanOut {
	return &FanOut{
		bufSize: outputBufferSize,
	}
}

// Subscribe creates and returns a new output channel.
func (f *FanOut) Subscribe() <-chan model.Candle {
	ch := make(chan model.Candle, f.bufSize)
	f.mu.Lock()
	f.outputs = append(f.outputs, ch)
	f.mu.Unlock()
	return ch
}

// Run reads from the input channel and fans out to all subscribers.
// Blocks until ctx is cancelled or input is closed.
func (f *FanOut) Run(ctx context.Context, input <-chan model.Candle) {
	defer func() {
		f.mu.RLock()
		for _, ch := range f.outputs {
			close(ch)
		}
		f.mu.RUnlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case candle, ok := <-input:
			if !ok {
				return
			}
			f.mu.RLock()
			for i, ch := range f.outputs {
				select {
				case ch <- candle:
				default:
					if f.OnDrop != nil {
						f.OnDrop(i)
					} else {
						log.Printf("[bus] output channel %d full, dropping candle %s", i, candle.Key())
					}
				}
			}
			f.mu.RUnlock()
		}
	}
}

// ChannelStats returns (length, capacity) for each subscriber channel.
// Used for reporting channel saturation percentage.
type ChannelStat struct {
	Len int
	Cap int
}

func (f *FanOut) ChannelStats() []ChannelStat {
	f.mu.RLock()
	defer f.mu.RUnlock()
	stats := make([]ChannelStat, len(f.outputs))
	for i, ch := range f.outputs {
		stats[i] = ChannelStat{Len: len(ch), Cap: cap(ch)}
	}
	return stats
}
