package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pquerna/otp/totp"

	"trading-systemv1/config"
	"trading-systemv1/internal/marketdata/agg"
	"trading-systemv1/internal/marketdata/bus"
	"trading-systemv1/internal/marketdata/tfbuilder"
	"trading-systemv1/internal/marketdata/ws"
	"trading-systemv1/internal/marketdata/wssim"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
	sqlitestore "trading-systemv1/internal/store/sqlite"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[mdengine] starting...")

	// ---- Staging mode check ----
	stagingMode := strings.EqualFold(os.Getenv("STAGING_MODE"), "true")
	if stagingMode {
		log.Println("[mdengine] *** STAGING MODE — using tickserver WS instead of Angel One ***")
	}

	// ---- Load config from env ----
	var cfg *config.Config
	if !stagingMode {
		cfg = config.Load() // requires Angel One env vars
	}

	// ---- Parse subscription tokens (production only) ----
	var tokenList []smartconnect.TokenListEntry
	if !stagingMode {
		tokenList = parseTokenList(cfg.SubscribeTokens)
		log.Printf("[mdengine] subscribing to %d token groups", len(tokenList))
	}

	// ---- Parse enabled timeframes ----
	var enabledTFs []int
	if stagingMode {
		enabledTFs = parseTFsFromEnv(getEnv("ENABLED_TFS", "60,120,180,300"))
	} else {
		enabledTFs = cfg.ParseTFs()
	}
	log.Printf("[mdengine] enabled TFs: %v seconds", enabledTFs)

	// ---- Setup pipeline channels ----
	tickCh := make(chan model.Tick, 10000)
	candleCh := make(chan model.Candle, 5000)
	tfCandleCh := make(chan model.TFCandle, 5000)

	// Channels for async Redis publishing (separate from compute path)
	redisTFCandleCh := make(chan model.TFCandle, 5000)
	sqliteTFCandleCh := make(chan model.TFCandle, 5000)

	// ---- Setup metrics & health ----
	metricsAddr := getEnv("METRICS_ADDR", ":9090")
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")
	sqlitePath := getEnv("SQLITE_PATH", "data/candles.db")
	if !stagingMode {
		metricsAddr = cfg.MetricsAddr
		redisAddr = cfg.RedisAddr
		redisPassword = cfg.RedisPassword
		sqlitePath = cfg.SQLitePath
	}

	prom := metrics.NewMetrics()
	health := metrics.NewHealthStatus()
	health.SetEnabledTFs(enabledTFs)
	metricsSrv := metrics.NewServer(metricsAddr, health)
	metricsSrv.Start()

	// ---- Setup context for graceful shutdown ----
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// ---- Start SQLite writer (off hot path) ----
	os.MkdirAll("data", 0o755)
	sqlWriter, err := sqlitestore.New(sqlitestore.WriterConfig{DBPath: sqlitePath})
	if err != nil {
		log.Fatalf("[mdengine] sqlite init failed: %v", err)
	}
	defer sqlWriter.Close()
	health.SetSQLiteOK(true)
	log.Println("[mdengine] sqlite writer ready")

	// ---- Start Redis writer ----
	var redisWriter *redisstore.Writer
	redisWriter, err = redisstore.New(redisstore.WriterConfig{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	if err != nil {
		log.Printf("[mdengine] WARNING: redis init failed: %v (continuing without redis)", err)
		health.SetRedisConnected(false)
	} else {
		health.SetRedisConnected(true)
		log.Println("[mdengine] redis writer ready")
	}

	// ---- Periodic liveness checks ----
	if redisWriter != nil {
		health.StartLivenessChecker(ctx, redisWriter.Client(), sqlWriter.DB(), 10*time.Second)
	} else {
		health.StartLivenessChecker(ctx, nil, sqlWriter.DB(), 10*time.Second)
	}

	// ---- Fan-out for 1s candles (SQLite + Redis) ----
	fanout := bus.New(5000)
	fanout.OnDrop = func(subscriberIdx int) {
		prom.FanoutDropsTotal.WithLabelValues(strconv.Itoa(subscriberIdx)).Inc()
	}

	sqliteCandleCh := fanout.Subscribe()
	var redis1sCandleCh <-chan model.Candle
	if redisWriter != nil {
		redis1sCandleCh = fanout.Subscribe()
	}

	go fanout.Run(ctx, candleCh)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := fanout.ChannelStats()
				for i, s := range stats {
					if s.Cap > 0 {
						pct := float64(s.Len) / float64(s.Cap) * 100
						prom.ChannelSaturationPct.WithLabelValues("fanout_" + strconv.Itoa(i)).Set(pct)
					}
				}
			}
		}
	}()

	go sqlWriter.Run(ctx, sqliteCandleCh)
	if redisWriter != nil && redis1sCandleCh != nil {
		go redisWriter.Run(ctx, redis1sCandleCh)
	}

	// ---- TF Builder (HOT PATH) ----
	tfBuilder := tfbuilder.New(enabledTFs)
	tfBuilder.OnTFCandle = func(c model.TFCandle) {
		prom.TFCandlesTotal.WithLabelValues(strconv.Itoa(c.TF)).Inc()
	}
	tfBuilder.OnStaleCandle = func() {
		prom.StaleCandlesRejected.Inc()
	}
	health.SetTFBuilderOK(true)
	log.Printf("[mdengine] TF builder started with TFs=%v (stale tolerance=%v)", enabledTFs, tfBuilder.StaleTolerance)

	tfBuilderIn := fanout.Subscribe()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case c, ok := <-tfBuilderIn:
				if !ok {
					return
				}
				start := time.Now()
				tfBuilder.Run1(c, tfCandleCh)
				prom.TFBuildDur.Observe(time.Since(start).Seconds())
			}
		}
	}()

	// ---- Fan out TF candles to Redis + SQLite (OFF hot path) ----
	redisFormingCh := make(chan model.TFCandle, 5000)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case tfc, ok := <-tfCandleCh:
				if !ok {
					return
				}
				if tfc.Forming {
					select {
					case redisFormingCh <- tfc:
					default:
					}
					continue
				}
				select {
				case redisTFCandleCh <- tfc:
				default:
				}
				select {
				case sqliteTFCandleCh <- tfc:
				default:
				}
			}
		}
	}()

	if redisWriter != nil {
		go redisWriter.RunTFCandles(ctx, redisTFCandleCh)
		go redisWriter.RunFormingTFCandles(ctx, redisFormingCh)
	}
	go sqlWriter.RunTFCandles(ctx, sqliteTFCandleCh)

	// ---- Aggregator (1s OHLC builder) ----
	aggregator := agg.New()
	aggregator.OnDroppedTick = func() {
		prom.DroppedTicks.Inc()
	}
	go aggregator.Run(ctx, tickCh, candleCh)
	log.Println("[mdengine] pipeline ready (24/7)")

	// ═══════════════════════════════════════════════════════════════
	// WS Lifecycle: STAGING vs PRODUCTION
	// ═══════════════════════════════════════════════════════════════
	if stagingMode {
		// ---- STAGING: connect to tickserver via wssim ----
		simWSURL := getEnv("SIM_WS_URL", "ws://localhost:9001/ws")
		log.Printf("[mdengine] staging tick source: %s", simWSURL)

		ingest, err := wssim.New(wssim.Config{
			URL:               simWSURL,
			ReconnectDelay:    2 * time.Second,
			MaxReconnectDelay: 30 * time.Second,
		})
		if err != nil {
			log.Fatalf("[mdengine] wssim init failed: %v", err)
		}
		ingest.OnReconnect = func() {
			prom.WSReconnects.Inc()
		}
		health.SetWSConnected(true)

		go func() {
			if err := ingest.Start(ctx, tickCh); err != nil {
				log.Printf("[mdengine] wssim error: %v", err)
				health.SetWSConnected(false)
			}
		}()

		log.Println("[mdengine] ╔════════════════════════════════════════════════════════════════╗")
		log.Println("[mdengine] ║  Market Data Engine (MS1) — STAGING MODE                      ║")
		log.Println("[mdengine] ║                                                               ║")
		log.Println("[mdengine] ║  [TickServer WS] → [1s Agg] → [TF Builder] → [Redis/SQLite]   ║")
		log.Printf("[mdengine] ║  TFs: %-56v ║", enabledTFs)
		log.Printf("[mdengine] ║  Source: %-52s ║", simWSURL)
		log.Println("[mdengine] ║  No Angel One credentials required                             ║")
		log.Println("[mdengine] ╚════════════════════════════════════════════════════════════════╝")
	} else {
		// ---- PRODUCTION: Angel One WS with market hours gating ----
		go func() {
			for {
				// --- Wait for market open ---
				now := time.Now()
				if !markethours.IsMarketOpen(now) {
					next := markethours.NextOpen(now)
					wait := next.Sub(now)
					log.Printf("[mdengine] ⏸ market closed. %s", markethours.StatusString(now))
					log.Printf("[mdengine] sleeping %v until next open %s",
						wait.Truncate(time.Second), next.In(markethours.IST).Format("Mon 15:04"))
					health.SetWSConnected(false)

					select {
					case <-ctx.Done():
						return
					case <-time.After(wait):
					}
				}

				// --- Fresh login (new TOTP + session) ---
				log.Println("[mdengine] 🔑 market open — generating fresh session...")
				totpCode, err := totp.GenerateCode(cfg.AngelTOTPSecret, time.Now())
				if err != nil {
					log.Printf("[mdengine] TOTP generation failed: %v, retrying in 30s", err)
					time.Sleep(30 * time.Second)
					continue
				}

				sc := smartconnect.NewSmartConnect(smartconnect.Config{
					APIKey: cfg.AngelAPIKey,
					Debug:  false,
				})
				userResp, err := sc.GenerateSession(cfg.AngelClientCode, cfg.AngelPassword, totpCode)
				if err != nil {
					log.Printf("[mdengine] login failed: %v, retrying in 30s", err)
					time.Sleep(30 * time.Second)
					continue
				}

				feedToken := sc.GetFeedToken()
				authToken := ""
				if data, ok := userResp["data"].(map[string]interface{}); ok {
					if jwt, ok := data["jwtToken"].(string); ok {
						authToken = jwt
					}
				}
				if feedToken == "" || authToken == "" {
					log.Printf("[mdengine] empty tokens from session, retrying in 30s")
					time.Sleep(30 * time.Second)
					continue
				}
				log.Printf("[mdengine] ✅ session ready, feedToken=%s...", feedToken[:min(10, len(feedToken))])

				// --- Connect WS with a deadline at market close ---
				closeTime := markethours.TodayClose(time.Now())
				wsCtx, wsCancel := context.WithDeadline(ctx, closeTime)

				ingest, err := ws.New(ws.IngestConfig{
					AuthToken:     authToken,
					APIKey:        cfg.AngelAPIKey,
					ClientCode:    cfg.AngelClientCode,
					FeedToken:     feedToken,
					SubscribeMode: smartconnect.ModeLTP,
					TokenList:     tokenList,
				})
				if err != nil {
					log.Printf("[mdengine] ws init failed: %v, retrying in 30s", err)
					wsCancel()
					time.Sleep(30 * time.Second)
					continue
				}

				ingest.OnReconnect = func() {
					prom.WSReconnects.Inc()
				}

				health.SetWSConnected(true)
				log.Printf("[mdengine] 📡 WS connected — will disconnect at %s",
					closeTime.In(markethours.IST).Format("15:04:05"))

				// This blocks until wsCtx deadline (3:30 PM) or parent ctx cancelled
				if err := ingest.Start(wsCtx, tickCh); err != nil {
					log.Printf("[mdengine] ws session ended: %v", err)
				}
				wsCancel()
				health.SetWSConnected(false)
				log.Println("[mdengine] 🔌 WS disconnected — market close")

				// Check if parent ctx was cancelled (shutdown signal)
				if ctx.Err() != nil {
					return
				}
				// Loop back to wait for next market open
			}
		}()

		log.Println("[mdengine] ╔═══════════════════════════════════════════════════════════════╗")
		log.Println("[mdengine] ║  Market Data Engine (MS1) — Production Mode                  ║")
		log.Println("[mdengine] ║                                                              ║")
		log.Println("[mdengine] ║  Pipeline (24/7): [Agg] → [TF Builder] → [Redis/SQLite]      ║")
		log.Println("[mdengine] ║  WS Feed (market hours): 9:15 AM – 3:30 PM IST, Mon–Fri      ║")
		log.Printf("[mdengine] ║  TFs: %v                              ║", enabledTFs)
		log.Println("[mdengine] ║  Fresh login + tokens at each market open                    ║")
		log.Println("[mdengine] ╚═══════════════════════════════════════════════════════════════╝")
		log.Printf("[mdengine] %s", markethours.StatusString(time.Now()))
	}

	// ---- Wait for shutdown signal ----
	<-sigCh
	log.Println("[mdengine] shutdown signal received, cleaning up...")
	cancel()

	// Give goroutines time to flush buffers
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*1e9)
	defer shutdownCancel()
	metricsSrv.Stop(shutdownCtx)

	if redisWriter != nil {
		redisWriter.Close()
	}

	log.Println("[mdengine] shutdown complete.")
}

// parseTokenList parses "exchangeType:token,exchangeType:token,..." into TokenListEntry slices.
func parseTokenList(s string) []smartconnect.TokenListEntry {
	groups := map[int][]string{}
	for _, pair := range splitString(s, ",") {
		parts := splitString(pair, ":")
		if len(parts) != 2 {
			continue
		}
		exType := 0
		for _, c := range parts[0] {
			exType = exType*10 + int(c-'0')
		}
		groups[exType] = append(groups[exType], parts[1])
	}

	var result []smartconnect.TokenListEntry
	for exType, tokens := range groups {
		result = append(result, smartconnect.TokenListEntry{
			ExchangeType: exType,
			Tokens:       tokens,
		})
	}
	return result
}

func splitString(s, sep string) []string {
	var result []string
	for _, part := range []byte(s) {
		if string(part) == sep {
			result = append(result, "")
		} else {
			if len(result) == 0 {
				result = append(result, "")
			}
			result[len(result)-1] += string(part)
		}
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseTFsFromEnv parses comma-separated TF seconds for staging mode.
func parseTFsFromEnv(s string) []int {
	var tfs []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			log.Printf("[mdengine] skipping invalid TF %q", p)
			continue
		}
		tfs = append(tfs, n)
	}
	return tfs
}
