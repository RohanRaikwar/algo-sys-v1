package model

// Instrument represents a tradeable instrument/symbol.
type Instrument struct {
	Token          string `json:"token"`
	Exchange       string `json:"exchange"`
	TradingSymbol  string `json:"trading_symbol"`
	Name           string `json:"name"`
	InstrumentType string `json:"instrument_type"` // EQ, FUT, CE, PE
	LotSize        int    `json:"lot_size"`
	TickSize       int64  `json:"tick_size"` // minimum price movement in paise
}

// Key returns a unique key for this instrument: "exchange:token".
func (i *Instrument) Key() string {
	return i.Exchange + ":" + i.Token
}
