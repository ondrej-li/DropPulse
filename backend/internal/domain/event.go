package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// CheckOutcome classifies one availability check.
type CheckOutcome string

const (
	// OutcomeOK means the target was reached and interpreted cleanly.
	OutcomeOK CheckOutcome = "ok"
	// OutcomeError means a network, timeout, upstream 5xx, or parse error occurred.
	OutcomeError CheckOutcome = "error"
	// OutcomeBlocked means the target returned a 403/429 or anti-bot challenge.
	OutcomeBlocked CheckOutcome = "blocked"
)

// Event is one watched availability page.
type Event struct {
	ID                   string       `json:"id"`
	UserID               string       `json:"-"`
	Name                 string       `json:"name"`
	URL                  string       `json:"url"`
	CheckIntervalSeconds int          `json:"check_interval_seconds"`
	Enabled              bool         `json:"enabled"`
	IsOn                 bool         `json:"is_on"`
	LastCheckedAt        *time.Time   `json:"last_checked_at,omitempty"`
	NextCheckAt          *time.Time   `json:"next_check_at,omitempty"`
	LastStatusCode       *int         `json:"last_status_code,omitempty"`
	LastOutcome          CheckOutcome `json:"last_outcome,omitempty"`
	LastDetail           string       `json:"last_detail,omitempty"`
	LastError            string       `json:"last_error,omitempty"`
	PausedReason         string       `json:"paused_reason,omitempty"`
	ConsecutiveFailures  int          `json:"consecutive_failures"`
	ConsecutiveBlocks    int          `json:"consecutive_blocks"`
	CheckCount           int64        `json:"check_count"`
	ErrorCount           int64        `json:"error_count"`
	LastDurationMs       *int         `json:"last_duration_ms,omitempty"`
	LastNotifiedAt       *time.Time   `json:"last_notified_at,omitempty"`
	LastNotificationErr  string       `json:"last_notification_error,omitempty"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`

	// UserEmail is the verified address on the owning account, when there is
	// one. It is loaded for notifications and never exposed by the API.
	UserEmail string `json:"-"`

	// NotifiedOn is the edge-trigger state: true once the on transition has
	// produced an email. It is deliberately not exposed by the API.
	NotifiedOn bool `json:"-"`
}

// CheckTrigger identifies what initiated a check.
type CheckTrigger string

const (
	TriggerScheduled   CheckTrigger = "scheduled"
	TriggerManual      CheckTrigger = "manual"
	TriggerAdminManual CheckTrigger = "admin_manual"
)

// CheckRecord carries the normalised result of one check into storage.
type CheckRecord struct {
	Trigger    CheckTrigger
	Outcome    CheckOutcome
	StatusCode *int
	Available  *bool
	Detail     string
	ErrorText  string
	RetryAfter time.Duration
	DurationMs int
}

// CheckTransition is the next persisted state derived from a check outcome.
type CheckTransition struct {
	IsOn                bool
	NotifiedOn          bool
	Enabled             bool
	PausedReason        string
	ConsecutiveFailures int
	ConsecutiveBlocks   int
	NextCheckAt         *time.Time
	Outcome             CheckOutcome
	JustAutoPaused      bool
}

const (
	// MaxErrorBackoff caps exponential backoff on retryable upstream errors.
	MaxErrorBackoff = 6 * time.Hour
	// MinBlockedBackoff is the minimum wait after a 429/403/challenge response.
	MinBlockedBackoff = 15 * time.Minute
	// MaxBlockedBackoff caps Retry-After and repeated-block delays.
	MaxBlockedBackoff = 24 * time.Hour
	// MaxConsecutiveBlocks is the number of consecutive blocks before an event
	// is automatically paused instead of continuing to hit the target site.
	MaxConsecutiveBlocks = 3
)

// ComputeCheckTransition applies availability, backoff, and auto-pause rules
// to an event. Crucially, upstream errors and blocks preserve IsOn and
// NotifiedOn so a transient glitch never marks an available event as sold out
// or re-arms duplicate notifications.
func ComputeCheckTransition(current Event, rec CheckRecord, now time.Time) CheckTransition {
	outcome := rec.Outcome
	if outcome == "" {
		if rec.ErrorText != "" {
			outcome = OutcomeError
		} else {
			outcome = OutcomeOK
		}
	}

	interval := time.Duration(current.CheckIntervalSeconds) * time.Second
	if interval < MinCheckInterval*time.Second {
		interval = DefaultCheckInterval * time.Second
	}

	switch outcome {
	case OutcomeOK:
		isOn := rec.StatusCode != nil && *rec.StatusCode == 200
		if rec.Available != nil {
			isOn = *rec.Available
		}
		next := now.Add(interval)
		return CheckTransition{
			IsOn:                isOn,
			NotifiedOn:          isOn && current.IsOn && current.NotifiedOn,
			Enabled:             current.Enabled,
			PausedReason:        "",
			ConsecutiveFailures: 0,
			ConsecutiveBlocks:   0,
			NextCheckAt:         &next,
			Outcome:             OutcomeOK,
		}

	case OutcomeBlocked:
		failures := current.ConsecutiveFailures + 1
		blocks := current.ConsecutiveBlocks + 1
		if blocks >= MaxConsecutiveBlocks {
			return CheckTransition{
				IsOn:                current.IsOn,
				NotifiedOn:          current.NotifiedOn,
				Enabled:             false,
				PausedReason:        "Paused after repeated blocks or anti-bot challenges; open the site manually to verify access.",
				ConsecutiveFailures: failures,
				ConsecutiveBlocks:   blocks,
				NextCheckAt:         nil,
				Outcome:             OutcomeBlocked,
				JustAutoPaused:      current.Enabled,
			}
		}
		delay := cappedBackoff(MinBlockedBackoff, blocks-1, MaxBlockedBackoff)
		if rec.RetryAfter > delay {
			delay = rec.RetryAfter
		}
		if delay > MaxBlockedBackoff {
			delay = MaxBlockedBackoff
		}
		next := now.Add(delay)
		return CheckTransition{
			IsOn:                current.IsOn,
			NotifiedOn:          current.NotifiedOn,
			Enabled:             current.Enabled,
			PausedReason:        current.PausedReason,
			ConsecutiveFailures: failures,
			ConsecutiveBlocks:   blocks,
			NextCheckAt:         &next,
			Outcome:             OutcomeBlocked,
		}

	default: // OutcomeError
		failures := current.ConsecutiveFailures + 1
		delay := cappedBackoff(interval, failures, MaxErrorBackoff)
		if rec.RetryAfter > delay {
			delay = rec.RetryAfter
		}
		if delay > MaxErrorBackoff {
			delay = MaxErrorBackoff
		}
		next := now.Add(delay)
		return CheckTransition{
			IsOn:                current.IsOn,
			NotifiedOn:          current.NotifiedOn,
			Enabled:             current.Enabled,
			PausedReason:        current.PausedReason,
			ConsecutiveFailures: failures,
			ConsecutiveBlocks:   0,
			NextCheckAt:         &next,
			Outcome:             OutcomeError,
		}
	}
}

func cappedBackoff(base time.Duration, exp int, capDuration time.Duration) time.Duration {
	if base <= 0 {
		base = time.Minute
	}
	if base >= capDuration {
		return capDuration
	}
	delay := base
	for i := 0; i < exp; i++ {
		if delay >= capDuration/2 {
			return capDuration
		}
		delay *= 2
	}
	return delay
}

// EventInput is a create or replace request after transport decoding. The
// service validates and normalises it before it reaches the store.
type EventInput struct {
	Name                 string
	URL                  string
	CheckIntervalSeconds int
	Enabled              bool
}

const (
	// MinCheckInterval is the shortest supported watch interval.
	MinCheckInterval = 60
	// MaxCheckInterval is the longest supported watch interval.
	MaxCheckInterval = 86400
	// DefaultCheckInterval is used when a create request omits the interval.
	DefaultCheckInterval = 300
	// MaxEventNameLength bounds display names in the UI and notification.
	MaxEventNameLength = 120
	// MaxEventURLLength bounds stored URLs before they reach the checker.
	MaxEventURLLength = 2048
)

// NormaliseEventName trims and validates a display name.
func NormaliseEventName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("name is required")
	}
	if len([]rune(name)) > MaxEventNameLength {
		return "", fmt.Errorf("name must be at most %d characters", MaxEventNameLength)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("name must not contain control characters")
		}
	}
	return name, nil
}

// NormaliseCheckInterval applies the default and validates the range.
func NormaliseCheckInterval(seconds int) (int, error) {
	if seconds == 0 {
		return DefaultCheckInterval, nil
	}
	if seconds < MinCheckInterval || seconds > MaxCheckInterval {
		return 0, fmt.Errorf("check interval must be between %d and %d seconds", MinCheckInterval, MaxCheckInterval)
	}
	return seconds, nil
}

// TruncateError bounds error text before it is stored and shown in the UI.
func TruncateError(raw string) string {
	const limit = 500
	raw = strings.TrimSpace(raw)
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}
