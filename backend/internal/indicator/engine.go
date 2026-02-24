package indicator

import (
	"context"

	"trading-systemv1/internal/model"
)

// IndicatorConfig specifies a single indicator to compute.
type IndicatorConfig struct {
	Type   string // "SMA", "EMA", "RSI"
	Period int
}

// TFIndicatorConfig groups indicator configs for a specific timeframe.
type TFIndicatorConfig struct {
	TF         int // timeframe in seconds
	Indicators []IndicatorConfig
}

// tokenIndicators holds live indicator instances for one token within a TF.
type tokenIndicators struct {
	indicators []Indicator
	configs    []IndicatorConfig
}

// Engine computes multiple indicators across multiple TFs for multiple tokens.
// Designed for single-goroutine usage — no locks needed.
type Engine struct {
	configs []TFIndicatorConfig

	// state[tfIdx][tokenKey] → *tokenIndicators
	state []map[string]*tokenIndicators
}

// NewEngine creates an indicator engine with the given per-TF indicator configs.
func NewEngine(configs []TFIndicatorConfig) *Engine {
	state := make([]map[string]*tokenIndicators, len(configs))
	for i := range state {
		state[i] = make(map[string]*tokenIndicators, 64)
	}
	return &Engine{
		configs: configs,
		state:   state,
	}
}

// Process takes a finalized TF candle and computes all indicators for that TF + token.
// Returns indicator results (may include not-ready indicators with Ready=false).
func (e *Engine) Process(tfc model.TFCandle) []model.IndicatorResult {
	// Find the matching TF index
	tfIdx := -1
	for i, cfg := range e.configs {
		if cfg.TF == tfc.TF {
			tfIdx = i
			break
		}
	}
	if tfIdx == -1 {
		return nil // TF not configured for indicators
	}

	key := tfc.Key()
	ti, exists := e.state[tfIdx][key]
	if !exists {
		// First candle for this token + TF — create indicator instances
		ti = e.createTokenIndicators(tfIdx)
		e.state[tfIdx][key] = ti
	}

	// Create a model.Candle from the TFCandle for indicator Update()
	candle := model.Candle{
		Token:    tfc.Token,
		Exchange: tfc.Exchange,
		TS:       tfc.TS,
		Open:     tfc.Open,
		High:     tfc.High,
		Low:      tfc.Low,
		Close:    tfc.Close,
		Volume:   tfc.Volume,
	}

	// Update all indicators and collect results (one pass)
	results := make([]model.IndicatorResult, 0, len(ti.indicators))
	for i, ind := range ti.indicators {
		ind.Update(candle)
		cfg := ti.configs[i]
		results = append(results, model.IndicatorResult{
			Name:     ind.Name() + "_" + itoaInd(cfg.Period),
			Token:    tfc.Token,
			Exchange: tfc.Exchange,
			TF:       tfc.TF,
			Value:    ind.Value(),
			TS:       tfc.TS,
			Ready:    ind.Ready(),
		})
	}

	return results
}

// ProcessPeek computes live indicator values for a forming TF candle using Peek().
// Does NOT mutate indicator state — safe for streaming updates every second.
// Returns nil if token hasn't been seen before (need at least one Process first).
func (e *Engine) ProcessPeek(tfc model.TFCandle) []model.IndicatorResult {
	// Find the matching TF index
	tfIdx := -1
	for i, cfg := range e.configs {
		if cfg.TF == tfc.TF {
			tfIdx = i
			break
		}
	}
	if tfIdx == -1 {
		return nil
	}

	key := tfc.Key()
	ti, exists := e.state[tfIdx][key]
	if !exists {
		// Token hasn't been seeded by a completed candle yet — skip peek.
		// indengine calls Process() on completed candles first, so this is safe.
		return nil
	}

	results := make([]model.IndicatorResult, 0, len(ti.indicators))
	for i, ind := range ti.indicators {
		cfg := ti.configs[i]
		results = append(results, model.IndicatorResult{
			Name:     ind.Name() + "_" + itoaInd(cfg.Period),
			Token:    tfc.Token,
			Exchange: tfc.Exchange,
			TF:       tfc.TF,
			Value:    ind.Peek(tfc.Close),
			TS:       tfc.TS,
			Ready:    ind.Ready(),
			Live:     true,
		})
	}
	return results
}

// Run consumes TF candles and emits indicator results. Blocks until ctx done.
func (e *Engine) Run(ctx context.Context, tfCandleCh <-chan model.TFCandle, resultCh chan<- model.IndicatorResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-tfCandleCh:
			if !ok {
				return
			}
			if tfc.Forming {
				continue // skip forming candles
			}
			results := e.Process(tfc)
			for _, r := range results {
				select {
				case resultCh <- r:
				default:
					// drop if channel full
				}
			}
		}
	}
}

// createTokenIndicators creates fresh indicator instances for a TF config.
func (e *Engine) createTokenIndicators(tfIdx int) *tokenIndicators {
	cfg := e.configs[tfIdx]
	inds := make([]Indicator, len(cfg.Indicators))
	for i, ic := range cfg.Indicators {
		switch ic.Type {
		case "SMA":
			inds[i] = NewSMA(ic.Period)
		case "EMA":
			inds[i] = NewEMA(ic.Period)
		case "RSI":
			inds[i] = NewRSI(ic.Period)
		default:
			inds[i] = NewSMA(ic.Period) // fallback
		}
	}
	return &tokenIndicators{
		indicators: inds,
		configs:    cfg.Indicators,
	}
}

// itoaInd converts int to string without importing strconv.
func itoaInd(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
