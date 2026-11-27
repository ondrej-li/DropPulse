// Package memstore provides an in-memory implementation of the event, admin,
// and local-test-user repositories for zero-Postgres local headless testing
// (DATABASE_URL=memory:// with LOCAL_TEST_AUTH=true).
package memstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

// Store holds in-memory users, events, check logs, and admin audit entries.
type Store struct {
	mu        sync.RWMutex
	users     map[string]domain.User
	events    map[string]domain.Event
	checks    []domain.EventCheckLog
	audits    []domain.AdminAuditEntry
	nextLogID int64
	nextAudit int64
}

// New creates an in-memory store pre-seeded with local accounts spanning every
// supported role so multi-user and RBAC flows work immediately.
func New() *Store {
	now := time.Now().UTC()
	s := &Store{
		users:  make(map[string]domain.User),
		events: make(map[string]domain.Event),
	}
	seeds := []domain.User{
		{
			ID:            "00000000-0000-4000-8000-000000000001",
			Username:      "admin",
			Email:         "admin@local.test",
			EmailVerified: true,
			DisplayName:   "Local Admin",
			Role:          domain.RoleAdmin,
			Status:        domain.StatusActive,
			CreatedAt:     now,
			UpdatedAt:     now,
			LastLoginAt:   &now,
		},
		{
			ID:            "00000000-0000-4000-8000-000000000002",
			Username:      "alice",
			Email:         "alice@local.test",
			EmailVerified: true,
			DisplayName:   "Alice Viewer",
			Role:          domain.RoleUser,
			Status:        domain.StatusActive,
			CreatedAt:     now,
			UpdatedAt:     now,
			LastLoginAt:   &now,
		},
		{
			ID:            "00000000-0000-4000-8000-000000000003",
			Username:      "cinema-vendor",
			Email:         "vendor@local.test",
			EmailVerified: true,
			DisplayName:   "Cinema Vendor",
			Role:          domain.RoleVendor,
			Status:        domain.StatusActive,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "00000000-0000-4000-8000-000000000004",
			Username:      "ops-agent",
			Email:         "agent@local.test",
			EmailVerified: true,
			DisplayName:   "Ops Agent",
			Role:          domain.RoleAgent,
			Status:        domain.StatusActive,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "00000000-0000-4000-8000-000000000005",
			Username:      "triage-ai",
			Email:         "ai@local.test",
			EmailVerified: true,
			DisplayName:   "Triage AI",
			Role:          domain.RoleAI,
			Status:        domain.StatusActive,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}
	for _, u := range seeds {
		s.users[u.ID] = u
	}
	return s
}

func localEntitlements() domain.Entitlements {
	return domain.Entitlements{
		PlanCode:           "local_test",
		MaxActiveWatches:   100,
		AllowedModes:       []domain.Mode{domain.ModeBasic, domain.ModeScripted, domain.ModeAI},
		MinIntervalSeconds: domain.MinCheckInterval,
		HistoryDays:        365,
	}
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		ns := time.Now().UTC().UnixNano()
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", uint32(ns>>32), uint64(ns)&0xffffffffffff)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func sanitiseName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case (r == '-' || r == '_' || r == '.') && b.Len() > 0:
			if r == '.' {
				b.WriteByte('-')
			} else {
				b.WriteRune(r)
			}
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.TrimRight(b.String(), "-_")
}

// EnsureTestUser finds or creates a test user for headless impersonation.
func (s *Store) EnsureTestUser(_ context.Context, ident, email string, role domain.Role, explicitRole bool) (domain.User, domain.Entitlements, error) {
	ident = strings.TrimSpace(ident)
	email = domain.NormaliseEmail(email)
	if ident == "" {
		ident = "admin"
	}
	if explicitRole && !role.Valid() {
		return domain.User{}, domain.Entitlements{}, errors.New("invalid role")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()

	if domain.ValidUUID(ident) {
		u, ok := s.users[strings.ToLower(ident)]
		if !ok || u.DeletedAt != nil {
			return domain.User{}, domain.Entitlements{}, store.ErrNotFound
		}
		if explicitRole && u.Username != "admin" {
			u.Role = role
			u.UpdatedAt = now
		}
		u.LastLoginAt = &now
		s.users[u.ID] = u
		return u, localEntitlements(), nil
	}

	var username string
	if strings.Contains(ident, "@") {
		if email == "" {
			email = domain.NormaliseEmail(ident)
		}
		username = sanitiseName(strings.SplitN(email, "@", 2)[0])
	} else {
		username = sanitiseName(ident)
		if email == "" {
			email = username + "@local.test"
		}
	}
	if username == "" {
		return domain.User{}, domain.Entitlements{}, errors.New("invalid test user identifier")
	}

	for id, u := range s.users {
		if u.DeletedAt != nil {
			continue
		}
		if u.Username == username || (email != "" && strings.EqualFold(u.Email, email)) {
			if explicitRole && u.Username != "admin" {
				u.Role = role
			}
			u.LastLoginAt = &now
			u.UpdatedAt = now
			s.users[id] = u
			return u, localEntitlements(), nil
		}
	}

	targetRole := domain.RoleUser
	if username == "admin" {
		targetRole = domain.RoleAdmin
	} else if explicitRole {
		targetRole = role
	}

	created := domain.User{
		ID:            newUUID(),
		Username:      username,
		Email:         email,
		EmailVerified: true,
		DisplayName:   username,
		Role:          targetRole,
		Status:        domain.StatusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastLoginAt:   &now,
	}
	s.users[created.ID] = created
	return created, localEntitlements(), nil
}

// GetUser loads one active/suspended non-deleted user by ID.
func (s *Store) GetUser(_ context.Context, id string) (domain.User, error) {
	if !domain.ValidUUID(id) {
		return domain.User{}, store.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[strings.ToLower(id)]
	if !ok || u.DeletedAt != nil {
		return domain.User{}, store.ErrNotFound
	}
	return u, nil
}

func (s *Store) hydrateEventLocked(ev domain.Event) domain.Event {
	if u, ok := s.users[ev.UserID]; ok && u.DeletedAt == nil && u.EmailVerified {
		ev.UserEmail = u.Email
	}
	return ev
}

// ListEvents returns a user's events, newest first.
func (s *Store) ListEvents(_ context.Context, userID string) ([]domain.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Event, 0)
	for _, ev := range s.events {
		if ev.UserID == userID {
			out = append(out, s.hydrateEventLocked(ev))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// GetEvent loads one event belonging to userID.
func (s *Store) GetEvent(_ context.Context, userID, id string) (domain.Event, error) {
	if !domain.ValidUUID(id) {
		return domain.Event{}, store.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ev, ok := s.events[strings.ToLower(id)]
	if !ok || ev.UserID != userID {
		return domain.Event{}, store.ErrNotFound
	}
	return s.hydrateEventLocked(ev), nil
}

// GetEventByID loads one event regardless of owner.
func (s *Store) GetEventByID(_ context.Context, id string) (domain.Event, error) {
	if !domain.ValidUUID(id) {
		return domain.Event{}, store.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ev, ok := s.events[strings.ToLower(id)]
	if !ok {
		return domain.Event{}, store.ErrNotFound
	}
	return s.hydrateEventLocked(ev), nil
}

// CreateEvent inserts a validated event.
func (s *Store) CreateEvent(_ context.Context, userID string, input domain.EventInput) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	ev := domain.Event{
		ID:                   newUUID(),
		UserID:               userID,
		Name:                 input.Name,
		URL:                  input.URL,
		CheckIntervalSeconds: input.CheckIntervalSeconds,
		Enabled:              input.Enabled,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	s.events[ev.ID] = ev
	return s.hydrateEventLocked(ev), nil
}

// UpdateEvent updates an event owned by userID.
func (s *Store) UpdateEvent(_ context.Context, userID, id string, input domain.EventInput) (domain.Event, error) {
	if !domain.ValidUUID(id) {
		return domain.Event{}, store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(id)
	ev, ok := s.events[key]
	if !ok || ev.UserID != userID {
		return domain.Event{}, store.ErrNotFound
	}
	ev = applyEventEdit(ev, input)
	s.events[key] = ev
	return s.hydrateEventLocked(ev), nil
}

func applyEventEdit(ev domain.Event, input domain.EventInput) domain.Event {
	urlChanged := ev.URL != input.URL
	reenabled := !ev.Enabled && input.Enabled
	ev.Name = input.Name
	ev.URL = input.URL
	ev.CheckIntervalSeconds = input.CheckIntervalSeconds
	ev.Enabled = input.Enabled
	if urlChanged {
		ev.IsOn = false
		ev.NotifiedOn = false
		ev.LastStatusCode = nil
		ev.LastOutcome = ""
		ev.LastDetail = ""
		ev.LastError = ""
		ev.LastDurationMs = nil
		ev.LastNotificationErr = ""
	}
	if urlChanged || reenabled {
		ev.LastCheckedAt = nil
		ev.NextCheckAt = nil
		ev.PausedReason = ""
		ev.ConsecutiveFailures = 0
		ev.ConsecutiveBlocks = 0
	}
	ev.UpdatedAt = time.Now().UTC()
	return ev
}

// DeleteEvent removes an event owned by userID.
func (s *Store) DeleteEvent(_ context.Context, userID, id string) (bool, error) {
	if !domain.ValidUUID(id) {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(id)
	ev, ok := s.events[key]
	if !ok || ev.UserID != userID {
		return false, nil
	}
	delete(s.events, key)
	return true, nil
}

// DueEvents returns enabled events due for checking.
func (s *Store) DueEvents(_ context.Context, limit int) ([]domain.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	due := make([]domain.Event, 0)
	for _, ev := range s.events {
		if !ev.Enabled {
			continue
		}
		if ev.NextCheckAt == nil || !ev.NextCheckAt.After(now) {
			due = append(due, s.hydrateEventLocked(ev))
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].CreatedAt.Before(due[j].CreatedAt) })
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

// RecordCheck applies CheckTransition and appends to the check history.
func (s *Store) RecordCheck(_ context.Context, eventID string, rec domain.CheckRecord) (domain.Event, bool, error) {
	if !domain.ValidUUID(eventID) {
		return domain.Event{}, false, store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(eventID)
	current, ok := s.events[key]
	if !ok {
		return domain.Event{}, false, store.ErrNotFound
	}
	now := time.Now().UTC()
	tr := domain.ComputeCheckTransition(current, rec, now)
	detail := domain.TruncateError(rec.Detail)
	errText := domain.TruncateError(rec.ErrorText)
	dur := rec.DurationMs
	if dur < 0 {
		dur = 0
	}

	current.IsOn = tr.IsOn
	current.NotifiedOn = tr.NotifiedOn
	current.Enabled = tr.Enabled
	current.PausedReason = tr.PausedReason
	current.ConsecutiveFailures = tr.ConsecutiveFailures
	current.ConsecutiveBlocks = tr.ConsecutiveBlocks
	current.NextCheckAt = tr.NextCheckAt
	current.LastStatusCode = rec.StatusCode
	current.LastOutcome = tr.Outcome
	current.LastDetail = detail
	current.LastError = errText
	current.CheckCount++
	if tr.Outcome != domain.OutcomeOK {
		current.ErrorCount++
	}
	current.LastDurationMs = &dur
	current.LastCheckedAt = &now
	current.UpdatedAt = now
	s.events[key] = current

	s.nextLogID++
	trigger := rec.Trigger
	if trigger == "" {
		trigger = domain.TriggerScheduled
	}
	s.checks = append(s.checks, domain.EventCheckLog{
		ID:         s.nextLogID,
		EventID:    current.ID,
		EventName:  current.Name,
		EventURL:   current.URL,
		CheckedAt:  now,
		Trigger:    trigger,
		Outcome:    tr.Outcome,
		IsOn:       tr.IsOn,
		StatusCode: rec.StatusCode,
		Detail:     detail,
		Error:      errText,
		DurationMs: dur,
	})
	return s.hydrateEventLocked(current), tr.JustAutoPaused, nil
}

// MarkNotified records notification delivery status.
func (s *Store) MarkNotified(_ context.Context, eventID, notificationErr string) error {
	if !domain.ValidUUID(eventID) {
		return store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(eventID)
	ev, ok := s.events[key]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	if notificationErr == "" {
		ev.NotifiedOn = true
		ev.LastNotifiedAt = &now
	}
	ev.LastNotificationErr = notificationErr
	ev.UpdatedAt = now
	s.events[key] = ev
	return nil
}

// CountActiveEvents counts enabled watches for userID.
func (s *Store) CountActiveEvents(_ context.Context, userID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, ev := range s.events {
		if ev.UserID == userID && ev.Enabled {
			n++
		}
	}
	return n, nil
}

// AdminOverview aggregates fleet statistics.
func (s *Store) AdminOverview(context.Context) (domain.AdminOverview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ov domain.AdminOverview
	for _, ev := range s.events {
		ov.TotalWatchers++
		if ev.Enabled {
			ov.EnabledWatchers++
		}
		if ev.IsOn {
			ov.OnWatchers++
		}
		if ev.Enabled && ev.ConsecutiveFailures > 0 {
			ov.BackingOffWatchers++
		}
		if ev.LastOutcome == domain.OutcomeBlocked {
			ov.BlockedWatchers++
		}
		if !ev.Enabled && ev.PausedReason != "" {
			ov.AutoPausedWatchers++
		}
	}
	for _, u := range s.users {
		if u.DeletedAt != nil {
			continue
		}
		ov.TotalUsers++
		switch u.Status {
		case domain.StatusActive:
			ov.ActiveUsers++
		case domain.StatusSuspended:
			ov.SuspendedUsers++
		}
	}
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	for _, c := range s.checks {
		if c.CheckedAt.Before(cutoff) {
			continue
		}
		ov.ChecksLast24h++
		if c.Outcome == domain.OutcomeError {
			ov.ErrorsLast24h++
		} else if c.Outcome == domain.OutcomeBlocked {
			ov.BlocksLast24h++
		}
	}
	return ov, nil
}

func (s *Store) toAdminEventLocked(ev domain.Event) domain.AdminEvent {
	ev = s.hydrateEventLocked(ev)
	u := s.users[ev.UserID]
	return domain.AdminEvent{
		Event:            ev,
		OwnerID:          ev.UserID,
		OwnerEmail:       u.Email,
		OwnerDisplayName: u.DisplayName,
		OwnerUsername:    u.Username,
		OwnerRole:        u.Role,
		OwnerStatus:      u.Status,
	}
}

// AdminListEvents returns all watchers across all accounts.
func (s *Store) AdminListEvents(_ context.Context, limit int) ([]domain.AdminEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.AdminEvent, 0, len(s.events))
	for _, ev := range s.events {
		out = append(out, s.toAdminEventLocked(ev))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AdminGetEvent loads any watcher by ID.
func (s *Store) AdminGetEvent(_ context.Context, id string) (domain.AdminEvent, error) {
	if !domain.ValidUUID(id) {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ev, ok := s.events[strings.ToLower(id)]
	if !ok {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	return s.toAdminEventLocked(ev), nil
}

// AdminUpdateEvent updates any watcher regardless of owner.
func (s *Store) AdminUpdateEvent(_ context.Context, id string, input domain.EventInput) (domain.AdminEvent, error) {
	if !domain.ValidUUID(id) {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(id)
	ev, ok := s.events[key]
	if !ok {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	ev = applyEventEdit(ev, input)
	s.events[key] = ev
	return s.toAdminEventLocked(ev), nil
}

// AdminResetEventBackoff clears backoff counters and optionally re-enables a watcher.
func (s *Store) AdminResetEventBackoff(_ context.Context, id string, enable bool) (domain.AdminEvent, error) {
	if !domain.ValidUUID(id) {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(id)
	ev, ok := s.events[key]
	if !ok {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	now := time.Now().UTC()
	ev.ConsecutiveFailures = 0
	ev.ConsecutiveBlocks = 0
	ev.PausedReason = ""
	ev.NextCheckAt = &now
	if enable {
		ev.Enabled = true
	}
	ev.UpdatedAt = now
	s.events[key] = ev
	return s.toAdminEventLocked(ev), nil
}

// AdminDeleteEvent deletes any watcher by ID.
func (s *Store) AdminDeleteEvent(_ context.Context, id string) (bool, error) {
	if !domain.ValidUUID(id) {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(id)
	if _, ok := s.events[key]; !ok {
		return false, nil
	}
	delete(s.events, key)
	return true, nil
}

// ListEventChecks returns check logs for a single watcher, newest first.
func (s *Store) ListEventChecks(_ context.Context, eventID string, limit int) ([]domain.EventCheckLog, error) {
	if !domain.ValidUUID(eventID) {
		return nil, store.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	target := strings.ToLower(eventID)
	out := make([]domain.EventCheckLog, 0)
	for i := len(s.checks) - 1; i >= 0; i-- {
		if strings.EqualFold(s.checks[i].EventID, target) {
			out = append(out, s.checks[i])
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// ListRecentChecks returns newest check logs across all watchers.
func (s *Store) ListRecentChecks(_ context.Context, limit int) ([]domain.EventCheckLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out := make([]domain.EventCheckLog, 0, limit)
	for i := len(s.checks) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.checks[i])
	}
	return out, nil
}

// RecordAdminAudit appends an admin audit entry.
func (s *Store) RecordAdminAudit(_ context.Context, actorID, action, targetID, requestID string, metadata map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextAudit++
	u := s.users[strings.ToLower(actorID)]
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	s.audits = append(s.audits, domain.AdminAuditEntry{
		ID:         s.nextAudit,
		ActorID:    actorID,
		ActorEmail: u.Email,
		ActorName:  name,
		Action:     action,
		TargetID:   targetID,
		RequestID:  requestID,
		Metadata:   metadata,
		CreatedAt:  time.Now().UTC(),
	})
	return nil
}

// ListAdminAudit returns newest admin audit entries.
func (s *Store) ListAdminAudit(_ context.Context, limit int) ([]domain.AdminAuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out := make([]domain.AdminAuditEntry, 0, limit)
	for i := len(s.audits) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.audits[i])
	}
	return out, nil
}

// AdminListUsers returns all non-deleted users with watch counts.
func (s *Store) AdminListUsers(_ context.Context, limit int) ([]domain.AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.AdminUser, 0, len(s.users))
	for _, u := range s.users {
		if u.DeletedAt != nil {
			continue
		}
		au := domain.AdminUser{User: u}
		for _, ev := range s.events {
			if ev.UserID == u.ID {
				au.TotalWatches++
				if ev.Enabled {
					au.ActiveWatches++
				}
			}
		}
		out = append(out, au)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AdminSetUserStatus suspends or reactivates a non-admin user.
func (s *Store) AdminSetUserStatus(_ context.Context, userID string, status domain.UserStatus) (domain.AdminUser, error) {
	if !domain.ValidUUID(userID) {
		return domain.AdminUser{}, store.ErrNotFound
	}
	if status != domain.StatusActive && status != domain.StatusSuspended {
		return domain.AdminUser{}, errors.New("status must be active or suspended")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(userID)
	u, ok := s.users[key]
	if !ok || u.DeletedAt != nil {
		return domain.AdminUser{}, store.ErrNotFound
	}
	if u.Role == domain.RoleAdmin {
		return domain.AdminUser{}, store.ErrProtectedAdmin
	}
	u.Status = status
	u.UpdatedAt = time.Now().UTC()
	s.users[key] = u

	au := domain.AdminUser{User: u}
	for _, ev := range s.events {
		if ev.UserID == u.ID {
			au.TotalWatches++
			if ev.Enabled {
				au.ActiveWatches++
			}
		}
	}
	return au, nil
}

// AdminRemoveUser removes watches and soft-deletes/anonymises a non-admin user.
func (s *Store) AdminRemoveUser(_ context.Context, userID string) error {
	if !domain.ValidUUID(userID) {
		return store.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(userID)
	u, ok := s.users[key]
	if !ok || u.DeletedAt != nil {
		return store.ErrNotFound
	}
	if u.Role == domain.RoleAdmin {
		return store.ErrProtectedAdmin
	}
	for eid, ev := range s.events {
		if ev.UserID == u.ID {
			delete(s.events, eid)
		}
	}
	now := time.Now().UTC()
	u.Status = domain.StatusDeleted
	u.DeletedAt = &now
	u.Email = ""
	u.EmailVerified = false
	u.Username = ""
	u.DisplayName = ""
	u.AvatarURL = ""
	u.UpdatedAt = now
	s.users[key] = u
	return nil
}
