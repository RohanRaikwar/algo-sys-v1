package indicator

import (
	"encoding/json"
	"fmt"
	"log"
	"trading-systemv1/internal/model"
)

// Snapshottable is implemented by indicators that support state serialization.
type Snapshottable interface {
	Indicator
	Snapshot() IndicatorSnapshot
	RestoreFromSnapshot(snap IndicatorSnapshot) error
}

// IndicatorSnapshot holds the serialized state of a single indicator instance.
type IndicatorSnapshot struct {
	Type   string `json:"type"`   // "SMA", "EMA", "SMMA", "RSI"
	Period int    `json:"period"` // indicator period

	// SMA fields
	Buf     []float64 `json:"buf,omitempty"`
	Idx     int       `json:"idx,omitempty"`
	Count   int       `json:"count"`
	Sum     float64   `json:"sum,omitempty"`
	Current float64   `json:"current"`

	// EMA fields
	Multiplier float64 `json:"multiplier,omitempty"`

	// RSI fields
	PrevClose float64 `json:"prev_close,omitempty"`
	AvgGain   float64 `json:"avg_gain,omitempty"`
	AvgLoss   float64 `json:"avg_loss,omitempty"`
}

// TokenSnapshot holds indicator snapshots for a single token within a TF.
type TokenSnapshot struct {
	Token      string              `json:"token"`
	Exchange   string              `json:"exchange"`
	TF         int                 `json:"tf"`
	Indicators []IndicatorSnapshot `json:"indicators"`
}

// EngineSnapshot holds the full state of the indicator engine.
type EngineSnapshot struct {
	StreamID string          `json:"stream_id"` // Redis Stream ID at checkpoint time
	Tokens   []TokenSnapshot `json:"tokens"`
	Version  int             `json:"version"` // schema version for forward compat
}

// MarshalJSON serializes the engine snapshot to JSON.
func (es *EngineSnapshot) MarshalJSON() ([]byte, error) {
	type Alias EngineSnapshot
	return json.Marshal((*Alias)(es))
}

// UnmarshalJSON deserializes the engine snapshot from JSON.
func (es *EngineSnapshot) UnmarshalJSON(data []byte) error {
	type Alias EngineSnapshot
	return json.Unmarshal(data, (*Alias)(es))
}

// SnapshotEngine captures the full state of an indicator Engine.
func SnapshotEngine(e *Engine, streamID string) (*EngineSnapshot, error) {
	snap := &EngineSnapshot{
		StreamID: streamID,
		Version:  1,
	}

	for tfIdx, cfg := range e.configs {
		for tokenKey, ti := range e.state[tfIdx] {
			ts := TokenSnapshot{
				Token:      tokenKey,
				TF:         cfg.TF,
				Indicators: make([]IndicatorSnapshot, 0, len(ti.indicators)),
			}
			// Extract exchange from tokenKey if present (format: "exchange:token")
			// The key format from TFCandle.Key() is "exchange:token"
			for i := range tokenKey {
				if tokenKey[i] == ':' {
					ts.Exchange = tokenKey[:i]
					ts.Token = tokenKey[i+1:]
					break
				}
			}
			if ts.Exchange == "" {
				ts.Token = tokenKey
			}

			for _, ind := range ti.indicators {
				si, ok := ind.(Snapshottable)
				if !ok {
					return nil, fmt.Errorf("indicator %s does not implement Snapshottable", ind.Name())
				}
				ts.Indicators = append(ts.Indicators, si.Snapshot())
			}
			snap.Tokens = append(snap.Tokens, ts)
		}
	}

	return snap, nil
}

// RestoreEngine rebuilds an indicator Engine from a snapshot.
// It is tolerant of config changes — indicators are matched by Type+Period
// rather than by index. Matching indicators get their state restored; new
// indicators start fresh (cold). Removed indicators are silently skipped.
func RestoreEngine(configs []TFIndicatorConfig, snap *EngineSnapshot) (*Engine, error) {
	e := NewEngine(configs)

	for _, ts := range snap.Tokens {
		// Find matching TF config
		tfIdx := -1
		for i, cfg := range e.configs {
			if cfg.TF == ts.TF {
				tfIdx = i
				break
			}
		}
		if tfIdx == -1 {
			continue // TF no longer configured — skip
		}

		ti := e.createTokenIndicators(tfIdx)

		// Build a lookup: "SMA:9" → IndicatorSnapshot for fast matching
		snapLookup := make(map[string]IndicatorSnapshot, len(ts.Indicators))
		for _, indSnap := range ts.Indicators {
			lookupKey := indSnap.Type + ":" + model.Itoa(indSnap.Period)
			snapLookup[lookupKey] = indSnap
		}

		// Match current indicators against snapshot by Type+Period
		restored, cold := 0, 0
		for i, ind := range ti.indicators {
			cfg := ti.configs[i]
			lookupKey := cfg.Type + ":" + model.Itoa(cfg.Period)

			indSnap, found := snapLookup[lookupKey]
			if !found {
				cold++
				continue // new indicator — stays fresh/zero
			}

			si, ok := ind.(Snapshottable)
			if !ok {
				cold++
				continue
			}
			if err := si.RestoreFromSnapshot(indSnap); err != nil {
				// Non-fatal: log and leave cold
				cold++
				continue
			}
			restored++
		}

		if cold > 0 {
			log.Printf("[restorer] TF=%d token=%s: restored %d, cold-started %d indicators",
				ts.TF, ts.Token, restored, cold)
		}

		// Reconstruct the token key
		key := ts.Token
		if ts.Exchange != "" {
			key = ts.Exchange + ":" + ts.Token
		}
		e.state[tfIdx][key] = ti
	}

	return e, nil
}
