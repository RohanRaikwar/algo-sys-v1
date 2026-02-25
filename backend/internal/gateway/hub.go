package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"trading-systemv1/internal/markethours"

	goredis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

// ActiveConfig holds the current indicator display configuration.
type ActiveConfig struct {
	Entries []IndicatorEntry `json:"entries"`
}

// IndicatorEntry represents a single indicator in the active config.
type IndicatorEntry struct {
	Name  string `json:"name"`
	TF    int    `json:"tf"`
	Color string `json:"color,omitempty"`
}

// Hub manages WebSocket clients and Redis PubSub fan-out.
type Hub struct {
	Rdb        *goredis.Client
	TFs        []int
	Tokens     []string
	Indicators []string

	mu      sync.RWMutex
	clients map[*Client]bool
	latest  map[string]latestEntry
	seq     int64

	activeConfig ActiveConfig
}

type latestEntry struct {
	Data json.RawMessage
	TS   time.Time
}

// NewHub creates a new Hub for managing WS clients and PubSub.
func NewHub(rdb *goredis.Client, tfs []int, tokens, indicators []string) *Hub {
	// Build default entries: each indicator for each TF
	var defaultEntries []IndicatorEntry
	for _, tf := range tfs {
		for _, ind := range indicators {
			defaultEntries = append(defaultEntries, IndicatorEntry{Name: ind, TF: tf})
		}
	}
	return &Hub{
		Rdb:        rdb,
		TFs:        tfs,
		Tokens:     tokens,
		Indicators: indicators,
		clients:    make(map[*Client]bool),
		latest:     make(map[string]latestEntry),
		activeConfig: ActiveConfig{
			Entries: defaultEntries,
		},
	}
}

// GetActiveConfig returns the current indicator display config.
func (h *Hub) GetActiveConfig() ActiveConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.activeConfig
}

// SetActiveConfig updates the active config and broadcasts to all clients.
func (h *Hub) SetActiveConfig(cfg ActiveConfig) {
	h.mu.Lock()
	h.activeConfig = cfg
	h.mu.Unlock()

	envelope, _ := json.Marshal(map[string]interface{}{
		"type":    "config_update",
		"entries": cfg.Entries,
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
	})

	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		select {
		case client.send <- envelope:
		default:
		}
	}
}

// Run starts the PubSub subscription loop. Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	channels := h.buildChannels()
	if len(channels) == 0 {
		log.Println("[api_gateway] WARNING: no channels to subscribe to")
		h.runPatternSubscribe(ctx)
		return
	}

	pubsub := h.Rdb.Subscribe(ctx, channels...)
	defer pubsub.Close()

	log.Printf("[api_gateway] subscribed to %d PubSub channels", len(channels))

	go h.runPatternSubscribe(ctx)

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			h.broadcast(msg.Channel, []byte(msg.Payload))
		}
	}
}

func (h *Hub) runPatternSubscribe(ctx context.Context) {
	pubsub := h.Rdb.PSubscribe(ctx, "pub:ind:*", "pub:tick:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			h.broadcast(msg.Channel, []byte(msg.Payload))
		}
	}
}

func (h *Hub) buildChannels() []string {
	var channels []string
	for _, ind := range h.Indicators {
		for _, tf := range h.TFs {
			for _, tok := range h.Tokens {
				ch := fmt.Sprintf("pub:ind:%s:%ds:%s", ind, tf, tok)
				channels = append(channels, ch)
			}
		}
	}
	for _, tf := range h.TFs {
		for _, tok := range h.Tokens {
			ch := fmt.Sprintf("pub:candle:%ds:%s", tf, tok)
			channels = append(channels, ch)
		}
	}
	for _, tok := range h.Tokens {
		ch := fmt.Sprintf("pub:candle:1s:%s", tok)
		channels = append(channels, ch)
	}
	return channels
}

// broadcast sends data to all connected WS clients.
// OPTIMIZED: hand-crafted JSON envelope instead of json.Marshal with reflection.
// Filtered: only sends to clients whose subscriptions match the channel.
func (h *Hub) broadcast(channel string, data []byte) {
	now := time.Now().UTC()

	h.mu.Lock()
	h.latest[channel] = latestEntry{Data: data, TS: now}
	h.seq++
	seq := h.seq
	h.mu.Unlock()

	// Hand-craft envelope JSON: ~1μs vs ~25μs for json.Marshal
	// {"channel":"...","data":...,"ts":"...","seq":N}
	buf := make([]byte, 0, len(channel)+len(data)+128)
	buf = append(buf, `{"channel":"`...)
	buf = append(buf, channel...)
	buf = append(buf, `","data":`...)
	buf = append(buf, data...)
	buf = append(buf, `,"ts":"`...)
	buf = now.AppendFormat(buf, time.RFC3339Nano)
	buf = append(buf, `","seq":`...)
	buf = strconv.AppendInt(buf, seq, 10)
	buf = append(buf, '}')

	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		// Filter: only send if client is subscribed to this channel
		if !client.matchesChannel(channel) {
			continue
		}
		select {
		case client.send <- buf:
		default:
		}
	}
}

// HandleWS upgrades an HTTP connection to WebSocket and registers the client.
func (h *Hub) HandleWS(w interface{ Header() map[string][]string }, r interface{ URL() string }) {
	// This is called via the handler wrapper in main.go
}

// HandleWSRequest handles WebSocket upgrade from standard http types.
func (h *Hub) HandleWSRequest(conn *websocket.Conn, lastTS string) {
	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
		hub:  h,
		subs: make(map[string]*ClientSubscription),
		filters: ClientFilters{
			TFs:    h.TFs,
			Tokens: h.Tokens,
		},
	}

	conn.EnableWriteCompression(true)

	h.mu.Lock()
	h.clients[client] = true
	h.mu.Unlock()

	log.Printf("[api_gateway] ws client connected (%d total)", len(h.clients))

	go client.sendInitialState(lastTS)
	go client.writePump()
	go client.readPump()
}

// RemoveClient removes a client from the hub.
func (h *Hub) RemoveClient(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
}

// GetLatestAll returns snapshot of all latest channel data.
func (h *Hub) GetLatestAll() map[string]json.RawMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cp := make(map[string]json.RawMessage, len(h.latest))
	for k, v := range h.latest {
		cp[k] = v.Data
	}
	return cp
}

// ClientCount returns the number of connected WS clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// StartMetricsBroadcast sends system metrics to all WS clients every 2s.
func (h *Hub) StartMetricsBroadcast(ctx context.Context, start time.Time) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			m := CollectMetrics(start)
			if v, ok := ReadIndicatorLatency(ctx, h.Rdb); ok {
				m.IndicatorMs = v
			}
			envelope, _ := json.Marshal(map[string]interface{}{
				"type":         "metrics",
				"metrics":      m,
				"marketOpen":   markethours.IsMarketOpen(now),
				"marketStatus": markethours.StatusString(now),
			})
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- envelope:
				default:
				}
			}
			h.mu.RUnlock()
		}
	}
}
