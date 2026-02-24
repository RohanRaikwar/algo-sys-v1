package model

// Position represents a tracked trading position.
type Position struct {
	Token         string `json:"token"`
	Exchange      string `json:"exchange"`
	TradingSymbol string `json:"trading_symbol"`
	ProductType   string `json:"product_type"` // INTRADAY, DELIVERY
	Qty           int64  `json:"qty"`          // positive = long, negative = short
	AvgPrice      int64  `json:"avg_price"`    // paise
	LastPrice     int64  `json:"last_price"`   // latest market price in paise
	RealizedPnL   int64  `json:"realized_pnl"` // paise
}

// UnrealizedPnL computes unrealized profit/loss in paise.
func (p *Position) UnrealizedPnL() int64 {
	return (p.LastPrice - p.AvgPrice) * p.Qty
}

// Key returns a unique key for this position: "exchange:token".
func (p *Position) Key() string {
	return p.Exchange + ":" + p.Token
}
