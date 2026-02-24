package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"

	goredis "github.com/go-redis/redis/v8"
)

// ReaderConfig configures the Redis reader.
type ReaderConfig struct {
	Addr          string
	Password      string
	DB            int
	ConsumerGroup string // consumer group name, e.g. "indengine"
	ConsumerName  string // unique consumer name, e.g. hostname
}

// Reader reads TF candles from Redis Streams via Consumer Groups
// and manages indicator snapshots in Redis Hashes.
type Reader struct {
	client        *goredis.Client
	consumerGroup string
	consumerName  string
}

// NewReader creates a new Redis Reader and pings the server.
func NewReader(cfg ReaderConfig) (*Reader, error) {
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

	group := cfg.ConsumerGroup
	if group == "" {
		group = "indengine"
	}
	consumer := cfg.ConsumerName
	if consumer == "" {
		consumer = "worker-1"
	}

	log.Printf("[redis-reader] connected to %s (group=%s, consumer=%s)", cfg.Addr, group, consumer)
	return &Reader{
		client:        client,
		consumerGroup: group,
		consumerName:  consumer,
	}, nil
}

// EnsureConsumerGroup creates a consumer group on the given streams if it doesn't exist.
// Uses "$" as start ID (only new messages) for fresh groups.
func (r *Reader) EnsureConsumerGroup(ctx context.Context, streams []string) error {
	for _, stream := range streams {
		err := r.client.XGroupCreateMkStream(ctx, stream, r.consumerGroup, "$").Err()
		if err != nil {
			// Ignore "BUSYGROUP" error — group already exists
			if err.Error() != "BUSYGROUP Consumer Group name already exists" {
				return fmt.Errorf("xgroup create %s: %w", stream, err)
			}
		}
	}
	return nil
}

// EnsureConsumerGroupFrom creates a consumer group starting from a specific stream ID.
// Used for replay after snapshot restore.
func (r *Reader) EnsureConsumerGroupFrom(ctx context.Context, stream, startID string) error {
	// Try to create the group from the specified ID
	err := r.client.XGroupCreateMkStream(ctx, stream, r.consumerGroup, startID).Err()
	if err != nil {
		if err.Error() == "BUSYGROUP Consumer Group name already exists" {
			// Group exists — set the last delivered ID
			return r.client.XGroupSetID(ctx, stream, r.consumerGroup, startID).Err()
		}
		return fmt.Errorf("xgroup create from %s at %s: %w", stream, startID, err)
	}
	return nil
}

// ConsumeTFCandles reads TF candles from Redis Streams using consumer groups.
// Blocks on XREADGROUP and sends parsed candles to the output channel.
// Returns when ctx is cancelled.
func (r *Reader) ConsumeTFCandles(ctx context.Context, streams []string, out chan<- model.TFCandle) error {
	// Build stream args: [stream1, stream2, ..., ">", ">", ...]
	args := make([]string, len(streams)*2)
	for i, s := range streams {
		args[i] = s
		args[len(streams)+i] = ">"
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		results, err := r.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    r.consumerGroup,
			Consumer: r.consumerName,
			Streams:  args,
			Count:    100,
			Block:    2 * time.Second,
		}).Result()
		if err != nil {
			if err == goredis.Nil || ctx.Err() != nil {
				continue
			}
			log.Printf("[redis-reader] xreadgroup error: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		for _, stream := range results {
			for _, msg := range stream.Messages {
				data, ok := msg.Values["data"].(string)
				if !ok {
					continue
				}

				var tfc model.TFCandle
				if err := json.Unmarshal([]byte(data), &tfc); err != nil {
					log.Printf("[redis-reader] unmarshal TFCandle error: %v", err)
					// ACK even on bad message to avoid poison pill
					r.client.XAck(ctx, stream.Stream, r.consumerGroup, msg.ID)
					continue
				}

				select {
				case out <- tfc:
				case <-ctx.Done():
					return ctx.Err()
				}

				// ACK after successful processing
				r.client.XAck(ctx, stream.Stream, r.consumerGroup, msg.ID)
			}
		}
	}
}

// RecoverPending processes any pending (unACKed) messages from a previous crash.
// This ensures at-least-once delivery semantics.
func (r *Reader) RecoverPending(ctx context.Context, streams []string, out chan<- model.TFCandle) error {
	for _, stream := range streams {
		for {
			pending, err := r.client.XPendingExt(ctx, &goredis.XPendingExtArgs{
				Stream: stream,
				Group:  r.consumerGroup,
				Start:  "-",
				End:    "+",
				Count:  100,
			}).Result()
			if err != nil || len(pending) == 0 {
				break
			}

			// Claim and process pending messages
			ids := make([]string, len(pending))
			for i, p := range pending {
				ids[i] = p.ID
			}

			claimed, err := r.client.XClaim(ctx, &goredis.XClaimArgs{
				Stream:   stream,
				Group:    r.consumerGroup,
				Consumer: r.consumerName,
				MinIdle:  0,
				Messages: ids,
			}).Result()
			if err != nil {
				log.Printf("[redis-reader] xclaim error on %s: %v", stream, err)
				break
			}

			for _, msg := range claimed {
				data, ok := msg.Values["data"].(string)
				if !ok {
					r.client.XAck(ctx, stream, r.consumerGroup, msg.ID)
					continue
				}

				var tfc model.TFCandle
				if err := json.Unmarshal([]byte(data), &tfc); err != nil {
					r.client.XAck(ctx, stream, r.consumerGroup, msg.ID)
					continue
				}

				select {
				case out <- tfc:
				case <-ctx.Done():
					return ctx.Err()
				}

				r.client.XAck(ctx, stream, r.consumerGroup, msg.ID)
			}

			if len(claimed) < len(ids) {
				break
			}
		}
	}
	return nil
}

// ReclaimStaleMessages finds PEL entries idle > minIdleMs across all consumers
// in the group and XCLAIMs them for this consumer. Returns reclaimed messages.
func (r *Reader) ReclaimStaleMessages(ctx context.Context, stream, group, consumer string, minIdleMs int64, batchSize int64) ([]goredis.XMessage, error) {
	// Get pending entries across ALL consumers (not just ours)
	pending, err := r.client.XPendingExt(ctx, &goredis.XPendingExtArgs{
		Stream: stream,
		Group:  group,
		Start:  "-",
		End:    "+",
		Count:  batchSize,
		Idle:   time.Duration(minIdleMs) * time.Millisecond,
	}).Result()
	if err != nil || len(pending) == 0 {
		return nil, err
	}

	// Filter to entries NOT owned by us (steal from dead consumers)
	var staleIDs []string
	for _, p := range pending {
		if p.Consumer != consumer {
			staleIDs = append(staleIDs, p.ID)
		}
	}
	if len(staleIDs) == 0 {
		return nil, nil
	}

	// XCLAIM with MinIdle to atomically steal stale entries
	claimed, err := r.client.XClaim(ctx, &goredis.XClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  time.Duration(minIdleMs) * time.Millisecond,
		Messages: staleIDs,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("xclaim %s: %w", stream, err)
	}

	log.Printf("[redis-reader] reclaimed %d stale PEL entries from %s", len(claimed), stream)
	return claimed, nil
}

// StartPELReclaimer runs a periodic background loop that scans for stale PEL entries
// across all streams and reclaims them via XCLAIM. Reclaimed messages are parsed and
// sent to outCh for reprocessing. Runs until ctx is cancelled.
func (r *Reader) StartPELReclaimer(ctx context.Context, streams []string, group, consumer string, interval time.Duration, minIdleMs int64, outCh chan<- model.TFCandle, onReclaim func(count int)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			totalReclaimed := 0
			for _, stream := range streams {
				claimed, err := r.ReclaimStaleMessages(ctx, stream, group, consumer, minIdleMs, 50)
				if err != nil {
					log.Printf("[redis-reader] PEL reclaim error on %s: %v", stream, err)
					continue
				}
				for _, msg := range claimed {
					data, ok := msg.Values["data"].(string)
					if !ok {
						r.client.XAck(ctx, stream, group, msg.ID)
						continue
					}
					var tfc model.TFCandle
					if err := json.Unmarshal([]byte(data), &tfc); err != nil {
						r.client.XAck(ctx, stream, group, msg.ID)
						continue
					}
					select {
					case outCh <- tfc:
					case <-ctx.Done():
						return
					}
					r.client.XAck(ctx, stream, group, msg.ID)
					totalReclaimed++
				}
			}
			if totalReclaimed > 0 && onReclaim != nil {
				onReclaim(totalReclaimed)
			}
		}
	}
}

// ReadSnapshot loads the latest indicator engine snapshot from Redis.
func (r *Reader) ReadSnapshot(ctx context.Context, snapshotKey string) (*indicator.EngineSnapshot, error) {
	data, err := r.client.Get(ctx, snapshotKey).Result()
	if err != nil {
		if err == goredis.Nil {
			return nil, nil // no snapshot found
		}
		return nil, fmt.Errorf("redis get snapshot %s: %w", snapshotKey, err)
	}

	var snap indicator.EngineSnapshot
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	return &snap, nil
}

// WriteSnapshot saves an indicator engine snapshot to Redis.
func (r *Reader) WriteSnapshot(ctx context.Context, snapshotKey string, snap *indicator.EngineSnapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	// Store with TTL of 24h (snapshots are also in SQLite for durability)
	return r.client.Set(ctx, snapshotKey, string(data), 24*time.Hour).Err()
}

// ReplayFromID reads all messages from a stream starting from a given ID.
// Used during restore to replay candles since the last snapshot.
func (r *Reader) ReplayFromID(ctx context.Context, stream, startID string, out chan<- model.TFCandle) (string, error) {
	lastID := startID
	for {
		results, err := r.client.XRange(ctx, stream, "("+lastID, "+").Result()
		if err != nil {
			return lastID, fmt.Errorf("xrange %s from %s: %w", stream, lastID, err)
		}

		if len(results) == 0 {
			break
		}

		for _, msg := range results {
			data, ok := msg.Values["data"].(string)
			if !ok {
				lastID = msg.ID
				continue
			}

			var tfc model.TFCandle
			if err := json.Unmarshal([]byte(data), &tfc); err != nil {
				lastID = msg.ID
				continue
			}

			select {
			case out <- tfc:
			case <-ctx.Done():
				return lastID, ctx.Err()
			}

			lastID = msg.ID
		}

		// If we got fewer than expected, we've reached the end
		if len(results) < 1000 {
			break
		}
	}
	return lastID, nil
}

// DiscoverTFStreams finds all TF candle streams matching the pattern for known tokens.
func (r *Reader) DiscoverTFStreams(ctx context.Context, tfs []int, tokens []string) []string {
	var streams []string
	for _, tf := range tfs {
		for _, tok := range tokens {
			stream := fmt.Sprintf("candle:%ds:%s", tf, tok)
			// Verify stream exists
			exists, err := r.client.Exists(ctx, stream).Result()
			if err == nil && exists > 0 {
				streams = append(streams, stream)
			}
		}
	}
	return streams
}

// SubscribeFormingCandles subscribes to pub:candle:* PubSub pattern and feeds
// forming TF candles into the output channel. Blocks until ctx is cancelled.
func (r *Reader) SubscribeFormingCandles(ctx context.Context, out chan<- model.TFCandle) error {
	pubsub := r.client.PSubscribe(ctx, "pub:candle:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var tfc model.TFCandle
			if err := json.Unmarshal([]byte(msg.Payload), &tfc); err != nil {
				continue
			}
			if !tfc.Forming {
				continue // only forward forming candles (completed ones come via XREADGROUP)
			}
			select {
			case out <- tfc:
			default:
			}
		}
	}
}

// Subscribe1sForPeek subscribes to pub:candle:1s:* PubSub and converts each
// 1s candle into a forming TFCandle for every TF in tfs. A local mini-aggregator
// tracks in-progress buckets and emits a Forming=true snapshot on every tick.
// This enables live indicator ProcessPeek without depending on the mdengine
// publishing forming TF candles.
func (r *Reader) Subscribe1sForPeek(ctx context.Context, tfs []int, out chan<- model.TFCandle) error {
	pubsub := r.client.PSubscribe(ctx, "pub:candle:1s:*")
	defer pubsub.Close()

	// Local forming-candle state: key = "tf:exchange:token", value = forming candle
	type formingState struct {
		bucket int64
		candle model.TFCandle
	}
	state := map[string]*formingState{}

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var c model.Candle
			if err := json.Unmarshal([]byte(msg.Payload), &c); err != nil {
				// Also try parsing as TFCandle (in case format changes)
				var tfc model.TFCandle
				if err2 := json.Unmarshal([]byte(msg.Payload), &tfc); err2 == nil && tfc.TF == 1 {
					c = model.Candle{
						Token: tfc.Token, Exchange: tfc.Exchange,
						TS: tfc.TS, Open: tfc.Open, High: tfc.High,
						Low: tfc.Low, Close: tfc.Close, Volume: tfc.Volume,
					}
				} else {
					continue
				}
			}

			ts := c.TS.Unix()
			for _, tf := range tfs {
				tf64 := int64(tf)
				bucket := ts - (ts % tf64)
				key := fmt.Sprintf("%d:%s:%s", tf, c.Exchange, c.Token)

				st, exists := state[key]
				if exists && bucket > st.bucket {
					// New bucket — reset (completed candle handled by stream consumer)
					exists = false
				}

				if !exists {
					state[key] = &formingState{
						bucket: bucket,
						candle: model.TFCandle{
							Token: c.Token, Exchange: c.Exchange,
							TF: tf, TS: c.TS,
							Open: c.Open, High: c.High,
							Low: c.Low, Close: c.Close,
							Volume: c.Volume, Count: 1,
							Forming: true,
						},
					}
					st = state[key]
				} else {
					// Same bucket — merge OHLCV
					fc := &st.candle
					if c.High > fc.High {
						fc.High = c.High
					}
					if c.Low < fc.Low {
						fc.Low = c.Low
					}
					fc.Close = c.Close
					fc.Volume += c.Volume
					fc.Count++
				}

				// Emit forming snapshot
				snap := st.candle
				select {
				case out <- snap:
				default:
				}
			}
		}
	}
}

// SubscribeChannel subscribes to a Redis Pub/Sub channel.
// Returns the PubSub handle so the caller can listen on .Channel().
func (r *Reader) SubscribeChannel(ctx context.Context, channel string) *goredis.PubSub {
	pubsub := r.client.Subscribe(ctx, channel)
	// Wait for confirmation
	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Printf("[redis-reader] subscribe to %s failed: %v", channel, err)
		pubsub.Close()
		return nil
	}
	return pubsub
}

// Publish publishes a message to a Redis Pub/Sub channel.
func (r *Reader) Publish(ctx context.Context, channel, message string) error {
	return r.client.Publish(ctx, channel, message).Err()
}

// Close closes the Redis client.
func (r *Reader) Close() error {
	return r.client.Close()
}
