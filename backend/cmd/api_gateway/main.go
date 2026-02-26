package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"trading-systemv1/internal/gateway"

	goredis "github.com/go-redis/redis/v8"
)

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
	hub := gateway.NewHub(rdb, tfs, tokenKeys, indicators)
	go hub.Run(ctx)

	// Register all HTTP routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux, hub, rdb, ctx, tfs, tokenKeys, indicators, processStart)

	srv := &http.Server{Addr: listenAddr, Handler: mux}

	// Start metrics broadcast (every 2s)
	go hub.StartMetricsBroadcast(ctx, processStart)

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

// ---- Config parsers ----

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
		case "4":
			exName = "BSE_FO"
		case "5":
			exName = "MCX_FO"
		case "7":
			exName = "NCX_FO"
		case "13":
			exName = "CDE_FO"
		default:
			log.Printf("[api_gateway] WARNING: unknown exchange type %q, defaulting to NSE", parts[0])
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
