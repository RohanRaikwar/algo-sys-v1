// Package smartconnect is a Go port of the provided Python SmartConnect class for Angel One SmartAPI.
// It mirrors routes, headers, login/token/session handling, request helpers, and common endpoint methods.
//
// Usage example:
//
//	sc := smartconnect.NewSmartConnect(smartconnect.Config{APIKey: "your_api_key", Debug: true})
//	user, err := sc.GenerateSession("CLIENTID", "PASSWORD", "TOTP")
//	if err != nil { log.Fatal(err) }
//	fmt.Println("Logged in as:", user["data"].(map[string]any)["clientcode"])
//	// Place order example:
//	orderID, err := sc.PlaceOrder(map[string]any{
//	    "variety": "NORMAL", "tradingsymbol": "SBIN-EQ", "symboltoken": "3045", "transactiontype": "BUY",
//	    "exchange": "NSE", "ordertype": "MARKET", "producttype": "INTRADAY", "duration": "DAY", "quantity": 1,
//	})
//	if err != nil { log.Fatal(err) }
//	fmt.Println("Order ID:", orderID)
package smartconnect

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

// ---- Config & client ----

type Config struct {
	APIKey       string
	AccessToken  string
	RefreshToken string
	FeedToken    string
	UserID       string

	RootURL        string // default: https://apiconnect.angelone.in
	LoginURL       string // default: https://smartapi.angelone.in/publisher-login
	Debug          bool
	Timeout        time.Duration // default: 7s
	ProxyURL       string        // optional HTTP proxy URL
	DisableSSL     bool          // if true, InsecureSkipVerify
	Accept         string        // default: application/json
	UserType       string        // default: USER
	SourceID       string        // default: WEB
	ClientPublicIP string        // default resolved, else 106.193.147.98 (as in Python finally)
	ClientLocalIP  string        // default resolved, else 127.0.0.1
	ClientMAC      string        // default from interface MAC
}

type SmartConnect struct {
	apiKey       string
	accessToken  string
	refreshToken string
	feedToken    string
	userID       string

	rootURL  string
	loginURL string
	debug    bool
	timeout  time.Duration

	httpClient *http.Client

	// header fields
	accept   string
	userType string
	sourceID string

	clientPublicIP string
	clientLocalIP  string
	clientMAC      string

	// Optional callback for 403 TokenException
	SessionExpiryHook func()
}

const (
	defaultRoot  = "https://apiconnect.angelone.in"
	defaultLogin = "https://smartapi.angelone.in/publisher-login"
)

var routes = map[string]string{
	"api.login":        "/rest/auth/angelbroking/user/v1/loginByPassword",
	"api.logout":       "/rest/secure/angelbroking/user/v1/logout",
	"api.token":        "/rest/auth/angelbroking/jwt/v1/generateTokens",
	"api.refresh":      "/rest/auth/angelbroking/jwt/v1/generateTokens",
	"api.user.profile": "/rest/secure/angelbroking/user/v1/getProfile",

	"api.order.place":             "/rest/secure/angelbroking/order/v1/placeOrder",
	"api.order.placefullresponse": "/rest/secure/angelbroking/order/v1/placeOrder",
	"api.order.modify":            "/rest/secure/angelbroking/order/v1/modifyOrder",
	"api.order.cancel":            "/rest/secure/angelbroking/order/v1/cancelOrder",
	"api.order.book":              "/rest/secure/angelbroking/order/v1/getOrderBook",

	"api.ltp.data":         "/rest/secure/angelbroking/order/v1/getLtpData",
	"api.trade.book":       "/rest/secure/angelbroking/order/v1/getTradeBook",
	"api.rms.limit":        "/rest/secure/angelbroking/user/v1/getRMS",
	"api.holding":          "/rest/secure/angelbroking/portfolio/v1/getHolding",
	"api.position":         "/rest/secure/angelbroking/order/v1/getPosition",
	"api.convert.position": "/rest/secure/angelbroking/order/v1/convertPosition",

	"api.gtt.create":  "/gtt-service/rest/secure/angelbroking/gtt/v1/createRule",
	"api.gtt.modify":  "/gtt-service/rest/secure/angelbroking/gtt/v1/modifyRule",
	"api.gtt.cancel":  "/gtt-service/rest/secure/angelbroking/gtt/v1/cancelRule",
	"api.gtt.details": "/rest/secure/angelbroking/gtt/v1/ruleDetails",
	"api.gtt.list":    "/rest/secure/angelbroking/gtt/v1/ruleList",

	"api.candle.data":  "/rest/secure/angelbroking/historical/v1/getCandleData",
	"api.oi.data":      "/rest/secure/angelbroking/historical/v1/getOIData",
	"api.market.data":  "/rest/secure/angelbroking/market/v1/quote",
	"api.search.scrip": "/rest/secure/angelbroking/order/v1/searchScrip",
	"api.allholding":   "/rest/secure/angelbroking/portfolio/v1/getAllHolding",

	"api.individual.order.details": "/rest/secure/angelbroking/order/v1/details/",
	"api.margin.api":               "rest/secure/angelbroking/margin/v1/batch",
	"api.estimateCharges":          "rest/secure/angelbroking/brokerage/v1/estimateCharges",
	"api.verifyDis":                "rest/secure/angelbroking/edis/v1/verifyDis",
	"api.generateTPIN":             "rest/secure/angelbroking/edis/v1/generateTPIN",
	"api.getTranStatus":            "rest/secure/angelbroking/edis/v1/getTranStatus",
	"api.optionGreek":              "rest/secure/angelbroking/marketData/v1/optionGreek",
	"api.gainersLosers":            "rest/secure/angelbroking/marketData/v1/gainersLosers",
	"api.putCallRatio":             "rest/secure/angelbroking/marketData/v1/putCallRatio",
	"api.oIBuildup":                "rest/secure/angelbroking/marketData/v1/OIBuildup",
	"api.nseIntraday":              "rest/secure/angelbroking/marketData/v1/nseIntraday",
	"api.bseIntraday":              "rest/secure/angelbroking/marketData/v1/bseIntraday",
}

func GetPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org?format=text")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(ip), nil
}

// GetLocalIP finds your local IP address
func GetLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, address := range addrs {
		// Check if it's an IP address and not a loopback
		if ipNet, ok := address.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no local IP found")
}

// NewSmartConnect initializes the client and sets up logging & TLS similar to Python version.
func NewSmartConnect(cfg Config) *SmartConnect {
	if cfg.RootURL == "" {
		cfg.RootURL = defaultRoot
	}
	if cfg.LoginURL == "" {
		cfg.LoginURL = defaultLogin
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 7 * time.Second
	}
	if cfg.Accept == "" {
		cfg.Accept = "application/json"
	}
	if cfg.UserType == "" {
		cfg.UserType = "USER"
	}
	if cfg.SourceID == "" {
		cfg.SourceID = "WEB"
	}
	localIP, err := GetLocalIP()
	if err != nil {
		log.Printf("Error getting local IP: %v", err)
	}
	publicIP, err := GetPublicIP()
	if err != nil {
		log.Printf("Error getting public IP: %v", err)
	}

	// Resolve defaults similar to Python finally block
	if cfg.ClientPublicIP == "" || cfg.ClientLocalIP == "" {
		// Try resolve; if fail, use hard-coded fallbacks
		cfg.ClientPublicIP = firstNonEmpty(publicIP, "106.193.147.98")
		cfg.ClientLocalIP = firstNonEmpty(localIP, "127.0.0.1")
	}
	if cfg.ClientMAC == "" {
		cfg.ClientMAC = getMACFallback()
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.DisableSSL, // mirrors Python's verify=not disable_ssl (unsafe)
		},
	}
	if cfg.ProxyURL != "" {
		if purl, err := url.Parse(cfg.ProxyURL); err == nil {
			tr.Proxy = http.ProxyURL(purl)
		}
	}
	fmt.Println("Local IP:", localIP)
	fmt.Println("Public IP:", publicIP)
	client := &http.Client{Transport: tr, Timeout: cfg.Timeout}

	// Set up date-based log file logs/YYYY-MM-DD/app.log
	logDir := path.Join("logs", time.Now().Format("2006-01-02"))
	_ = os.MkdirAll(logDir, 0o755)
	logPath := path.Join(logDir, "app.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f)
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	return &SmartConnect{
		apiKey:         cfg.APIKey,
		accessToken:    cfg.AccessToken,
		refreshToken:   cfg.RefreshToken,
		feedToken:      cfg.FeedToken,
		userID:         cfg.UserID,
		rootURL:        strings.TrimRight(cfg.RootURL, "/"),
		loginURL:       cfg.LoginURL,
		debug:          cfg.Debug,
		timeout:        cfg.Timeout,
		httpClient:     client,
		accept:         cfg.Accept,
		userType:       cfg.UserType,
		sourceID:       cfg.SourceID,
		clientPublicIP: cfg.ClientPublicIP,
		clientLocalIP:  cfg.ClientLocalIP,
		clientMAC:      cfg.ClientMAC,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func getMACFallback() string {
	// Get a MAC-ish fallback based on interfaces
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if len(ifc.HardwareAddr) > 0 {
			return ifc.HardwareAddr.String()
		}
	}
	// Fallback to a pseudo-UUID-like MAC
	re := regexp.MustCompile("..")
	b := []byte("001122334455")
	parts := re.FindAll(b, -1)
	s := make([]string, 0, len(parts))
	for _, p := range parts {
		s = append(s, string(p))
	}
	return strings.Join(s, ":")
}

// ---- Helpers ----

func (sc *SmartConnect) requestHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", sc.accept)
	h.Set("Accept", sc.accept)
	h.Set("X-ClientLocalIP", sc.clientLocalIP)
	h.Set("X-ClientPublicIP", sc.clientPublicIP)
	h.Set("X-MACAddress", sc.clientMAC)
	h.Set("X-PrivateKey", sc.apiKey)
	h.Set("X-UserType", sc.userType)
	h.Set("X-SourceID", sc.sourceID)
	if sc.accessToken != "" {
		h.Set("Authorization", "Bearer "+sc.accessToken)
	}
	return h
}

func (sc *SmartConnect) buildURL(route string) (string, error) {
	uri, ok := routes[route]
	if !ok {
		return "", fmt.Errorf("unknown route: %s", route)
	}
	return sc.rootURL + uri, nil
}

func (sc *SmartConnect) doRequest(method, route string, params map[string]any) (map[string]any, []byte, int, error) {
	fullURL, err := sc.buildURL(route)
	if err != nil {
		return nil, nil, 0, err
	}

	var body io.Reader
	reqURL := fullURL

	if method == http.MethodGet || method == http.MethodDelete {
		if len(params) > 0 {
			q := url.Values{}
			for k, v := range params {
				q.Set(k, toString(v))
			}
			if strings.Contains(reqURL, "?") {
				reqURL += "&" + q.Encode()
			} else {
				reqURL += "?" + q.Encode()
			}
		}
	} else {
		if params == nil {
			params = map[string]any{}
		}
		b, _ := json.Marshal(params)
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header = sc.requestHeaders()

	if sc.debug {
		log.Printf("Request: %s %s params=%v headers=%v", method, reqURL, params, req.Header)
	}

	resp, err := sc.httpClient.Do(req)
	if err != nil {
		log.Printf("HTTP error: %s %s err=%v", method, reqURL, err)
		return nil, nil, 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}

	if sc.debug {
		log.Printf("Response: code=%d body=%s", resp.StatusCode, string(raw))
	}

	// Expect JSON for application/json
	var out map[string]any
	if strings.Contains(sc.accept, "json") {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, raw, resp.StatusCode, fmt.Errorf("couldn't parse JSON response: %w", err)
		}
		// Handle API error style: {"error_type": "TokenException", "message": "..."}
		if et, ok := out["error_type"].(string); ok && et != "" {
			if sc.SessionExpiryHook != nil && resp.StatusCode == http.StatusForbidden && et == "TokenException" {
				sc.SessionExpiryHook()
			}
			msg, _ := out["message"].(string)
			return out, raw, resp.StatusCode, fmt.Errorf("%s: %s", et, msg)
		}
		// If status==false, log error but still return body to caller (mirror Python)
		if st, ok := out["status"].(bool); ok && !st {
			msg, _ := out["message"].(string)
			log.Printf("API request failed: %s %s status=false message=%s resp=%s", method, reqURL, msg, string(raw))
		}
		return out, raw, resp.StatusCode, nil
	}

	// CSV or others
	return map[string]any{"raw": string(raw)}, raw, resp.StatusCode, nil
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// ---- Public helpers (aliases) ----

func (sc *SmartConnect) get(route string, params map[string]any) (map[string]any, error) {
	m, _, _, err := sc.doRequest(http.MethodGet, route, params)
	return m, err
}
func (sc *SmartConnect) post(route string, params map[string]any) (map[string]any, error) {
	m, _, _, err := sc.doRequest(http.MethodPost, route, params)
	return m, err
}
func (sc *SmartConnect) put(route string, params map[string]any) (map[string]any, error) {
	m, _, _, err := sc.doRequest(http.MethodPut, route, params)
	return m, err
}
func (sc *SmartConnect) delete(route string, params map[string]any) (map[string]any, error) {
	m, _, _, err := sc.doRequest(http.MethodDelete, route, params)
	return m, err
}

// ---- Setters/Getters ----

func (sc *SmartConnect) SetUserID(id string)      { sc.userID = id }
func (sc *SmartConnect) GetUserID() string        { return sc.userID }
func (sc *SmartConnect) SetAccessToken(t string)  { sc.accessToken = t }
func (sc *SmartConnect) SetRefreshToken(t string) { sc.refreshToken = t }
func (sc *SmartConnect) SetFeedToken(t string)    { sc.feedToken = t }
func (sc *SmartConnect) GetFeedToken() string     { return sc.feedToken }
func (sc *SmartConnect) LoginURL() string {
	return fmt.Sprintf("%s?api_key=%s", sc.loginURL, sc.apiKey)
}

// ---- API Methods (parity with Python) ----

// GenerateSession(clientCode,password,totp) -> sets tokens and returns user profile payload
func (sc *SmartConnect) GenerateSession(clientCode, password, totp string) (map[string]any, error) {
	fmt.Println("Generating session for clientCode:", clientCode, "password:", password, "totp:", totp)
	params := map[string]any{"clientcode": clientCode, "password": password, "totp": totp}
	res, err := sc.post("api.login", params)

	if err != nil {
		return res, err
	}

	st, _ := res["status"].(bool)
	if !st {
		return res, errors.New("login failed")
	}
	data, ok := res["data"].(map[string]any)
	if !ok {
		return res, errors.New("unexpected login response format")
	}

	jwtToken, _ := data["jwtToken"].(string)
	refreshToken, _ := data["refreshToken"].(string)
	feedToken, _ := data["feedToken"].(string)

	sc.SetAccessToken(jwtToken)
	sc.SetRefreshToken(refreshToken)
	sc.SetFeedToken(feedToken)

	user, err := sc.GetProfile(refreshToken)
	if err != nil {
		return user, err
	}

	if udata, ok := user["data"].(map[string]any); ok {
		if cc, _ := udata["clientcode"].(string); cc != "" {
			sc.SetUserID(cc)
		}
		udata["jwtToken"] = "Bearer " + jwtToken
		udata["refreshToken"] = refreshToken
		udata["feedToken"] = feedToken
		user["data"] = udata
	}

	return user, nil
}

func (sc *SmartConnect) TerminateSession(clientCode string) (map[string]any, error) {
	return sc.post("api.logout", map[string]any{"clientcode": clientCode})
}

func (sc *SmartConnect) GenerateToken(refreshToken string) (map[string]any, error) {
	res, err := sc.post("api.token", map[string]any{"refreshToken": refreshToken})
	if err != nil {
		return res, err
	}
	if data, ok := res["data"].(map[string]any); ok {
		if jwt, _ := data["jwtToken"].(string); jwt != "" {
			sc.SetAccessToken(jwt)
		}
		if ft, _ := data["feedToken"].(string); ft != "" {
			sc.SetFeedToken(ft)
		}
	}
	return res, nil
}

// RenewAccessToken mirrors Python renewAccessToken(); returns tokenSet map
func (sc *SmartConnect) RenewAccessToken() (map[string]any, error) {
	res, err := sc.post("api.refresh", map[string]any{
		"jwtToken":     sc.accessToken,
		"refreshToken": sc.refreshToken,
	})
	if err != nil {
		return res, err
	}

	tokenSet := map[string]any{}
	if data, ok := res["data"].(map[string]any); ok {
		if jwt, _ := data["jwtToken"].(string); jwt != "" {
			tokenSet["jwtToken"] = jwt
		}
		if rt, _ := data["refreshToken"].(string); rt != "" {
			tokenSet["refreshToken"] = rt
		}
	}
	tokenSet["clientcode"] = sc.userID
	return tokenSet, nil
}

func (sc *SmartConnect) GetProfile(refreshToken string) (map[string]any, error) {
	return sc.get("api.user.profile", map[string]any{"refreshToken": refreshToken})
}

// Orders
func (sc *SmartConnect) PlaceOrder(params map[string]any) (string, error) {
	cleanNil(params)
	res, err := sc.post("api.order.place", params)
	if err != nil {
		return "", err
	}
	st, _ := res["status"].(bool)
	if !st {
		return "", fmt.Errorf("place order failed: %v", res)
	}
	if data, ok := res["data"].(map[string]any); ok {
		if oid, _ := data["orderid"].(string); oid != "" {
			return oid, nil
		}
	}
	return "", fmt.Errorf("invalid response format: %v", res)
}

func (sc *SmartConnect) PlaceOrderFullResponse(params map[string]any) (map[string]any, error) {
	cleanNil(params)
	res, err := sc.post("api.order.placefullresponse", params)
	if err != nil {
		return nil, err
	}
	st, _ := res["status"].(bool)
	if !st {
		return nil, fmt.Errorf("place order failed: %v", res)
	}
	return res, nil
}

func (sc *SmartConnect) ModifyOrder(params map[string]any) (map[string]any, error) {
	cleanNil(params)
	return sc.post("api.order.modify", params)
}

func (sc *SmartConnect) CancelOrder(orderID, variety string) (map[string]any, error) {
	return sc.post("api.order.cancel", map[string]any{"variety": variety, "orderid": orderID})
}

func (sc *SmartConnect) OrderBook() (map[string]any, error)  { return sc.get("api.order.book", nil) }
func (sc *SmartConnect) TradeBook() (map[string]any, error)  { return sc.get("api.trade.book", nil) }
func (sc *SmartConnect) RMSLimit() (map[string]any, error)   { return sc.get("api.rms.limit", nil) }
func (sc *SmartConnect) Position() (map[string]any, error)   { return sc.get("api.position", nil) }
func (sc *SmartConnect) Holding() (map[string]any, error)    { return sc.get("api.holding", nil) }
func (sc *SmartConnect) AllHolding() (map[string]any, error) { return sc.get("api.allholding", nil) }

func (sc *SmartConnect) ConvertPosition(params map[string]any) (map[string]any, error) {
	cleanNil(params)
	return sc.post("api.convert.position", params)
}

// GTT
func (sc *SmartConnect) GTTCreateRule(params map[string]any) (string, error) {
	cleanNil(params)
	res, err := sc.post("api.gtt.create", params)
	if err != nil {
		return "", err
	}
	if data, ok := res["data"].(map[string]any); ok {
		if id, _ := data["id"].(string); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("unexpected response: %v", res)
}

func (sc *SmartConnect) GTTModifyRule(params map[string]any) (string, error) {
	cleanNil(params)
	res, err := sc.post("api.gtt.modify", params)
	if err != nil {
		return "", err
	}
	if data, ok := res["data"].(map[string]any); ok {
		if id, _ := data["id"].(string); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("unexpected response: %v", res)
}

func (sc *SmartConnect) GTTCancelRule(params map[string]any) (map[string]any, error) {
	cleanNil(params)
	return sc.post("api.gtt.cancel", params)
}

func (sc *SmartConnect) GTTDetails(id string) (map[string]any, error) {
	return sc.post("api.gtt.details", map[string]any{"id": id})
}

// status should be []string like []string{"CANCELLED"}
func (sc *SmartConnect) GTTLists(status []string, page, count int) (map[string]any, error) {
	params := map[string]any{"status": status, "page": page, "count": count}
	return sc.post("api.gtt.list", params)
}

// Market / Data
func (sc *SmartConnect) GetCandleData(params map[string]any) (map[string]any, error) {
	cleanNil(params)
	return sc.post("api.candle.data", params)
}
func (sc *SmartConnect) GetOIData(params map[string]any) (map[string]any, error) {
	cleanNil(params)
	return sc.post("api.oi.data", params)
}
func (sc *SmartConnect) GetMarketData(mode string, exchangeTokens any) (map[string]any, error) {
	params := map[string]any{"mode": mode, "exchangeTokens": exchangeTokens}
	return sc.post("api.market.data", params)
}

func (sc *SmartConnect) SearchScrip(exchange, searchscrip string) (map[string]any, error) {
	res, err := sc.post("api.search.scrip", map[string]any{"exchange": exchange, "searchscrip": searchscrip})
	if err != nil {
		return res, err
	}
	// Log informational messages similar to Python (optional)
	return res, nil
}

// Authenticated GET to arbitrary URL (used by individual order details)
func (sc *SmartConnect) MakeAuthenticatedGET(urlStr string, accessToken string) (map[string]any, int, error) {
	if accessToken == "" {
		accessToken = sc.accessToken
	}
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, 0, err
	}
	hdr := sc.requestHeaders()
	if accessToken != "" {
		hdr.Set("Authorization", "Bearer "+accessToken)
	}
	req.Header = hdr

	resp, err := sc.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("Error in MakeAuthenticatedGET: status=%d body=%s", resp.StatusCode, string(raw))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

func (sc *SmartConnect) IndividualOrderDetails(qParam string) (map[string]any, error) {
	base := sc.rootURL + routes["api.individual.order.details"] + qParam
	out, _, err := func() (map[string]any, int, error) { return sc.MakeAuthenticatedGET(base, sc.accessToken) }()
	return out, err
}

// Margin & Brokerage, eDIS, MarketData extras
func (sc *SmartConnect) GetMarginAPI(params map[string]any) (map[string]any, error) {
	return sc.post("api.margin.api", params)
}
func (sc *SmartConnect) EstimateCharges(params map[string]any) (map[string]any, error) {
	return sc.post("api.estimateCharges", params)
}
func (sc *SmartConnect) VerifyDis(params map[string]any) (map[string]any, error) {
	return sc.post("api.verifyDis", params)
}
func (sc *SmartConnect) GenerateTPIN(params map[string]any) (map[string]any, error) {
	return sc.post("api.generateTPIN", params)
}
func (sc *SmartConnect) GetTranStatus(params map[string]any) (map[string]any, error) {
	return sc.post("api.getTranStatus", params)
}
func (sc *SmartConnect) OptionGreek(params map[string]any) (map[string]any, error) {
	return sc.post("api.optionGreek", params)
}
func (sc *SmartConnect) GainersLosers(params map[string]any) (map[string]any, error) {
	return sc.post("api.gainersLosers", params)
}
func (sc *SmartConnect) PutCallRatio() (map[string]any, error) {
	return sc.get("api.putCallRatio", nil)
}
func (sc *SmartConnect) NSEIntraday() (map[string]any, error) { return sc.get("api.nseIntraday", nil) }
func (sc *SmartConnect) BSEIntraday() (map[string]any, error) { return sc.get("api.bseIntraday", nil) }
func (sc *SmartConnect) OIBuildup(params map[string]any) (map[string]any, error) {
	return sc.post("api.oIBuildup", params)
}

// ---- Utils ----

func cleanNil(m map[string]any) {
	for k, v := range m {
		if v == nil {
			delete(m, k)
		}
	}
}
