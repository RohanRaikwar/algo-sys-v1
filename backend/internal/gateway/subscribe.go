package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// ── WS Protocol Message Types ──

// SubscribeMsg is the client → server SUBSCRIBE request.
type SubscribeMsg struct {
	Type       string          `json:"type"`       // "SUBSCRIBE"
	ReqID      string          `json:"reqId"`      // client-generated request ID
	Symbol     string          `json:"symbol"`     // e.g. "NSE:99926000"
	TF         int             `json:"tf"`         // timeframe in seconds
	History    HistoryRequest  `json:"history"`    // how many historical bars
	Indicators []IndicatorSpec `json:"indicators"` // indicator profile
}

// HistoryRequest specifies how many historical candles to fetch.
type HistoryRequest struct {
	Candles int `json:"candles"` // number of historical candles
}

// IndicatorSpec describes a single indicator in the client's profile.
type IndicatorSpec struct {
	ID     string         `json:"id"`           // e.g. "smma", "ema", "sma", "rsi"
	Source string         `json:"source"`       // e.g. "close", "high", "low"
	Params map[string]int `json:"params"`       // e.g. {"length": 21}
	TF     int            `json:"tf,omitempty"` // per-indicator TF override (seconds)
}

// UnsubscribeMsg is the client → server UNSUBSCRIBE request.
type UnsubscribeMsg struct {
	Type   string `json:"type"` // "UNSUBSCRIBE"
	ReqID  string `json:"reqId"`
	Symbol string `json:"symbol"`
	TF     int    `json:"tf"`
}

// SnapshotResponse is the server → client SNAPSHOT with historical data.
type SnapshotResponse struct {
	Type       string                        `json:"type"` // "SNAPSHOT"
	ReqID      string                        `json:"reqId"`
	Symbol     string                        `json:"symbol"`
	TF         int                           `json:"tf"`
	Candles    []SnapshotCandle              `json:"candles"`
	Indicators map[string][]SnapshotIndPoint `json:"indicators"`
}

// SnapshotCandle is a single candle in the snapshot.
type SnapshotCandle struct {
	TS     string  `json:"ts"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
	Count  float64 `json:"count"`
}

// SnapshotIndPoint is a single indicator point in the snapshot.
type SnapshotIndPoint struct {
	TS    string  `json:"ts"`
	Value float64 `json:"value"`
	Ready bool    `json:"ready"`
}

// LiveUpdate is the server → client LIVE message for a closed candle.
type LiveUpdate struct {
	Type       string                   `json:"type"` // "LIVE"
	Symbol     string                   `json:"symbol"`
	TF         int                      `json:"tf"`
	Candle     *SnapshotCandle          `json:"candle,omitempty"`
	Indicators map[string]*LiveIndValue `json:"indicators,omitempty"`
}

// LiveIndValue is a live indicator value.
type LiveIndValue struct {
	Value float64 `json:"value"`
	Ready bool    `json:"ready"`
	Live  bool    `json:"live,omitempty"`
}

// ErrorResponse is the server → client ERROR message.
type ErrorResponse struct {
	Type  string `json:"type"` // "ERROR"
	ReqID string `json:"reqId,omitempty"`
	Error string `json:"error"`
}

// ── Subscription State ──

// IndEntry is a resolved indicator identity with composite key (name + tf).
type IndEntry struct {
	Name string
	TF   int
}

// Key returns the composite identity "NAME:TF".
func (e IndEntry) Key() string {
	return e.Name + ":" + strconv.Itoa(e.TF)
}

// ClientSubscription holds per-(symbol, tf) state for a client.
type ClientSubscription struct {
	Symbol     string
	TF         int
	Indicators []IndicatorSpec
	IndEntries []IndEntry // resolved (name, tf) pairs — no collisions
}

// SubKey returns the map key for this subscription.
func (s *ClientSubscription) SubKey() string {
	return s.Symbol + ":" + strconv.Itoa(s.TF)
}

// ── Helpers ──

// IndicatorSpecToName converts a spec like {id:"smma", params:{length:21}} → "SMMA_21"
func IndicatorSpecToName(spec IndicatorSpec) string {
	typ := strings.ToUpper(spec.ID)
	length, ok := spec.Params["length"]
	if !ok {
		length = 14 // default
	}
	return typ + "_" + strconv.Itoa(length)
}

// IndicatorSpecToConfig converts to the indengine format "TYPE:PERIOD"
func IndicatorSpecToConfig(spec IndicatorSpec) string {
	typ := strings.ToUpper(spec.ID)
	length, ok := spec.Params["length"]
	if !ok {
		length = 14
	}
	return typ + ":" + strconv.Itoa(length)
}

// ResolveIndicatorNames converts all specs to their resolved names.
func ResolveIndicatorNames(specs []IndicatorSpec) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = IndicatorSpecToName(spec)
	}
	return names
}

// ResolveIndEntries builds a list of (name, tf) entries for MTF support.
// Uses composite identity so SMA_20@60 and SMA_20@300 don't collide.
func ResolveIndEntries(specs []IndicatorSpec, defaultTF int) []IndEntry {
	entries := make([]IndEntry, len(specs))
	for i, spec := range specs {
		tf := defaultTF
		if spec.TF > 0 {
			tf = spec.TF
		}
		entries[i] = IndEntry{Name: IndicatorSpecToName(spec), TF: tf}
	}
	return entries
}

// ── Redis History Fetching ──

// BuildSnapshotFromRedis reads historical candles + indicator data from Redis.
func BuildSnapshotFromRedis(ctx context.Context, rdb *goredis.Client, sub *ClientSubscription, candleLimit int) (*SnapshotResponse, error) {
	if candleLimit <= 0 {
		candleLimit = 500
	}
	if candleLimit > 1000 {
		candleLimit = 1000
	}

	snap := &SnapshotResponse{
		Type:       "SNAPSHOT",
		Symbol:     sub.Symbol,
		TF:         sub.TF,
		Candles:    make([]SnapshotCandle, 0, candleLimit),
		Indicators: make(map[string][]SnapshotIndPoint, len(sub.IndEntries)),
	}

	// 1. Fetch candles from Redis stream
	candleStreamKey := fmt.Sprintf("candle:%ds:%s", sub.TF, sub.Symbol)
	candleMsgs, err := rdb.XRevRangeN(ctx, candleStreamKey, "+", "-", int64(candleLimit)).Result()
	if err != nil {
		log.Printf("[subscribe] candle stream read error for %s: %v", candleStreamKey, err)
		// Don't fail — just return empty candles
	} else {
		// Reverse to chronological order
		for i, j := 0, len(candleMsgs)-1; i < j; i, j = i+1, j-1 {
			candleMsgs[i], candleMsgs[j] = candleMsgs[j], candleMsgs[i]
		}
		for _, msg := range candleMsgs {
			dataStr, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}
			var c SnapshotCandle
			if err := json.Unmarshal([]byte(dataStr), &c); err != nil {
				continue
			}
			if c.TS != "" {
				snap.Candles = append(snap.Candles, c)
			}
		}
	}

	// Compute candle price band for filtering warmup-phase indicator values
	var bandLo, bandHi float64
	// Compute candle time range to clamp indicator data to visible range
	var candleTimeMin, candleTimeMax time.Time
	if len(snap.Candles) > 0 {
		bandLo = snap.Candles[0].Low
		bandHi = snap.Candles[0].High
		for _, c := range snap.Candles[1:] {
			lo := c.Low
			hi := c.High
			if lo < bandLo {
				bandLo = lo
			}
			if hi > bandHi {
				bandHi = hi
			}
		}
		margin := (bandHi - bandLo) * 0.10
		bandLo -= margin
		bandHi += margin
		log.Printf("[subscribe] candle price band: %.2f – %.2f (with 10%% margin)", bandLo, bandHi)

		// Parse first and last candle timestamps for time-range clamping
		if t, err := time.Parse(time.RFC3339, snap.Candles[0].TS); err == nil {
			candleTimeMin = t
		}
		if t, err := time.Parse(time.RFC3339, snap.Candles[len(snap.Candles)-1].TS); err == nil {
			candleTimeMax = t
		}
		// Add 1-candle margin on each side
		if !candleTimeMin.IsZero() {
			candleTimeMin = candleTimeMin.Add(-time.Duration(sub.TF) * time.Second)
		}
		if !candleTimeMax.IsZero() {
			candleTimeMax = candleTimeMax.Add(time.Duration(sub.TF) * time.Second)
		}
		log.Printf("[subscribe] candle time range: %s – %s", candleTimeMin, candleTimeMax)
	}

	// 2. Fetch indicator histories from Redis streams (using per-indicator TF)
	for _, entry := range sub.IndEntries {
		// Key as "NAME:TF" so frontend knows the indicator's computation TF
		snapKey := entry.Key()
		indStreamKey := fmt.Sprintf("ind:%s:%ds:%s", entry.Name, entry.TF, sub.Symbol)
		indMsgs, err := rdb.XRevRangeN(ctx, indStreamKey, "+", "-", int64(candleLimit)).Result()
		if err != nil {
			log.Printf("[subscribe] indicator stream read error for %s: %v", indStreamKey, err)
			snap.Indicators[snapKey] = []SnapshotIndPoint{}
			continue
		}

		// Reverse to chronological order
		for i, j := 0, len(indMsgs)-1; i < j; i, j = i+1, j-1 {
			indMsgs[i], indMsgs[j] = indMsgs[j], indMsgs[i]
		}

		points := make([]SnapshotIndPoint, 0, len(indMsgs))
		for _, msg := range indMsgs {
			dataStr, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}
			var p struct {
				Value float64 `json:"value"`
				TS    string  `json:"ts"`
				Ready bool    `json:"ready"`
			}
			if err := json.Unmarshal([]byte(dataStr), &p); err != nil {
				continue
			}
			// Skip non-ready or empty timestamp
			if !p.Ready || p.TS == "" {
				continue
			}
			// Skip warmup-phase values that fall outside the candle price band
			// (only for price-overlay indicators like SMA/EMA/SMMA, not RSI)
			if bandHi > 0 && !strings.HasPrefix(entry.Name, "RSI") {
				if p.Value < bandLo || p.Value > bandHi {
					continue
				}
			}
			// Clamp to candle time range — skip indicator points outside visible candles
			if !candleTimeMin.IsZero() && !candleTimeMax.IsZero() {
				if pt, err := time.Parse(time.RFC3339, p.TS); err == nil {
					if pt.Before(candleTimeMin) || pt.After(candleTimeMax) {
						continue
					}
				}
			}
			points = append(points, SnapshotIndPoint{
				TS:    p.TS,
				Value: p.Value,
				Ready: p.Ready,
			})
		}

		// Deduplicate by timestamp: keep the LAST value for each TS
		// (stream may contain multiple entries per candle from backfill recomputation)
		seen := make(map[string]int, len(points))
		deduped := make([]SnapshotIndPoint, 0, len(points))
		for _, pt := range points {
			if idx, ok := seen[pt.TS]; ok {
				deduped[idx] = pt // overwrite with newer value
			} else {
				seen[pt.TS] = len(deduped)
				deduped = append(deduped, pt)
			}
		}

		// Sort by timestamp to ensure chronological order
		// (backfill batch-inserts may have non-chronological ts within stream)
		sort.Slice(deduped, func(i, j int) bool {
			return deduped[i].TS < deduped[j].TS
		})

		snap.Indicators[snapKey] = deduped
	}

	return snap, nil
}

// SendJSON marshals and sends a message to the client's send channel.
func SendJSON(c *Client, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[subscribe] json marshal error: %v", err)
		return
	}
	select {
	case c.send <- data:
	default:
		log.Println("[subscribe] client send buffer full, dropping message")
	}
}

// SendError sends an error response to the client.
func SendError(c *Client, reqID, errMsg string) {
	SendJSON(c, ErrorResponse{
		Type:  "ERROR",
		ReqID: reqID,
		Error: errMsg,
	})
}

// publishNewIndicators checks which indicators need to be added to indengine
// and publishes the full set to the config:indicators Redis channel.
// Returns true if new indicators were added.
func publishNewIndicators(ctx context.Context, rdb *goredis.Client, hub *Hub, newSpecs []IndicatorSpec) bool {
	// Build the set of all currently known + new indicator configs
	known := make(map[string]bool)
	var allConfigs []string

	hub.mu.RLock()
	indicators := make([]string, len(hub.Indicators))
	copy(indicators, hub.Indicators)
	hub.mu.RUnlock()

	for _, ind := range indicators {
		// Hub.Indicators stores names like "SMA_9" — convert to "SMA:9"
		parts := strings.SplitN(ind, "_", 2)
		if len(parts) == 2 {
			config := parts[0] + ":" + parts[1]
			known[config] = true
			allConfigs = append(allConfigs, config)
		}
	}

	hasNew := false
	for _, spec := range newSpecs {
		config := IndicatorSpecToConfig(spec)
		if !known[config] {
			known[config] = true
			allConfigs = append(allConfigs, config)
			// Also add to hub.Indicators so future checks know about it
			hub.mu.Lock()
			hub.Indicators = append(hub.Indicators, IndicatorSpecToName(spec))
			hub.mu.Unlock()
			hasNew = true
		}
	}

	if !hasNew {
		return false
	}

	payload := strings.Join(allConfigs, ",")
	log.Printf("[subscribe] publishing new indicator config to indengine: %s", payload)

	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := rdb.Publish(tctx, "config:indicators", payload).Err(); err != nil {
		log.Printf("[subscribe] WARNING: failed to publish config:indicators: %v", err)
	}
	return true
}

// waitForIndicators polls Redis until all subscribed indicator streams have data,
// or until the timeout expires. This allows indengine time to backfill after a
// dynamic config reload.
func waitForIndicators(ctx context.Context, rdb *goredis.Client, sub *ClientSubscription, timeout time.Duration) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			log.Printf("[subscribe] timed out waiting for indicators to appear")
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			allReady := true
			for _, entry := range sub.IndEntries {
				key := fmt.Sprintf("ind:%s:%ds:%s", entry.Name, entry.TF, sub.Symbol)
				n, err := rdb.XLen(ctx, key).Result()
				if err != nil || n == 0 {
					allReady = false
					break
				}
			}
			if allReady {
				log.Printf("[subscribe] all %d indicator streams ready", len(sub.IndEntries))
				return
			}
		}
	}
}
