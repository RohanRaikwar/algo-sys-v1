package model

import (
	"context"
	"time"
)

// ── Storage Port Interfaces ──
// These interfaces decouple business logic from concrete storage implementations
// (Redis, SQLite). Each implementation satisfies one or more of these interfaces.

// CandleWriter writes raw 1s candles and TF candles.
type CandleWriter interface {
	// Run reads candles from candleCh and writes them.
	// Blocks until ctx is cancelled or candleCh is closed.
	Run(ctx context.Context, candleCh <-chan Candle)

	// RunTFCandles reads TF candles from a channel and writes them.
	// Blocks until ctx is cancelled or channel is closed.
	RunTFCandles(ctx context.Context, tfCandleCh <-chan TFCandle)

	// Close releases underlying resources.
	Close() error
}

// CandleReader reads TF candles for backfill and replay.
type CandleReader interface {
	// ReadTFCandles reads candles for a specific instrument and TF.
	ReadTFCandles(exchange, token string, tf int, afterTS int64) ([]TFCandle, error)

	// ReadAllTFCandles reads all TF candles for a given timeframe.
	ReadAllTFCandles(tf int, afterTS int64) ([]TFCandle, error)

	// Close releases underlying resources.
	Close() error
}

// IndicatorWriter writes indicator results and engine snapshots.
type IndicatorWriter interface {
	// WriteIndicatorBatch writes multiple indicator results in a single batch.
	WriteIndicatorBatch(ctx context.Context, results []IndicatorResult)

	// Close releases underlying resources.
	Close() error
}

// IndicatorReader reads indicator data and engine snapshots.
type IndicatorReader interface {
	// Close releases underlying resources.
	Close() error
}

// SnapshotStore reads and writes indicator engine snapshots as raw JSON.
// Using []byte avoids a model→indicator→model import cycle.
type SnapshotStore interface {
	// SaveSnapshotJSON persists a JSON-encoded engine snapshot.
	SaveSnapshotJSON(data []byte) error

	// ReadLatestSnapshotJSON loads the most recent snapshot as raw JSON.
	// Returns nil, nil if no snapshot exists.
	ReadLatestSnapshotJSON() ([]byte, error)
}

// StreamConsumer consumes TF candles from a stream (e.g. Redis Streams).
type StreamConsumer interface {
	// ConsumeTFCandles reads TF candles via consumer groups.
	// Blocks until ctx is cancelled.
	ConsumeTFCandles(ctx context.Context, streams []string, out chan<- TFCandle) error

	// RecoverPending processes any unACKed messages from a previous crash.
	RecoverPending(ctx context.Context, streams []string, out chan<- TFCandle) error

	// EnsureConsumerGroup creates consumer groups on streams.
	EnsureConsumerGroup(ctx context.Context, streams []string) error

	// ReplayFromID reads all messages from a stream starting at a given ID.
	ReplayFromID(ctx context.Context, stream, startID string, out chan<- TFCandle) (string, error)

	// DiscoverTFStreams finds streams matching known TFs and tokens.
	DiscoverTFStreams(ctx context.Context, tfs []int, tokens []string) []string

	// StartPELReclaimer runs periodic reclamation of stale PEL entries.
	StartPELReclaimer(ctx context.Context, streams []string, group, consumer string,
		interval time.Duration, minIdleMs int64, outCh chan<- TFCandle, onReclaim func(count int))

	// Close releases underlying resources.
	Close() error
}
