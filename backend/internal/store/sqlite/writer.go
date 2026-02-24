package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"

	_ "github.com/mattn/go-sqlite3"
)

const (
	defaultBatchSize  = 100
	defaultFlushDelay = 200 * time.Millisecond
)

// WriterConfig configures the SQLite writer.
type WriterConfig struct {
	DBPath string // path to SQLite database file, e.g. "data/candles.db"
}

// Writer is a single-goroutine SQLite writer with transaction batching.
type Writer struct {
	db *sql.DB
}

// DB returns the underlying sql.DB for health checks.
func (w *Writer) DB() *sql.DB { return w.db }

// New creates a new SQLite Writer, initializes the database with WAL mode and schema.
func New(cfg WriterConfig) (*Writer, error) {
	db, err := sql.Open("sqlite3", cfg.DBPath+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// Set connection pool for single-writer
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Create table if not exists
	if err := createSchema(db); err != nil {
		return nil, fmt.Errorf("sqlite schema: %w", err)
	}

	log.Printf("[sqlite] opened database at %s", cfg.DBPath)
	return &Writer{db: db}, nil
}

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS candles_1s (
			token      TEXT    NOT NULL,
			exchange   TEXT    NOT NULL,
			ts         INTEGER NOT NULL,
			open       INTEGER NOT NULL,
			high       INTEGER NOT NULL,
			low        INTEGER NOT NULL,
			close      INTEGER NOT NULL,
			volume     INTEGER,
			ticks_count INTEGER,
			PRIMARY KEY (exchange, token, ts)
		);

		CREATE TABLE IF NOT EXISTS candles_tf (
			token      TEXT    NOT NULL,
			exchange   TEXT    NOT NULL,
			tf         INTEGER NOT NULL,
			ts         INTEGER NOT NULL,
			open       INTEGER NOT NULL,
			high       INTEGER NOT NULL,
			low        INTEGER NOT NULL,
			close      INTEGER NOT NULL,
			volume     INTEGER,
			count      INTEGER,
			PRIMARY KEY (exchange, token, tf, ts)
		);

		CREATE TABLE IF NOT EXISTS indicator_snapshots (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			data       TEXT    NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (strftime('%%s', 'now'))
		);
	`)
	return err
}

// Run reads candles from candleCh and inserts them in batched transactions.
// Flushes every batchSize candles OR every flushDelay, whichever first.
// Blocks until ctx is cancelled or candleCh is closed.
func (w *Writer) Run(ctx context.Context, candleCh <-chan model.Candle) {
	batch := make([]model.Candle, 0, defaultBatchSize)
	timer := time.NewTimer(defaultFlushDelay)
	defer timer.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		start := time.Now()
		if err := w.insertBatch(batch); err != nil {
			log.Printf("[sqlite] batch insert error: %v", err)
		} else {
			log.Printf("[sqlite] committed %d candles in %v", len(batch), time.Since(start))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return

		case candle, ok := <-candleCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, candle)
			if len(batch) >= defaultBatchSize {
				flush()
				timer.Reset(defaultFlushDelay)
			}

		case <-timer.C:
			flush()
			timer.Reset(defaultFlushDelay)
		}
	}
}

// insertBatch inserts a batch of candles in a single transaction.
func (w *Writer) insertBatch(candles []model.Candle) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO candles_1s (token, exchange, ts, open, high, low, close, volume, ticks_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, c := range candles {
		_, err := stmt.Exec(c.Token, c.Exchange, c.TS.Unix(), c.Open, c.High, c.Low, c.Close, c.Volume, c.TicksCount)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// GetLastTimestamp returns the last stored candle timestamp for a given instrument.
// Returns 0 if no candles exist.
func (w *Writer) GetLastTimestamp(exchange, token string) (int64, error) {
	var ts sql.NullInt64
	err := w.db.QueryRow(
		`SELECT MAX(ts) FROM candles_1s WHERE exchange = ? AND token = ?`,
		exchange, token,
	).Scan(&ts)
	if err != nil {
		return 0, err
	}
	if !ts.Valid {
		return 0, nil
	}
	return ts.Int64, nil
}

// RunTFCandles reads TF candles from a channel and inserts them in batched transactions.
func (w *Writer) RunTFCandles(ctx context.Context, tfCandleCh <-chan model.TFCandle) {
	batch := make([]model.TFCandle, 0, defaultBatchSize)
	timer := time.NewTimer(defaultFlushDelay)
	defer timer.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		start := time.Now()
		if err := w.insertTFBatch(batch); err != nil {
			log.Printf("[sqlite] TF batch insert error: %v", err)
		} else {
			log.Printf("[sqlite] committed %d TF candles in %v", len(batch), time.Since(start))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case tfc, ok := <-tfCandleCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, tfc)
			if len(batch) >= defaultBatchSize {
				flush()
				timer.Reset(defaultFlushDelay)
			}
		case <-timer.C:
			flush()
			timer.Reset(defaultFlushDelay)
		}
	}
}

// insertTFBatch inserts a batch of TF candles in a single transaction.
func (w *Writer) insertTFBatch(candles []model.TFCandle) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO candles_tf (token, exchange, tf, ts, open, high, low, close, volume, count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, c := range candles {
		_, err := stmt.Exec(c.Token, c.Exchange, c.TF, c.TS.Unix(), c.Open, c.High, c.Low, c.Close, c.Volume, c.Count)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// SaveSnapshot saves an indicator engine snapshot to SQLite.
func (w *Writer) SaveSnapshot(snap *indicator.EngineSnapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	_, err = w.db.Exec(`INSERT INTO indicator_snapshots (data) VALUES (?)`, string(data))
	if err != nil {
		return fmt.Errorf("sqlite insert snapshot: %w", err)
	}

	// Prune old snapshots — keep last 10
	_, err = w.db.Exec(`DELETE FROM indicator_snapshots WHERE id NOT IN (SELECT id FROM indicator_snapshots ORDER BY created_at DESC LIMIT 10)`)
	if err != nil {
		log.Printf("[sqlite] prune snapshots warning: %v", err)
	}

	return nil
}

// Close closes the database.
func (w *Writer) Close() error {
	return w.db.Close()
}
