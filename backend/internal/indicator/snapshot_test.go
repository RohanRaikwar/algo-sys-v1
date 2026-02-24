package indicator

import (
	"math"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func makeTFCandleSnap(token string, tf int, closePaise int64) model.TFCandle {
	return model.TFCandle{
		Token:    token,
		Exchange: "NSE",
		TF:       tf,
		TS:       time.Now().UTC(),
		Open:     closePaise,
		High:     closePaise + 100,
		Low:      closePaise - 100,
		Close:    closePaise,
		Volume:   100,
		Count:    60,
		Forming:  false,
	}
}

func TestSnapshot_SMA_RoundTrip(t *testing.T) {
	sma := NewSMA(5)
	prices := []int64{10000, 10100, 10200, 10300, 10400, 10500, 10600}

	for _, p := range prices {
		sma.Update(model.Candle{Close: p})
	}

	// Snapshot
	snap := sma.Snapshot()

	// Restore
	sma2 := NewSMA(5)
	if err := sma2.RestoreFromSnapshot(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Values must match exactly
	if sma.Value() != sma2.Value() {
		t.Errorf("value mismatch: original=%.4f restored=%.4f", sma.Value(), sma2.Value())
	}
	if sma.Ready() != sma2.Ready() {
		t.Errorf("ready mismatch: original=%v restored=%v", sma.Ready(), sma2.Ready())
	}

	// Feed more data — both must produce identical results
	for _, p := range []int64{10700, 10800, 10900} {
		sma.Update(model.Candle{Close: p})
		sma2.Update(model.Candle{Close: p})
		if math.Abs(sma.Value()-sma2.Value()) > 1e-10 {
			t.Errorf("post-restore divergence: original=%.6f restored=%.6f", sma.Value(), sma2.Value())
		}
	}
}

func TestSnapshot_EMA_RoundTrip(t *testing.T) {
	ema := NewEMA(5)
	prices := []int64{10000, 10100, 10200, 10300, 10400, 10500, 10600}

	for _, p := range prices {
		ema.Update(model.Candle{Close: p})
	}

	snap := ema.Snapshot()

	ema2 := NewEMA(5)
	if err := ema2.RestoreFromSnapshot(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if ema.Value() != ema2.Value() {
		t.Errorf("value mismatch: original=%.4f restored=%.4f", ema.Value(), ema2.Value())
	}

	// Feed more data
	for _, p := range []int64{10700, 10800, 10900} {
		ema.Update(model.Candle{Close: p})
		ema2.Update(model.Candle{Close: p})
		if math.Abs(ema.Value()-ema2.Value()) > 1e-10 {
			t.Errorf("post-restore divergence: original=%.6f restored=%.6f", ema.Value(), ema2.Value())
		}
	}
}

func TestSnapshot_SMMA_RoundTrip(t *testing.T) {
	smma := NewSMMA(5)
	prices := []int64{10000, 10100, 10200, 10300, 10400, 10500, 10600}

	for _, p := range prices {
		smma.Update(model.Candle{Close: p})
	}

	snap := smma.Snapshot()

	smma2 := NewSMMA(5)
	if err := smma2.RestoreFromSnapshot(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if smma.Value() != smma2.Value() {
		t.Errorf("value mismatch: original=%.4f restored=%.4f", smma.Value(), smma2.Value())
	}

	// Feed more data
	for _, p := range []int64{10700, 10800, 10900} {
		smma.Update(model.Candle{Close: p})
		smma2.Update(model.Candle{Close: p})
		if math.Abs(smma.Value()-smma2.Value()) > 1e-10 {
			t.Errorf("post-restore divergence: original=%.6f restored=%.6f", smma.Value(), smma2.Value())
		}
	}
}

func TestSnapshot_RSI_RoundTrip(t *testing.T) {
	rsi := NewRSI(14)
	// Simulate 20 price changes
	prices := []int64{
		10000, 10100, 10050, 10200, 10150, 10300, 10250, 10400,
		10350, 10500, 10450, 10600, 10550, 10700, 10650, 10800,
		10750, 10900, 10850, 11000,
	}

	for _, p := range prices {
		rsi.Update(model.Candle{Close: p})
	}

	snap := rsi.Snapshot()

	rsi2 := NewRSI(14)
	if err := rsi2.RestoreFromSnapshot(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if rsi.Value() != rsi2.Value() {
		t.Errorf("value mismatch: original=%.4f restored=%.4f", rsi.Value(), rsi2.Value())
	}

	// Feed more data
	for _, p := range []int64{11100, 11050, 11200} {
		rsi.Update(model.Candle{Close: p})
		rsi2.Update(model.Candle{Close: p})
		if math.Abs(rsi.Value()-rsi2.Value()) > 1e-10 {
			t.Errorf("post-restore divergence: original=%.6f restored=%.6f", rsi.Value(), rsi2.Value())
		}
	}
}

func TestSnapshot_Engine_RoundTrip(t *testing.T) {
	configs := []TFIndicatorConfig{
		{
			TF: 60,
			Indicators: []IndicatorConfig{
				{Type: "SMA", Period: 5},
				{Type: "EMA", Period: 5},
				{Type: "RSI", Period: 14},
			},
		},
	}

	engine := NewEngine(configs)

	// Feed 20 candles with varying prices
	for i := 0; i < 20; i++ {
		engine.Process(makeTFCandleSnap("SBIN", 60, int64(10000+i*100)))
	}

	// Snapshot the engine
	snap, err := SnapshotEngine(engine, "test-stream-id")
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	if snap.StreamID != "test-stream-id" {
		t.Errorf("stream ID mismatch: got %s", snap.StreamID)
	}

	// Restore
	engine2, err := RestoreEngine(configs, snap)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Feed more candles to both engines — must produce identical results
	for i := 0; i < 5; i++ {
		price := int64(12000 + i*100)
		r1 := engine.Process(makeTFCandleSnap("SBIN", 60, price))
		r2 := engine2.Process(makeTFCandleSnap("SBIN", 60, price))

		if len(r1) != len(r2) {
			t.Fatalf("result count mismatch at candle %d: %d vs %d", i, len(r1), len(r2))
		}

		for j := range r1 {
			if math.Abs(r1[j].Value-r2[j].Value) > 1e-10 {
				t.Errorf("candle %d indicator %s: original=%.6f restored=%.6f",
					i, r1[j].Name, r1[j].Value, r2[j].Value)
			}
		}
	}
}
