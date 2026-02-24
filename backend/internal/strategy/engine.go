// Package strategy provides the strategy engine for running trading strategies.
//
// A Strategy receives market data (candles, ticks) and emits trading signals (BUY/SELL/EXIT).
// The Engine manages strategy lifecycle: registration, data routing, and signal collection.
package strategy

import (
	"context"

	"trading-systemv1/internal/model"
)

// Signal represents a trading signal emitted by a strategy.
type Signal struct {
	StrategyName string `json:"strategy_name"`
	Action       Action `json:"action"` // BUY, SELL, EXIT
	Token        string `json:"token"`
	Exchange     string `json:"exchange"`
	Qty          int64  `json:"qty"`
	Price        int64  `json:"price"` // 0 = market order
	Reason       string `json:"reason"`
}

// Action represents a trading action.
type Action string

const (
	ActionBuy  Action = "BUY"
	ActionSell Action = "SELL"
	ActionExit Action = "EXIT"
)

// Strategy is the interface that all trading strategies must implement.
type Strategy interface {
	// Name returns the unique name of the strategy.
	Name() string

	// OnCandle is called for each new 1-second candle.
	// Return a Signal if the strategy wants to act, or nil to skip.
	OnCandle(candle model.Candle) *Signal

	// OnTick is called for each raw tick (optional, can be a no-op).
	OnTick(tick model.Tick)
}

// Engine manages registered strategies and routes market data to them.
type Engine struct {
	strategies []Strategy
	signalCh   chan Signal
}

// NewEngine creates a new strategy engine.
func NewEngine(signalBufferSize int) *Engine {
	return &Engine{
		signalCh: make(chan Signal, signalBufferSize),
	}
}

// Register adds a strategy to the engine.
func (e *Engine) Register(s Strategy) {
	e.strategies = append(e.strategies, s)
}

// Signals returns the channel of signals emitted by strategies.
func (e *Engine) Signals() <-chan Signal {
	return e.signalCh
}

// Run consumes candles and routes them to all registered strategies.
// Blocks until ctx is cancelled or candleCh is closed.
func (e *Engine) Run(ctx context.Context, candleCh <-chan model.Candle) {
	for {
		select {
		case <-ctx.Done():
			return
		case candle, ok := <-candleCh:
			if !ok {
				return
			}
			for _, s := range e.strategies {
				if sig := s.OnCandle(candle); sig != nil {
					select {
					case e.signalCh <- *sig:
					default:
						// signal channel full, drop
					}
				}
			}
		}
	}
}
