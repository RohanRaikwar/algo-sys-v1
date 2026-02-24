package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Angel One credentials
	AngelAPIKey     string
	AngelClientCode string
	AngelPassword   string
	AngelTOTPSecret string

	// Infrastructure
	RedisAddr     string
	RedisPassword string
	SQLitePath    string
	MetricsAddr   string

	// Subscription
	SubscribeTokens string

	// Dynamic Timeframes (comma-separated seconds, e.g. "60,300,900")
	EnabledTFs string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		AngelAPIKey:     mustEnv("ANGEL_API_KEY"),
		AngelClientCode: mustEnv("ANGEL_CLIENT_CODE"),
		AngelPassword:   mustEnv("ANGEL_PASSWORD"),
		AngelTOTPSecret: mustEnv("ANGEL_TOTP_SECRET"),

		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		SQLitePath:    getEnv("SQLITE_PATH", "data/candles.db"),
		MetricsAddr:   getEnv("METRICS_ADDR", ":9090"),

		// Default: NIFTY 50 on NSE_CM
		SubscribeTokens: getEnv("SUBSCRIBE_TOKENS", "1:99926000"),

		// Default TFs: 1m, 5m, 15m
		EnabledTFs: getEnv("ENABLED_TFS", "60,120,180,300"),
	}
}

// ParseTFs parses the EnabledTFs string into a sorted slice of timeframe durations in seconds.
func (c *Config) ParseTFs() []int {
	parts := strings.Split(c.EnabledTFs, ",")
	tfs := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			log.Printf("[config] skipping invalid TF value: %q", p)
			continue
		}
		tfs = append(tfs, n)
	}
	return tfs
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[config] required env var %s not set", key)
	}
	return v
}

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
