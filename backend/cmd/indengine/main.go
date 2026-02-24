package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
	sqlitestore "trading-systemv1/internal/store/sqlite"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[indengine] starting Indicator Engine microservice...")

	// ---- Load config ----
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")
	sqlitePath := getEnv("SQLITE_PATH", "data/candles.db")
	consumerGroup := getEnv("CONSUMER_GROUP", "indengine")
	consumerName := getEnv("CONSUMER_NAME", "worker-1")
	enabledTFsStr := getEnv("ENABLED_TFS", "60,120,180,300")
	snapshotIntervalStr := getEnv("SNAPSHOT_INTERVAL_SEC", "30")
	subscribeTokens := getEnv("SUBSCRIBE_TOKENS", "")
	snapshotKey := getEnv("SNAPSHOT_KEY", "ind:snapshot:engine")
	httpAddr := getEnv("INDENGINE_HTTP_ADDR", ":9095")
	pelIntervalStr := getEnv("PEL_RECLAIM_INTERVAL_SEC", "30")
	pelMinIdleStr := getEnv("PEL_MIN_IDLE_MS", "60000")

	pelInterval, _ := strconv.Atoi(pelIntervalStr)
	if pelInterval <= 0 {
		pelInterval = 30
	}
	pelMinIdle, _ := strconv.ParseInt(pelMinIdleStr, 10, 64)
	if pelMinIdle <= 0 {
		pelMinIdle = 60000
	}

	snapshotInterval, _ := strconv.Atoi(snapshotIntervalStr)
	if snapshotInterval <= 0 {
		snapshotInterval = 30
	}

	enabledTFs := parseTFs(enabledTFsStr)
	log.Printf("[indengine] enabled TFs: %v", enabledTFs)
	log.Printf("[indengine] snapshot interval: %ds", snapshotInterval)

	// ---- Build indicator configs ----
	indConfigs := buildIndicatorConfigs(enabledTFs)

	// ---- Setup context for graceful shutdown ----
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// ---- Connect to Redis ----
	redisReader, err := redisstore.NewReader(redisstore.ReaderConfig{
		Addr:          redisAddr,
		Password:      redisPassword,
		ConsumerGroup: consumerGroup,
		ConsumerName:  consumerName,
	})
	if err != nil {
		log.Fatalf("[indengine] redis reader init failed: %v", err)
	}
	defer redisReader.Close()

	// Also need a Redis writer for publishing indicator results + snapshots
	redisWriter, err := redisstore.New(redisstore.WriterConfig{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	if err != nil {
		log.Fatalf("[indengine] redis writer init failed: %v", err)
	}
	defer redisWriter.Close()

	// ---- Open SQLite for snapshot/backfill ----
	sqlReader, err := sqlitestore.NewReader(sqlitePath)
	if err != nil {
		log.Printf("[indengine] WARNING: sqlite reader init failed: %v (continuing without SQLite backfill)", err)
	} else {
		defer sqlReader.Close()
	}

	// SQLite writer for saving snapshots
	os.MkdirAll("data", 0o755)
	sqlWriter, err := sqlitestore.New(sqlitestore.WriterConfig{DBPath: sqlitePath})
	if err != nil {
		log.Printf("[indengine] WARNING: sqlite writer init failed: %v", err)
	} else {
		defer sqlWriter.Close()
	}

	// ---- Step 1: Restore indicator engine from snapshot ----
	restorer := indicator.NewRestorer(indConfigs)
	var snap *indicator.EngineSnapshot

	// Try Redis snapshot first
	snap, err = redisReader.ReadSnapshot(ctx, snapshotKey)
	if err != nil {
		log.Printf("[indengine] redis snapshot read error: %v", err)
	}

	// Fallback to SQLite
	if snap == nil && sqlReader != nil {
		snap, err = sqlReader.ReadLatestSnapshot()
		if err != nil {
			log.Printf("[indengine] sqlite snapshot read error: %v", err)
		}
	}

	engine, err := restorer.RestoreFromSnap(snap)
	if err != nil {
		log.Fatalf("[indengine] engine restore failed: %v", err)
	}

	// ---- Step 1b: Backfill from SQLite to warm up cold indicators ----
	if sqlReader != nil {
		backfilled := restorer.BackfillFromSQLite(engine, sqlReader)
		if backfilled > 0 {
			log.Printf("[indengine] warmed up indicators with %d historical candles", backfilled)
		}
	}

	// ---- Step 2: Determine TF streams to consume ----
	// Build stream names from config
	tokenKeys := parseTokenKeys(subscribeTokens)
	var streams []string
	for _, tf := range enabledTFs {
		if len(tokenKeys) > 0 {
			for _, tk := range tokenKeys {
				streams = append(streams, "candle:"+strconv.Itoa(tf)+"s:"+tk)
			}
		} else {
			// Discover streams from Redis
			discovered := redisReader.DiscoverTFStreams(ctx, []int{tf}, tokenKeys)
			streams = append(streams, discovered...)
		}
	}
	log.Printf("[indengine] consuming from %d streams: %v", len(streams), streams)

	// ---- Step 3: Replay delta if we have a snapshot ----
	if snap != nil && snap.StreamID != "" {
		log.Printf("[indengine] replaying delta from stream ID: %s", snap.StreamID)
		replayCh := make(chan model.TFCandle, 5000)
		go func() {
			for _, stream := range streams {
				_, err := redisReader.ReplayFromID(ctx, stream, snap.StreamID, replayCh)
				if err != nil {
					log.Printf("[indengine] replay error on %s: %v", stream, err)
				}
			}
			close(replayCh)
		}()

		deltaCount := 0
		for tfc := range replayCh {
			if !tfc.Forming {
				engine.Process(tfc)
				deltaCount++
			}
		}
		log.Printf("[indengine] ✅ replayed %d delta candles", deltaCount)
	}

	// ---- Step 4: Ensure consumer groups exist ----
	if len(streams) > 0 {
		if err := redisReader.EnsureConsumerGroup(ctx, streams); err != nil {
			log.Printf("[indengine] WARNING: consumer group setup: %v", err)
		}
	}

	// ---- Step 5: Channels for async publishing ----
	tfCandleCh := make(chan model.TFCandle, 5000)

	// ---- Setup metrics ----
	prom := metrics.NewMetrics()

	// ---- Step 6: Recover any pending messages ----
	if len(streams) > 0 {
		if err := redisReader.RecoverPending(ctx, streams, tfCandleCh); err != nil {
			log.Printf("[indengine] pending recovery error: %v", err)
		}
	}

	// ---- Step 6b: Start periodic PEL reclaimer ----
	if len(streams) > 0 {
		go redisReader.StartPELReclaimer(ctx, streams, consumerGroup, consumerName,
			time.Duration(pelInterval)*time.Second, pelMinIdle, tfCandleCh,
			func(count int) {
				prom.PELMessagesReclaimed.Add(float64(count))
				log.Printf("[indengine] reclaimed %d stale PEL messages", count)
			})
		log.Printf("[indengine] PEL reclaimer started (interval=%ds, minIdle=%dms)", pelInterval, pelMinIdle)
	}

	// ---- Step 7: Start indicator processing goroutine ----
	// Processes candles and batches all indicator results into a single Redis pipeline
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case tfc, ok := <-tfCandleCh:
				if !ok {
					return
				}

				var results []model.IndicatorResult
				if tfc.Forming {
					// Live preview — Peek without mutating state
					results = engine.ProcessPeek(tfc)
					log.Printf("[debug] forming candle TF=%ds %s:%s close=%d → %d results",
						tfc.TF, tfc.Exchange, tfc.Token, tfc.Close, len(results))
				} else {
					// Completed candle — Update state
					results = engine.Process(tfc)
				}

				for _, r := range results {
					if r.Ready && !r.Live {
						log.Printf("[indicator] %s TF=%ds %s:%s = %.4f",
							r.Name, r.TF, r.Exchange, r.Token, r.Value)
					}
				}

				// Batch all results into a single Redis pipeline (1 roundtrip)
				if len(results) > 0 {
					redisWriter.WriteIndicatorBatch(ctx, results)
				}
			}
		}
	}()

	// ---- Step 9: Start Redis stream consumer ----
	if len(streams) > 0 {
		go func() {
			if err := redisReader.ConsumeTFCandles(ctx, streams, tfCandleCh); err != nil {
				log.Printf("[indengine] consumer error: %v", err)
			}
		}()
	}

	// ---- Step 9b: Subscribe to 1s candles for live indicator peek ----
	go func() {
		if err := redisReader.Subscribe1sForPeek(ctx, enabledTFs, tfCandleCh); err != nil {
			log.Printf("[indengine] 1s peek subscription error: %v", err)
		}
	}()
	log.Println("[indengine] subscribed to 1s candle PubSub for live indicator peek")

	// ---- Step 10: Periodic snapshot checkpoint ----
	go func() {
		ticker := time.NewTicker(time.Duration(snapshotInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap, err := indicator.SnapshotEngine(engine, getLastStreamID(ctx, redisReader, streams))
				if err != nil {
					log.Printf("[indengine] snapshot error: %v", err)
					continue
				}

				// Save to Redis
				if err := redisReader.WriteSnapshot(ctx, snapshotKey, snap); err != nil {
					log.Printf("[indengine] redis snapshot write error: %v", err)
				}

				// Save to SQLite
				if sqlWriter != nil {
					if err := sqlWriter.SaveSnapshot(snap); err != nil {
						log.Printf("[indengine] sqlite snapshot write error: %v", err)
					}
				}

				log.Printf("[indengine] ✅ checkpoint saved (%d tokens)", len(snap.Tokens))
			}
		}
	}()

	// ---- Step 11: Start HTTP server for live config reload ----
	reloadEngine := func(newSpecs []indicator.IndicatorConfig) {
		newConfigs := make([]indicator.TFIndicatorConfig, len(enabledTFs))
		for i, tf := range enabledTFs {
			newConfigs[i] = indicator.TFIndicatorConfig{TF: tf, Indicators: newSpecs}
		}
		if err := indicator.ValidateConfigs(newConfigs); err != nil {
			log.Printf("[indengine] invalid config: %v", err)
			return
		}
		preserved, created := engine.ReloadConfigs(newConfigs)
		log.Printf("[indengine] reloaded: preserved=%d, created=%d", preserved, created)
		// Backfill new indicators from SQLite
		if created > 0 && sqlReader != nil {
			newRestorer := indicator.NewRestorer(newConfigs)
			newRestorer.BackfillFromSQLite(engine, sqlReader)
		}
	}

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			var newConfigs []indicator.TFIndicatorConfig
			if err := json.NewDecoder(r.Body).Decode(&newConfigs); err != nil {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := indicator.ValidateConfigs(newConfigs); err != nil {
				http.Error(w, "validation: "+err.Error(), http.StatusBadRequest)
				return
			}
			preserved, created := engine.ReloadConfigs(newConfigs)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":    "ok",
				"preserved": preserved,
				"created":   created,
			})
		})
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "ok")
		})
		log.Printf("[indengine] HTTP server on %s (/reload, /healthz)", httpAddr)
		if err := http.ListenAndServe(httpAddr, mux); err != nil {
			log.Printf("[indengine] HTTP server error: %v", err)
		}
	}()

	// ---- Step 12: Subscribe to Redis config:indicators for dynamic reload ----
	go func() {
		pubsub := redisReader.SubscribeChannel(ctx, "config:indicators")
		if pubsub == nil {
			log.Println("[indengine] WARNING: could not subscribe to config:indicators")
			return
		}
		defer pubsub.Close()
		log.Println("[indengine] subscribed to config:indicators for dynamic reload")

		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				log.Printf("[indengine] received config update: %s", msg.Payload)
				newSpecs := parseIndicatorSpecs(msg.Payload)
				reloadEngine(newSpecs)
			}
		}
	}()

	// ---- Print startup banner ----
	log.Println("[indengine] ╔════════════════════════════════════════════════════════╗")
	log.Println("[indengine] ║  Indicator Engine (MS2) Active                        ║")
	log.Println("[indengine] ║                                                       ║")
	log.Println("[indengine] ║  [Redis Streams] → [Indicators] → [Redis Publish]     ║")
	log.Println("[indengine] ║  Snapshot checkpoint every " + snapshotIntervalStr + "s                      ║")
	log.Printf("[indengine] ║  TFs: %v                                   ║", enabledTFs)
	log.Println("[indengine] ╚════════════════════════════════════════════════════════╝")
	log.Println("[indengine] ✅ all systems running. Press Ctrl+C to stop.")

	// ---- Wait for shutdown ----
	<-sigCh
	log.Println("[indengine] shutdown signal received, saving final snapshot...")
	cancel()

	// Final snapshot on shutdown
	finalSnap, err := indicator.SnapshotEngine(engine, "shutdown")
	if err == nil {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutCancel()

		if redisReader != nil {
			redisReader.WriteSnapshot(shutCtx, snapshotKey, finalSnap)
		}
		if sqlWriter != nil {
			sqlWriter.SaveSnapshot(finalSnap)
		}
		log.Println("[indengine] final snapshot saved")
	}

	log.Println("[indengine] shutdown complete.")
}

// getLastStreamID gets the last delivered ID for the consumer group from any stream.
func getLastStreamID(ctx context.Context, reader *redisstore.Reader, streams []string) string {
	// For now, return current time-based ID as a marker
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + "-0"
}

// buildIndicatorConfigs creates indicator configurations per TF from the
// INDICATOR_CONFIGS env var.  Format: "TYPE:PERIOD,TYPE:PERIOD,..."
// Example: "SMA:9,SMA:20,SMA:50,SMA:200,EMA:9,EMA:21,RSI:14"
// If the env var is empty, sensible defaults are used.
func buildIndicatorConfigs(tfs []int) []indicator.TFIndicatorConfig {
	indSpecs := parseIndicatorSpecs(getEnv("INDICATOR_CONFIGS", ""))
	configs := make([]indicator.TFIndicatorConfig, len(tfs))
	for i, tf := range tfs {
		configs[i] = indicator.TFIndicatorConfig{
			TF:         tf,
			Indicators: indSpecs,
		}
	}
	return configs
}

// parseIndicatorSpecs parses "TYPE:PERIOD,..." into []IndicatorConfig.
// Returns defaults if input is empty.
func parseIndicatorSpecs(s string) []indicator.IndicatorConfig {
	if s == "" {
		return []indicator.IndicatorConfig{
			{Type: "SMA", Period: 9},
			{Type: "SMA", Period: 20},
			{Type: "SMA", Period: 50},
			{Type: "SMA", Period: 200},
			{Type: "EMA", Period: 9},
			{Type: "EMA", Period: 21},
			{Type: "RSI", Period: 14},
		}
	}

	var configs []indicator.IndicatorConfig
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		tokens := strings.SplitN(part, ":", 2)
		if len(tokens) != 2 {
			continue
		}
		typ := strings.ToUpper(strings.TrimSpace(tokens[0]))
		period, err := strconv.Atoi(strings.TrimSpace(tokens[1]))
		if err != nil || period <= 0 {
			log.Printf("[indengine] skipping invalid indicator spec: %q", part)
			continue
		}
		configs = append(configs, indicator.IndicatorConfig{Type: typ, Period: period})
	}
	if len(configs) == 0 {
		log.Println("[indengine] WARNING: no valid indicators parsed, using defaults")
		return parseIndicatorSpecs("")
	}
	log.Printf("[indengine] loaded %d indicator specs from INDICATOR_CONFIGS", len(configs))
	return configs
}

func parseTFs(s string) []int {
	parts := strings.Split(s, ",")
	tfs := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			continue
		}
		tfs = append(tfs, n)
	}
	return tfs
}

// parseTokenKeys parses "exchangeType:token,..." into "exchange:token" keys.
// Since we use NSE convention: exchangeType 1 = "NSE"
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
		// Map exchange type to name
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

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

// Ensure json import is used (for snapshot marshal in getLastStreamID context)
var _ = json.Marshal
