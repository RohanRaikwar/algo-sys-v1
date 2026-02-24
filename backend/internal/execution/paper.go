package execution

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/strategy"
)

// Fill represents a simulated order fill.
type Fill struct {
	OrderID   string          `json:"order_id"`
	Signal    strategy.Signal `json:"signal"`
	FillPrice int64           `json:"fill_price"` // in paise
	FillQty   int64           `json:"fill_qty"`
	FilledAt  time.Time       `json:"filled_at"`
	Slippage  int64           `json:"slippage"` // simulated slippage in paise
}

// PaperExecutor simulates order execution without real broker calls.
// Useful for backtesting and paper trading.
type PaperExecutor struct {
	mu       sync.RWMutex
	fills    []Fill
	resultCh chan OrderResult
	orderSeq int64

	// Simulation parameters
	slippageBps int64 // basis points of slippage (e.g., 5 = 0.05%)
}

// NewPaperExecutor creates a paper trading executor.
// slippageBps controls simulated slippage in basis points.
func NewPaperExecutor(resultBufferSize int, slippageBps int64) *PaperExecutor {
	return &PaperExecutor{
		fills:       make([]Fill, 0, 1000),
		resultCh:    make(chan OrderResult, resultBufferSize),
		slippageBps: slippageBps,
	}
}

// Results returns the channel of order results.
func (p *PaperExecutor) Results() <-chan OrderResult {
	return p.resultCh
}

// GetFills returns a snapshot of all fills.
func (p *PaperExecutor) GetFills() []Fill {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cp := make([]Fill, len(p.fills))
	copy(cp, p.fills)
	return cp
}

// Run consumes strategy signals and simulates execution.
func (p *PaperExecutor) Run(ctx context.Context, signalCh <-chan strategy.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-signalCh:
			if !ok {
				return
			}
			p.executeSignal(sig)
		}
	}
}

func (p *PaperExecutor) executeSignal(sig strategy.Signal) {
	p.mu.Lock()
	p.orderSeq++
	orderID := fmt.Sprintf("PAPER-%d", p.orderSeq)

	// Calculate fill price with simulated slippage
	fillPrice := sig.Price
	if fillPrice == 0 {
		// Market order — no specific price, use 0 (will be set by portfolio)
		fillPrice = 0
	}

	slippage := int64(0)
	if fillPrice > 0 && p.slippageBps > 0 {
		slippage = fillPrice * p.slippageBps / 10000
		if sig.Action == strategy.ActionBuy {
			fillPrice += slippage // buy higher
		} else {
			fillPrice -= slippage // sell lower
		}
	}

	fill := Fill{
		OrderID:   orderID,
		Signal:    sig,
		FillPrice: fillPrice,
		FillQty:   sig.Qty,
		FilledAt:  time.Now(),
		Slippage:  slippage,
	}
	p.fills = append(p.fills, fill)
	p.mu.Unlock()

	log.Printf("[paper] %s %s %s:%s qty=%d price=%d (slip=%d) order=%s reason=%s",
		sig.Action, sig.StrategyName, sig.Exchange, sig.Token,
		sig.Qty, fillPrice, slippage, orderID, sig.Reason)

	p.resultCh <- OrderResult{
		OrderID: orderID,
		Status:  "FILLED",
		Message: fmt.Sprintf("paper filled at %d", fillPrice),
		Signal:  sig,
	}
}
