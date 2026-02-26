package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
// It acts as a compositor, delegating to focused components:
//   - PubSubRouter: Redis subscription + message routing
//   - Broadcaster: envelope construction + client-filtered fan-out
//   - ConfigStore: active indicator config CRUD + broadcast
type Hub struct {
	Rdb        *goredis.Client
	TFs        []int
	Tokens     []string
	Indicators []string

	mu      sync.RWMutex
	clients map[*Client]bool
	latest  map[string]latestEntry
	seq     int64

	// Per-channel monotonic sequence numbers for gap detection
	channelSeqs map[string]int64

	// Per-channel replay buffers for gap backfill
	replayBufs map[string]*ReplayBuffer

	activeConfig ActiveConfig

	// End-to-end latency tracker (Point 8)
	Latency *LatencyTracker

	// Sub-components
	Router      *PubSubRouter
	Broadcaster *Broadcaster
	ConfigStore *ConfigStore
}

type latestEntry struct {
	Data json.RawMessage
	TS   time.Time
	Seq  int64 // per-channel seq for gap detection
}

// NewHub creates a new Hub for managing WS clients and PubSub.
func NewHub(rdb *goredis.Client, tfs []int, tokens, indicators []string) *Hub {
	// Start with empty active config — indicators are added dynamically by the frontend
	h := &Hub{
		Rdb:         rdb,
		TFs:         tfs,
		Tokens:      tokens,
		Indicators:  indicators,
		clients:     make(map[*Client]bool),
		latest:      make(map[string]latestEntry),
		channelSeqs: make(map[string]int64),
		replayBufs:  make(map[string]*ReplayBuffer),
		Latency:     NewLatencyTracker(10000), // 10k sample ring buffer
		activeConfig: ActiveConfig{
			Entries: []IndicatorEntry{},
		},
	}
	// Wire sub-components
	h.Router = NewPubSubRouter(h)
	h.Broadcaster = NewBroadcaster(h)
	h.ConfigStore = NewConfigStore(h, rdb)

	// Restore active config from Redis (if previously persisted)
	h.ConfigStore.Load(context.Background())

	return h
}

// GetActiveConfig delegates to ConfigStore.
func (h *Hub) GetActiveConfig() ActiveConfig {
	return h.ConfigStore.Get()
}

// SetActiveConfig delegates to ConfigStore.
func (h *Hub) SetActiveConfig(cfg ActiveConfig) {
	h.ConfigStore.Set(cfg)
}

// Run starts the PubSub subscription loop. Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	channels := h.buildChannels()
	if len(channels) == 0 {
		log.Println("[api_gateway] WARNING: no channels to subscribe to")
		h.Router.RunPattern(ctx)
		return
	}

	go h.Router.RunPattern(ctx)
	h.Router.RunExplicit(ctx)
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

// broadcast delegates to Broadcaster for performance-optimized fan-out.
func (h *Hub) broadcast(channel string, data []byte) {
	h.Broadcaster.Broadcast(channel, data)
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
	count := len(h.clients)
	h.mu.Unlock()

	log.Printf("[api_gateway] ws client connected (%d total)", count)

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

// GetReplayRange returns buffered envelopes for a channel in [fromSeq, toSeq].
// Used by the /api/missed REST endpoint for client gap backfill.
func (h *Hub) GetReplayRange(channel string, fromSeq, toSeq int64) [][]byte {
	h.mu.RLock()
	rb, exists := h.replayBufs[channel]
	h.mu.RUnlock()
	if !exists {
		return nil
	}
	entries := rb.Range(fromSeq, toSeq)
	result := make([][]byte, len(entries))
	for i, e := range entries {
		result[i] = e.Data
	}
	return result
}

// GetChannelSeq returns the current sequence number for a channel.
func (h *Hub) GetChannelSeq(channel string) int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.channelSeqs[channel]
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
			if h.Latency != nil {
				m.LatencyP50, m.LatencyP95, m.LatencyP99 = h.Latency.Percentiles()
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
