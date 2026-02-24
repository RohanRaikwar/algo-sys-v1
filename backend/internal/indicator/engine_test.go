package indicator

import (
	"context"
	"math"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func makeTFCandle(token string, tf int, closePaise int64) model.TFCandle {
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

func TestEngine_SMA20(t *testing.T) {
	engine := NewEngine([]TFIndicatorConfig{
		{
			TF: 60,
			Indicators: []IndicatorConfig{
				{Type: "SMA", Period: 20},
			},
		},
	})

	// Feed 25 candles with close = 100.00 rupees (10000 paise)
	for i := 0; i < 25; i++ {
		results := engine.Process(makeTFCandle("SBIN", 60, 10000))
		if i >= 19 { // SMA period=20, ready after 20 candles
			if len(results) != 1 {
				t.Fatalf("candle %d: expected 1 result, got %d", i, len(results))
			}
			if !results[0].Ready {
				t.Errorf("candle %d: expected Ready=true", i)
			}
			// All closes are 100.00, so SMA should be 100.00
			if math.Abs(results[0].Value-100.0) > 0.001 {
				t.Errorf("candle %d: expected SMA=100.0, got %.4f", i, results[0].Value)
			}
			if results[0].Name != "SMA_20" {
				t.Errorf("candle %d: expected name=SMA_20, got %s", i, results[0].Name)
			}
		}
	}
}

func TestEngine_MultiIndicator(t *testing.T) {
	engine := NewEngine([]TFIndicatorConfig{
		{
			TF: 60,
			Indicators: []IndicatorConfig{
				{Type: "SMA", Period: 5},
				{Type: "EMA", Period: 5},
				{Type: "RSI", Period: 14},
			},
		},
	})

	for i := 0; i < 20; i++ {
		results := engine.Process(makeTFCandle("A", 60, int64(10000+i*100)))
		if len(results) != 3 {
			t.Fatalf("candle %d: expected 3 results, got %d", i, len(results))
		}
	}
}

func TestEngine_MultiTF(t *testing.T) {
	engine := NewEngine([]TFIndicatorConfig{
		{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 5}}},
		{TF: 300, Indicators: []IndicatorConfig{{Type: "EMA", Period: 10}}},
	})

	// Process a 60s candle
	results60 := engine.Process(makeTFCandle("X", 60, 5000))
	if len(results60) != 1 {
		t.Fatalf("expected 1 result for TF=60, got %d", len(results60))
	}
	if results60[0].TF != 60 {
		t.Errorf("expected TF=60, got %d", results60[0].TF)
	}

	// Process a 300s candle
	results300 := engine.Process(makeTFCandle("X", 300, 5000))
	if len(results300) != 1 {
		t.Fatalf("expected 1 result for TF=300, got %d", len(results300))
	}
	if results300[0].TF != 300 {
		t.Errorf("expected TF=300, got %d", results300[0].TF)
	}

	// Process a candle with unconfigured TF
	resultsNone := engine.Process(makeTFCandle("X", 900, 5000))
	if len(resultsNone) != 0 {
		t.Errorf("expected 0 results for unconfigured TF=900, got %d", len(resultsNone))
	}
}

func TestEngine_SkipsFormingCandles(t *testing.T) {
	engine := NewEngine([]TFIndicatorConfig{
		{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 5}}},
	})

	forming := makeTFCandle("Y", 60, 5000)
	forming.Forming = true

	// Direct Process call with forming candle should still work (it's Run that skips)
	// But let's test the Run behavior
	tfCh := make(chan model.TFCandle, 10)
	resCh := make(chan model.IndicatorResult, 10)

	go func() {
		tfCh <- forming
		close(tfCh)
	}()

	engine.Run(context.Background(), tfCh, resCh)

	select {
	case <-resCh:
		t.Fatal("should not receive results for forming candles")
	default:
		// expected
	}
}

func TestProcessPeek_NilBeforeProcess(t *testing.T) {
	engine := NewEngine([]TFIndicatorConfig{
		{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 5}}},
	})

	forming := makeTFCandle("Z", 60, 5000)
	forming.Forming = true

	// ProcessPeek on unknown token should return nil
	results := engine.ProcessPeek(forming)
	if results != nil {
		t.Fatalf("expected nil results before any Process, got %d", len(results))
	}
}

func TestProcessPeek_LiveResults(t *testing.T) {
	engine := NewEngine([]TFIndicatorConfig{
		{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 5}}},
	})

	// Feed 5 completed candles at 100.00 to make SMA ready
	for i := 0; i < 5; i++ {
		engine.Process(makeTFCandle("T1", 60, 10000))
	}

	// Now peek with a forming candle at 110.00
	forming := makeTFCandle("T1", 60, 11000)
	forming.Forming = true

	results := engine.ProcessPeek(forming)
	if len(results) != 1 {
		t.Fatalf("expected 1 peek result, got %d", len(results))
	}

	if !results[0].Live {
		t.Error("expected Live=true on peek result")
	}
	if !results[0].Ready {
		t.Error("expected Ready=true on peek result")
	}

	// Peek value should be (100*4 + 110)/5 = 102.00
	expected := 102.0
	if math.Abs(results[0].Value-expected) > 0.01 {
		t.Errorf("expected peek value=%.2f, got %.4f", expected, results[0].Value)
	}
}

func TestProcessPeek_DoesNotMutateState(t *testing.T) {
	engine := NewEngine([]TFIndicatorConfig{
		{TF: 60, Indicators: []IndicatorConfig{{Type: "SMA", Period: 5}}},
	})

	// Feed 5 candles at 100.00
	for i := 0; i < 5; i++ {
		engine.Process(makeTFCandle("M1", 60, 10000))
	}

	// Record value before peek
	baseline := engine.Process(makeTFCandle("M1", 60, 10000))
	valueBefore := baseline[0].Value

	// Peek with a wildly different price
	forming := makeTFCandle("M1", 60, 99999)
	forming.Forming = true
	engine.ProcessPeek(forming)

	// Process another normal candle — value should NOT be affected by peek
	after := engine.Process(makeTFCandle("M1", 60, 10000))
	if math.Abs(after[0].Value-valueBefore) > 0.001 {
		t.Errorf("ProcessPeek mutated state! before=%.4f after=%.4f", valueBefore, after[0].Value)
	}
}
