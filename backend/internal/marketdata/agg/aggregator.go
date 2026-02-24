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
type Aggregator struct {
	mu     sync.Mutex
	states map[string]*candleState // key = "exchange:token"

	flushInterval time.Duration

	// Metrics hooks (optional, set externally)
	OnDroppedTick func()
}

// New creates a new Aggregator.
func New() *Aggregator {
	return &Aggregator{
		states:        make(map[string]*candleState),
		flushInterval: 100 * time.Millisecond, // check frequency for bucket rollover
	}
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
			// Periodic flush: emit any candles whose bucket is in the past
			a.flushOld(candleCh)
		}
	}
}

// processTick incorporates a single tick into the candle state.
func (a *Aggregator) processTick(tick model.Tick, candleCh chan<- model.Candle) {
	bucket := tick.TickTS.Unix()
	key := tick.Exchange + ":" + tick.Token

	a.mu.Lock()
	defer a.mu.Unlock()

	state, exists := a.states[key]

	if exists && bucket < state.bucket {
		// Late tick — belongs to an older bucket, drop it
		dropped := a.OnDroppedTick
		a.mu.Unlock()
		if dropped != nil {
			dropped()
		}
		a.mu.Lock()
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

// flushOld emits candles for any bucket that is strictly in the past.
func (a *Aggregator) flushOld(candleCh chan<- model.Candle) {
	now := time.Now().Unix()

	a.mu.Lock()
	defer a.mu.Unlock()

	for key, state := range a.states {
		if state.bucket < now {
			a.emit(state, candleCh)
			delete(a.states, key)
		}
	}
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
		log.Printf("[agg] candleCh full, dropping candle %s ts=%v", state.candle.Key(), state.candle.TS)
	}
}
