package tfbuilder

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestBuilder_StaleCandle_Rejected(t *testing.T) {
	b := New([]int{60})
	// Default StaleTolerance = 2s
	outCh := make(chan model.TFCandle, 5000)

	now := time.Now().UTC()
	currentBucket := now.Unix() - (now.Unix() % 60)

	staleCount := 0
	b.OnStaleCandle = func() { staleCount++ }

	// First, send a candle at the current bucket to establish state
	b.process(model.Candle{
		Token: "NIFTY", Exchange: "NSE",
		TS:   time.Unix(currentBucket+5, 0).UTC(),
		Open: 100, High: 110, Low: 90, Close: 105, Volume: 1,
	}, outCh)

	// Advance the bucket to the next one to establish the "current" forming state
	b.process(model.Candle{
		Token: "NIFTY", Exchange: "NSE",
		TS:   time.Unix(currentBucket+65, 0).UTC(),
		Open: 200, High: 210, Low: 190, Close: 205, Volume: 1,
	}, outCh)

	// Drain
	for len(outCh) > 0 {
		<-outCh
	}

	// Now the forming bucket is at currentBucket+60.
	// Send a candle from the PREVIOUS bucket (60s behind = lag > 2s tolerance).
	// bucket for this candle = currentBucket, forming bucket = currentBucket+60
	// lag = 60s > 2s → should be rejected
	b.process(model.Candle{
		Token: "NIFTY", Exchange: "NSE",
		TS:   time.Unix(currentBucket+10, 0).UTC(),
		Open: 50, High: 60, Low: 40, Close: 55, Volume: 1,
	}, outCh)

	if staleCount != 1 {
		t.Errorf("expected 1 stale candle rejection, got %d", staleCount)
	}

	// Verify no output from the stale candle
	for len(outCh) > 0 {
		c := <-outCh
		if c.Open == 50 {
			t.Fatalf("stale candle should not have been processed: %+v", c)
		}
	}
}

func TestBuilder_StaleCandle_WithinTolerance_Accepted(t *testing.T) {
	b := New([]int{60})
	outCh := make(chan model.TFCandle, 100)

	now := time.Now().UTC()
	bucket := now.Unix() - (now.Unix() % 60)

	staleCount := 0
	b.OnStaleCandle = func() { staleCount++ }

	// Send candle in the current bucket — always accepted (first candle)
	b.process(model.Candle{
		Token: "NIFTY", Exchange: "NSE",
		TS:   time.Unix(bucket+1, 0).UTC(),
		Open: 100, High: 110, Low: 90, Close: 105, Volume: 1,
	}, outCh)

	if staleCount != 0 {
		t.Errorf("expected 0 stale callbacks, got %d", staleCount)
	}
	if len(outCh) == 0 {
		t.Error("expected forming candle output")
	}
}

func TestBuilder_StaleTolerance_Disabled(t *testing.T) {
	b := New([]int{60})
	b.StaleTolerance = 0 // disable
	outCh := make(chan model.TFCandle, 5000)

	staleCount := 0
	b.OnStaleCandle = func() { staleCount++ }

	// Establish state at a recent bucket
	now := time.Now().UTC()
	bucket := now.Unix() - (now.Unix() % 60)
	b.process(model.Candle{
		Token: "NIFTY", Exchange: "NSE",
		TS:   time.Unix(bucket+65, 0).UTC(), // next bucket
		Open: 200, High: 210, Low: 190, Close: 205, Volume: 1,
	}, outCh)
	b.process(model.Candle{
		Token: "NIFTY", Exchange: "NSE",
		TS:   time.Unix(bucket+125, 0).UTC(), // bucket+120
		Open: 300, High: 310, Low: 290, Close: 305, Volume: 1,
	}, outCh)

	// Now send an old candle — should NOT be rejected since tolerance is disabled
	b.process(model.Candle{
		Token: "NIFTY", Exchange: "NSE",
		TS:   time.Unix(bucket+1, 0).UTC(), // original bucket, way behind
		Open: 50, High: 60, Low: 40, Close: 55, Volume: 1,
	}, outCh)

	if staleCount != 0 {
		t.Errorf("expected 0 stale callbacks with tolerance disabled, got %d", staleCount)
	}
}
