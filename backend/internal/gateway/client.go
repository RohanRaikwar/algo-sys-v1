package gateway

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client represents a single WebSocket peer.
type Client struct {
	conn    *websocket.Conn
	send    chan []byte
	hub     *Hub
	filters ClientFilters

	// Per-client subscriptions: key = "symbol:tf"
	subMu sync.RWMutex
	subs  map[string]*ClientSubscription
}

// ClientFilters allows per-client subscription filtering.
type ClientFilters struct {
	TFs        []int    `json:"tfs"`
	Tokens     []string `json:"tokens"`
	Indicators []string `json:"indicators"`
}

func (c *Client) sendInitialState(lastTS string) {
	c.hub.mu.RLock()
	defer c.hub.mu.RUnlock()

	var cutoff time.Time
	if lastTS != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, lastTS); err == nil {
			cutoff = parsed
		}
	}

	for channel, entry := range c.hub.latest {
		if !cutoff.IsZero() && !entry.TS.After(cutoff) {
			continue
		}

		envelope, _ := json.Marshal(map[string]interface{}{
			"channel": channel,
			"data":    json.RawMessage(entry.Data),
			"ts":      entry.TS.Format(time.RFC3339Nano),
			"initial": true,
		})
		select {
		case c.send <- envelope:
		default:
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			// Write coalescing: use NextWriter to batch queued messages
			// into a single WebSocket frame with newline separators
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(msg)

			// Drain any queued messages into the same write
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) readPump() {
	defer func() {
		c.hub.RemoveClient(c)
		c.conn.Close()
		log.Println("[api_gateway] ws client disconnected")
	}()

	c.conn.SetReadLimit(4096) // Increased for SUBSCRIBE messages
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		// Parse message type
		var base struct {
			Type string `json:"type"`
			Ping int64  `json:"ping"`
		}
		if json.Unmarshal(msg, &base) != nil {
			continue
		}

		switch base.Type {
		case "SUBSCRIBE":
			var subMsg SubscribeMsg
			if err := json.Unmarshal(msg, &subMsg); err != nil {
				SendError(c, "", "invalid SUBSCRIBE: "+err.Error())
				continue
			}
			go c.handleSubscribe(subMsg)

		case "UNSUBSCRIBE":
			var unsubMsg UnsubscribeMsg
			if err := json.Unmarshal(msg, &unsubMsg); err != nil {
				continue
			}
			c.handleUnsubscribe(unsubMsg)

		default:
			// Handle ping/pong (backward compat)
			if base.Ping > 0 {
				pong, _ := json.Marshal(map[string]interface{}{
					"type":      "pong",
					"ping":      base.Ping,
					"server_ts": time.Now().UnixMilli(),
				})
				select {
				case c.send <- pong:
				default:
				}
				continue
			}
			// Legacy: filter update
			var filters ClientFilters
			if json.Unmarshal(msg, &filters) == nil {
				c.filters = filters
			}
		}
	}
}

// handleSubscribe processes a SUBSCRIBE message from the client.
func (c *Client) handleSubscribe(msg SubscribeMsg) {
	if msg.Symbol == "" || msg.TF <= 0 {
		SendError(c, msg.ReqID, "symbol and tf are required")
		return
	}

	// Resolve indicator names
	indNames := ResolveIndicatorNames(msg.Indicators)

	sub := &ClientSubscription{
		Symbol:     msg.Symbol,
		TF:         msg.TF,
		Indicators: msg.Indicators,
		IndNames:   indNames,
	}

	// Store subscription
	c.subMu.Lock()
	if c.subs == nil {
		c.subs = make(map[string]*ClientSubscription)
	}
	c.subs[sub.SubKey()] = sub
	c.subMu.Unlock()

	log.Printf("[subscribe] client subscribed: symbol=%s tf=%d indicators=%v",
		msg.Symbol, msg.TF, indNames)

	// Check if indengine needs new indicators
	ctx := context.Background()
	hasNew := publishNewIndicators(ctx, c.hub.Rdb, c.hub, msg.Indicators)

	// If new indicators were just published, wait for indengine to compute them
	if hasNew {
		log.Printf("[subscribe] waiting for indengine to compute new indicators...")
		waitForIndicators(ctx, c.hub.Rdb, sub, 8*time.Second)
	}

	// Build and send snapshot
	candleLimit := msg.History.Candles
	if candleLimit <= 0 {
		candleLimit = 500
	}

	snap, err := BuildSnapshotFromRedis(ctx, c.hub.Rdb, sub, candleLimit)
	if err != nil {
		SendError(c, msg.ReqID, "snapshot build failed: "+err.Error())
		return
	}
	snap.ReqID = msg.ReqID

	SendJSON(c, snap)
	log.Printf("[subscribe] sent snapshot: symbol=%s tf=%d candles=%d indicators=%d",
		msg.Symbol, msg.TF, len(snap.Candles), len(snap.Indicators))
}

// handleUnsubscribe removes a subscription.
func (c *Client) handleUnsubscribe(msg UnsubscribeMsg) {
	sub := &ClientSubscription{Symbol: msg.Symbol, TF: msg.TF}
	c.subMu.Lock()
	delete(c.subs, sub.SubKey())
	c.subMu.Unlock()

	log.Printf("[subscribe] client unsubscribed: symbol=%s tf=%d", msg.Symbol, msg.TF)
}

// matchesChannel checks if a PubSub channel matches any of this client's subscriptions.
// Returns true if the client should receive this message.
func (c *Client) matchesChannel(channel string) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()

	if len(c.subs) == 0 {
		// No subscriptions — legacy mode, receive everything
		return true
	}

	parsed := parseChannel(channel)
	if parsed == nil {
		return true // non-data channel (metrics, config) — always deliver
	}

	symbol := parsed.exchange + ":" + parsed.token
	for _, sub := range c.subs {
		if sub.Symbol != symbol {
			continue
		}
		if sub.TF != parsed.tf {
			continue
		}
		// Candle channel — matches
		if parsed.chType == "candle" {
			return true
		}
		// Indicator channel — check if this indicator is in the sub
		if parsed.chType == "indicator" {
			for _, name := range sub.IndNames {
				if name == parsed.indName {
					return true
				}
			}
		}
	}
	return false
}

// parsedChannel holds the parsed components of a Redis PubSub channel name.
type parsedChannel struct {
	chType   string // "candle", "indicator", "tick"
	indName  string // for indicator channels: "SMA_9", "EMA_4"
	tf       int    // timeframe in seconds
	exchange string // "NSE"
	token    string // "99926000"
}

// parseChannel parses a PubSub channel like "pub:candle:60s:NSE:99926000"
// or "pub:ind:SMA_9:60s:NSE:99926000".
func parseChannel(channel string) *parsedChannel {
	parts := strings.Split(channel, ":")
	if len(parts) < 4 {
		return nil
	}

	// pub:candle:60s:NSE:99926000  (5 parts)
	if parts[0] == "pub" && parts[1] == "candle" && len(parts) >= 5 {
		tf := parseTFStr(parts[2])
		return &parsedChannel{
			chType:   "candle",
			tf:       tf,
			exchange: parts[3],
			token:    parts[4],
		}
	}

	// pub:ind:SMA_9:60s:NSE:99926000  (6 parts)
	if parts[0] == "pub" && parts[1] == "ind" && len(parts) >= 6 {
		tf := parseTFStr(parts[3])
		return &parsedChannel{
			chType:   "indicator",
			indName:  parts[2],
			tf:       tf,
			exchange: parts[4],
			token:    parts[5],
		}
	}

	// pub:tick:NSE:99926000  (4 parts)
	if parts[0] == "pub" && parts[1] == "tick" && len(parts) >= 4 {
		return &parsedChannel{
			chType:   "tick",
			exchange: parts[2],
			token:    parts[3],
		}
	}

	return nil
}

// parseTFStr parses "60s" → 60.
func parseTFStr(s string) int {
	s = strings.TrimSuffix(s, "s")
	n := 0
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int(ch-'0')
		}
	}
	return n
}
