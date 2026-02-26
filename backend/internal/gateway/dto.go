package gateway

// TFInfo is the REST response type for /api/tfs.
type TFInfo struct {
	Seconds int    `json:"seconds"`
	Label   string `json:"label"`
}

// CandleOut is the REST response type for /api/candles.
type CandleOut struct {
	TS       string  `json:"ts"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	Volume   float64 `json:"volume"`
	Count    float64 `json:"count"`
	Token    string  `json:"token"`
	Exchange string  `json:"exchange"`
	TF       int     `json:"tf"`
	Forming  bool    `json:"forming"`
}

// IndPoint is the REST response type for /api/indicators/history.
type IndPoint struct {
	Value float64 `json:"value"`
	TS    string  `json:"ts"`
	Ready bool    `json:"ready"`
}
