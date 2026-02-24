// cmd/backtest replays historical candle data from SQLite through the indicator
// engine to validate indicators and strategies without live market data.
//
// Usage:
//
//	go run ./cmd/backtest --speed=100 --tf=60,300 --from=0
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/marketdata/replay"
	"trading-systemv1/internal/model"
	sqlitestore "trading-systemv1/internal/store/sqlite"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	// Flags
	speed := flag.Float64("speed", 0, "Playback speed multiplier (0=max, 1=realtime, 100=100x)")
	tfStr := flag.String("tf", "60,300", "Comma-separated TFs to replay")
	fromTS := flag.Int64("from", 0, "Unix timestamp to start replay from (0=all)")
	dbPath := flag.String("db", "data/candles.db", "Path to SQLite database")
	indicatorCfg := flag.String("indicators", "", "Indicator specs: TYPE:PERIOD,... (default: SMA:20,EMA:9,RSI:14)")
	flag.Parse()

	tfs := parseTFs(*tfStr)
	if len(tfs) == 0 {
		log.Fatal("[backtest] no valid TFs specified")
	}

	// Open SQLite
	reader, err := sqlitestore.NewReader(*dbPath)
	if err != nil {
		log.Fatalf("[backtest] sqlite open failed: %v", err)
	}
	defer reader.Close()

	// Build indicator engine
	indSpecs := parseIndicatorSpecs(*indicatorCfg)
	var indConfigs []indicator.TFIndicatorConfig
	for _, tf := range tfs {
		indConfigs = append(indConfigs, indicator.TFIndicatorConfig{
			TF:         tf,
			Indicators: indSpecs,
		})
	}

	restorer := indicator.NewRestorer(indConfigs)
	engine, err := restorer.RestoreFromSnap(nil) // cold start
	if err != nil {
		log.Fatalf("[backtest] engine init failed: %v", err)
	}

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Create replayer
	replayer := replay.New(reader)
	candleCh := make(chan model.TFCandle, 10000)

	// Replay in background
	go func() {
		if err := replayer.Run(ctx, tfs, *fromTS, *speed, candleCh); err != nil {
			log.Printf("[backtest] replay error: %v", err)
		}
		close(candleCh)
	}()

	// Process candles through indicator engine
	processed := 0
	readyResults := 0
	for candle := range candleCh {
		results := engine.Process(candle)
		processed++
		for _, r := range results {
			if r.Ready {
				readyResults++
				if processed <= 10 || processed%100 == 0 {
					fmt.Printf("  [%s] %s TF=%ds %s:%s = %.4f\n",
						candle.TS.Format("15:04:05"), r.Name, r.TF, r.Exchange, r.Token, r.Value)
				}
			}
		}
	}

	// Print summary
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║        BACKTEST COMPLETE             ║")
	fmt.Println("╠══════════════════════════════════════╣")
	fmt.Printf("║  Candles processed: %-16d ║\n", processed)
	fmt.Printf("║  Indicator results: %-16d ║\n", readyResults)
	fmt.Printf("║  TFs:               %-16v ║\n", tfs)
	fmt.Println("╚══════════════════════════════════════╝")
}

func parseTFs(s string) []int {
	var tfs []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			tfs = append(tfs, n)
		}
	}
	return tfs
}

func parseIndicatorSpecs(s string) []indicator.IndicatorConfig {
	if s == "" {
		return []indicator.IndicatorConfig{
			{Type: "SMA", Period: 20},
			{Type: "SMA", Period: 50},
			{Type: "EMA", Period: 9},
			{Type: "EMA", Period: 21},
			{Type: "RSI", Period: 14},
		}
	}
	var configs []indicator.IndicatorConfig
	for _, part := range strings.Split(s, ",") {
		tokens := strings.SplitN(strings.TrimSpace(part), ":", 2)
		if len(tokens) != 2 {
			continue
		}
		period, err := strconv.Atoi(strings.TrimSpace(tokens[1]))
		if err != nil || period <= 0 {
			continue
		}
		configs = append(configs, indicator.IndicatorConfig{
			Type:   strings.ToUpper(strings.TrimSpace(tokens[0])),
			Period: period,
		})
	}
	return configs
}
