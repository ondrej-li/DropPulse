package domain

import "time"

// AdminEvent is an Event enriched with its owning account's metadata for the
// administrator interface.
type AdminEvent struct {
	Event
	OwnerID          string     `json:"owner_id"`
	OwnerEmail       string     `json:"owner_email,omitempty"`
	OwnerDisplayName string     `json:"owner_display_name,omitempty"`
	OwnerUsername    string     `json:"owner_username,omitempty"`
	OwnerRole        Role       `json:"owner_role"`
	OwnerStatus      UserStatus `json:"owner_status"`
}

// EventCheckLog is one persisted check run from event_checks.
type EventCheckLog struct {
	ID         int64        `json:"id"`
	EventID    string       `json:"event_id"`
	EventName  string       `json:"event_name,omitempty"`
	EventURL   string       `json:"event_url,omitempty"`
	CheckedAt  time.Time    `json:"checked_at"`
	Trigger    CheckTrigger `json:"trigger"`
	Outcome    CheckOutcome `json:"outcome"`
	IsOn       bool         `json:"is_on"`
	StatusCode *int         `json:"status_code,omitempty"`
	Detail     string       `json:"detail,omitempty"`
	Error      string       `json:"error,omitempty"`
	DurationMs int          `json:"duration_ms"`
}

// AdminAuditEntry is one recorded administrator action.
type AdminAuditEntry struct {
	ID         int64          `json:"id"`
	ActorID    string         `json:"actor_id,omitempty"`
	ActorEmail string         `json:"actor_email,omitempty"`
	ActorName  string         `json:"actor_name,omitempty"`
	Action     string         `json:"action"`
	TargetID   string         `json:"target_id,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// AdminUser is a User enriched with operational counts for admin management.
type AdminUser struct {
	User
	ActiveWatches  int `json:"active_watches"`
	TotalWatches   int `json:"total_watches"`
	ActiveSessions int `json:"active_sessions"`
}

// AdminOverview aggregates fleet-wide watcher, user, and 24-hour check stats.
type AdminOverview struct {
	TotalWatchers      int    `json:"total_watchers"`
	EnabledWatchers    int    `json:"enabled_watchers"`
	OnWatchers         int    `json:"on_watchers"`
	BackingOffWatchers int    `json:"backing_off_watchers"`
	BlockedWatchers    int    `json:"blocked_watchers"`
	AutoPausedWatchers int    `json:"auto_paused_watchers"`
	TotalUsers         int    `json:"total_users"`
	ActiveUsers        int    `json:"active_users"`
	SuspendedUsers     int    `json:"suspended_users"`
	ChecksLast24h      int64  `json:"checks_last_24h"`
	ErrorsLast24h      int64  `json:"errors_last_24h"`
	BlocksLast24h      int64  `json:"blocks_last_24h"`
	MailTransport      string `json:"mail_transport"`
}

// DryRunResult is the live output of a non-mutating admin probe.
type DryRunResult struct {
	CheckKind         CheckKind    `json:"check_kind,omitempty"`
	Outcome           CheckOutcome `json:"outcome"`
	IsOn              bool         `json:"is_on"`
	Available         *bool        `json:"available,omitempty"`
	StatusCode        *int         `json:"status_code,omitempty"`
	Detail            string       `json:"detail,omitempty"`
	Error             string       `json:"error,omitempty"`
	RetryAfterSeconds int          `json:"retry_after_seconds,omitempty"`
	DurationMs        int          `json:"duration_ms"`
	CheckedAt         time.Time    `json:"checked_at"`
}
