// Package execution handles order placement, modification, and cancellation
// through the broker API (Angel One SmartConnect).
//
// The Executor receives signals from the strategy engine and translates them
// into broker API calls. It tracks order state and handles retries.
package execution

import (
	"context"
	"log"

	"trading-systemv1/internal/strategy"
)

// OrderResult represents the outcome of an order placement.
type OrderResult struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"` // PLACED, REJECTED, ERROR
	Message string `json:"message"`
	Signal  strategy.Signal
}

// Executor places orders based on strategy signals.
type Executor struct {
	// TODO: Add broker client (SmartConnect) reference
	resultCh chan OrderResult
}

// NewExecutor creates a new order executor.
func NewExecutor(resultBufferSize int) *Executor {
	return &Executor{
		resultCh: make(chan OrderResult, resultBufferSize),
	}
}

// Results returns the channel of order results.
func (e *Executor) Results() <-chan OrderResult {
	return e.resultCh
}

// Run consumes signals and places orders.
// Blocks until ctx is cancelled or signalCh is closed.
func (e *Executor) Run(ctx context.Context, signalCh <-chan strategy.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-signalCh:
			if !ok {
				return
			}
			log.Printf("[executor] received signal: %s %s %s qty=%d",
				sig.Action, sig.Exchange, sig.Token, sig.Qty)
			// TODO: Implement actual order placement via SmartConnect
			// For now, just log and emit a placeholder result
			e.resultCh <- OrderResult{
				OrderID: "TODO",
				Status:  "PENDING",
				Message: "executor not yet implemented",
				Signal:  sig,
			}
		}
	}
}
