package strategy

import (
	"log"

	"trading-systemv1/internal/model"
)

// SMACrossover implements a simple SMA crossover strategy.
//
// Buy signal: fast SMA crosses above slow SMA (golden cross)
// Sell signal: fast SMA crosses below slow SMA (death cross)
//
// Optional RSI filter prevents buying when overbought (>70)
// or selling when oversold (<30).
type SMACrossover struct {
	name       string
	fastPeriod int
	slowPeriod int
	qty        int64

	// Ring buffers for SMA calculation
	fastBuf []int64
	slowBuf []int64
	fastIdx int
	slowIdx int
	fastSum int64
	slowSum int64
	count   int

	// Previous SMA values for crossover detection
	prevFast float64
	prevSlow float64
	ready    bool

	// RSI filter (optional)
	rsiEnabled bool
	rsiPeriod  int
	rsiGain    float64
	rsiLoss    float64
	prevClose  int64
	rsiCount   int
	lastRSI    float64
}

// NewSMACrossover creates a new SMA crossover strategy.
// fastPeriod < slowPeriod (e.g., 9 and 21).
// qty is the number of shares per trade.
func NewSMACrossover(fastPeriod, slowPeriod int, qty int64, enableRSI bool, rsiPeriod int) *SMACrossover {
	return &SMACrossover{
		name:       "SMA_Crossover",
		fastPeriod: fastPeriod,
		slowPeriod: slowPeriod,
		qty:        qty,
		fastBuf:    make([]int64, fastPeriod),
		slowBuf:    make([]int64, slowPeriod),
		rsiEnabled: enableRSI,
		rsiPeriod:  rsiPeriod,
	}
}

func (s *SMACrossover) Name() string {
	return s.name
}

func (s *SMACrossover) OnTick(tick model.Tick) {
	// No-op: we operate on candles only
}

func (s *SMACrossover) OnCandle(candle model.Candle) *Signal {
	price := candle.Close
	s.count++

	// Update RSI if enabled
	if s.rsiEnabled && s.count > 1 {
		s.updateRSI(price)
	}
	s.prevClose = price

	// Update fast SMA ring buffer
	s.fastSum -= s.fastBuf[s.fastIdx]
	s.fastBuf[s.fastIdx] = price
	s.fastSum += price
	s.fastIdx = (s.fastIdx + 1) % s.fastPeriod

	// Update slow SMA ring buffer
	s.slowSum -= s.slowBuf[s.slowIdx]
	s.slowBuf[s.slowIdx] = price
	s.slowSum += price
	s.slowIdx = (s.slowIdx + 1) % s.slowPeriod

	// Need enough data for both SMAs
	if s.count < s.slowPeriod {
		return nil
	}

	fastSMA := float64(s.fastSum) / float64(s.fastPeriod)
	slowSMA := float64(s.slowSum) / float64(s.slowPeriod)

	defer func() {
		s.prevFast = fastSMA
		s.prevSlow = slowSMA
		s.ready = true
	}()

	if !s.ready {
		return nil
	}

	// Golden cross: fast crosses above slow
	if s.prevFast <= s.prevSlow && fastSMA > slowSMA {
		if s.rsiEnabled && s.lastRSI > 70 {
			log.Printf("[strategy] %s: golden cross filtered by RSI %.1f > 70", s.name, s.lastRSI)
			return nil
		}
		return &Signal{
			StrategyName: s.name,
			Action:       ActionBuy,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Price:        0, // market order
			Reason:       "SMA golden cross (fast > slow)",
		}
	}

	// Death cross: fast crosses below slow
	if s.prevFast >= s.prevSlow && fastSMA < slowSMA {
		if s.rsiEnabled && s.lastRSI < 30 {
			log.Printf("[strategy] %s: death cross filtered by RSI %.1f < 30", s.name, s.lastRSI)
			return nil
		}
		return &Signal{
			StrategyName: s.name,
			Action:       ActionSell,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Price:        0,
			Reason:       "SMA death cross (fast < slow)",
		}
	}

	return nil
}

func (s *SMACrossover) updateRSI(price int64) {
	change := float64(price - s.prevClose)
	s.rsiCount++

	if s.rsiCount <= s.rsiPeriod {
		if change > 0 {
			s.rsiGain += change
		} else {
			s.rsiLoss -= change
		}
		if s.rsiCount == s.rsiPeriod {
			s.rsiGain /= float64(s.rsiPeriod)
			s.rsiLoss /= float64(s.rsiPeriod)
		}
	} else {
		n := float64(s.rsiPeriod)
		if change > 0 {
			s.rsiGain = (s.rsiGain*(n-1) + change) / n
			s.rsiLoss = (s.rsiLoss * (n - 1)) / n
		} else {
			s.rsiGain = (s.rsiGain * (n - 1)) / n
			s.rsiLoss = (s.rsiLoss*(n-1) - change) / n
		}
	}

	if s.rsiLoss == 0 {
		s.lastRSI = 100
	} else {
		rs := s.rsiGain / s.rsiLoss
		s.lastRSI = 100 - (100 / (1 + rs))
	}
}
