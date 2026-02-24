// Package api provides HTTP/gRPC API handlers for the trading system.
package api

import (
	"net/http"
)

// Router sets up HTTP routes for the API server.
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// TODO: Add endpoints for:
	// GET  /api/v1/candles?token=X&exchange=Y&from=T1&to=T2
	// GET  /api/v1/positions
	// POST /api/v1/orders
	// GET  /api/v1/strategies
	// WS   /api/v1/stream (real-time candle stream)

	return mux
}
