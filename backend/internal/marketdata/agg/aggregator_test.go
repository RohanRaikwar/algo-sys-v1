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

	if dropped != 1 {
		t.Errorf("expected 1 dropped tick, got %d", dropped)
	}
}
