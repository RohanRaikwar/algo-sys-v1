// Package tfbuilder provides an incremental timeframe resampler.
// It consumes finalized 1-second candles and maintains "forming" TF candle
// states that are updated in O(1) per candle per TF. When a TF bucket
// closes (i.e., a candle arrives in a new bucket), the previous TF candle
// is finalized and emitted.
package tfbuilder

import (
	"context"
	"log"
	"time"

	"trading-systemv1/internal/model"
)

// tfState holds the forming candle state for one (token, TF) pair.
type tfState struct {
	bucket  int64 // bucket start = ts - ts%tf (Unix seconds)
	candle  model.TFCandle
	started bool
}

// Builder resamples 1s candles into multiple dynamic timeframes.
// Goroutine-safe: designed to run in a single goroutine (single consumer).
type Builder struct {
	tfs []int // enabled TF durations in seconds

	// Per-TF per-token state.
	// Key structure: states[tfIdx][tokenKey] → *tfState
	states []map[string]*tfState

	// Staleness validation: reject candles older than bucket_start - tolerance.
	// Default: 2s. Set to 0 to disable.
	StaleTolerance time.Duration

	// Metrics hooks
	OnTFCandle    func(c model.TFCandle) // called on finalized TF candle (optional)
	OnStaleCandle func()                 // called when a stale candle is rejected (optional)
}

// New creates a TF builder with the given timeframes (in seconds).
func New(tfs []int) *Builder {
	states := make([]map[string]*tfState, len(tfs))
	for i := range states {
		states[i] = make(map[string]*tfState, 64) // preallocate for ~64 tokens
	}
	return &Builder{
		tfs:            tfs,
		states:         states,
		StaleTolerance: 2 * time.Second, // default: reject candles > 2s stale
	}
}

// UpdateTFs dynamically updates the enabled timeframes.
// Existing forming candles for removed TFs are finalized and emitted.
func (b *Builder) UpdateTFs(newTFs []int, outCh chan<- model.TFCandle) {
	// Build set of new TFs
	newSet := make(map[int]bool, len(newTFs))
	for _, tf := range newTFs {
		newSet[tf] = true
	}

	// Finalize forming candles for TFs being removed
	for i, tf := range b.tfs {
		if !newSet[tf] {
			for _, st := range b.states[i] {
				if st.started {
					st.candle.Forming = false
					emit(outCh, st.candle)
				}
			}
		}
	}

	// Rebuild states: keep existing states for TFs that persist, add new ones
	oldStates := make(map[int]map[string]*tfState, len(b.tfs))
	for i, tf := range b.tfs {
		oldStates[tf] = b.states[i]
	}

	b.tfs = newTFs
	b.states = make([]map[string]*tfState, len(newTFs))
	for i, tf := range newTFs {
		if old, ok := oldStates[tf]; ok {
			b.states[i] = old
		} else {
			b.states[i] = make(map[string]*tfState, 64)
		}
	}
}

// Run consumes 1s candles from candleCh, resamples them into TF candles,
// and sends finalized TF candles to outCh. Blocks until ctx is cancelled.
func (b *Builder) Run(ctx context.Context, candleCh <-chan model.Candle, outCh chan<- model.TFCandle) {
	for {
		select {
		case <-ctx.Done():
			b.flushAll(outCh)
			return
		case c, ok := <-candleCh:
			if !ok {
				b.flushAll(outCh)
				return
			}
			b.process(c, outCh)
		}
	}
}

// Process handles a single 1s candle against all enabled TFs.
// This is the hot path — O(1) per TF.
func (b *Builder) process(c model.Candle, outCh chan<- model.TFCandle) {
	ts := c.TS.Unix()
	key := c.Key()

	for i, tf := range b.tfs {
		tf64 := int64(tf)
		bucket := ts - (ts % tf64) // align to TF boundary

		st, exists := b.states[i][key]

		// Staleness check: reject candles whose bucket is behind the
		// current forming bucket by more than StaleTolerance.
		// This prevents late/out-of-order candles from corrupting
		// an already-advancing bucket.
		if b.StaleTolerance > 0 && exists && bucket < st.bucket {
			lag := time.Duration(st.bucket-bucket) * time.Second
			if lag > b.StaleTolerance {
				if b.OnStaleCandle != nil {
					b.OnStaleCandle()
				}
				continue // skip this TF for the stale candle
			}
		}

		if exists && bucket > st.bucket {
			// New bucket — finalize the forming candle
			st.candle.Forming = false
			emit(outCh, st.candle)
			if b.OnTFCandle != nil {
				b.OnTFCandle(st.candle)
			}
			exists = false
		}

		if !exists {
			// Start a new forming candle for this bucket
			newState := &tfState{
				bucket:  bucket,
				started: true,
				candle: model.TFCandle{
					Token:    c.Token,
					Exchange: c.Exchange,
					TF:       tf,
					TS:       time.Unix(bucket, 0).UTC(),
					Open:     c.Open,
					High:     c.High,
					Low:      c.Low,
					Close:    c.Close,
					Volume:   c.Volume,
					Count:    1,
					Forming:  true,
				},
			}
			b.states[i][key] = newState
			// Emit immediately so live-preview pipeline sees the first tick.
			snap := newState.candle
			emit(outCh, snap)
			continue
		}

		// Same bucket — merge OHLCV (O(1))
		fc := &st.candle
		if c.High > fc.High {
			fc.High = c.High
		}
		if c.Low < fc.Low {
			fc.Low = c.Low
		}
		fc.Close = c.Close
		fc.Volume += c.Volume
		fc.Count++

		// Emit a forming snapshot so the live-preview pipeline can peek at
		// the in-progress candle every second.  We copy the struct to avoid
		// a race if the caller holds onto the value after the next tick.
		snap := *fc // shallow copy is safe (no pointer fields)
		emit(outCh, snap)
	}
}

// flushAll finalizes and emits all forming candles.
func (b *Builder) flushAll(outCh chan<- model.TFCandle) {
	for i := range b.tfs {
		for key, st := range b.states[i] {
			if st.started {
				st.candle.Forming = false
				emit(outCh, st.candle)
			}
			delete(b.states[i], key)
		}
	}
}

// emit sends a TF candle to the output channel. Non-blocking to avoid deadlocks.
func emit(outCh chan<- model.TFCandle, c model.TFCandle) {
	select {
	case outCh <- c:
	default:
		log.Printf("[tfbuilder] outCh full, dropping TF candle %s tf=%d ts=%v", c.Key(), c.TF, c.TS)
	}
}

// TFs returns the current list of enabled timeframes.
func (b *Builder) TFs() []int {
	return b.tfs
}

// Run1 processes a single 1s candle against all TFs (hot path).
// This avoids channel overhead when called inline from the pipeline.
func (b *Builder) Run1(c model.Candle, outCh chan<- model.TFCandle) {
	b.process(c, outCh)
}
