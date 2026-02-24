// cmd/tickserver — Demo WebSocket tick server.
// Broadcasts simulated tick data for testing mdengine-sim without real broker credentials.
//
// Tick JSON shape is identical to model.Tick:
//
//	{"token":"2885","exchange":"NSE","price":185005000,"qty":10,"tick_ts":"..."}
//
// Price is stored in paise (1 INR = 100 paise), same as live feed.
//
// Config (env vars):
//
//	TICK_SERVER_ADDR  — listen address  (default: ":9001")
//	TICK_TOKENS       — comma-separated TOKEN:EXCHANGE pairs (default: "99926000:NSE")
//	TICK_INTERVAL_MS  — broadcast interval milliseconds (default: "100")
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// tickMsg mirrors model.Tick for JSON serialisation.
type tickMsg struct {
	Token    string    `json:"token"`
	Exchange string    `json:"exchange"`
	Price    int64     `json:"price"` // paise
	Qty      int64     `json:"qty"`
	TickTS   time.Time `json:"tick_ts"`
}

// instrument holds per-symbol simulation state.
type instrument struct {
	Token    string
	Exchange string
	Price    int64 // current simulated price in paise
}

// ─── Hub ──────────────────────────────────────────────────────────────────────

type hub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]chan []byte
}

func newHub() *hub {
	return &hub{clients: make(map[*websocket.Conn]chan []byte)}
}

func (h *hub) register(conn *websocket.Conn) chan []byte {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	h.clients[conn] = ch
	h.mu.Unlock()
	return ch
}

func (h *hub) unregister(conn *websocket.Conn) {
	h.mu.Lock()
	if ch, ok := h.clients[conn]; ok {
		close(ch)
		delete(h.clients, conn)
	}
	h.mu.Unlock()
}

func (h *hub) broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.clients {
		select {
		case ch <- msg:
		default: // slow client — drop tick
		}
	}
}

// ─── WebSocket handler ────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func wsHandler(h *hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[tickserver] upgrade error: %v", err)
			return
		}
		log.Printf("[tickserver] client connected: %s", r.RemoteAddr)

		ch := h.register(conn)
		defer func() {
			h.unregister(conn)
			conn.Close()
			log.Printf("[tickserver] client disconnected: %s", r.RemoteAddr)
		}()

		// Write pump: sends tick JSON to this client.
		for msg := range ch {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}

// ─── Tick generator ──────────────────────────────────────────────────────────

// walkPrice applies a tiny random walk (±0.1%) to simulate price movement.
func walkPrice(price int64) int64 {
	// Change ±0.0% to ±0.1% each tick
	pct := (rand.Float64()*0.2 - 0.1) / 100.0
	delta := int64(float64(price) * pct)
	newPrice := price + delta
	if newPrice < 100 { // floor at 1 paise
		newPrice = 100
	}
	return newPrice
}

func runGenerator(h *hub, instruments []instrument, intervalMs int) {
	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	defer ticker.Stop()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	_ = rng

	for range ticker.C {
		for i := range instruments {
			instruments[i].Price = walkPrice(instruments[i].Price)
			msg := tickMsg{
				Token:    instruments[i].Token,
				Exchange: instruments[i].Exchange,
				Price:    instruments[i].Price,
				Qty:      int64(rand.Intn(100) + 1),
				TickTS:   time.Now().UTC(),
			}
			b, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			h.broadcast(b)
		}
	}
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[tickserver] starting demo tick server...")

	// Config
	addr := envOrDefault("TICK_SERVER_ADDR", ":9001")
	tokensEnv := envOrDefault("TICK_TOKENS", "99926000:NSE")
	intervalMs := envIntOrDefault("TICK_INTERVAL_MS", 100)

	// Parse TOKEN:EXCHANGE pairs
	instruments := parseInstruments(tokensEnv)
	if len(instruments) == 0 {
		log.Fatalf("[tickserver] no instruments configured via TICK_TOKENS")
	}
	log.Printf("[tickserver] instruments: %+v", instruments)
	log.Printf("[tickserver] broadcast interval: %dms", intervalMs)

	h := newHub()

	// Start tick generator
	go runGenerator(h, instruments, intervalMs)

	// HTTP routes
	http.HandleFunc("/ws", wsHandler(h))
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"status":"ok","service":"tickserver"}`)
	})

	log.Printf("[tickserver] ✅ listening on %s  (WebSocket: ws://localhost%s/ws)", addr, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("[tickserver] server error: %v", err)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseInstruments(s string) []instrument {
	// Default starting prices in paise (INR × 100)
	defaultPrices := map[string]int64{
		"2885":     185050_00, // ~₹18505.00 (Reliance)
		"1594":     250000_00, // ~₹25000.00
		"99926009": 25660_00,  // ~₹25660.00 (NIFTY sim alt)
		"99926000": 25660_00,  // ~₹25660.00 (NIFTY 50 index sim)
	}

	var result []instrument
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		seg := strings.SplitN(part, ":", 2)
		if len(seg) != 2 {
			log.Printf("[tickserver] skipping invalid token spec: %q", part)
			continue
		}
		token, exchange := strings.TrimSpace(seg[0]), strings.TrimSpace(seg[1])
		price := defaultPrices[token]
		if price == 0 {
			price = 100000_00 // default ₹1000.00
		}
		result = append(result, instrument{
			Token:    token,
			Exchange: exchange,
			Price:    price,
		})
	}
	return result
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
