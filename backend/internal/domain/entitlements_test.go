package domain

import (
	"encoding/json"
	"testing"
)

func TestMergeLimitsAppliesOverrides(t *testing.T) {
	base := json.RawMessage(`{
		"max_active_watches": 3,
		"allowed_modes": ["basic"],
		"min_interval_seconds": 900,
		"ai_setup_previews_per_day": 0,
		"ai_checks_per_day": 0,
		"history_days": 30
	}`)
	overrides := json.RawMessage(`{
		"max_active_watches": 9,
		"allowed_modes": ["basic", "ai_assisted"]
	}`)
	ent, err := MergeLimits("pro", base, overrides)
	if err != nil {
		t.Fatalf("MergeLimits: %v", err)
	}
	if ent.PlanCode != "pro" {
		t.Fatalf("PlanCode = %q, want pro", ent.PlanCode)
	}
	if ent.MaxActiveWatches != 9 {
		t.Fatalf("MaxActiveWatches = %d, want 9", ent.MaxActiveWatches)
	}
	if !ent.AllowsMode(ModeAI) || ent.AllowsMode(ModeScripted) {
		t.Fatalf("unexpected mode entitlement: %+v", ent.AllowedModes)
	}
	if ent.MinInterval() <= 0 {
		t.Fatal("MinInterval must be positive")
	}
}

func TestMergeLimitsRejectsUnknownMode(t *testing.T) {
	base := json.RawMessage(`{
		"max_active_watches": 1,
		"allowed_modes": ["telepathy"],
		"min_interval_seconds": 60,
		"ai_setup_previews_per_day": 0,
		"ai_checks_per_day": 0,
		"history_days": 1
	}`)
	if _, err := MergeLimits("bad", base, nil); err == nil {
		t.Fatal("expected an unknown mode to be rejected")
	}
}

func TestValidateRejectsEmptyModes(t *testing.T) {
	ent := Entitlements{
		MaxActiveWatches:   0,
		AllowedModes:       nil,
		MinIntervalSeconds: 60,
	}
	if err := ent.Validate(); err == nil {
		t.Fatal("expected empty allowed_modes to be rejected")
	}
}
