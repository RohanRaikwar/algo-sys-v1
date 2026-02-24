package smartconnect

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// SmartWebSocketV2 is a Go translation of the provided Python SmartWebSocketV2 class.
// Note: adapt logging, DB hooks and trade hooks to your project. This implementation
// uses gorilla/websocket and provides Subscribe / Unsubscribe, auto-resubscribe,
// heartbeat (ping/pong) handling and binary parsing helpers.

const (
	RootURI           = "wss://smartapisocket.angelone.in/smart-stream"
	HeartBeatMessage  = "ping"
	HeartBeatInterval = 10 * time.Second
	QuotaDepthLimit   = 50
)

// Subscription action/ modes / exchanges
const (
	SubscribeAction   = 1
	UnsubscribeAction = 0

	ModeLTP       = 1
	ModeQuote     = 2
	ModeSnapQuote = 3
	ModeDepth     = 4

	NSE_CM = 1
	NSE_FO = 2
	BSE_CM = 3
	BSE_FO = 4
	MCX_FO = 5
	NCX_FO = 7
	CDE_FO = 13
)

var SubscriptionModeMap = map[int]string{
	1: "LTP",
	2: "QUOTE",
	3: "SNAP_QUOTE",
	4: "DEPTH",
}

// TokenListEntry represents exchangeType + tokens for subscribe/unsubscribe
type TokenListEntry struct {
	ExchangeType int      `json:"exchangeType"`
	Tokens       []string `json:"tokens"`
}

// internal representation of subscribed requests grouped by mode and exchangeType
type modeMap map[int][]string

// SmartWebSocketV2 struct
type SmartWebSocketV3 struct {
	AuthToken  string
	APIKey     string
	ClientCode string
	FeedToken  string

	Conn   *websocket.Conn
	Dialer *websocket.Dialer

	mu              sync.Mutex
	inputRequestMap map[int]modeMap // mode -> exchangeType -> tokens
	resubscribeFlag bool
	disconnectFlag  bool

	lastPongTimestamp time.Time

	// retry config
	maxRetryAttempt     int
	retryStrategy       int // 0 simple, 1 exponential
	retryDelay          time.Duration
	retryMultiplier     int
	retryDuration       time.Duration
	currentRetryAttempt int

	// Callbacks
	OnData           func(msg map[string]interface{})
	OnOpen           func()
	OnClose          func()
	OnError          func(code, msg string)
	OnControlMessage func(msg map[string]interface{})

	ctx    context.Context
	cancel context.CancelFunc
}

func NewSmartWebSocketV3(authToken, apiKey, clientCode, feedToken string, maxRetryAttempt int, retryStrategy int, retryDelaySec int, retryMultiplier int, retryDurationMin int) (*SmartWebSocketV3, error) {
	if authToken == "" || apiKey == "" || clientCode == "" || feedToken == "" {
		return nil, errors.New("provide valid value for all the tokens")
	}

	fmt.Println("authToken", "ch;;;")
	log.Println("Connecting to WebSocket...")

	ctx, cancel := context.WithCancel(context.Background())
	return &SmartWebSocketV3{
		AuthToken:       authToken,
		APIKey:          apiKey,
		ClientCode:      clientCode,
		FeedToken:       feedToken,
		Dialer:          websocket.DefaultDialer,
		inputRequestMap: make(map[int]modeMap),
		resubscribeFlag: false,
		disconnectFlag:  true,
		maxRetryAttempt: maxRetryAttempt,
		retryStrategy:   retryStrategy,
		retryDelay:      time.Duration(retryDelaySec) * time.Second,
		retryMultiplier: retryMultiplier,
		retryDuration:   time.Duration(retryDurationMin) * time.Minute,
		ctx:             ctx,
		cancel:          cancel,
	}, nil
}

// Connect establishes websocket connection and starts read/heartbeat loops
func (s *SmartWebSocketV3) Connect() error {

	header := http.Header{}
	header.Add("Authorization", s.AuthToken)
	header.Add("x-api-key", s.APIKey)
	header.Add("x-client-code", s.ClientCode)
	header.Add("x-feed-token", s.FeedToken)

	conn, resp, err := s.Dialer.Dial(RootURI, header)
	if err != nil {
		if resp != nil {
			log.Printf("Dial failed, status: %s", resp.Status)
		}
		return err
	}

	fmt.Println("Connecting to WebSocket... status:", resp.Status)

	s.Conn = conn
	s.disconnectFlag = false
	s.resubscribeFlag = false
	s.currentRetryAttempt = 0

	// Set handlers for ping/pong
	s.Conn.SetPingHandler(func(appData string) error {
		fmt.Printf("received ping: %s\n", appData)
		log.Printf("received ping: %s", appData)
		// update ping timestamp
		// respond automatically by gorilla: we need to write pong
		return s.Conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})

	s.Conn.SetPongHandler(func(appData string) error {
		log.Printf("received pong: %s", appData)
		s.lastPongTimestamp = time.Now()
		return nil
	})

	// Launch read loop
	go s.readLoop()
	// Launch heartbeat
	go s.heartbeatLoop()

	if s.OnOpen != nil {
		s.OnOpen()
	}

	return nil
}

func (s *SmartWebSocketV3) CloseConnection() {
	s.mu.Lock()
	s.resubscribeFlag = false
	s.disconnectFlag = true
	s.mu.Unlock()

	if s.Conn != nil {
		s.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		s.Conn.Close()
	}
	s.cancel()
}

// Subscribe sends a subscription request and saves token state for resubscribe
func (s *SmartWebSocketV3) Subscribe(correlationID string, mode int, tokenList []TokenListEntry) error {
	// validate depth mode quota
	if mode == ModeDepth {
		total := 0
		for _, t := range tokenList {
			total += len(t.Tokens)
		}
		if total > QuotaDepthLimit {
			return fmt.Errorf("quota exceeded: you can subscribe to a maximum of %d tokens only", QuotaDepthLimit)
		}
	}

	// build request JSON
	req := map[string]interface{}{
		"correlationID": correlationID,
		"action":        SubscribeAction,
		"params": map[string]interface{}{
			"mode":      mode,
			"tokenList": tokenList,
		},
	}

	// update internal map
	s.mu.Lock()
	if s.inputRequestMap[mode] == nil {
		s.inputRequestMap[mode] = make(modeMap)
	}
	for _, tl := range tokenList {
		ex := tl.ExchangeType
		if existing, ok := s.inputRequestMap[mode][ex]; ok {
			s.inputRequestMap[mode][ex] = append(existing, tl.Tokens...)
		} else {
			s.inputRequestMap[mode][ex] = append([]string{}, tl.Tokens...)
		}
	}
	s.mu.Unlock()

	// send JSON over websocket
	if s.Conn == nil {
		return errors.New("no connection")
	}

	if err := s.Conn.WriteJSON(req); err != nil {
		return err
	}

	s.resubscribeFlag = true
	return nil
}

// Unsubscribe similar to subscribe
func (s *SmartWebSocketV3) Unsubscribe(correlationID string, mode int, tokenList []TokenListEntry) error {
	req := map[string]interface{}{
		"correlationID": correlationID,
		"action":        UnsubscribeAction,
		"params": map[string]interface{}{
			"mode":      mode,
			"tokenList": tokenList,
		},
	}

	// best-effort update internal state: remove tokens from map
	s.mu.Lock()
	if m := s.inputRequestMap[mode]; m != nil {
		for _, tl := range tokenList {
			ex := tl.ExchangeType
			if tokens, ok := m[ex]; ok {
				// remove tokens present in tl.Tokens from tokens slice
				newTokens := filterRemove(tokens, tl.Tokens)
				if len(newTokens) == 0 {
					delete(m, ex)
				} else {
					m[ex] = newTokens
				}
			}
		}
	}
	s.mu.Unlock()

	if s.Conn == nil {
		return errors.New("no connection")
	}
	if err := s.Conn.WriteJSON(req); err != nil {
		return err
	}
	s.resubscribeFlag = true
	return nil
}

func filterRemove(src, remove []string) []string {
	m := make(map[string]struct{}, len(remove))
	for _, r := range remove {
		m[r] = struct{}{}
	}
	out := make([]string, 0, len(src))
	for _, v := range src {
		if _, ok := m[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}

// Resubscribe resends stored subscription requests
func (s *SmartWebSocketV3) Resubscribe() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for mode, mm := range s.inputRequestMap {
		var tokenList []TokenListEntry
		for ex, toks := range mm {
			tokenList = append(tokenList, TokenListEntry{ExchangeType: ex, Tokens: toks})
		}
		req := map[string]interface{}{
			"action": SubscribeAction,
			"params": map[string]interface{}{
				"mode":      mode,
				"tokenList": tokenList,
			},
		}
		if s.Conn == nil {
			return errors.New("no connection")
		}
		if err := s.Conn.WriteJSON(req); err != nil {
			return err
		}
	}
	return nil
}

// readLoop reads messages, handles binary parsing and control messages
func (s *SmartWebSocketV3) readLoop() {
	defer func() {
		if s.OnClose != nil {
			s.OnClose()
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			if s.Conn == nil {
				time.Sleep(1 * time.Second)
				continue
			}
			mt, message, err := s.Conn.ReadMessage()
			if err != nil {
				log.Printf("read error: %v", err)
				// try reconnect/resubscribe logic
				s.handleError(err)
				return
			}

			if mt == websocket.BinaryMessage {
				parsed, perr := s.parseBinaryData(message)
				if perr != nil {
					log.Printf("parse error: %v", perr)
					continue
				}
				// if control message (subscription_mode missing?) In Python they treat control when subscription_mode not in parsed
				if _, ok := parsed["subscription_mode"]; !ok {
					// control message
					if s.OnControlMessage != nil {
						s.OnControlMessage(parsed)
					}
					// handle ping/pong if provided as control - in their code control used subscription_mode==0/1
					if sm, ok2 := parsed["subscription_mode"].(int); ok2 {
						if sm == 0 {
							s.handlePong()
						}
						if sm == 1 {
							s.handlePing()
						}
					}
				} else {
					if s.OnData != nil {
						s.OnData(parsed)
					}
				}
			} else if mt == websocket.TextMessage {
				// Text frames may contain "pong" etc.
				if string(message) == "pong" {
					s.handlePong()
				} else {
					// Attempt JSON decode
					var obj map[string]interface{}
					if err := json.Unmarshal(message, &obj); err == nil {
						if s.OnData != nil {
							s.OnData(obj)
						}
					}
				}
			}
		}
	}
}

func (s *SmartWebSocketV3) handleError(err error) {
	// simple reconnect strategy
	s.mu.Lock()
	s.resubscribeFlag = true
	attempts := s.currentRetryAttempt
	max := s.maxRetryAttempt
	s.mu.Unlock()

	for attempts < max {
		attempts++
		if s.retryStrategy == 0 {
			time.Sleep(s.retryDelay)
		} else {
			d := s.retryDelay * time.Duration(s.retryMultiplier^(attempts-1))
			time.Sleep(d)
		}
		// attempt reconnect
		if err := s.Connect(); err == nil {
			// resubscribe
			s.Resubscribe()
			return
		}
	}

	if s.OnError != nil {
		s.OnError("Max retry attempt reached", "Connection closed")
	}
}

func (s *SmartWebSocketV3) handlePong() {
	s.lastPongTimestamp = time.Now()
	log.Printf("on_pong at %s", s.lastPongTimestamp.Format(time.RFC3339))
}

func (s *SmartWebSocketV3) handlePing() {
	log.Printf("on_ping at %s", time.Now().Format(time.RFC3339))
}

// heartbeatLoop sends periodic ping messages
func (s *SmartWebSocketV3) heartbeatLoop() {
	ticker := time.NewTicker(HeartBeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if s.Conn != nil {
				err := s.Conn.WriteMessage(websocket.PingMessage, []byte(HeartBeatMessage))
				if err != nil {
					log.Printf("ping write error: %v", err)
					s.handleError(err)
					return
				}
			}
		}
	}
}

// parseBinaryData converts binary message into a map similar to Python implementation
func (s *SmartWebSocketV3) parseBinaryData(b []byte) (map[string]interface{}, error) {
	if len(b) < 51 {
		return nil, errors.New("binary payload too short")
	}
	r := bytes.NewReader(b)
	out := make(map[string]interface{})
	// subscription_mode: 1 byte unsigned
	var subMode uint8
	binary.Read(r, binary.LittleEndian, &subMode)
	out["subscription_mode"] = int(subMode)
	// exchange_type: next byte
	var exType uint8
	binary.Read(r, binary.LittleEndian, &exType)
	out["exchange_type"] = int(exType)
	// token: next 25 bytes (2:27)
	tokenBytes := b[2:27]
	out["token"] = parseTokenValue(tokenBytes)
	// sequence_number: bytes 27:35 int64
	seq := int64(binary.LittleEndian.Uint64(b[27:35]))
	out["sequence_number"] = seq
	// exchange_timestamp: bytes 35:43 int64
	exTs := int64(binary.LittleEndian.Uint64(b[35:43]))
	out["exchange_timestamp"] = exTs
	// last_traded_price: bytes 43:51 int64
	ltp := int64(binary.LittleEndian.Uint64(b[43:51]))
	out["last_traded_price"] = ltp

	// try to add more fields for QUOTE / SNAP_QUOTE
	if int(subMode) == ModeQuote || int(subMode) == ModeSnapQuote {
		if len(b) >= 99 {
			out["last_traded_quantity"] = int64(binary.LittleEndian.Uint64(b[51:59]))
			out["average_traded_price"] = int64(binary.LittleEndian.Uint64(b[59:67]))
			out["volume_trade_for_the_day"] = int64(binary.LittleEndian.Uint64(b[67:75]))
			// total_buy_quantity and total_sell_quantity were "d" (double) in python -> float64
			var tbq float64
			buf := bytes.NewReader(b[75:83])
			binary.Read(buf, binary.LittleEndian, &tbq)
			out["total_buy_quantity"] = tbq
			var tsq float64
			buf2 := bytes.NewReader(b[83:91])
			binary.Read(buf2, binary.LittleEndian, &tsq)
			out["total_sell_quantity"] = tsq
			out["open_price_of_the_day"] = int64(binary.LittleEndian.Uint64(b[91:99]))
			out["high_price_of_the_day"] = int64(binary.LittleEndian.Uint64(b[99:107]))
			out["low_price_of_the_day"] = int64(binary.LittleEndian.Uint64(b[107:115]))
			out["closed_price"] = int64(binary.LittleEndian.Uint64(b[115:123]))
		}
	}

	// SNAP_QUOTE additional fields
	if int(subMode) == ModeSnapQuote {
		if len(b) >= 379 {
			out["last_traded_timestamp"] = int64(binary.LittleEndian.Uint64(b[123:131]))
			out["open_interest"] = int64(binary.LittleEndian.Uint64(b[131:139]))
			out["open_interest_change_percentage"] = int64(binary.LittleEndian.Uint64(b[139:147]))
			// parse best 5 buy/sell between bytes 147:347
			best := parseBest5BuySell(b[147:347])
			out["best_5_buy_data"] = best["best_5_buy_data"]
			out["best_5_sell_data"] = best["best_5_sell_data"]
			out["upper_circuit_limit"] = int64(binary.LittleEndian.Uint64(b[347:355]))
			out["lower_circuit_limit"] = int64(binary.LittleEndian.Uint64(b[355:363]))
			out["52_week_high_price"] = int64(binary.LittleEndian.Uint64(b[363:371]))
			out["52_week_low_price"] = int64(binary.LittleEndian.Uint64(b[371:379]))
		}
	}

	// DEPTH handling (simplified)
	if int(subMode) == ModeDepth {
		// packet_received_time at 35:43
		out["packet_received_time"] = exTs
		// depth data after 43: parse 20*10 = 200 bytes buy and 200 bytes sell (simple)
		if len(b) >= 243 {
			depth := parseDepth20(b[43:])
			out["depth_20_buy_data"] = depth["depth_20_buy_data"]
			out["depth_20_sell_data"] = depth["depth_20_sell_data"]
		}
	}

	return out, nil
}

func parseTokenValue(b []byte) string {
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func parseBest5BuySell(b []byte) map[string]interface{} {
	packets := [][]byte{}
	for i := 0; i < len(b); i += 20 {
		end := i + 20
		if end > len(b) {
			end = len(b)
		}
		packets = append(packets, b[i:end])
	}
	buy := []map[string]interface{}{}
	sell := []map[string]interface{}{}
	for _, p := range packets {
		if len(p) < 20 {
			continue
		}
		flag := int(binary.LittleEndian.Uint16(p[0:2]))
		quantity := int64(binary.LittleEndian.Uint64(append(p[2:10], make([]byte, 0)...)))
		price := int64(binary.LittleEndian.Uint64(append(p[10:18], make([]byte, 0)...)))
		numOrders := int(binary.LittleEndian.Uint16(p[18:20]))
		each := map[string]interface{}{"flag": flag, "quantity": quantity, "price": price, "no_of_orders": numOrders}
		if flag == 0 {
			buy = append(buy, each)
		} else {
			sell = append(sell, each)
		}
	}
	return map[string]interface{}{"best_5_buy_data": buy, "best_5_sell_data": sell}
}

func parseDepth20(b []byte) map[string]interface{} {
	buy := []map[string]interface{}{}
	sell := []map[string]interface{}{}
	// expect at least 400 bytes (200 buy + 200 sell)
	for i := 0; i < 20; i++ {
		bs := i * 10
		ss := 200 + i*10
		if bs+10 <= len(b) {
			q := int32(binary.LittleEndian.Uint32(b[bs : bs+4]))
			p := int32(binary.LittleEndian.Uint32(b[bs+4 : bs+8]))
			n := int16(binary.LittleEndian.Uint16(b[bs+8 : bs+10]))
			buy = append(buy, map[string]interface{}{"quantity": q, "price": p, "num_of_orders": n})
		}
		if ss+10 <= len(b) {
			q := int32(binary.LittleEndian.Uint32(b[ss : ss+4]))
			p := int32(binary.LittleEndian.Uint32(b[ss+4 : ss+8]))
			n := int16(binary.LittleEndian.Uint16(b[ss+8 : ss+10]))
			sell = append(sell, map[string]interface{}{"quantity": q, "price": p, "num_of_orders": n})
		}
	}
	return map[string]interface{}{"depth_20_buy_data": buy, "depth_20_sell_data": sell}
}

// Example main to show usage (remove in production)
func ExampleMain() {
	// basic logging to file
	f, _ := os.OpenFile("smartws.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	log.SetOutput(f)

	s, _ := NewSmartWebSocketV3("auth", "api", "client", "feed", 3, 0, 10, 2, 60)
	s.OnData = func(msg map[string]interface{}) {
		b, _ := json.Marshal(msg)
		log.Printf("data: %s", string(b))
	}
	s.OnOpen = func() { log.Println("opened") }
	s.OnClose = func() { log.Println("closed") }
	s.OnError = func(code, msg string) { log.Printf("err: %s %s", code, msg) }

	if err := s.Connect(); err != nil {
		log.Fatalf("connect err: %v", err)
	}

	// trap SIGINT to gracefully close
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	log.Println("shutting down")
	s.CloseConnection()
}
