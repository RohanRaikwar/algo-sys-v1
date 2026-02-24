package model

import (
	"encoding/json"
	"time"
)

// Candle represents a 1-second OHLC candle for a single instrument.
// All prices are in paise (int64) to avoid floating-point drift.
type Candle struct {
	Token      string    `json:"token"`
	Exchange   string    `json:"exchange"`
	TS         time.Time `json:"ts"`          // bucket start time (UTC, second-aligned)
	Open       int64     `json:"open"`        // paise
	High       int64     `json:"high"`        // paise
	Low        int64     `json:"low"`         // paise
	Close      int64     `json:"close"`       // paise
	Volume     int64     `json:"volume"`      // cumulative quantity in this second
	TicksCount int       `json:"ticks_count"` // number of ticks aggregated
}

// Key returns a unique key for this candle's instrument: "exchange:token".
func (c *Candle) Key() string {
	return c.Exchange + ":" + c.Token
}

// JSON returns the JSON-encoded candle (ignoring errors for hot-path usage).
func (c *Candle) JSON() []byte {
	b, _ := json.Marshal(c)
	return b
}
