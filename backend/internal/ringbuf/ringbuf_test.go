package ringbuf

import (
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestRing_BasicPushPop(t *testing.T) {
	r := New(4) // rounds to 4

	c1 := model.Candle{Token: "A", Open: 100}
	c2 := model.Candle{Token: "B", Open: 200}

	if !r.Push(c1) {
		t.Fatal("push c1 should succeed")
	}
	if !r.Push(c2) {
		t.Fatal("push c2 should succeed")
	}

	if r.Len() != 2 {
		t.Fatalf("expected len=2, got %d", r.Len())
	}

	got, ok := r.Pop()
	if !ok || got.Token != "A" {
		t.Fatalf("expected A, got %v ok=%v", got.Token, ok)
	}

	got, ok = r.Pop()
	if !ok || got.Token != "B" {
		t.Fatalf("expected B, got %v ok=%v", got.Token, ok)
	}

	_, ok = r.Pop()
	if ok {
		t.Fatal("pop from empty should return false")
	}
}

func TestRing_Overflow(t *testing.T) {
	r := New(2) // capacity = 2

	r.Push(model.Candle{Token: "1"})
	r.Push(model.Candle{Token: "2"})

	// Buffer is full
	ok := r.Push(model.Candle{Token: "3"})
	if ok {
		t.Fatal("push to full buffer should return false")
	}
	if r.Overflow() != 1 {
		t.Fatalf("expected overflow=1, got %d", r.Overflow())
	}
}

func TestRing_Wraparound(t *testing.T) {
	r := New(4)

	// Fill and drain multiple times to test wraparound
	for round := 0; round < 5; round++ {
		for i := 0; i < 4; i++ {
			if !r.Push(model.Candle{Token: "X", Open: int64(round*10 + i)}) {
				t.Fatalf("round %d push %d failed", round, i)
			}
		}
		for i := 0; i < 4; i++ {
			c, ok := r.Pop()
			if !ok {
				t.Fatalf("round %d pop %d failed", round, i)
			}
			if c.Open != int64(round*10+i) {
				t.Fatalf("round %d pop %d: expected open=%d, got %d", round, i, round*10+i, c.Open)
			}
		}
	}
}

func TestRing_SPSC_Concurrent(t *testing.T) {
	const count = 100_000
	r := New(1024)

	var wg sync.WaitGroup
	wg.Add(2)

	// Producer
	go func() {
		defer wg.Done()
		for i := 0; i < count; i++ {
			for !r.Push(model.Candle{Open: int64(i)}) {
				// spin-wait (busy loop for test only)
			}
		}
	}()

	// Consumer
	received := make([]int64, 0, count)
	go func() {
		defer wg.Done()
		for len(received) < count {
			c, ok := r.Pop()
			if ok {
				received = append(received, c.Open)
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SPSC test timed out")
	}

	// Verify ordering
	for i, v := range received {
		if v != int64(i) {
			t.Fatalf("at index %d: expected %d, got %d", i, i, v)
		}
	}
}

func TestRing_NextPow2(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 1}, {1, 1}, {2, 2}, {3, 4}, {5, 8}, {7, 8}, {8, 8}, {9, 16}, {1023, 1024},
	}
	for _, tc := range cases {
		got := nextPow2(tc.in)
		if got != tc.want {
			t.Errorf("nextPow2(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
