package agg

import (
	"context"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// candleState holds the in-progress candle for one instrument in the current second bucket.
type candleState struct {
	bucket int64 // Unix second of this bucket
	candle model.Candle
}

// Aggregator builds 1-second OHLC candles from a stream of ticks.
// It runs in a single goroutine and emits finalized candles when the second rolls over.
//
// Event-time watermark: candles are finalized based on the event-time watermark
// (max event-time seen minus ReorderBuffer), not wall-clock time. This handles
// out-of-order ticks that arrive within the reorder window.
type Aggregator struct {
	mu     sync.Mutex
	states map[string]*candleState // key = "exchange:token"

	flushInterval time.Duration

	// ReorderBuffer is the duration to hold out-of-order ticks before
	// considering their bucket finalized. Default: 300ms.
	ReorderBuffer time.Duration

	// Event-time watermark tracking
	maxEventTS int64 // max canonical tick timestamp seen (Unix seconds)
	watermark  int64 // maxEventTS - ReorderBuffer (Unix seconds)

	// Metrics hooks (optional, set externally)
	OnDroppedTick func() // called when candleCh is full
	OnLateTick    func() // called when tick arrives behind watermark (event-time)
}

// New creates a new Aggregator with default settings.
func New() *Aggregator {
	return &Aggregator{
		states:        make(map[string]*candleState),
		flushInterval: 100 * time.Millisecond, // check frequency for bucket rollover
		ReorderBuffer: 300 * time.Millisecond, // default out-of-order tolerance
	}
}

// WatermarkDelay returns the current lag between wall-clock time and the
// event-time watermark. Useful for observability.
func (a *Aggregator) WatermarkDelay() time.Duration {
	a.mu.Lock()
	wm := a.watermark
	a.mu.Unlock()
	if wm == 0 {
		return 0
	}
	return time.Since(time.Unix(wm, 0))
}

// Run consumes ticks from tickCh in a single goroutine, aggregates into 1s candles,
// and sends finalized candles to candleCh. Blocks until ctx is cancelled.
func (a *Aggregator) Run(ctx context.Context, tickCh <-chan model.Tick, candleCh chan<- model.Candle) {
	ticker := time.NewTicker(a.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Flush any remaining open candles before exit
			a.flushAll(candleCh)
			return

		case tick, ok := <-tickCh:
			if !ok {
				a.flushAll(candleCh)
				return
			}
			a.processTick(tick, candleCh)

		case <-ticker.C:
			// Periodic flush: emit any candles whose bucket is behind the watermark
			a.flushOld(candleCh)
		}
	}
}

// processTick incorporates a single tick into the candle state.
// Uses event-time watermark to determine whether a tick is late.
func (a *Aggregator) processTick(tick model.Tick, candleCh chan<- model.Candle) {
	canonicalTS := tick.CanonicalTS()
	bucket := canonicalTS.Unix()
	key := tick.Exchange + ":" + tick.Token

	a.mu.Lock()
	defer a.mu.Unlock()

	// Advance watermark based on max event-time seen
	if bucket > a.maxEventTS {
		a.maxEventTS = bucket
		bufSec := int64(a.ReorderBuffer.Seconds())
		if bufSec < 1 {
			bufSec = 1 // minimum 1 second granularity for bucket-level watermark
		}
		a.watermark = a.maxEventTS - bufSec
	}

	// Drop ticks behind the watermark — their buckets are already finalized (immutable)
	if a.watermark > 0 && bucket < a.watermark {
		cb := a.OnLateTick
		a.mu.Unlock()
		if cb != nil {
			cb()
		}
		a.mu.Lock()
		return
	}

	state, exists := a.states[key]

	if exists && bucket < state.bucket {
		// Tick is for an older (but not yet flushed) bucket.
		// With event-time watermark, this bucket hasn't been finalized yet,
		// so we need to create a new bucket entry for it.
		// The old state stays as-is; we start a new one for the older bucket.
		a.states[key+":"+time.Unix(bucket, 0).UTC().Format("15:04:05")] = &candleState{
			bucket: bucket,
			candle: model.Candle{
				Token:      tick.Token,
				Exchange:   tick.Exchange,
				TS:         time.Unix(bucket, 0).UTC(),
				Open:       tick.Price,
				High:       tick.Price,
				Low:        tick.Price,
				Close:      tick.Price,
				Volume:     tick.Qty,
				TicksCount: 1,
			},
		}
		return
	}

	if exists && bucket > state.bucket {
		// New bucket — finalize the old candle first
		a.emit(state, candleCh)
		delete(a.states, key)
		exists = false
	}

	if !exists {
		// Start a new candle for this bucket
		a.states[key] = &candleState{
			bucket: bucket,
			candle: model.Candle{
				Token:      tick.Token,
				Exchange:   tick.Exchange,
				TS:         time.Unix(bucket, 0).UTC(),
				Open:       tick.Price,
				High:       tick.Price,
				Low:        tick.Price,
				Close:      tick.Price,
				Volume:     tick.Qty,
				TicksCount: 1,
			},
		}
		return
	}

	// Same bucket — update OHLC
	c := &state.candle
	if tick.Price > c.High {
		c.High = tick.Price
	}
	if tick.Price < c.Low {
		c.Low = tick.Price
	}
	c.Close = tick.Price
	c.Volume += tick.Qty
	c.TicksCount++
}

// flushOld emits candles for any bucket that is behind the event-time watermark.
func (a *Aggregator) flushOld(candleCh chan<- model.Candle) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.watermark == 0 {
		// No ticks received yet; fall back to wall-clock time
		now := time.Now().Unix()
		for key, state := range a.states {
			if state.bucket < now {
				a.emit(state, candleCh)
				delete(a.states, key)
			}
		}
		return
	}

	for key, state := range a.states {
		if state.bucket < a.watermark {
			a.emit(state, candleCh)
			delete(a.states, key)
		}
	}
}

// FlushSession finalizes and emits all in-progress candles.
// Called at market close to ensure the last candle includes the closing price.
// Safe to call from any goroutine — uses internal mutex.
func (a *Aggregator) FlushSession(candleCh chan<- model.Candle) {
	a.flushAll(candleCh)
	log.Println("[agg] session flushed — all forming candles finalized")
}

// flushAll emits all open candles regardless of bucket.
func (a *Aggregator) flushAll(candleCh chan<- model.Candle) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for key, state := range a.states {
		a.emit(state, candleCh)
		delete(a.states, key)
	}
}

// emit sends a finalized candle to candleCh. Non-blocking to avoid deadlocks.
func (a *Aggregator) emit(state *candleState, candleCh chan<- model.Candle) {
	select {
	case candleCh <- state.candle:
	default:
		if a.OnDroppedTick != nil {
			a.OnDroppedTick()
		}
		log.Printf("[agg] candleCh full, dropping candle %s ts=%v", state.candle.Key(), state.candle.TS)
	}
}
