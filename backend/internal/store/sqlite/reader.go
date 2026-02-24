package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"

	_ "github.com/mattn/go-sqlite3"
)

// Reader provides read-only access to SQLite for backfill and snapshot restore.
type Reader struct {
	db *sql.DB
}

// NewReader opens a SQLite connection for reading.
func NewReader(dbPath string) (*Reader, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("sqlite open reader: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)

	log.Printf("[sqlite-reader] opened %s", dbPath)
	return &Reader{db: db}, nil
}

// ReadTFCandles reads TF candles from the candles_tf table for a given exchange:token and TF.
// Results are ordered by timestamp ascending for correct replay order.
func (r *Reader) ReadTFCandles(exchange, token string, tf int, afterTS int64) ([]model.TFCandle, error) {
	rows, err := r.db.Query(`
		SELECT token, exchange, tf, ts, open, high, low, close, volume, count
		FROM candles_tf
		WHERE exchange = ? AND token = ? AND tf = ? AND ts > ?
		ORDER BY ts ASC
	`, exchange, token, tf, afterTS)
	if err != nil {
		return nil, fmt.Errorf("sqlite query candles_tf: %w", err)
	}
	defer rows.Close()

	var candles []model.TFCandle
	for rows.Next() {
		var c model.TFCandle
		var tsUnix int64
		if err := rows.Scan(&c.Token, &c.Exchange, &c.TF, &tsUnix, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Count); err != nil {
			return nil, fmt.Errorf("sqlite scan candles_tf: %w", err)
		}
		c.TS = time.Unix(tsUnix, 0).UTC()
		candles = append(candles, c)
	}
	return candles, rows.Err()
}

// ReadAllTFCandles reads all TF candles from SQLite for backfill, ordered by timestamp.
func (r *Reader) ReadAllTFCandles(tf int, afterTS int64) ([]model.TFCandle, error) {
	rows, err := r.db.Query(`
		SELECT token, exchange, tf, ts, open, high, low, close, volume, count
		FROM candles_tf
		WHERE tf = ? AND ts > ?
		ORDER BY ts ASC
	`, tf, afterTS)
	if err != nil {
		return nil, fmt.Errorf("sqlite query all candles_tf: %w", err)
	}
	defer rows.Close()

	var candles []model.TFCandle
	for rows.Next() {
		var c model.TFCandle
		var tsUnix int64
		if err := rows.Scan(&c.Token, &c.Exchange, &c.TF, &tsUnix, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Count); err != nil {
			return nil, fmt.Errorf("sqlite scan candles_tf: %w", err)
		}
		c.TS = time.Unix(tsUnix, 0).UTC()
		candles = append(candles, c)
	}
	return candles, rows.Err()
}

// ReadLatestSnapshot loads the most recent indicator engine snapshot from SQLite.
func (r *Reader) ReadLatestSnapshot() (*indicator.EngineSnapshot, error) {
	var data string
	err := r.db.QueryRow(`
		SELECT data FROM indicator_snapshots
		ORDER BY created_at DESC
		LIMIT 1
	`).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // no snapshot
		}
		return nil, fmt.Errorf("sqlite read snapshot: %w", err)
	}

	var snap indicator.EngineSnapshot
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	return &snap, nil
}

// Close closes the reader.
func (r *Reader) Close() error {
	return r.db.Close()
}
