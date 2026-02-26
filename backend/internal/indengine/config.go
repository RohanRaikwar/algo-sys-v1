package indengine

import (
	"log"
	"os"
	"strconv"
	"strings"

	"trading-systemv1/internal/indicator"
)

// Config holds all env-parsed configuration for the indicator engine service.
type Config struct {
	RedisAddr          string
	RedisPassword      string
	SQLitePath         string
	ConsumerGroup      string
	ConsumerName       string
	EnabledTFs         []int
	SnapshotIntervalS  int
	SubscribeTokenKeys []string // "exchange:token" keys
	SnapshotKey        string
	HTTPAddr           string
	PELIntervalS       int
	PELMinIdleMs       int64
	IndicatorConfigs   []indicator.TFIndicatorConfig
}

// LoadConfig reads all environment variables and returns a Config.
func LoadConfig() Config {
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
	indConfigs := BuildIndicatorConfigs(enabledTFs)
	tokenKeys := parseTokenKeys(subscribeTokens)

	return Config{
		RedisAddr:          redisAddr,
		RedisPassword:      redisPassword,
		SQLitePath:         sqlitePath,
		ConsumerGroup:      consumerGroup,
		ConsumerName:       consumerName,
		EnabledTFs:         enabledTFs,
		SnapshotIntervalS:  snapshotInterval,
		SubscribeTokenKeys: tokenKeys,
		SnapshotKey:        snapshotKey,
		HTTPAddr:           httpAddr,
		PELIntervalS:       pelInterval,
		PELMinIdleMs:       pelMinIdle,
		IndicatorConfigs:   indConfigs,
	}
}

// BuildIndicatorConfigs creates indicator configurations per TF from the
// INDICATOR_CONFIGS env var.  Format: "TYPE:PERIOD,TYPE:PERIOD,..."
// Example: "SMA:9,SMA:20,SMA:50,SMA:200,EMA:9,EMA:21,RSI:14"
// If the env var is empty, sensible defaults are used.
func BuildIndicatorConfigs(tfs []int) []indicator.TFIndicatorConfig {
	indSpecs := ParseIndicatorSpecs(getEnv("INDICATOR_CONFIGS", ""))
	configs := make([]indicator.TFIndicatorConfig, len(tfs))
	for i, tf := range tfs {
		configs[i] = indicator.TFIndicatorConfig{
			TF:         tf,
			Indicators: indSpecs,
		}
	}
	return configs
}

// ParseIndicatorSpecs parses "TYPE:PERIOD,..." into []IndicatorConfig.
// Returns defaults if input is empty.
func ParseIndicatorSpecs(s string) []indicator.IndicatorConfig {
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
		return ParseIndicatorSpecs("")
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
