package bus

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestFanOut_BroadcastsToAll(t *testing.T) {
	fo := New(10)
	out1 := fo.Subscribe()
	out2 := fo.Subscribe()

	input := make(chan model.Candle, 10)
	ctx, cancel := context.WithCancel(context.Background())
	go fo.Run(ctx, input)

	candle := model.Candle{
		Token:    "3045",
		Exchange: "NSE",
		Open:     100,
		High:     110,
		Low:      90,
		Close:    105,
	}

	input <- candle
	time.Sleep(50 * time.Millisecond)

	select {
	case c := <-out1:
		if c.Token != "3045" {
			t.Errorf("out1: expected token 3045, got %s", c.Token)
		}
	case <-time.After(time.Second):
		t.Fatal("out1: timed out waiting for candle")
	}

	select {
	case c := <-out2:
		if c.Token != "3045" {
			t.Errorf("out2: expected token 3045, got %s", c.Token)
		}
	case <-time.After(time.Second):
		t.Fatal("out2: timed out waiting for candle")
	}

	cancel()
}
