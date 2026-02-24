package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"trading-systemv1/internal/markethours"

	goredis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:       func(r *http.Request) bool { return true },
	EnableCompression: true,
}

var processStart = time.Now()

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[api_gateway] starting...")

	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")
	listenAddr := getEnv("GATEWAY_ADDR", ":9090")
	enabledTFs := getEnv("ENABLED_TFS", "60,120,180,300")
	subscribeTokens := getEnv("SUBSCRIBE_TOKENS", "1:99926000")

	// Connect to Redis
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("[api_gateway] redis connection failed: %v", err)
	}
	log.Printf("[api_gateway] redis connected at %s", redisAddr)

	// Parse config
	tfs := parseTFs(enabledTFs)
	tokenKeys := parseTokenKeys(subscribeTokens)
	indicators := parseIndicatorNames(getEnv("INDICATOR_CONFIGS", ""))

	// Hub manages all WebSocket connections
	hub := NewHub(rdb, tfs, tokenKeys, indicators)
	go hub.Run(ctx)

	// HTTP routes with CORS middleware
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	})

	// REST: latest indicator values
	mux.HandleFunc("/api/indicators/latest", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")
		latest := hub.GetLatestAll()
		json.NewEncoder(w).Encode(latest)
	})

	// REST: available timeframes
	mux.HandleFunc("/api/tfs", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")
		type tfInfo struct {
			Seconds int    `json:"seconds"`
			Label   string `json:"label"`
		}
		tfList := make([]tfInfo, len(tfs))
		for i, tf := range tfs {
			tfList[i] = tfInfo{Seconds: tf, Label: tfLabel(tf)}
		}
		json.NewEncoder(w).Encode(tfList)
	})

	// REST: config
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tfs":        tfs,
			"tokens":     tokenKeys,
			"indicators": indicators,
		})
	})

	// REST: GET/POST /api/indicators/active — current filter config
	mux.HandleFunc("/api/indicators/active", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == "POST" {
			var req ActiveConfig
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			hub.SetActiveConfig(req)
			log.Printf("[api_gateway] active config updated: %d entries", len(req.Entries))

			// Publish unique indicator specs to Redis for indengine dynamic reload
			seen := make(map[string]bool)
			var specs []string
			for _, entry := range req.Entries {
				parts := strings.SplitN(entry.Name, "_", 2)
				if len(parts) == 2 {
					spec := parts[0] + ":" + parts[1]
					if !seen[spec] {
						seen[spec] = true
						specs = append(specs, spec)
					}
				}
			}
			if len(specs) > 0 {
				payload := strings.Join(specs, ",")
				if err := rdb.Publish(ctx, "config:indicators", payload).Err(); err != nil {
					log.Printf("[api_gateway] WARNING: failed to publish config:indicators: %v", err)
				} else {
					log.Printf("[api_gateway] published indicator config to indengine: %s", payload)
				}
			}

			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// GET
		json.NewEncoder(w).Encode(hub.GetActiveConfig())
	})

	// REST: system metrics snapshot
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(collectMetrics(processStart))
	})

	// REST: historical candles from Redis streams
	mux.HandleFunc("/api/candles", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")

		tfStr := r.URL.Query().Get("tf")
		token := r.URL.Query().Get("token")
		limitStr := r.URL.Query().Get("limit")
		beforeStr := r.URL.Query().Get("before")

		if tfStr == "" {
			tfStr = "60"
		}
		tfVal, _ := strconv.Atoi(tfStr)
		if tfVal <= 0 {
			tfVal = 60
		}

		limit := 200
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
				limit = l
			}
		}

		if token == "" && len(tokenKeys) > 0 {
			token = tokenKeys[0]
		}

		streamKey := fmt.Sprintf("candle:%ds:%s", tfVal, token)

		upperBound := "+"
		if beforeStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			} else if t, err := time.Parse(time.RFC3339, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			}
		}

		msgs, err := rdb.XRevRangeN(ctx, streamKey, upperBound, "-", int64(limit)).Result()
		if err != nil {
			json.NewEncoder(w).Encode([]interface{}{})
			return
		}

		// Reverse to chronological order
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}

		type CandleOut struct {
			TS       string  `json:"ts"`
			Open     float64 `json:"open"`
			High     float64 `json:"high"`
			Low      float64 `json:"low"`
			Close    float64 `json:"close"`
			Volume   float64 `json:"volume"`
			Count    float64 `json:"count"`
			Token    string  `json:"token"`
			Exchange string  `json:"exchange"`
			TF       int     `json:"tf"`
			Forming  bool    `json:"forming"`
		}

		candles := make([]CandleOut, 0, len(msgs))
		for _, msg := range msgs {
			dataStr, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}
			var c CandleOut
			if err := json.Unmarshal([]byte(dataStr), &c); err != nil {
				continue
			}
			c.TF = tfVal
			if c.TS != "" {
				candles = append(candles, c)
			}
		}

		json.NewEncoder(w).Encode(candles)
	})

	// REST: historical indicator values from Redis streams
	mux.HandleFunc("/api/indicators/history", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		w.Header().Set("Content-Type", "application/json")

		name := r.URL.Query().Get("name")
		tfStr := r.URL.Query().Get("tf")
		token := r.URL.Query().Get("token")
		limitStr := r.URL.Query().Get("limit")

		if name == "" || tfStr == "" {
			json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		tfVal, _ := strconv.Atoi(tfStr)
		if tfVal <= 0 {
			tfVal = 60
		}
		limit := 300
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
				limit = l
			}
		}
		if token == "" && len(tokenKeys) > 0 {
			token = tokenKeys[0]
		}

		streamKey := fmt.Sprintf("ind:%s:%ds:%s", name, tfVal, token)

		upperBound := "+"
		if beforeStr := r.URL.Query().Get("before"); beforeStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			} else if t, err := time.Parse(time.RFC3339, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			}
		}

		msgs, err := rdb.XRevRangeN(ctx, streamKey, upperBound, "-", int64(limit)).Result()
		if err != nil {
			json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		// Reverse to chronological order
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}

		type IndPoint struct {
			Value float64 `json:"value"`
			TS    string  `json:"ts"`
			Ready bool    `json:"ready"`
		}

		points := make([]IndPoint, 0, len(msgs))
		for _, msg := range msgs {
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
			if p.Ready && p.TS != "" {
				points = append(points, IndPoint{Value: p.Value, TS: p.TS, Ready: p.Ready})
			}
		}

		json.NewEncoder(w).Encode(points)
	})

	srv := &http.Server{Addr: listenAddr, Handler: mux}

	// Start metrics broadcast (every 2 s)
	go hub.startMetricsBroadcast(ctx, processStart)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("[api_gateway] ✅ serving at http://localhost%s", listenAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("[api_gateway] server error: %v", err)
		}
	}()

	<-sigCh
	log.Println("[api_gateway] shutting down...")
	cancel()
	srv.Shutdown(context.Background())
}

// ---- CORS helper ----

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// ---- Hub manages WebSocket clients and Redis PubSub ----

type ActiveConfig struct {
	Entries []IndicatorEntry `json:"entries"`
}

type IndicatorEntry struct {
	Name  string `json:"name"`
	TF    int    `json:"tf"`
	Color string `json:"color,omitempty"`
}

type Hub struct {
	rdb        *goredis.Client
	tfs        []int
	tokens     []string
	indicators []string

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

type Client struct {
	conn    *websocket.Conn
	send    chan []byte
	hub     *Hub
	filters ClientFilters
}

type ClientFilters struct {
	TFs        []int    `json:"tfs"`
	Tokens     []string `json:"tokens"`
	Indicators []string `json:"indicators"`
}

func NewHub(rdb *goredis.Client, tfs []int, tokens, indicators []string) *Hub {
	return &Hub{
		rdb:        rdb,
		tfs:        tfs,
		tokens:     tokens,
		indicators: indicators,
		clients:    make(map[*Client]bool),
		latest:     make(map[string]latestEntry),
		activeConfig: ActiveConfig{
			Entries: []IndicatorEntry{
				{Name: "SMA_9", TF: 60},
				{Name: "EMA_4", TF: 60},
				{Name: "SMA_21", TF: 60},
			},
		},
	}
}

func (h *Hub) GetActiveConfig() ActiveConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.activeConfig
}

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

func (h *Hub) Run(ctx context.Context) {
	channels := h.buildChannels()
	if len(channels) == 0 {
		log.Println("[api_gateway] WARNING: no channels to subscribe to")
		h.runPatternSubscribe(ctx)
		return
	}

	pubsub := h.rdb.Subscribe(ctx, channels...)
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
	pubsub := h.rdb.PSubscribe(ctx, "pub:ind:*", "pub:tick:*")
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
	for _, ind := range h.indicators {
		for _, tf := range h.tfs {
			for _, tok := range h.tokens {
				ch := fmt.Sprintf("pub:ind:%s:%ds:%s", ind, tf, tok)
				channels = append(channels, ch)
			}
		}
	}
	for _, tf := range h.tfs {
		for _, tok := range h.tokens {
			ch := fmt.Sprintf("pub:candle:%ds:%s", tf, tok)
			channels = append(channels, ch)
		}
	}
	for _, tok := range h.tokens {
		ch := fmt.Sprintf("pub:candle:1s:%s", tok)
		channels = append(channels, ch)
	}
	return channels
}

func (h *Hub) broadcast(channel string, data []byte) {
	now := time.Now().UTC()

	h.mu.Lock()
	h.latest[channel] = latestEntry{Data: data, TS: now}
	h.seq++
	seq := h.seq
	h.mu.Unlock()

	envelope, _ := json.Marshal(map[string]interface{}{
		"channel": channel,
		"data":    json.RawMessage(data),
		"ts":      now.Format(time.RFC3339Nano),
		"seq":     seq,
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

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[api_gateway] ws upgrade error: %v", err)
		return
	}

	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
		hub:  h,
		filters: ClientFilters{
			TFs:    h.tfs,
			Tokens: h.tokens,
		},
	}

	conn.EnableWriteCompression(true)

	h.mu.Lock()
	h.clients[client] = true
	h.mu.Unlock()

	log.Printf("[api_gateway] ws client connected (%d total)", len(h.clients))

	lastTS := r.URL.Query().Get("last_ts")
	go client.sendInitialState(lastTS)

	go client.writePump()
	go client.readPump()
}

func (h *Hub) removeClient(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
}

func (h *Hub) GetLatestAll() map[string]json.RawMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()
	copy := make(map[string]json.RawMessage, len(h.latest))
	for k, v := range h.latest {
		copy[k] = v.Data
	}
	return copy
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
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
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
		c.hub.removeClient(c)
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

// ---- System Metrics ----

type SystemMetrics struct {
	CPULoad1    float64 `json:"cpu_load_1"`
	CPULoad5    float64 `json:"cpu_load_5"`
	CPULoad15   float64 `json:"cpu_load_15"`
	CPUPercent  float64 `json:"cpu_percent"`
	CPUCores    int     `json:"cpu_cores"`
	MemUsedMB   float64 `json:"mem_used_mb"`
	MemTotalMB  float64 `json:"mem_total_mb"`
	MemPercent  float64 `json:"mem_percent"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	SysMB       float64 `json:"sys_mb"`
	GCRuns      uint32  `json:"gc_runs"`
	Goroutines  int     `json:"goroutines"`
	UptimeSec   int64   `json:"uptime_sec"`
	TS          string  `json:"ts"`
}

type cpuSample struct {
	idle  uint64
	total uint64
}

var prevCPU cpuSample

func readCPUSample() cpuSample {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			break
		}
		var total, idle uint64
		for i := 1; i < len(fields); i++ {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			total += v
			if i == 4 {
				idle = v
			}
		}
		return cpuSample{idle: idle, total: total}
	}
	return cpuSample{}
}

func collectMetrics(start time.Time) SystemMetrics {
	m := SystemMetrics{
		Goroutines: runtime.NumGoroutine(),
		UptimeSec:  int64(time.Since(start).Seconds()),
		TS:         time.Now().UTC().Format(time.RFC3339Nano),
		CPUCores:   runtime.NumCPU(),
	}

	cur := readCPUSample()
	if prevCPU.total > 0 && cur.total > prevCPU.total {
		dTotal := float64(cur.total - prevCPU.total)
		dIdle := float64(cur.idle - prevCPU.idle)
		m.CPUPercent = (1.0 - dIdle/dTotal) * 100.0
	}
	prevCPU = cur

	if f, err := os.Open("/proc/loadavg"); err == nil {
		scanner := bufio.NewScanner(f)
		if scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 3 {
				if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
					m.CPULoad1 = v
				}
				if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
					m.CPULoad5 = v
				}
				if v, err := strconv.ParseFloat(fields[2], 64); err == nil {
					m.CPULoad15 = v
				}
			}
		}
		f.Close()
	}

	if f, err := os.Open("/proc/meminfo"); err == nil {
		var total, available uint64
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
						total = v
					}
				}
			}
			if strings.HasPrefix(line, "MemAvailable:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
						available = v
					}
				}
			}
		}
		f.Close()
		if total > 0 {
			used := total - available
			m.MemTotalMB = float64(total) / 1024
			m.MemUsedMB = float64(used) / 1024
			m.MemPercent = float64(used) / float64(total) * 100
		}
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.HeapAllocMB = float64(ms.HeapAlloc) / 1024 / 1024
	m.SysMB = float64(ms.Sys) / 1024 / 1024
	m.GCRuns = ms.NumGC

	return m
}

func (h *Hub) startMetricsBroadcast(ctx context.Context, start time.Time) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			m := collectMetrics(start)
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

// ---- Helpers ----

func parseTFs(s string) []int {
	var tfs []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n := 0
		for _, c := range p {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > 0 {
			tfs = append(tfs, n)
		}
	}
	return tfs
}

func parseTokenKeys(s string) []string {
	if s == "" {
		return nil
	}
	var keys []string
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		exName := "NSE"
		switch parts[0] {
		case "1":
			exName = "NSE"
		case "2":
			exName = "NFO"
		case "3":
			exName = "BSE"
		}
		keys = append(keys, exName+":"+parts[1])
	}
	return keys
}

func tfLabel(tf int) string {
	if tf < 60 {
		return fmt.Sprintf("%ds", tf)
	}
	if tf < 3600 {
		return fmt.Sprintf("%dm", tf/60)
	}
	return fmt.Sprintf("%dh", tf/3600)
}

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func parseIndicatorNames(s string) []string {
	defaults := []string{"SMA_9", "SMA_20", "SMA_50", "SMA_200", "EMA_9", "EMA_21", "RSI_14"}
	if s == "" {
		return defaults
	}

	var names []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		tokens := strings.SplitN(part, ":", 2)
		if len(tokens) != 2 {
			continue
		}
		typ := strings.ToUpper(strings.TrimSpace(tokens[0]))
		period := strings.TrimSpace(tokens[1])
		if typ == "" || period == "" {
			continue
		}
		names = append(names, typ+"_"+period)
	}
	if len(names) == 0 {
		return defaults
	}
	log.Printf("[api_gateway] loaded %d indicators from INDICATOR_CONFIGS", len(names))
	return names
}
