package gateway

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// Client represents a single WebSocket peer.
type Client struct {
	conn    *websocket.Conn
	send    chan []byte
	hub     *Hub
	filters ClientFilters
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

	c.conn.SetReadLimit(512)
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
		var pingMsg struct {
			Ping int64 `json:"ping"`
		}
		if json.Unmarshal(msg, &pingMsg) == nil && pingMsg.Ping > 0 {
			pong, _ := json.Marshal(map[string]interface{}{
				"type":      "pong",
				"ping":      pingMsg.Ping,
				"server_ts": time.Now().UnixMilli(),
			})
			select {
			case c.send <- pong:
			default:
			}
			continue
		}
		var filters ClientFilters
		if json.Unmarshal(msg, &filters) == nil {
			c.filters = filters
		}
	}
}
