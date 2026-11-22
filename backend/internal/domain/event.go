package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Event is one watched availability page. The only watch type in the local
// proof of concept is an HTTPS status check.
type Event struct {
	ID                   string     `json:"id"`
	UserID               string     `json:"-"`
	Name                 string     `json:"name"`
	URL                  string     `json:"url"`
	CheckIntervalSeconds int        `json:"check_interval_seconds"`
	Enabled              bool       `json:"enabled"`
	IsOn                 bool       `json:"is_on"`
	LastCheckedAt        *time.Time `json:"last_checked_at,omitempty"`
	LastStatusCode       *int       `json:"last_status_code,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	LastNotifiedAt       *time.Time `json:"last_notified_at,omitempty"`
	LastNotificationErr  string     `json:"last_notification_error,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`

	// UserEmail is the verified address on the owning account, when there is
	// one. It is loaded for notifications and never exposed by the API.
	UserEmail string `json:"-"`

	// NotifiedOn is the edge-trigger state: true once the on transition has
	// produced an email. It is deliberately not exposed by the API.
	NotifiedOn bool `json:"-"`
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
