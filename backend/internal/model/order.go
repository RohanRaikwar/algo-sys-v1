package model

import "time"

// Order represents a broker order.
type Order struct {
	OrderID         string    `json:"order_id"`
	Token           string    `json:"token"`
	Exchange        string    `json:"exchange"`
	TradingSymbol   string    `json:"trading_symbol"`
	TransactionType string    `json:"transaction_type"` // BUY, SELL
	OrderType       string    `json:"order_type"`       // MARKET, LIMIT, SL, SL-M
	ProductType     string    `json:"product_type"`     // INTRADAY, DELIVERY, CARRYFORWARD
	Variety         string    `json:"variety"`          // NORMAL, STOPLOSS, AMO
	Qty             int64     `json:"qty"`
	Price           int64     `json:"price"`         // limit price in paise (0 for market)
	TriggerPrice    int64     `json:"trigger_price"` // trigger price in paise
	Status          string    `json:"status"`        // PLACED, OPEN, COMPLETE, REJECTED, CANCELLED
	FilledQty       int64     `json:"filled_qty"`
	AvgPrice        int64     `json:"avg_price"` // fill average in paise
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
