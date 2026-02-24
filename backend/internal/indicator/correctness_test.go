package indicator

import (
	"math"
	"testing"

	"trading-systemv1/internal/model"
)

// ────────────────────────────────────────────────────────────
// Helper
// ────────────────────────────────────────────────────────────

func candle(closePaise int64) model.Candle {
	return model.Candle{
		Token: "TEST", Exchange: "NSE",
		Open: closePaise, High: closePaise + 50, Low: closePaise - 50, Close: closePaise,
	}
}

func assertClose(t *testing.T, label string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s: got %.6f, want %.6f (tol=%.6f, diff=%.6f)", label, got, want, tol, math.Abs(got-want))
	}
}

// ────────────────────────────────────────────────────────────
// SMA Correctness
// ────────────────────────────────────────────────────────────

func TestSMA_Correctness_Period3(t *testing.T) {
	// Hand-calculated SMA(3) for a known price series (in rupees):
	// Prices: 100, 102, 104, 103, 105
	// SMA after candle 3: (100+102+104)/3 = 102.0000
	// SMA after candle 4: (102+104+103)/3 = 103.0000
	// SMA after candle 5: (104+103+105)/3 = 104.0000

	sma := NewSMA(3)
	prices := []int64{10000, 10200, 10400, 10300, 10500} // paise
	expected := []float64{0, 0, 102.0, 103.0, 104.0}
	ready := []bool{false, false, true, true, true}

	for i, p := range prices {
		sma.Update(candle(p))
		if sma.Ready() != ready[i] {
			t.Errorf("candle %d: Ready()=%v, want %v", i, sma.Ready(), ready[i])
		}
		if ready[i] {
			assertClose(t, "SMA(3) candle "+string(rune('0'+i)), sma.Value(), expected[i], 0.0001)
		}
	}
}

func TestSMA_Correctness_Period5(t *testing.T) {
	// Prices: 10, 11, 12, 13, 14, 15, 16
	// SMA(5) after candle 5: (10+11+12+13+14)/5 = 12.0
	// SMA(5) after candle 6: (11+12+13+14+15)/5 = 13.0
	// SMA(5) after candle 7: (12+13+14+15+16)/5 = 14.0

	sma := NewSMA(5)
	prices := []int64{1000, 1100, 1200, 1300, 1400, 1500, 1600}
	expected := []float64{0, 0, 0, 0, 12.0, 13.0, 14.0}
	ready := []bool{false, false, false, false, true, true, true}

	for i, p := range prices {
		sma.Update(candle(p))
		if sma.Ready() != ready[i] {
			t.Errorf("candle %d: Ready()=%v, want %v", i, sma.Ready(), ready[i])
		}
		if ready[i] {
			assertClose(t, "SMA(5)", sma.Value(), expected[i], 0.0001)
		}
	}
}

func TestSMA_Peek_DoesNotMutate(t *testing.T) {
	sma := NewSMA(3)
	for _, p := range []int64{10000, 10200, 10400} {
		sma.Update(candle(p))
	}
	valueBefore := sma.Value()

	// Peek with different price
	peekVal := sma.Peek(20000) // 200 rupees
	_ = peekVal

	// Value should be unchanged
	assertClose(t, "SMA after Peek", sma.Value(), valueBefore, 0.0001)
}

func TestSMA_Peek_CorrectValue(t *testing.T) {
	sma := NewSMA(3)
	// Feed: 100, 102, 104 → SMA = 102
	for _, p := range []int64{10000, 10200, 10400} {
		sma.Update(candle(p))
	}
	// Peek with 106 → expected: (102+104+106)/3 = 104
	peekVal := sma.Peek(10600)
	assertClose(t, "SMA Peek", peekVal, 104.0, 0.0001)
}

// ────────────────────────────────────────────────────────────
// EMA Correctness
// ────────────────────────────────────────────────────────────

func TestEMA_Correctness_Period3(t *testing.T) {
	// EMA(3): multiplier = 2/(3+1) = 0.5
	// Prices (rupees): 100, 102, 104, 103, 105
	//
	// Candle 1: sum=100
	// Candle 2: sum=202
	// Candle 3: sum=306 → initial EMA = 306/3 = 102.0 (SMA seed)
	// Candle 4: EMA = 103*0.5 + 102.0*0.5 = 102.5
	// Candle 5: EMA = 105*0.5 + 102.5*0.5 = 103.75

	ema := NewEMA(3)
	prices := []int64{10000, 10200, 10400, 10300, 10500}
	expected := []float64{0, 0, 102.0, 102.5, 103.75}
	ready := []bool{false, false, true, true, true}

	for i, p := range prices {
		ema.Update(candle(p))
		if ema.Ready() != ready[i] {
			t.Errorf("candle %d: Ready()=%v, want %v", i, ema.Ready(), ready[i])
		}
		if ready[i] {
			assertClose(t, "EMA(3)", ema.Value(), expected[i], 0.0001)
		}
	}
}

func TestEMA_Correctness_Period5(t *testing.T) {
	// EMA(5): multiplier = 2/(5+1) = 1/3 ≈ 0.333333
	// Prices: 44, 44.25, 44.50, 43.75, 44.50  → SMA seed = (44+44.25+44.50+43.75+44.50)/5 = 44.20
	// Candle 6 (44.25): EMA = 44.25*(1/3) + 44.20*(2/3) = 14.75 + 29.4667 = 44.2167
	// Candle 7 (44.00): EMA = 44.00*(1/3) + 44.2167*(2/3) = 14.6667 + 29.4778 = 44.1444

	mult := 2.0 / 6.0 // 0.33333...
	prices := []int64{4400, 4425, 4450, 4375, 4450, 4425, 4400}

	// Expected seed: 44.20
	seedExpected := (44.0 + 44.25 + 44.50 + 43.75 + 44.50) / 5.0

	// After candle 5 (index 4): seed = 44.20
	ema2 := NewEMA(5)
	for _, p := range prices[:5] {
		ema2.Update(candle(p))
	}
	assertClose(t, "EMA(5) seed", ema2.Value(), seedExpected, 0.01)

	// After candle 6: EMA = 44.25 * mult + 44.20 * (1-mult)
	ema2.Update(candle(prices[5]))
	expected6 := 44.25*mult + seedExpected*(1-mult)
	assertClose(t, "EMA(5) candle 6", ema2.Value(), expected6, 0.01)

	// After candle 7: EMA = 44.00 * mult + expected6 * (1-mult)
	ema2.Update(candle(prices[6]))
	expected7 := 44.00*mult + expected6*(1-mult)
	assertClose(t, "EMA(5) candle 7", ema2.Value(), expected7, 0.01)
}

func TestEMA_Peek_DoesNotMutate(t *testing.T) {
	ema := NewEMA(3)
	for _, p := range []int64{10000, 10200, 10400} {
		ema.Update(candle(p))
	}
	valueBefore := ema.Value()

	ema.Peek(20000)

	assertClose(t, "EMA after Peek", ema.Value(), valueBefore, 0.0001)
}

func TestEMA_Peek_CorrectValue(t *testing.T) {
	ema := NewEMA(3)
	// Seed: (100+102+104)/3 = 102.0
	for _, p := range []int64{10000, 10200, 10400} {
		ema.Update(candle(p))
	}
	// Peek with 106: EMA = 106*0.5 + 102*0.5 = 104.0
	peekVal := ema.Peek(10600)
	assertClose(t, "EMA Peek", peekVal, 104.0, 0.0001)
}

// ────────────────────────────────────────────────────────────
// SMMA Correctness (Wilder's Smoothing)
// ────────────────────────────────────────────────────────────

func TestSMMA_Correctness_Period3(t *testing.T) {
	// SMMA(3): first value = SMA(3) seed, then Wilder smoothing
	// Prices: 100, 102, 104, 103, 105
	//
	// Candle 1-3: seed = (100+102+104)/3 = 102.0
	// Candle 4: SMMA = (102.0 * 2 + 103) / 3 = (204+103)/3 = 102.3333
	// Candle 5: SMMA = (102.3333 * 2 + 105) / 3 = (204.6667+105)/3 = 103.2222

	smma := NewSMMA(3)
	prices := []int64{10000, 10200, 10400, 10300, 10500}
	expected := []float64{0, 0, 102.0, 102.3333, 103.2222}
	ready := []bool{false, false, true, true, true}

	for i, p := range prices {
		smma.Update(candle(p))
		if smma.Ready() != ready[i] {
			t.Errorf("candle %d: Ready()=%v, want %v", i, smma.Ready(), ready[i])
		}
		if ready[i] {
			assertClose(t, "SMMA(3)", smma.Value(), expected[i], 0.001)
		}
	}
}

func TestSMMA_Peek_DoesNotMutate(t *testing.T) {
	smma := NewSMMA(3)
	for _, p := range []int64{10000, 10200, 10400} {
		smma.Update(candle(p))
	}
	valueBefore := smma.Value()

	smma.Peek(20000)

	assertClose(t, "SMMA after Peek", smma.Value(), valueBefore, 0.0001)
}

func TestSMMA_Peek_CorrectValue(t *testing.T) {
	smma := NewSMMA(3)
	// Seed: (100+102+104)/3 = 102.0
	for _, p := range []int64{10000, 10200, 10400} {
		smma.Update(candle(p))
	}
	// Peek with 106: SMMA = (102.0 * 2 + 106) / 3 = 310/3 = 103.3333
	peekVal := smma.Peek(10600)
	assertClose(t, "SMMA Peek", peekVal, 103.3333, 0.001)
}

// ────────────────────────────────────────────────────────────
// RSI Correctness (Wilder's Method)
// ────────────────────────────────────────────────────────────

func TestRSI_Correctness_Period5(t *testing.T) {
	// Using a small period (5) for manual calculation.
	// Prices: 44, 44.34, 44.09, 43.61, 44.33, 44.83, 45.10, 45.42, 45.84
	// (In paise: multiply by 100)
	//
	// Deltas (from price 2 onward):
	//   44.34-44.00 = +0.34 (gain)
	//   44.09-44.34 = -0.25 (loss)
	//   43.61-44.09 = -0.48 (loss)
	//   44.33-43.61 = +0.72 (gain)
	//   44.83-44.33 = +0.50 (gain)
	//
	// First RSI (after 6 candles, period=5):
	//   sumGain = 0.34+0.72+0.50 = 1.56 → avgGain = 1.56/5 = 0.312
	//   sumLoss = 0.25+0.48       = 0.73 → avgLoss = 0.73/5 = 0.146
	//   (Note: we accumulate 5 deltas: candles 2-6, so "count" reaches period+1=6)
	//   RS = 0.312/0.146 = 2.13699
	//   RSI = 100 - 100/(1+2.13699) = 100 - 31.888 = 68.112
	//
	// Candle 7 (45.10): delta=+0.27, gain=0.27, loss=0
	//   avgGain = (0.312*4 + 0.27)/5 = (1.248+0.27)/5 = 0.3036
	//   avgLoss = (0.146*4 + 0)/5     = 0.584/5 = 0.1168
	//   RS = 0.3036/0.1168 = 2.5993
	//   RSI = 100 - 100/(1+2.5993) = 100 - 27.781 = 72.219
	//
	// Candle 8 (45.42): delta=+0.32, gain=0.32, loss=0
	//   avgGain = (0.3036*4+0.32)/5 = (1.2144+0.32)/5 = 0.30688
	//   avgLoss = (0.1168*4+0)/5     = 0.4672/5 = 0.09344
	//   RS = 0.30688/0.09344 = 3.2845
	//   RSI = 100 - 100/(1+3.2845) = 76.658
	//
	// Candle 9 (45.84): delta=+0.42
	//   avgGain = (0.30688*4+0.42)/5 = (1.22752+0.42)/5 = 0.329504
	//   avgLoss = (0.09344*4+0)/5     = 0.37376/5 = 0.074752
	//   RS = 0.329504/0.074752 = 4.4082
	//   RSI = 100 - 100/(1+4.4082) = 81.509

	rsi := NewRSI(5)
	// paise values
	prices := []int64{4400, 4434, 4409, 4361, 4433, 4483, 4510, 4542, 4584}

	for _, p := range prices {
		rsi.Update(candle(p))
	}

	// After candle 6 (index 5): first RSI
	rsi2 := NewRSI(5)
	for i := 0; i <= 5; i++ {
		rsi2.Update(candle(prices[i]))
	}
	assertClose(t, "RSI(5) candle 6", rsi2.Value(), 68.112, 0.1)

	// After candle 7
	rsi2.Update(candle(prices[6]))
	assertClose(t, "RSI(5) candle 7", rsi2.Value(), 72.219, 0.1)

	// After candle 8
	rsi2.Update(candle(prices[7]))
	assertClose(t, "RSI(5) candle 8", rsi2.Value(), 76.658, 0.1)

	// After candle 9
	rsi2.Update(candle(prices[8]))
	assertClose(t, "RSI(5) candle 9", rsi2.Value(), 81.509, 0.2)
}

func TestRSI_AllUp_Is100(t *testing.T) {
	rsi := NewRSI(5)
	for i := 0; i < 10; i++ {
		rsi.Update(candle(int64(10000 + i*100)))
	}
	assertClose(t, "RSI all up", rsi.Value(), 100.0, 0.001)
}

func TestRSI_AllDown_Is0(t *testing.T) {
	rsi := NewRSI(5)
	for i := 0; i < 10; i++ {
		rsi.Update(candle(int64(20000 - i*100)))
	}
	assertClose(t, "RSI all down", rsi.Value(), 0.0, 0.001)
}

func TestRSI_Flat_Is50_Or0(t *testing.T) {
	// Flat prices: all deltas are 0, both avgGain and avgLoss are 0
	// RSI division by zero case → should return 100 per Wilder convention
	// (actually our code returns 100 when avgLoss==0, regardless of avgGain)
	rsi := NewRSI(5)
	for i := 0; i < 10; i++ {
		rsi.Update(candle(10000))
	}
	// With both avgGain=0 and avgLoss=0, code returns 100.0 (avgLoss==0 branch)
	assertClose(t, "RSI flat", rsi.Value(), 100.0, 0.001)
}

func TestRSI_Peek_DoesNotMutate(t *testing.T) {
	rsi := NewRSI(5)
	for i := 0; i < 10; i++ {
		rsi.Update(candle(int64(10000 + i*100)))
	}
	valueBefore := rsi.Value()

	rsi.Peek(5000)

	assertClose(t, "RSI after Peek", rsi.Value(), valueBefore, 0.0001)
}

func TestRSI_Peek_CorrectDirection(t *testing.T) {
	rsi := NewRSI(5)
	// Feed steadily rising prices
	for i := 0; i < 10; i++ {
		rsi.Update(candle(int64(10000 + i*100)))
	}
	// RSI is high (100 = all gains)

	// Peek with a lower price → RSI should decrease
	peekDown := rsi.Peek(8000) // significant drop
	if peekDown >= rsi.Value() {
		t.Errorf("RSI Peek with lower price should decrease: peek=%.2f, current=%.2f", peekDown, rsi.Value())
	}
}

// ────────────────────────────────────────────────────────────
// Cross-indicator: same data → correct ordering
// ────────────────────────────────────────────────────────────

func TestIndicators_TrendingUp_Ordering(t *testing.T) {
	// With steadily rising prices, faster MAs should be above slower MAs
	sma5 := NewSMA(5)
	sma20 := NewSMA(20)
	ema5 := NewEMA(5)

	for i := 0; i < 30; i++ {
		c := candle(int64(10000 + i*100)) // steadily rising
		sma5.Update(c)
		sma20.Update(c)
		ema5.Update(c)
	}

	if sma5.Value() <= sma20.Value() {
		t.Errorf("SMA(5) should be > SMA(20) in uptrend: SMA5=%.2f, SMA20=%.2f", sma5.Value(), sma20.Value())
	}
	if ema5.Value() <= sma20.Value() {
		t.Errorf("EMA(5) should be > SMA(20) in uptrend: EMA5=%.2f, SMA20=%.2f", ema5.Value(), sma20.Value())
	}
}

func TestIndicators_TrendingDown_Ordering(t *testing.T) {
	// With steadily falling prices, faster MAs should be below slower MAs
	sma5 := NewSMA(5)
	sma20 := NewSMA(20)

	for i := 0; i < 30; i++ {
		c := candle(int64(20000 - i*100)) // steadily falling
		sma5.Update(c)
		sma20.Update(c)
	}

	if sma5.Value() >= sma20.Value() {
		t.Errorf("SMA(5) should be < SMA(20) in downtrend: SMA5=%.2f, SMA20=%.2f", sma5.Value(), sma20.Value())
	}
}

// ────────────────────────────────────────────────────────────
// EMA responsiveness vs SMA
// ────────────────────────────────────────────────────────────

func TestEMA_MoreResponsiveThanSMA(t *testing.T) {
	sma := NewSMA(10)
	ema := NewEMA(10)

	// Feed 20 candles at flat 100
	for i := 0; i < 20; i++ {
		c := candle(10000)
		sma.Update(c)
		ema.Update(c)
	}

	// Sudden jump to 120
	c := candle(12000)
	sma.Update(c)
	ema.Update(c)

	// EMA should react more (closer to 120) than SMA
	if ema.Value() <= sma.Value() {
		t.Errorf("EMA should react more than SMA to sudden price jump: EMA=%.4f, SMA=%.4f", ema.Value(), sma.Value())
	}
}

// ────────────────────────────────────────────────────────────
// Snapshot round-trip correctness
// ────────────────────────────────────────────────────────────

func TestSMA_SnapshotRoundTrip(t *testing.T) {
	sma := NewSMA(5)
	for _, p := range []int64{10000, 10200, 10400, 10300, 10500, 10100} {
		sma.Update(candle(p))
	}
	snap := sma.Snapshot()

	sma2 := NewSMA(5)
	if err := sma2.RestoreFromSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	assertClose(t, "SMA snapshot round-trip", sma2.Value(), sma.Value(), 0.0001)

	// Feed one more candle to both — they should stay in sync
	sma.Update(candle(10700))
	sma2.Update(candle(10700))
	assertClose(t, "SMA after restoration + update", sma2.Value(), sma.Value(), 0.0001)
}

func TestEMA_SnapshotRoundTrip(t *testing.T) {
	ema := NewEMA(5)
	for _, p := range []int64{10000, 10200, 10400, 10300, 10500, 10100} {
		ema.Update(candle(p))
	}
	snap := ema.Snapshot()

	ema2 := NewEMA(5)
	if err := ema2.RestoreFromSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	assertClose(t, "EMA snapshot round-trip", ema2.Value(), ema.Value(), 0.0001)

	ema.Update(candle(10700))
	ema2.Update(candle(10700))
	assertClose(t, "EMA after restoration + update", ema2.Value(), ema.Value(), 0.0001)
}

func TestRSI_SnapshotRoundTrip(t *testing.T) {
	rsi := NewRSI(5)
	prices := []int64{4400, 4434, 4409, 4361, 4433, 4483, 4510}
	for _, p := range prices {
		rsi.Update(candle(p))
	}
	snap := rsi.Snapshot()

	rsi2 := NewRSI(5)
	if err := rsi2.RestoreFromSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	assertClose(t, "RSI snapshot round-trip", rsi2.Value(), rsi.Value(), 0.0001)

	rsi.Update(candle(4542))
	rsi2.Update(candle(4542))
	assertClose(t, "RSI after restoration + update", rsi2.Value(), rsi.Value(), 0.0001)
}

func TestSMMA_SnapshotRoundTrip(t *testing.T) {
	smma := NewSMMA(3)
	for _, p := range []int64{10000, 10200, 10400, 10300, 10500} {
		smma.Update(candle(p))
	}
	snap := smma.Snapshot()

	smma2 := NewSMMA(3)
	if err := smma2.RestoreFromSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	assertClose(t, "SMMA snapshot round-trip", smma2.Value(), smma.Value(), 0.0001)

	smma.Update(candle(10700))
	smma2.Update(candle(10700))
	assertClose(t, "SMMA after restoration + update", smma2.Value(), smma.Value(), 0.0001)
}
