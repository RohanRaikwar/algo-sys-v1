package model

import "time"

// Tick represents a single market data tick from the Angel One WebSocket.
// Price is stored as int64 in paise (1 INR = 100 paise) to avoid float drift.
type Tick struct {
	Token    string    `json:"token"`
	Exchange string    `json:"exchange"`
	Price    int64     `json:"price"`   // paise (LTP)
	Qty      int64     `json:"qty"`     // last traded quantity
	TickTS   time.Time `json:"tick_ts"` // UTC timestamp
}
