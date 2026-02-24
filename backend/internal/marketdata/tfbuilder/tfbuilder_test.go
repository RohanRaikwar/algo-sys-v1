package tfbuilder

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// makeCandle creates a test 1s candle at the given Unix second.
func makeCandle(token string, unixSec int64, open, high, low, close_, vol int64) model.Candle {
	return model.Candle{
		Token:      token,
		Exchange:   "NSE",
		TS:         time.Unix(unixSec, 0).UTC(),
		Open:       open,
		High:       high,
		Low:        low,
		Close:      close_,
		Volume:     vol,
		TicksCount: 1,
	}
}

func TestBuilder_60s_Resampling(t *testing.T) {
	b := New([]int{60})  // 1-minute TF
	b.StaleTolerance = 0 // disable for tests with historical timestamps
	outCh := make(chan model.TFCandle, 5000)

	// Feed 60 1s candles (second 0 to 59) — all in bucket 0
	// Then feed 1 candle in second 60 to trigger finalization
	baseTS := int64(1700000000) // arbitrary base aligned to minute
	baseTS = baseTS - (baseTS % 60)

	for i := int64(0); i < 60; i++ {
		b.process(makeCandle("SBIN", baseTS+i, 500+i, 510+i, 490+i, 505+i, 100), outCh)
	}

	// Drain all forming candles from the channel
	for len(outCh) > 0 {
		c := <-outCh
		if !c.Forming {
			t.Fatalf("unexpected finalized candle before bucket close: %+v", c)
		}
	}

	// Trigger new bucket
	b.process(makeCandle("SBIN", baseTS+60, 600, 610, 590, 605, 100), outCh)

	// Should now have 1 finalized candle among the outputs
	var finalized *model.TFCandle
	for len(outCh) > 0 {
		c := <-outCh
		if !c.Forming {
			finalized = &c
			break
		}
	}

	if finalized == nil {
		t.Fatal("expected a finalized candle after bucket close")
	}
	c := *finalized
	if c.TF != 60 {
		t.Errorf("expected TF=60, got %d", c.TF)
	}
	if c.Token != "SBIN" {
		t.Errorf("expected token=SBIN, got %s", c.Token)
	}
	if c.Open != 500 {
		t.Errorf("expected open=500, got %d", c.Open)
	}
	if c.Close != 564 { // 505 + 59
		t.Errorf("expected close=564, got %d", c.Close)
	}
	if c.High != 569 { // 510 + 59
		t.Errorf("expected high=569, got %d", c.High)
	}
	if c.Low != 490 {
		t.Errorf("expected low=490, got %d", c.Low)
	}
	if c.Volume != 6000 { // 60 * 100
		t.Errorf("expected volume=6000, got %d", c.Volume)
	}
	if c.Count != 60 {
		t.Errorf("expected count=60, got %d", c.Count)
	}
	if c.Forming {
		t.Error("expected forming=false")
	}
}

func TestBuilder_MultipleTFs(t *testing.T) {
	b := New([]int{60, 300}) // 1m and 5m
	b.StaleTolerance = 0     // disable for tests with historical timestamps
	outCh := make(chan model.TFCandle, 10000)

	baseTS := int64(1700000000)
	baseTS = baseTS - (baseTS % 300) // align to 5m boundary

	// Feed 300 candles (5 minutes worth)
	for i := int64(0); i < 300; i++ {
		b.process(makeCandle("RELIANCE", baseTS+i, 2000, 2100, 1900, 2050, 10), outCh)
	}

	// Trigger new bucket for both TFs
	b.process(makeCandle("RELIANCE", baseTS+300, 2100, 2200, 2000, 2150, 10), outCh)

	// Drain channel and separate finalized candles by TF
	var candles1m, candles5m []model.TFCandle
	for len(outCh) > 0 {
		c := <-outCh
		if c.Forming {
			continue // skip forming candles
		}
		if c.TF == 60 {
			candles1m = append(candles1m, c)
		} else if c.TF == 300 {
			candles5m = append(candles5m, c)
		}
	}

	if len(candles1m) != 5 {
		t.Errorf("expected 5 finalized 1m candles, got %d", len(candles1m))
	}
	if len(candles5m) != 1 {
		t.Errorf("expected 1 finalized 5m candle, got %d", len(candles5m))
	}

	// Verify 5m candle has all 300 1s candles merged
	if len(candles5m) > 0 {
		c := candles5m[0]
		if c.Count != 300 {
			t.Errorf("5m candle count: expected 300, got %d", c.Count)
		}
		if c.Volume != 3000 {
			t.Errorf("5m candle volume: expected 3000, got %d", c.Volume)
		}
	}
}

func TestBuilder_MultiToken(t *testing.T) {
	b := New([]int{60})
	b.StaleTolerance = 0
	outCh := make(chan model.TFCandle, 5000)

	baseTS := int64(1700000000)
	baseTS = baseTS - (baseTS % 60)

	// Two tokens same bucket
	for i := int64(0); i < 60; i++ {
		b.process(makeCandle("A", baseTS+i, 100, 110, 90, 105, 1), outCh)
		b.process(makeCandle("B", baseTS+i, 200, 210, 190, 205, 2), outCh)
	}

	// Trigger flush
	b.process(makeCandle("A", baseTS+60, 100, 110, 90, 105, 1), outCh)
	b.process(makeCandle("B", baseTS+60, 200, 210, 190, 205, 2), outCh)

	tokens := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case c := <-outCh:
			tokens[c.Token] = true
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	}

	if !tokens["A"] || !tokens["B"] {
		t.Errorf("expected candles for both A and B, got %v", tokens)
	}
}

func TestBuilder_Run(t *testing.T) {
	b := New([]int{60})
	b.StaleTolerance = 0
	candleCh := make(chan model.Candle, 200)
	outCh := make(chan model.TFCandle, 5000)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx, candleCh, outCh)
		close(done)
	}()

	baseTS := int64(1700000000)
	baseTS = baseTS - (baseTS % 60)

	// Send 60 candles + 1 to trigger
	for i := int64(0); i <= 60; i++ {
		candleCh <- makeCandle("T", baseTS+i, 100, 110, 90, 105, 1)
	}

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done // wait for goroutine to finish

	// Drain from outCh (safe now since goroutine exited)
	count := 0
	for {
		select {
		case <-outCh:
			count++
		default:
			goto drained
		}
	}
drained:

	if count < 1 {
		t.Errorf("expected at least 1 finalized TF candle, got %d", count)
	}
}

func TestBuilder_PartialBucket_NoFinalize(t *testing.T) {
	b := New([]int{60})
	b.StaleTolerance = 0
	outCh := make(chan model.TFCandle, 5000)

	baseTS := int64(1700000000)
	baseTS = baseTS - (baseTS % 60)

	// Only 30 candles, no bucket close
	for i := int64(0); i < 30; i++ {
		b.process(makeCandle("X", baseTS+i, 100, 110, 90, 105, 1), outCh)
	}

	// Drain the forming candles (one per 1s candle processed)
	for {
		select {
		case c := <-outCh:
			if !c.Forming {
				t.Fatalf("unexpected finalized candle from partial bucket: %+v", c)
			}
		default:
			return // all good — only forming candles emitted, no finalized
		}
	}
}
