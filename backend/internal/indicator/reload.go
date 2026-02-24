package indicator

import (
	"fmt"
	"log"
)

// ReloadConfigs updates the indicator engine with new configurations.
// It preserves state for unchanged TF+indicator combos and reinitializes
// only the indicators that changed. Returns the number of preserved and
// new indicator instances.
func (e *Engine) ReloadConfigs(newConfigs []TFIndicatorConfig) (preserved, created int) {
	// Build lookup of old state by TF
	oldStateByTF := make(map[int]map[string]*tokenIndicators)
	for i, cfg := range e.configs {
		oldStateByTF[cfg.TF] = e.state[i]
	}

	// Build new state array
	newState := make([]map[string]*tokenIndicators, len(newConfigs))
	for i, newCfg := range newConfigs {
		if oldTFState, ok := oldStateByTF[newCfg.TF]; ok {
			// TF exists in old config — check if indicator set matches
			if indicatorConfigsMatch(e.findConfig(newCfg.TF), newCfg) {
				// Same indicators — preserve entire TF state
				newState[i] = oldTFState
				for range oldTFState {
					preserved++
				}
				log.Printf("[reload] TF=%d: preserved %d token states", newCfg.TF, len(oldTFState))
			} else {
				// Different indicators — cold-start this TF
				newState[i] = make(map[string]*tokenIndicators, 64)
				created++
				log.Printf("[reload] TF=%d: indicator config changed, cold-starting", newCfg.TF)
			}
		} else {
			// New TF — fresh state
			newState[i] = make(map[string]*tokenIndicators, 64)
			created++
			log.Printf("[reload] TF=%d: new timeframe, cold-starting", newCfg.TF)
		}
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

// findConfig returns the TFIndicatorConfig for the given TF, or empty if not found.
func (e *Engine) findConfig(tf int) TFIndicatorConfig {
	for _, cfg := range e.configs {
		if cfg.TF == tf {
			return cfg
		}
	}
	return TFIndicatorConfig{}
}

// indicatorConfigsMatch compares two TFIndicatorConfigs to see if they define
// the same set of indicators.
func indicatorConfigsMatch(a, b TFIndicatorConfig) bool {
	if a.TF != b.TF {
		return false
	}
	if len(a.Indicators) != len(b.Indicators) {
		return false
	}
	for i := range a.Indicators {
		ai := a.Indicators[i]
		bi := b.Indicators[i]
		if ai.Type != bi.Type || ai.Period != bi.Period {
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
