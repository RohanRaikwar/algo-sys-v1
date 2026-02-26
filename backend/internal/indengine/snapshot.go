package indengine

import (
	"context"
	"log"
	"strconv"
	"time"

	"trading-systemv1/internal/indicator"
)

// snapshotLoop periodically saves engine state to Redis and SQLite.
func (svc *Service) snapshotLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(svc.cfg.SnapshotIntervalS) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, err := indicator.SnapshotEngine(svc.engine, svc.getLastStreamID(ctx))
			if err != nil {
				log.Printf("[indengine] snapshot error: %v", err)
				continue
			}

			// Save to Redis
			if err := svc.redisReader.WriteSnapshot(ctx, svc.cfg.SnapshotKey, snap); err != nil {
				log.Printf("[indengine] redis snapshot write error: %v", err)
			}

			// Save to SQLite
			if svc.sqlWriter != nil {
				if err := svc.sqlWriter.SaveSnapshot(snap); err != nil {
					log.Printf("[indengine] sqlite snapshot write error: %v", err)
				}
			}

			log.Printf("[indengine] ✅ checkpoint saved (%d tokens)", len(snap.Tokens))
		}
	}
}

// getLastStreamID returns a time-based stream ID marker for snapshots.
func (svc *Service) getLastStreamID(ctx context.Context) string {
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + "-0"
}
