package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:       func(r *http.Request) bool { return true },
	EnableCompression: true,
}

// SetCORS sets CORS headers for REST endpoints.
func SetCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// RegisterRoutes registers all HTTP routes on the provided mux.
func RegisterRoutes(mux *http.ServeMux, hub *Hub, rdb *goredis.Client, ctx context.Context, tfs []int, tokenKeys, indicators []string, processStart time.Time) {
	// WebSocket endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[api_gateway] ws upgrade error: %v", err)
			return
		}
		lastTS := r.URL.Query().Get("last_ts")
		hub.HandleWSRequest(conn, lastTS)
	})

	// REST: latest indicator values
	mux.HandleFunc("/api/indicators/latest", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w)
		w.Header().Set("Content-Type", "application/json")
		latest := hub.GetLatestAll()
		json.NewEncoder(w).Encode(latest)
	})

	// REST: available timeframes
	mux.HandleFunc("/api/tfs", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w)
		w.Header().Set("Content-Type", "application/json")
		type tfInfo struct {
			Seconds int    `json:"seconds"`
			Label   string `json:"label"`
		}
		tfList := make([]tfInfo, len(tfs))
		for i, tf := range tfs {
			tfList[i] = tfInfo{Seconds: tf, Label: TFLabel(tf)}
		}
		json.NewEncoder(w).Encode(tfList)
	})

	// REST: config
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tfs":        tfs,
			"tokens":     tokenKeys,
			"indicators": indicators,
		})
	})

	// REST: GET/POST /api/indicators/active
	mux.HandleFunc("/api/indicators/active", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w)
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
		SetCORS(w)
		w.Header().Set("Content-Type", "application/json")
		m := CollectMetrics(processStart)
		if v, ok := ReadIndicatorLatency(r.Context(), rdb); ok {
			m.IndicatorMs = v
		}
		json.NewEncoder(w).Encode(m)
	})

	// REST: historical candles from Redis streams
	mux.HandleFunc("/api/candles", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w)
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
		SetCORS(w)
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

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w)
		w.Header().Set("Content-Type", "application/json")

		redisOK := true
		if err := rdb.Ping(r.Context()).Err(); err != nil {
			redisOK = false
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "ok",
			"redis":      redisOK,
			"ws_clients": hub.ClientCount(),
			"uptime_sec": int64(time.Since(processStart).Seconds()),
			"ts":         time.Now().UTC().Format(time.RFC3339Nano),
		})
	})
}
