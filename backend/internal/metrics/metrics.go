package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for the OHLC engine.
type Metrics struct {
	TicksTotal      prometheus.Counter
	CandlesTotal    prometheus.Counter
	WSReconnects    prometheus.Counter
	DroppedTicks    prometheus.Counter
	RedisWriteDur   prometheus.Histogram
	SQLiteCommitDur prometheus.Histogram
	CandleLag       prometheus.Gauge

	// TF resampler metrics
	TFCandlesTotal *prometheus.CounterVec
	TFBuildDur     prometheus.Histogram

	// Indicator engine metrics
	IndicatorComputeDur prometheus.Histogram
	IndicatorsTotal     prometheus.Counter

	// Ring buffer overflow
	RingBufOverflow prometheus.Counter

	// Backpressure metrics (improvement #5)
	FanoutDropsTotal     *prometheus.CounterVec // labels: subscriber
	ChannelSaturationPct *prometheus.GaugeVec   // labels: channel_name

	// Staleness metrics (improvement #2)
	StaleCandlesRejected prometheus.Counter

	// PEL reclaim metrics (improvement #1)
	PELMessagesReclaimed prometheus.Counter

	// Circuit breaker metrics (improvement #6)
	RedisCircuitBreakerState prometheus.Gauge // 0=closed, 1=open, 2=half-open
	RedisCircuitBreakerTrips prometheus.Counter
	RedisBufferedWrites      prometheus.Counter

	// End-to-end observability (improvement #8)
	E2ELatency       prometheus.Histogram // tick-to-WS-emit latency
	WatermarkDelay   prometheus.Gauge     // current watermark delay vs wall clock
	LateTicks        prometheus.Counter   // ticks dropped behind watermark
	ReorderBufferLen prometheus.Gauge     // current reorder buffer occupancy

	// Market session state (ADR-006)
	MarketState        prometheus.Gauge       // 0=closed, 1=open
	SessionTransitions *prometheus.CounterVec // labels: type=open|close|ws_disconnect
}

// NewMetrics registers and returns all Prometheus metrics.
func NewMetrics() *Metrics {
	m := &Metrics{
		TicksTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_ticks_total",
			Help: "Total ticks received from WebSocket",
		}),
		CandlesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_candles_total",
			Help: "Total 1s candles emitted",
		}),
		WSReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_ws_reconnects_total",
			Help: "Total WebSocket reconnection attempts",
		}),
		DroppedTicks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_dropped_ticks_total",
			Help: "Ticks dropped (late or channel full)",
		}),
		RedisWriteDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_redis_write_duration_seconds",
			Help:    "Redis write latency",
			Buckets: prometheus.DefBuckets,
		}),
		SQLiteCommitDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_sqlite_commit_duration_seconds",
			Help:    "SQLite batch commit latency",
			Buckets: prometheus.DefBuckets,
		}),
		CandleLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_candle_lag_seconds",
			Help: "Lag between candle timestamp and emission time",
		}),

		// TF metrics
		TFCandlesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_tf_candles_total",
			Help: "Total TF candles emitted (by timeframe)",
		}, []string{"tf"}),
		TFBuildDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_tf_build_duration_seconds",
			Help:    "TF resampler processing latency per candle",
			Buckets: []float64{0.000001, 0.000005, 0.00001, 0.00005, 0.0001, 0.0005, 0.001},
		}),

		// Indicator metrics
		IndicatorComputeDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_indicator_compute_duration_seconds",
			Help:    "Indicator engine compute latency per TF candle",
			Buckets: []float64{0.000001, 0.000005, 0.00001, 0.00005, 0.0001, 0.0005, 0.001},
		}),
		IndicatorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_indicators_total",
			Help: "Total indicator values computed",
		}),

		// Ring buffer
		RingBufOverflow: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_ringbuf_overflow_total",
			Help: "Ring buffer push overflows (dropped candles)",
		}),

		// Backpressure
		FanoutDropsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_fanout_drops_total",
			Help: "Candles dropped by FanOut bus per subscriber",
		}, []string{"subscriber"}),
		ChannelSaturationPct: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mdengine_channel_saturation_pct",
			Help: "Channel fill percentage (len/cap * 100)",
		}, []string{"channel_name"}),

		// Staleness
		StaleCandlesRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_stale_candles_rejected_total",
			Help: "Candles rejected by TF Builder due to staleness",
		}),

		// PEL reclaim
		PELMessagesReclaimed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "indengine_pel_messages_reclaimed_total",
			Help: "Messages reclaimed from dead consumers via XCLAIM",
		}),

		// Circuit breaker
		RedisCircuitBreakerState: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_redis_circuit_breaker_state",
			Help: "Redis circuit breaker state (0=closed, 1=open, 2=half-open)",
		}),
		RedisCircuitBreakerTrips: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_redis_circuit_breaker_trips_total",
			Help: "Times the Redis circuit breaker tripped open",
		}),
		RedisBufferedWrites: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_redis_buffered_writes_total",
			Help: "Writes buffered locally during Redis circuit breaker open state",
		}),

		// E2E observability
		E2ELatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_e2e_latency_seconds",
			Help:    "End-to-end latency from tick ingest to WS emit",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		}),
		WatermarkDelay: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_watermark_delay_seconds",
			Help: "Lag between wall-clock time and event-time watermark",
		}),
		LateTicks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_late_ticks_total",
			Help: "Ticks dropped because they arrived behind the event-time watermark",
		}),
		ReorderBufferLen: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_reorder_buffer_len",
			Help: "Current number of candle buckets held in the reorder buffer",
		}),

		// Market session (ADR-006)
		MarketState: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_market_state",
			Help: "Market session state (0=closed, 1=open)",
		}),
		SessionTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_session_transitions_total",
			Help: "Market session transitions (open, close, ws_disconnect)",
		}, []string{"type"}),
	}

	prometheus.MustRegister(
		m.TicksTotal,
		m.CandlesTotal,
		m.WSReconnects,
		m.DroppedTicks,
		m.RedisWriteDur,
		m.SQLiteCommitDur,
		m.CandleLag,
		m.TFCandlesTotal,
		m.TFBuildDur,
		m.IndicatorComputeDur,
		m.IndicatorsTotal,
		m.RingBufOverflow,
		m.FanoutDropsTotal,
		m.ChannelSaturationPct,
		m.StaleCandlesRejected,
		m.PELMessagesReclaimed,
		m.RedisCircuitBreakerState,
		m.RedisCircuitBreakerTrips,
		m.RedisBufferedWrites,
		m.E2ELatency,
		m.WatermarkDelay,
		m.LateTicks,
		m.ReorderBufferLen,
		m.MarketState,
		m.SessionTransitions,
	)

	return m
}

// HealthStatus represents the system health.
type HealthStatus struct {
	mu sync.RWMutex

	WSConnected    bool      `json:"ws_connected"`
	LastTickTime   time.Time `json:"last_tick_time"`
	RedisConnected bool      `json:"redis_connected"`
	SQLiteOK       bool      `json:"sqlite_ok"`
	TFBuilderOK    bool      `json:"tf_builder_ok"`
	IndicatorOK    bool      `json:"indicator_ok"`
	EnabledTFs     []int     `json:"enabled_tfs"`

	// Liveness probe results
	RedisLatencyMs  float64   `json:"redis_latency_ms"`
	SQLiteLatencyMs float64   `json:"sqlite_latency_ms"`
	LastCheckAt     time.Time `json:"last_check_at"`
	StartedAt       time.Time `json:"started_at"`
}

// NewHealthStatus returns a default health status.
func NewHealthStatus() *HealthStatus {
	return &HealthStatus{
		StartedAt: time.Now(),
	}
}

func (h *HealthStatus) SetWSConnected(v bool) {
	h.mu.Lock()
	h.WSConnected = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetLastTickTime(t time.Time) {
	h.mu.Lock()
	h.LastTickTime = t
	h.mu.Unlock()
}

func (h *HealthStatus) SetRedisConnected(v bool) {
	h.mu.Lock()
	h.RedisConnected = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetSQLiteOK(v bool) {
	h.mu.Lock()
	h.SQLiteOK = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetTFBuilderOK(v bool) {
	h.mu.Lock()
	h.TFBuilderOK = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetIndicatorOK(v bool) {
	h.mu.Lock()
	h.IndicatorOK = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetEnabledTFs(tfs []int) {
	h.mu.Lock()
	h.EnabledTFs = tfs
	h.mu.Unlock()
}

// CheckRedis pings Redis and records latency + connectivity.
func (h *HealthStatus) CheckRedis(ctx context.Context, rdb *goredis.Client) {
	start := time.Now()
	err := rdb.Ping(ctx).Err()
	latency := time.Since(start)

	h.mu.Lock()
	h.RedisConnected = err == nil
	h.RedisLatencyMs = float64(latency.Microseconds()) / 1000.0
	h.LastCheckAt = time.Now()
	h.mu.Unlock()
}

// CheckSQLite runs a trivial query and records latency + health.
func (h *HealthStatus) CheckSQLite(ctx context.Context, db *sql.DB) {
	start := time.Now()
	err := db.PingContext(ctx)
	latency := time.Since(start)

	h.mu.Lock()
	h.SQLiteOK = err == nil
	h.SQLiteLatencyMs = float64(latency.Microseconds()) / 1000.0
	h.LastCheckAt = time.Now()
	h.mu.Unlock()
}

// StartLivenessChecker runs periodic dependency checks.
func (h *HealthStatus) StartLivenessChecker(ctx context.Context, rdb *goredis.Client, sqlDB *sql.DB, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				if rdb != nil {
					h.CheckRedis(probeCtx, rdb)
				}
				if sqlDB != nil {
					h.CheckSQLite(probeCtx, sqlDB)
				}
				cancel()
			}
		}
	}()
}

// ServeHTTP handles the /healthz endpoint.
func (h *HealthStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Determine overall status
	overallStatus := "healthy"
	httpCode := http.StatusOK

	if !h.WSConnected || !h.RedisConnected || !h.SQLiteOK {
		overallStatus = "degraded"
		httpCode = http.StatusServiceUnavailable
	}
	if !h.RedisConnected && !h.SQLiteOK {
		overallStatus = "unhealthy"
	}

	// Tick age
	tickAge := ""
	if !h.LastTickTime.IsZero() {
		tickAge = time.Since(h.LastTickTime).Round(time.Millisecond).String()
	}

	status := struct {
		Status          string  `json:"status"`
		Uptime          string  `json:"uptime"`
		WSConnected     bool    `json:"ws_connected"`
		LastTickTime    string  `json:"last_tick_time"`
		TickAge         string  `json:"tick_age"`
		RedisConnected  bool    `json:"redis_connected"`
		RedisLatencyMs  float64 `json:"redis_latency_ms"`
		SQLiteOK        bool    `json:"sqlite_ok"`
		SQLiteLatencyMs float64 `json:"sqlite_latency_ms"`
		TFBuilderOK     bool    `json:"tf_builder_ok"`
		IndicatorOK     bool    `json:"indicator_ok"`
		EnabledTFs      []int   `json:"enabled_tfs"`
		LastCheckAt     string  `json:"last_check_at"`
	}{
		Status:          overallStatus,
		Uptime:          time.Since(h.StartedAt).Round(time.Second).String(),
		WSConnected:     h.WSConnected,
		LastTickTime:    h.LastTickTime.Format(time.RFC3339),
		TickAge:         tickAge,
		RedisConnected:  h.RedisConnected,
		RedisLatencyMs:  h.RedisLatencyMs,
		SQLiteOK:        h.SQLiteOK,
		SQLiteLatencyMs: h.SQLiteLatencyMs,
		TFBuilderOK:     h.TFBuilderOK,
		IndicatorOK:     h.IndicatorOK,
		EnabledTFs:      h.EnabledTFs,
		LastCheckAt:     h.LastCheckAt.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	if httpCode != http.StatusOK {
		w.WriteHeader(httpCode)
	}
	json.NewEncoder(w).Encode(status)
}

// Server runs an HTTP server exposing /metrics and /healthz.
type Server struct {
	health *HealthStatus
	addr   string
	srv    *http.Server
}

// NewServer creates a metrics and health server.
func NewServer(addr string, health *HealthStatus) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", health.ServeHTTP)

	return &Server{
		health: health,
		addr:   addr,
		srv: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Start launches the HTTP server in a goroutine.
func (s *Server) Start() {
	go func() {
		log.Printf("[metrics] server listening on %s", s.addr)
		if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("[metrics] server error: %v", err)
		}
	}()
}

// Stop gracefully shuts down the metrics server.
func (s *Server) Stop(ctx context.Context) {
	s.srv.Shutdown(ctx)
}
