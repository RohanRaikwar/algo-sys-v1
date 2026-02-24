package redis

import (
	"context"
	"fmt"
	"log"
	"time"
	"unsafe"

	"trading-systemv1/internal/model"

	goredis "github.com/go-redis/redis/v8"
)

const (
	// Stream trimming: ~3h of 1s candles + buffer
	stream1sMaxLen   = 12000
	defaultLatestTTL = 30 * time.Minute
)

// WriterConfig configures the Redis writer.
type WriterConfig struct {
	Addr     string // Redis address, e.g. "localhost:6379"
	Password string
	DB       int
}

// Writer writes candles, TF candles, and indicator results to Redis.
type Writer struct {
	client *goredis.Client
}

// Client returns the underlying Redis client for health checks.
func (w *Writer) Client() *goredis.Client { return w.client }

// New creates a new Redis Writer and pings the server.
func New(cfg WriterConfig) (*Writer, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	log.Printf("[redis] connected to %s", cfg.Addr)
	return &Writer{client: client}, nil
}

// Run reads 1s candles from candleCh and writes them to Redis.
// Blocks until ctx is cancelled or candleCh is closed.
func (w *Writer) Run(ctx context.Context, candleCh <-chan model.Candle) {
	for {
		select {
		case <-ctx.Done():
			return
		case candle, ok := <-candleCh:
			if !ok {
				return
			}
			w.writeCandle(ctx, candle)
		}
	}
}

// RunTFCandles reads TF candles and writes them to Redis Streams.
// Blocks until ctx is cancelled or channel is closed.
func (w *Writer) RunTFCandles(ctx context.Context, tfCandleCh <-chan model.TFCandle) {
	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-tfCandleCh:
			if !ok {
				return
			}
			w.writeTFCandle(ctx, tfc)
		}
	}
}

// RunFormingTFCandles publishes forming TF candles via PubSub ONLY (no XADD).
// Used for live/streaming indicator peek updates every second.
// OPTIMIZED: uses string concat instead of fmt.Sprintf.
func (w *Writer) RunFormingTFCandles(ctx context.Context, ch <-chan model.TFCandle) {
	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-ch:
			if !ok {
				return
			}
			jsonBytes := tfc.JSON()
			jsonData := *(*string)(unsafe.Pointer(&jsonBytes))
			pubsubCh := "pub:candle:" + itoa(tfc.TF) + "s:" + tfc.Exchange + ":" + tfc.Token
			w.client.Publish(ctx, pubsubCh, jsonData)
		}
	}
}

// RunIndicators reads indicator results and writes them to Redis Streams.
// Blocks until ctx is cancelled or channel is closed.
func (w *Writer) RunIndicators(ctx context.Context, indCh <-chan model.IndicatorResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case ind, ok := <-indCh:
			if !ok {
				return
			}
			w.writeIndicator(ctx, ind)
		}
	}
}

// WriteIndicatorBatch writes multiple indicator results in a single Redis pipeline.
// This batches XADD + SET + PUBLISH for all results into one network roundtrip.
// Optimized: uses pre-built channel names, []byte→string zero-copy, no fmt.Sprintf.
func (w *Writer) WriteIndicatorBatch(ctx context.Context, results []model.IndicatorResult) {
	if len(results) == 0 {
		return
	}

	pipe := w.client.Pipeline()
	for i := range results {
		ind := &results[i]
		if !ind.Ready && !ind.Live {
			continue
		}

		jsonBytes := ind.JSON()
		// Zero-copy []byte→string (safe: jsonBytes is not mutated after this)
		jsonData := *(*string)(unsafe.Pointer(&jsonBytes))
		pubsubCh := ind.PubSubChannel()

		if ind.Live {
			pipe.Publish(ctx, pubsubCh, jsonData)
			continue
		}

		// Confirmed: XADD + SET + PUBLISH
		streamKey := ind.StreamKey()
		maxLen := int64(10800/ind.TF) + 100
		if maxLen < 200 {
			maxLen = 200
		}
		pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: streamKey,
			MaxLen: maxLen,
			Approx: true,
			Values: map[string]interface{}{"data": jsonData},
		})
		latestKey := "ind:" + ind.Name + ":" + itoa(ind.TF) + "s:latest:" + ind.Exchange + ":" + ind.Token
		pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)
		pipe.Publish(ctx, pubsubCh, jsonData)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] indicator batch pipeline error (%d results): %v", len(results), err)
	}
}

// PublishFormingBatch publishes multiple forming TF candles in a single pipeline.
func (w *Writer) PublishFormingBatch(ctx context.Context, candles []model.TFCandle) {
	if len(candles) == 0 {
		return
	}

	pipe := w.client.Pipeline()
	for _, tfc := range candles {
		jsonData := string(tfc.JSON())
		pubsubCh := fmt.Sprintf("pub:candle:%ds:%s:%s", tfc.TF, tfc.Exchange, tfc.Token)
		pipe.Publish(ctx, pubsubCh, jsonData)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] forming batch pipeline error (%d candles): %v", len(candles), err)
	}
}

// LoadTFRegistry reads the tf:enabled set from Redis.
// Returns empty slice if key doesn't exist.
func (w *Writer) LoadTFRegistry(ctx context.Context) ([]int, error) {
	members, err := w.client.SMembers(ctx, "tf:enabled").Result()
	if err != nil {
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("redis SMEMBERS tf:enabled: %w", err)
	}

	tfs := make([]int, 0, len(members))
	for _, m := range members {
		n := 0
		for _, c := range m {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > 0 {
			tfs = append(tfs, n)
		}
	}
	return tfs, nil
}

// writeCandle performs pipelined writes for a 1s candle.
func (w *Writer) writeCandle(ctx context.Context, candle model.Candle) {
	latestKey := fmt.Sprintf("candle:1s:latest:%s:%s", candle.Exchange, candle.Token)
	streamKey := fmt.Sprintf("candle:1s:%s:%s", candle.Exchange, candle.Token)
	pubsubCh := fmt.Sprintf("pub:candle:1s:%s:%s", candle.Exchange, candle.Token)
	jsonData := string(candle.JSON())

	pipe := w.client.Pipeline()

	// SET latest candle with TTL
	pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)

	// XADD to stream with auto-trimming (~3h window)
	pipe.XAdd(ctx, &goredis.XAddArgs{
		Stream: streamKey,
		MaxLen: stream1sMaxLen,
		Approx: true,
		Values: map[string]interface{}{
			"data": jsonData,
		},
	})

	// PUBLISH to pubsub channel
	pipe.Publish(ctx, pubsubCh, jsonData)

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] pipeline error for %s: %v", candle.Key(), err)
	}
}

// writeTFCandle publishes a TF candle to its Redis Stream.
func (w *Writer) writeTFCandle(ctx context.Context, tfc model.TFCandle) {
	streamKey := tfc.StreamKey()
	// Proportional MAXLEN: 3h of TF candles = 10800/TF + buffer
	maxLen := int64(10800/tfc.TF) + 100
	if maxLen < 200 {
		maxLen = 200
	}

	jsonData := string(tfc.JSON())

	pipe := w.client.Pipeline()

	// XADD to TF stream
	pipe.XAdd(ctx, &goredis.XAddArgs{
		Stream: streamKey,
		MaxLen: maxLen,
		Approx: true,
		Values: map[string]interface{}{
			"data": jsonData,
		},
	})

	// SET latest TF candle
	latestKey := fmt.Sprintf("candle:%ds:latest:%s:%s", tfc.TF, tfc.Exchange, tfc.Token)
	pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)

	// PUBLISH for real-time subscribers
	pubsubCh := fmt.Sprintf("pub:candle:%ds:%s:%s", tfc.TF, tfc.Exchange, tfc.Token)
	pipe.Publish(ctx, pubsubCh, jsonData)

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] TF candle pipeline error for %s: %v", tfc.Key(), err)
	}
}

// writeIndicator publishes an indicator result to its Redis Stream.
func (w *Writer) writeIndicator(ctx context.Context, ind model.IndicatorResult) {
	if !ind.Ready && !ind.Live {
		return // skip not-ready confirmed indicators
	}

	jsonBytes := ind.JSON()
	jsonData := *(*string)(unsafe.Pointer(&jsonBytes))
	pubsubCh := ind.PubSubChannel()

	if ind.Live {
		// Live/preview results: PubSub only (no XADD streams, no SET latest)
		w.client.Publish(ctx, pubsubCh, jsonData)
		return
	}

	// Confirmed results: full pipeline (XADD + SET + PUBLISH)
	streamKey := ind.StreamKey()
	pipe := w.client.Pipeline()

	// XADD to indicator stream (keep ~3h worth)
	maxLen := int64(10800/ind.TF) + 100
	if maxLen < 200 {
		maxLen = 200
	}
	pipe.XAdd(ctx, &goredis.XAddArgs{
		Stream: streamKey,
		MaxLen: maxLen,
		Approx: true,
		Values: map[string]interface{}{
			"data": jsonData,
		},
	})

	// SET latest indicator value
	latestKey := "ind:" + ind.Name + ":" + itoa(ind.TF) + "s:latest:" + ind.Exchange + ":" + ind.Token
	pipe.Set(ctx, latestKey, jsonData, defaultLatestTTL)

	// PUBLISH for real-time subscribers (dashboard)
	pipe.Publish(ctx, pubsubCh, jsonData)

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("[redis] indicator pipeline error for %s: %v", ind.Name, err)
	}
}

// Close closes the Redis client.
func (w *Writer) Close() error {
	return w.client.Close()
}
