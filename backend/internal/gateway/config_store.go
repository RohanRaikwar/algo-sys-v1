package gateway

import (
	"context"
	"encoding/json"
	"log"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

const activeConfigRedisKey = "gateway:active_config"

// ConfigStore manages the active indicator configuration and broadcasts changes.
type ConfigStore struct {
	hub *Hub
	rdb *goredis.Client
}

// NewConfigStore creates a ConfigStore backed by the given Hub.
func NewConfigStore(hub *Hub, rdb *goredis.Client) *ConfigStore {
	return &ConfigStore{hub: hub, rdb: rdb}
}

// Load restores the active config from Redis (if available).
// Called once during gateway startup. Returns true if config was restored.
func (cs *ConfigStore) Load(ctx context.Context) bool {
	data, err := cs.rdb.Get(ctx, activeConfigRedisKey).Result()
	if err != nil {
		return false
	}
	var cfg ActiveConfig
	if json.Unmarshal([]byte(data), &cfg) != nil {
		return false
	}
	cs.hub.mu.Lock()
	cs.hub.activeConfig = cfg
	cs.hub.mu.Unlock()
	log.Printf("[config_store] restored active config from Redis: %d entries", len(cfg.Entries))
	return true
}

// Get returns the current active indicator configuration.
func (cs *ConfigStore) Get() ActiveConfig {
	cs.hub.mu.RLock()
	defer cs.hub.mu.RUnlock()
	return cs.hub.activeConfig
}

// Set updates the active config, persists to Redis, and broadcasts to all connected clients.
func (cs *ConfigStore) Set(cfg ActiveConfig) {
	cs.hub.mu.Lock()
	cs.hub.activeConfig = cfg
	cs.hub.mu.Unlock()

	// Persist to Redis (fire-and-forget — frontend is still source of truth)
	if cs.rdb != nil {
		data, err := json.Marshal(cfg)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := cs.rdb.Set(ctx, activeConfigRedisKey, data, 0).Err(); err != nil {
				log.Printf("[config_store] WARNING: failed to persist active config to Redis: %v", err)
			}
		}
	}

	envelope, _ := json.Marshal(map[string]interface{}{
		"type":    "config_update",
		"entries": cfg.Entries,
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
	})

	cs.hub.mu.RLock()
	defer cs.hub.mu.RUnlock()
	for client := range cs.hub.clients {
		select {
		case client.send <- envelope:
		default:
		}
	}
}
