package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Mode is the complexity of an event watch. The mode is a property of the
// shared event, not of an individual subscription.
type Mode string

const (
	ModeBasic    Mode = "basic"
	ModeScripted Mode = "scripted"
	ModeAI       Mode = "ai_assisted"
)

// Valid reports whether m is a known watch mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeBasic, ModeScripted, ModeAI:
		return true
	default:
		return false
	}
}

// Entitlements is the typed form of a plan's limits. Clients receive it from
// /api/v1/me; the server uses the same values for enforcement.
type Entitlements struct {
	PlanCode              string `json:"plan_code"`
	MaxActiveWatches      int    `json:"max_active_watches"`
	AllowedModes          []Mode `json:"allowed_modes"`
	MinIntervalSeconds    int    `json:"min_interval_seconds"`
	AISetupPreviewsPerDay int    `json:"ai_setup_previews_per_day"`
	AIChecksPerDay        int    `json:"ai_checks_per_day"`
	HistoryDays           int    `json:"history_days"`
}

// AllowsMode reports whether the plan permits a watch mode.
func (e Entitlements) AllowsMode(m Mode) bool {
	for _, allowed := range e.AllowedModes {
		if allowed == m {
			return true
		}
	}
	return false
}

// MinInterval returns the plan's minimum check interval.
func (e Entitlements) MinInterval() time.Duration {
	return time.Duration(e.MinIntervalSeconds) * time.Second
}

// Validate checks values that must never be trusted from the plans table.
func (e Entitlements) Validate() error {
	if e.MaxActiveWatches < 0 {
		return errors.New("max_active_watches must not be negative")
	}
	if e.MinIntervalSeconds <= 0 {
		return errors.New("min_interval_seconds must be positive")
	}
	if e.AISetupPreviewsPerDay < 0 || e.AIChecksPerDay < 0 {
		return errors.New("AI quotas must not be negative")
	}
	if e.HistoryDays < 0 {
		return errors.New("history_days must not be negative")
	}
	seen := make(map[Mode]struct{}, len(e.AllowedModes))
	for _, mode := range e.AllowedModes {
		if !mode.Valid() {
			return fmt.Errorf("unknown watch mode %q", mode)
		}
		if _, ok := seen[mode]; ok {
			return fmt.Errorf("duplicate watch mode %q", mode)
		}
		seen[mode] = struct{}{}
	}
	if len(e.AllowedModes) == 0 {
		return errors.New("allowed_modes must not be empty")
	}
	return nil
}

// MergeLimits applies the support override object on top of the plan limits
// and decodes the result into typed entitlements. The merge is deliberately a
// shallow JSON-object merge: an override replaces a whole limit, it never
// mutates nested data.
func MergeLimits(planCode string, base, overrides json.RawMessage) (Entitlements, error) {
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return Entitlements{}, fmt.Errorf("decode plan limits: %w", err)
	}
	if len(overrides) > 0 && string(overrides) != "null" {
		var patch map[string]json.RawMessage
		if err := json.Unmarshal(overrides, &patch); err != nil {
			return Entitlements{}, fmt.Errorf("decode plan overrides: %w", err)
		}
		for key, value := range patch {
			merged[key] = value
		}
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return Entitlements{}, fmt.Errorf("encode merged limits: %w", err)
	}
	var ent Entitlements
	if err := json.Unmarshal(raw, &ent); err != nil {
		return Entitlements{}, fmt.Errorf("decode entitlements: %w", err)
	}
	ent.PlanCode = planCode
	if err := ent.Validate(); err != nil {
		return Entitlements{}, fmt.Errorf("invalid entitlements for plan %q: %w", planCode, err)
	}
	return ent, nil
}
