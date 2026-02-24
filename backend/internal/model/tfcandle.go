package model

import (
	"encoding/json"
	"time"
)

// TFCandle represents a resampled OHLC candle for a dynamic timeframe.
// TF is the timeframe duration in seconds (e.g., 60 = 1 minute).
// All prices are in paise (int64) to avoid floating-point drift.
type TFCandle struct {
	Token    string    `json:"token"`
	Exchange string    `json:"exchange"`
	TF       int       `json:"tf"`      // timeframe in seconds
	TS       time.Time `json:"ts"`      // bucket start time (UTC, TF-aligned)
	Open     int64     `json:"open"`    // paise
	High     int64     `json:"high"`    // paise
	Low      int64     `json:"low"`     // paise
	Close    int64     `json:"close"`   // paise
	Volume   int64     `json:"volume"`  // cumulative quantity
	Count    int       `json:"count"`   // number of 1s candles merged
	Forming  bool      `json:"forming"` // true if bucket is still open
}

// Key returns "exchange:token".
func (c *TFCandle) Key() string {
	return c.Exchange + ":" + c.Token
}

// StreamKey returns the Redis stream key: "candle:{TF}s:{exchange}:{token}".
func (c *TFCandle) StreamKey() string {
	return "candle:" + itoa(c.TF) + "s:" + c.Exchange + ":" + c.Token
}

// JSON returns the JSON-encoded TF candle.
func (c *TFCandle) JSON() []byte {
	b, _ := json.Marshal(c)
	return b
}

// IndicatorResult holds a computed indicator value for a specific token + TF.
type IndicatorResult struct {
	Name     string    `json:"name"` // e.g. "SMA_20", "EMA_9", "RSI_14"
	Token    string    `json:"token"`
	Exchange string    `json:"exchange"`
	TF       int       `json:"tf"` // timeframe in seconds
	Value    float64   `json:"value"`
	TS       time.Time `json:"ts"`    // candle timestamp that produced this value
	Ready    bool      `json:"ready"` // true when indicator has enough data
	Live     bool      `json:"live"`  // true for preview values from forming candles
}

// StreamKey returns the Redis stream key: "ind:{name}:{TF}s:{exchange}:{token}".
func (r *IndicatorResult) StreamKey() string {
	return "ind:" + r.Name + ":" + itoa(r.TF) + "s:" + r.Exchange + ":" + r.Token
}

// JSON returns the JSON-encoded indicator result.
func (r *IndicatorResult) JSON() []byte {
	b, _ := json.Marshal(r)
	return b
}

// itoa is a minimal int-to-string without importing strconv in hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
