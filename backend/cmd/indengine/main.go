package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"trading-systemv1/internal/indengine"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	cfg := indengine.LoadConfig()
	log.Printf("[indengine] enabled TFs: %v, snapshot interval: %ds", cfg.EnabledTFs, cfg.SnapshotIntervalS)

	svc, err := indengine.New(cfg)
	if err != nil {
		log.Fatalf("[indengine] init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := svc.Run(ctx); err != nil {
		log.Fatalf("[indengine] fatal: %v", err)
	}
}
