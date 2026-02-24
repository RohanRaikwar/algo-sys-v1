// Package replay provides a candle replayer that reads historical data from
// SQLite and emits it at configurable speed for backtesting.
package replay

import (
	"context"
	"log"
	"time"

	"trading-systemv1/internal/model"
	sqlitestore "trading-systemv1/internal/store/sqlite"
)

// Replayer reads historical TF candles from SQLite and replays them
// at a configurable speed multiplier.
type Replayer struct {
	reader *sqlitestore.Reader
}

// New creates a Replayer backed by a SQLite reader.
func New(reader *sqlitestore.Reader) *Replayer {
	return &Replayer{reader: reader}
}

// Run replays all candles for the given TFs, emitting them into outCh.
// speed controls the playback rate: 1.0 = real-time, 10.0 = 10x, 0 = as fast as possible.
// fromTS filters candles to those after this Unix timestamp (0 = all).
func (r *Replayer) Run(ctx context.Context, tfs []int, fromTS int64, speed float64, outCh chan<- model.TFCandle) error {
	// Collect all candles across TFs, sorted by time
	var allCandles []model.TFCandle
	for _, tf := range tfs {
		candles, err := r.reader.ReadAllTFCandles(tf, fromTS)
		if err != nil {
			return err
		}
		allCandles = append(allCandles, candles...)
	}

	if len(allCandles) == 0 {
		log.Println("[replay] no candles found in SQLite")
		return nil
	}

	// Sort by timestamp (they may be interleaved across TFs)
	sortCandles(allCandles)

	log.Printf("[replay] loaded %d candles across %d TFs, speed=%.1fx", len(allCandles), len(tfs), speed)

	var prevTS time.Time
	emitted := 0

	for _, c := range allCandles {
		select {
		case <-ctx.Done():
			log.Printf("[replay] cancelled after %d candles", emitted)
			return ctx.Err()
		default:
		}

		// Simulate time gaps between candles
		if speed > 0 && !prevTS.IsZero() {
			gap := c.TS.Sub(prevTS)
			if gap > 0 {
				scaledGap := time.Duration(float64(gap) / speed)
				// Cap max sleep to avoid very long waits
				if scaledGap > 5*time.Second {
					scaledGap = 5 * time.Second
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(scaledGap):
				}
			}
		}
		prevTS = c.TS

		// Mark as finalized (not forming) for indicator processing
		c.Forming = false
		outCh <- c
		emitted++
	}

	log.Printf("[replay] completed: %d candles replayed", emitted)
	return nil
}

// sortCandles sorts candles by timestamp (insertion sort — stable and fine for replay sizes).
func sortCandles(candles []model.TFCandle) {
	for i := 1; i < len(candles); i++ {
		for j := i; j > 0 && candles[j].TS.Before(candles[j-1].TS); j-- {
			candles[j], candles[j-1] = candles[j-1], candles[j]
		}
	}
}
