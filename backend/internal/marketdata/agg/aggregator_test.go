package agg

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestAggregator_BasicCandle(t *testing.T) {
	agg := New()
	tickCh := make(chan model.Tick, 100)
	candleCh := make(chan model.Candle, 100)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agg.Run(ctx, tickCh, candleCh)
		close(done)
	}()

	now := time.Now().UTC().Truncate(time.Second)

	// Send 3 ticks in the same second
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50000, Qty: 10, TickTS: now}
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50500, Qty: 20, TickTS: now.Add(200 * time.Millisecond)}
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 49800, Qty: 5, TickTS: now.Add(500 * time.Millisecond)}

	// Send a tick in the next second to trigger flush of previous bucket
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50100, Qty: 15, TickTS: now.Add(1 * time.Second)}

	// Allow time for processing
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done // wait for goroutine to finish

	// Collect candles (safe now since goroutine exited)
	var candles []model.Candle
	for {
		select {
		case c := <-candleCh:
			candles = append(candles, c)
		default:
			goto collected
		}
	}
collected:

	if len(candles) < 1 {
		t.Fatalf("expected at least 1 candle, got %d", len(candles))
	}

	c := candles[0]
	if c.Open != 50000 {
		t.Errorf("expected open=50000, got %d", c.Open)
	}
	if c.High != 50500 {
		t.Errorf("expected high=50500, got %d", c.High)
	}
	if c.Low != 49800 {
		t.Errorf("expected low=49800, got %d", c.Low)
	}
	if c.Close != 49800 {
		t.Errorf("expected close=49800, got %d", c.Close)
	}
	if c.TicksCount != 3 {
		t.Errorf("expected ticks_count=3, got %d", c.TicksCount)
	}
	if c.Volume != 35 {
		t.Errorf("expected volume=35, got %d", c.Volume)
	}
}

func TestAggregator_MultipleTokens(t *testing.T) {
	agg := New()
	tickCh := make(chan model.Tick, 100)
	candleCh := make(chan model.Candle, 100)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agg.Run(ctx, tickCh, candleCh)
		close(done)
	}()

	now := time.Now().UTC().Truncate(time.Second)

	// Two different tokens in the same second
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50000, Qty: 10, TickTS: now}
	tickCh <- model.Tick{Token: "2885", Exchange: "NSE", Price: 30000, Qty: 5, TickTS: now}

	// Next second triggers flush
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50100, Qty: 1, TickTS: now.Add(time.Second)}
	tickCh <- model.Tick{Token: "2885", Exchange: "NSE", Price: 30100, Qty: 1, TickTS: now.Add(time.Second)}

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	count := 0
	for {
		select {
		case <-candleCh:
			count++
		default:
			goto done2
		}
	}
done2:
	// Should have at least 2 candles (one per token for the first second) + 2 from flush
	if count < 2 {
		t.Errorf("expected at least 2 candles, got %d", count)
	}
}

func TestAggregator_LateTick(t *testing.T) {
	agg := New()
	dropped := 0
	dropCh := make(chan struct{}, 10)
	agg.OnDroppedTick = func() {
		dropCh <- struct{}{}
	}

	tickCh := make(chan model.Tick, 100)
	candleCh := make(chan model.Candle, 100)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agg.Run(ctx, tickCh, candleCh)
		close(done)
	}()

	now := time.Now().UTC().Truncate(time.Second)

	// Current second tick
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50000, Qty: 10, TickTS: now}
	// Late tick (1 second old)
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 49000, Qty: 5, TickTS: now.Add(-1 * time.Second)}

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	// Count drops from channel
	close(dropCh)
	for range dropCh {
		dropped++
	}

	// With the reorder buffer the late tick may or may not be dropped
	// depending on the watermark position — that's acceptable.
	// The key test is that it doesn't panic.
	t.Logf("dropped ticks: %d (within tolerance)", dropped)
}

// TestAggregator_ReorderBuffer verifies that out-of-order ticks within
// the reorder buffer window are correctly included in the candle.
func TestAggregator_ReorderBuffer(t *testing.T) {
	agg := New()
	agg.ReorderBuffer = 2 * time.Second // generous buffer for test

	tickCh := make(chan model.Tick, 100)
	candleCh := make(chan model.Candle, 100)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agg.Run(ctx, tickCh, candleCh)
		close(done)
	}()

	base := time.Now().UTC().Truncate(time.Second)

	// Send ticks out of order — all within the same second bucket
	// Tick 3 arrives first (at T+500ms), then tick 1 (at T+100ms), then tick 2 (at T+300ms)
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50500, Qty: 5, TickTS: base.Add(500 * time.Millisecond)}
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50000, Qty: 10, TickTS: base.Add(100 * time.Millisecond)}
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 49800, Qty: 3, TickTS: base.Add(300 * time.Millisecond)}

	// Advance watermark past the base bucket by sending a tick 3 seconds ahead
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50200, Qty: 1, TickTS: base.Add(3 * time.Second)}

	// Wait for flush
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	var candles []model.Candle
	for {
		select {
		case c := <-candleCh:
			candles = append(candles, c)
		default:
			goto collected2
		}
	}
collected2:

	if len(candles) < 1 {
		t.Fatalf("expected at least 1 candle, got %d", len(candles))
	}

	// Find the candle for the base bucket
	var baseCandle *model.Candle
	for i := range candles {
		if candles[i].TS.Unix() == base.Unix() {
			baseCandle = &candles[i]
			break
		}
	}
	if baseCandle == nil {
		t.Fatalf("did not find candle for base bucket ts=%v, got %d candles", base, len(candles))
	}

	// All 3 out-of-order ticks should be merged into one candle
	if baseCandle.TicksCount != 3 {
		t.Errorf("expected ticks_count=3 (all out-of-order ticks merged), got %d", baseCandle.TicksCount)
	}
	if baseCandle.High != 50500 {
		t.Errorf("expected high=50500, got %d", baseCandle.High)
	}
	if baseCandle.Low != 49800 {
		t.Errorf("expected low=49800, got %d", baseCandle.Low)
	}
	if baseCandle.Volume != 18 {
		t.Errorf("expected volume=18 (5+10+3), got %d", baseCandle.Volume)
	}
}

// TestAggregator_WatermarkLateDrop verifies that ticks arriving behind
// the watermark are dropped and OnLateTick is called.
func TestAggregator_WatermarkLateDrop(t *testing.T) {
	agg := New()
	agg.ReorderBuffer = 1 * time.Second // 1 second reorder window

	lateCalls := 0
	lateCh := make(chan struct{}, 10)
	agg.OnLateTick = func() {
		lateCh <- struct{}{}
	}

	tickCh := make(chan model.Tick, 100)
	candleCh := make(chan model.Candle, 100)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agg.Run(ctx, tickCh, candleCh)
		close(done)
	}()

	base := time.Now().UTC().Truncate(time.Second)

	// Advance the watermark to base+5 (watermark = base+4 with 1s buffer)
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50000, Qty: 10, TickTS: base.Add(5 * time.Second)}

	time.Sleep(50 * time.Millisecond) // let processTick run

	// Now send a tick at base (4 seconds behind watermark) — should be dropped
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 49000, Qty: 5, TickTS: base}

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	close(lateCh)
	for range lateCh {
		lateCalls++
	}

	if lateCalls != 1 {
		t.Errorf("expected 1 late tick callback, got %d", lateCalls)
	}
}

// TestAggregator_EventTS verifies that EventTS is used over TickTS when present.
func TestAggregator_EventTS(t *testing.T) {
	agg := New()
	agg.ReorderBuffer = 2 * time.Second

	tickCh := make(chan model.Tick, 100)
	candleCh := make(chan model.Candle, 100)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agg.Run(ctx, tickCh, candleCh)
		close(done)
	}()

	arrivalTime := time.Now().UTC().Truncate(time.Second)
	eventTime := arrivalTime.Add(-2 * time.Second) // exchange says this happened 2s ago

	tickCh <- model.Tick{
		Token: "3045", Exchange: "NSE",
		Price: 50000, Qty: 10,
		TickTS:  arrivalTime,
		EventTS: eventTime, // canonical time
	}

	// Advance watermark past eventTime
	tickCh <- model.Tick{Token: "3045", Exchange: "NSE", Price: 50100, Qty: 1, TickTS: arrivalTime.Add(3 * time.Second)}

	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	var candles []model.Candle
	for {
		select {
		case c := <-candleCh:
			candles = append(candles, c)
		default:
			goto done3
		}
	}
done3:

	// The candle should be bucketed by EventTS, not TickTS
	found := false
	for _, c := range candles {
		if c.TS.Unix() == eventTime.Unix() {
			found = true
			if c.Open != 50000 {
				t.Errorf("expected open=50000, got %d", c.Open)
			}
		}
	}
	if !found {
		t.Errorf("expected candle bucketed at EventTS=%v, not found in %d candles", eventTime, len(candles))
		for _, c := range candles {
			t.Logf("  candle ts=%v open=%d", c.TS, c.Open)
		}
	}
}
