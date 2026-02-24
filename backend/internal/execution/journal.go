package execution

import (
	"database/sql"
	"log"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Journal persists trade fills to SQLite for analysis and audit.
type Journal struct {
	mu sync.Mutex
	db *sql.DB
}

// NewJournal opens (or creates) a SQLite journal database.
func NewJournal(dbPath string) (*Journal, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_sync=NORMAL")
	if err != nil {
		return nil, err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS trades (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		order_id    TEXT NOT NULL,
		strategy    TEXT NOT NULL,
		action      TEXT NOT NULL,
		token       TEXT NOT NULL,
		exchange    TEXT NOT NULL,
		qty         INTEGER NOT NULL,
		price       INTEGER NOT NULL,
		slippage    INTEGER DEFAULT 0,
		reason      TEXT,
		filled_at   DATETIME NOT NULL,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_trades_strategy ON trades(strategy);
	CREATE INDEX IF NOT EXISTS idx_trades_token ON trades(token, exchange);
	CREATE INDEX IF NOT EXISTS idx_trades_filled_at ON trades(filled_at);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	log.Printf("[journal] opened trade journal at %s", dbPath)
	return &Journal{db: db}, nil
}

// RecordFill persists a fill to the journal.
func (j *Journal) RecordFill(fill Fill) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	_, err := j.db.Exec(
		`INSERT INTO trades (order_id, strategy, action, token, exchange, qty, price, slippage, reason, filled_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fill.OrderID,
		fill.Signal.StrategyName,
		string(fill.Signal.Action),
		fill.Signal.Token,
		fill.Signal.Exchange,
		fill.FillQty,
		fill.FillPrice,
		fill.Slippage,
		fill.Signal.Reason,
		fill.FilledAt.Format(time.RFC3339),
	)
	return err
}

// TradeRecord represents a row from the trades table.
type TradeRecord struct {
	ID       int64  `json:"id"`
	OrderID  string `json:"order_id"`
	Strategy string `json:"strategy"`
	Action   string `json:"action"`
	Token    string `json:"token"`
	Exchange string `json:"exchange"`
	Qty      int64  `json:"qty"`
	Price    int64  `json:"price"`
	Slippage int64  `json:"slippage"`
	Reason   string `json:"reason"`
	FilledAt string `json:"filled_at"`
}

// GetTrades returns the last N trades, newest first.
func (j *Journal) GetTrades(limit int) ([]TradeRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	rows, err := j.db.Query(
		`SELECT id, order_id, strategy, action, token, exchange, qty, price, slippage, reason, filled_at
		 FROM trades ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []TradeRecord
	for rows.Next() {
		var t TradeRecord
		if err := rows.Scan(&t.ID, &t.OrderID, &t.Strategy, &t.Action, &t.Token,
			&t.Exchange, &t.Qty, &t.Price, &t.Slippage, &t.Reason, &t.FilledAt); err != nil {
			continue
		}
		trades = append(trades, t)
	}
	return trades, nil
}

// Close closes the journal database.
func (j *Journal) Close() error {
	return j.db.Close()
}
