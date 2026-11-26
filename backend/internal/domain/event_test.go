package domain

import (
	"testing"
	"time"
)

func TestNormaliseEventName(t *testing.T) {
	if got, err := NormaliseEventName("  Saturday show  "); err != nil || got != "Saturday show" {
		t.Fatalf("NormaliseEventName = %q, %v", got, err)
	}
	if _, err := NormaliseEventName("   "); err == nil {
		t.Fatal("empty name must be rejected")
	}
	if _, err := NormaliseEventName("bad\nname"); err == nil {
		t.Fatal("control characters must be rejected")
	}
	long := make([]rune, MaxEventNameLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NormaliseEventName(string(long)); err == nil {
		t.Fatal("over-long name must be rejected")
	}
}

func TestNormaliseCheckInterval(t *testing.T) {
	if got, err := NormaliseCheckInterval(0); err != nil || got != DefaultCheckInterval {
		t.Fatalf("default interval = %d, %v", got, err)
	}
	for _, value := range []int{MinCheckInterval, 900, MaxCheckInterval} {
		if got, err := NormaliseCheckInterval(value); err != nil || got != value {
			t.Fatalf("interval %d = %d, %v", value, got, err)
		}
	}
	for _, value := range []int{-1, 59, 86401} {
		if _, err := NormaliseCheckInterval(value); err == nil {
			t.Fatalf("interval %d must be rejected", value)
		}
	}
}

func TestComputeCheckTransitionPreservesOnStateOnError(t *testing.T) {
	now := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	current := Event{
		CheckIntervalSeconds: 300,
		Enabled:              true,
		IsOn:                 true,
		NotifiedOn:           true,
	}

	tr1 := ComputeCheckTransition(current, CheckRecord{
		Outcome:   OutcomeError,
		ErrorText: "upstream 503",
	}, now)
	if !tr1.IsOn || !tr1.NotifiedOn {
		t.Fatalf("transient error must preserve IsOn/NotifiedOn: got on=%v notified=%v", tr1.IsOn, tr1.NotifiedOn)
	}
	if tr1.ConsecutiveFailures != 1 {
		t.Fatalf("failures = %d, want 1", tr1.ConsecutiveFailures)
	}
	if tr1.NextCheckAt == nil || !tr1.NextCheckAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("first error backoff = %v, want %v", tr1.NextCheckAt, now.Add(10*time.Minute))
	}

	current.ConsecutiveFailures = tr1.ConsecutiveFailures
	tr2 := ComputeCheckTransition(current, CheckRecord{Outcome: OutcomeError, ErrorText: "timeout"}, now)
	if tr2.NextCheckAt == nil || !tr2.NextCheckAt.Equal(now.Add(20*time.Minute)) {
		t.Fatalf("second error backoff = %v, want %v", tr2.NextCheckAt, now.Add(20*time.Minute))
	}
}

func TestComputeCheckTransitionBlocksAndAutoPauses(t *testing.T) {
	now := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	event := Event{
		CheckIntervalSeconds: 300,
		Enabled:              true,
		IsOn:                 true,
		NotifiedOn:           true,
	}

	// Block 1 with short Retry-After must still respect 15-minute floor.
	tr1 := ComputeCheckTransition(event, CheckRecord{
		Outcome:    OutcomeBlocked,
		ErrorText:  "HTTP 429",
		RetryAfter: 30 * time.Second,
	}, now)
	if !tr1.Enabled || tr1.JustAutoPaused || tr1.ConsecutiveBlocks != 1 {
		t.Fatalf("block 1 = %+v", tr1)
	}
	if tr1.NextCheckAt == nil || !tr1.NextCheckAt.Equal(now.Add(MinBlockedBackoff)) {
		t.Fatalf("block 1 next = %v, want +15m", tr1.NextCheckAt)
	}
	if !tr1.IsOn || !tr1.NotifiedOn {
		t.Fatal("blocked check must preserve IsOn/NotifiedOn")
	}

	// Block 2 with long Retry-After honours Retry-After.
	event.ConsecutiveFailures = tr1.ConsecutiveFailures
	event.ConsecutiveBlocks = tr1.ConsecutiveBlocks
	tr2 := ComputeCheckTransition(event, CheckRecord{
		Outcome:    OutcomeBlocked,
		ErrorText:  "HTTP 429",
		RetryAfter: 45 * time.Minute,
	}, now)
	if tr2.NextCheckAt == nil || !tr2.NextCheckAt.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("block 2 next = %v, want +45m", tr2.NextCheckAt)
	}

	// Block 3 auto-pauses the watch and signals JustAutoPaused.
	event.ConsecutiveFailures = tr2.ConsecutiveFailures
	event.ConsecutiveBlocks = tr2.ConsecutiveBlocks
	tr3 := ComputeCheckTransition(event, CheckRecord{
		Outcome:   OutcomeBlocked,
		ErrorText: "HTTP 403",
	}, now)
	if tr3.Enabled || !tr3.JustAutoPaused || tr3.NextCheckAt != nil || tr3.PausedReason == "" {
		t.Fatalf("block 3 must auto-pause: %+v", tr3)
	}
}
