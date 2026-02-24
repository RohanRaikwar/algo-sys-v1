package ws

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"trading-systemv1/internal/model"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

// exchangeTypeToName maps Angel One WS exchange_type ints to exchange name strings.
var exchangeTypeToName = map[int]string{
	1:  "NSE",
	2:  "NFO",
	3:  "BSE",
	4:  "BFO",
	5:  "MCX",
	7:  "NCX",
	13: "CDE",
}

// IngestConfig holds configuration for the WS ingest.
type IngestConfig struct {
	AuthToken  string
	APIKey     string
	ClientCode string
	FeedToken  string

	// Tokens to subscribe, grouped by exchange type and mode.
	SubscribeMode int
	TokenList     []smartconnect.TokenListEntry
}

// Ingest connects to Angel One WebSocket and pushes normalized ticks into tickCh.
type Ingest struct {
	cfg IngestConfig
	ws  *smartconnect.SmartWebSocketV3

	// Optional metrics hooks
	OnReconnect func()
}

// New creates a new Ingest instance.
func New(cfg IngestConfig) (*Ingest, error) {
	ws, err := smartconnect.NewSmartWebSocketV3(
		cfg.AuthToken,
		cfg.APIKey,
		cfg.ClientCode,
		cfg.FeedToken,
		5,  // maxRetryAttempt
		1,  // retryStrategy: exponential
		5,  // retryDelaySec
		2,  // retryMultiplier
		30, // retryDurationMin
	)
	if err != nil {
		return nil, fmt.Errorf("ws ingest: create websocket: %w", err)
	}

	return &Ingest{cfg: cfg, ws: ws}, nil
}

// Start connects to the WebSocket and begins streaming ticks into tickCh.
// Blocks until ctx is cancelled.
func (ing *Ingest) Start(ctx context.Context, tickCh chan<- model.Tick) error {
	doneCh := make(chan struct{})

	ing.ws.OnOpen = func() {
		log.Printf("[ws] connected, subscribing mode=%d tokens=%+v", ing.cfg.SubscribeMode, ing.cfg.TokenList)
		err := ing.ws.Subscribe("ohlc_ingest", ing.cfg.SubscribeMode, ing.cfg.TokenList)
		if err != nil {
			log.Printf("[ws] subscribe error: %v", err)
		} else {
			log.Println("[ws] subscription sent successfully")
		}
	}

	ing.ws.OnData = func(msg map[string]interface{}) {
		log.Printf("[ws] raw tick: token=%v ltp=%v exchange_type=%v", msg["token"], msg["last_traded_price"], msg["exchange_type"])
		tick, err := parseTick(msg)
		if err != nil {
			log.Printf("[ws] parse error: %v", err)
			return
		}

		select {
		case tickCh <- tick:
		default:
			log.Println("[ws] tickCh full, dropping tick")
		}
	}

	ing.ws.OnClose = func() {
		log.Println("[ws] connection closed")
		if ing.OnReconnect != nil {
			ing.OnReconnect()
		}
	}

	ing.ws.OnError = func(code, msg string) {
		log.Printf("[ws] error: code=%s msg=%s", code, msg)
	}

	if err := ing.ws.Connect(); err != nil {
		return fmt.Errorf("ws ingest: connect: %w", err)
	}

	// Block until context is done
	go func() {
		<-ctx.Done()
		ing.ws.CloseConnection()
		close(doneCh)
	}()

	<-doneCh
	return nil
}

// parseTick converts the raw WS message map into a model.Tick.
func parseTick(msg map[string]interface{}) (model.Tick, error) {
	token, _ := msg["token"].(string)
	if token == "" {
		return model.Tick{}, fmt.Errorf("missing token")
	}

	exType := toInt(msg["exchange_type"])
	exchange := exchangeTypeToName[exType]
	if exchange == "" {
		exchange = fmt.Sprintf("EX_%d", exType)
	}

	price := toInt64(msg["last_traded_price"])
	qty := toInt64(msg["last_traded_quantity"])

	// Use exchange timestamp if available, otherwise use current time
	var tickTS time.Time
	if exTS := toInt64(msg["exchange_timestamp"]); exTS > 0 {
		// Angel One sends epoch milliseconds
		tickTS = time.Unix(0, exTS*int64(time.Millisecond)).UTC()
	} else {
		tickTS = time.Now().UTC()
	}

	return model.Tick{
		Token:    token,
		Exchange: exchange,
		Price:    price,
		Qty:      qty,
		TickTS:   tickTS,
	}, nil
}

func toInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}

func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}
