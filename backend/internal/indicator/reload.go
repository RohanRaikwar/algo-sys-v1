package indicator

import (
	"fmt"
	"log"
)

// ReloadConfigs updates the indicator engine with new configurations.
// It preserves state for indicators that already exist and only creates
// new instances for genuinely new indicators. This prevents losing
// accumulated state (warmup history) when adding a new indicator.
// Returns the number of preserved and new indicator instances.
func (e *Engine) ReloadConfigs(newConfigs []TFIndicatorConfig) (preserved, created int) {
	// Build lookup of old configs + state by TF
	oldCfgByTF := make(map[int]TFIndicatorConfig)
	oldStateByTF := make(map[int]map[string]*tokenIndicators)
	for i, cfg := range e.configs {
		oldCfgByTF[cfg.TF] = cfg
		oldStateByTF[cfg.TF] = e.state[i]
	}

	// Build new state array
	newState := make([]map[string]*tokenIndicators, len(newConfigs))
	for i, newCfg := range newConfigs {
		oldCfg, tfExists := oldCfgByTF[newCfg.TF]
		oldTFState := oldStateByTF[newCfg.TF]

		if !tfExists || oldTFState == nil {
			// Brand-new TF — cold-start
			newState[i] = make(map[string]*tokenIndicators, 64)
			created++
			log.Printf("[reload] TF=%d: new timeframe, cold-starting", newCfg.TF)
			continue
		}

		// TF exists — check if indicators are identical (fast path)
		if indicatorSetsEqual(oldCfg.Indicators, newCfg.Indicators) {
			newState[i] = oldTFState
			preserved += len(oldTFState)
			log.Printf("[reload] TF=%d: unchanged, preserved %d token states", newCfg.TF, len(oldTFState))
			continue
		}

		// Indicator set changed — migrate per-token state
		// Build index: which old indicators map to which new positions
		migrated := make(map[string]*tokenIndicators, len(oldTFState))
		for tokenKey, oldTI := range oldTFState {
			newTI := migrateTokenIndicators(oldTI, oldCfg.Indicators, newCfg.Indicators)
			migrated[tokenKey] = newTI
			preserved++
		}
		newState[i] = migrated
		created++ // mark that new indicators need backfill
		log.Printf("[reload] TF=%d: migrated %d token states (added new indicators)", newCfg.TF, len(migrated))
	}

	e.configs = newConfigs
	e.state = newState

	// Rebuild TF index for O(1) lookup
	e.tfIndex = make(map[int]int, len(newConfigs))
	for i, cfg := range newConfigs {
		e.tfIndex[cfg.TF] = i
	}

	log.Printf("[reload] ✅ config reloaded: %d configs, %d preserved, %d new",
		len(newConfigs), preserved, created)

	return preserved, created
}

// migrateTokenIndicators creates a new tokenIndicators for the new config,
// preserving state from existing indicators that match by Type+Period.
func migrateTokenIndicators(oldTI *tokenIndicators, oldConfigs, newConfigs []IndicatorConfig) *tokenIndicators {
	// Build lookup of old indicators by "TYPE_PERIOD"
	oldByKey := make(map[string]Indicator, len(oldTI.indicators))
	for i, cfg := range oldTI.configs {
		key := cfg.Type + "_" + itoaInd(cfg.Period)
		oldByKey[key] = oldTI.indicators[i]
	}

	// Build new indicator instances, reusing old ones where possible
	newInds := make([]Indicator, len(newConfigs))
	for i, cfg := range newConfigs {
		key := cfg.Type + "_" + itoaInd(cfg.Period)
		if existing, ok := oldByKey[key]; ok {
			newInds[i] = existing // preserve accumulated state
		} else {
			// New indicator — create fresh instance
			switch cfg.Type {
			case "SMA":
				newInds[i] = NewSMA(cfg.Period)
			case "EMA":
				newInds[i] = NewEMA(cfg.Period)
			case "SMMA":
				newInds[i] = NewSMMA(cfg.Period)
			case "RSI":
				newInds[i] = NewRSI(cfg.Period)
			default:
				newInds[i] = NewSMA(cfg.Period)
			}
		}
	}

	return &tokenIndicators{
		indicators: newInds,
		configs:    newConfigs,
	}
}

// findConfig returns the TFIndicatorConfig for the given TF, or empty if not found.
func (e *Engine) findConfig(tf int) TFIndicatorConfig {
	for _, cfg := range e.configs {
		if cfg.TF == tf {
			return cfg
		}
	}
	return TFIndicatorConfig{}
}

// indicatorSetsEqual checks if two indicator config slices have the exact same
// set of indicators (order-independent).
func indicatorSetsEqual(a, b []IndicatorConfig) bool {
	if len(a) != len(b) {
		return false
	}
	setA := make(map[string]bool, len(a))
	for _, ic := range a {
		setA[ic.Type+"_"+itoaInd(ic.Period)] = true
	}
	for _, ic := range b {
		if !setA[ic.Type+"_"+itoaInd(ic.Period)] {
			return false
		}
	}
	return true
}

// ValidateConfigs checks a set of TFIndicatorConfigs for errors.
func ValidateConfigs(configs []TFIndicatorConfig) error {
	seen := make(map[int]bool)
	for _, cfg := range configs {
		if cfg.TF <= 0 {
			return fmt.Errorf("invalid TF=%d: must be positive", cfg.TF)
		}
		if seen[cfg.TF] {
			return fmt.Errorf("duplicate TF=%d", cfg.TF)
		}
		seen[cfg.TF] = true

		for _, ind := range cfg.Indicators {
			switch ind.Type {
			case "SMA", "EMA", "SMMA", "RSI":
				// valid
			default:
				return fmt.Errorf("unknown indicator type %q for TF=%d", ind.Type, cfg.TF)
			}
			if ind.Period <= 0 {
				return fmt.Errorf("invalid period=%d for %s on TF=%d", ind.Period, ind.Type, cfg.TF)
			}
		}
	}
	return nil
}
