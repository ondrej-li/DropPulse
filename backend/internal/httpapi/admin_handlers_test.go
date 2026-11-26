package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/events"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
	"github.com/ondrej-li/DropPulse/backend/internal/watch"
)

const (
	adminActorID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	otherAdminID   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	regularUserID  = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	sampleEventID  = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	createdEventID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
)

type fakeAdminStore struct {
	users  map[string]domain.AdminUser
	events map[string]domain.Event
	checks []domain.EventCheckLog
	audits []domain.AdminAuditEntry
}

func newFakeAdminStore() *fakeAdminStore {
	now := time.Now().UTC()
	return &fakeAdminStore{
		users: map[string]domain.AdminUser{
			adminActorID: {
				User: domain.User{ID: adminActorID, Email: "admin@example.com", Role: domain.RoleAdmin, Status: domain.StatusActive, CreatedAt: now},
			},
			otherAdminID: {
				User: domain.User{ID: otherAdminID, Email: "co-admin@example.com", Role: domain.RoleAdmin, Status: domain.StatusActive, CreatedAt: now},
			},
			regularUserID: {
				User: domain.User{ID: regularUserID, Email: "viewer@example.com", Role: domain.RoleUser, Status: domain.StatusActive, CreatedAt: now},
			},
		},
		events: map[string]domain.Event{
			sampleEventID: {
				ID:                   sampleEventID,
				UserID:               regularUserID,
				Name:                 "Flora IMAX",
				URL:                  "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX",
				CheckIntervalSeconds: 300,
				Enabled:              false,
				ConsecutiveFailures:  3,
				ConsecutiveBlocks:    3,
				PausedReason:         "Paused after repeated blocks",
				CreatedAt:            now,
				UpdatedAt:            now,
			},
		},
	}
}

// Implement events.Repository on fakeAdminStore.
func (f *fakeAdminStore) ListEvents(_ context.Context, userID string) ([]domain.Event, error) {
	out := make([]domain.Event, 0)
	for _, ev := range f.events {
		if ev.UserID == userID {
			out = append(out, ev)
		}
	}
	return out, nil
}
func (f *fakeAdminStore) GetEvent(_ context.Context, userID, id string) (domain.Event, error) {
	ev, ok := f.events[id]
	if !ok || ev.UserID != userID {
		return domain.Event{}, store.ErrNotFound
	}
	return ev, nil
}
func (f *fakeAdminStore) GetEventByID(_ context.Context, id string) (domain.Event, error) {
	ev, ok := f.events[id]
	if !ok {
		return domain.Event{}, store.ErrNotFound
	}
	return ev, nil
}
func (f *fakeAdminStore) CreateEvent(_ context.Context, userID string, in domain.EventInput) (domain.Event, error) {
	ev := domain.Event{
		ID:                   createdEventID,
		UserID:               userID,
		Name:                 in.Name,
		URL:                  in.URL,
		CheckIntervalSeconds: in.CheckIntervalSeconds,
		Enabled:              in.Enabled,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
	f.events[ev.ID] = ev
	return ev, nil
}
func (f *fakeAdminStore) UpdateEvent(_ context.Context, userID, id string, in domain.EventInput) (domain.Event, error) {
	ev, ok := f.events[id]
	if !ok || ev.UserID != userID {
		return domain.Event{}, store.ErrNotFound
	}
	ev.Name, ev.URL, ev.CheckIntervalSeconds, ev.Enabled = in.Name, in.URL, in.CheckIntervalSeconds, in.Enabled
	f.events[id] = ev
	return ev, nil
}
func (f *fakeAdminStore) DeleteEvent(_ context.Context, userID, id string) (bool, error) {
	ev, ok := f.events[id]
	if !ok || ev.UserID != userID {
		return false, nil
	}
	delete(f.events, id)
	return true, nil
}
func (f *fakeAdminStore) DueEvents(context.Context, int) ([]domain.Event, error) { return nil, nil }
func (f *fakeAdminStore) RecordCheck(_ context.Context, id string, rec domain.CheckRecord) (domain.Event, bool, error) {
	ev, ok := f.events[id]
	if !ok {
		return domain.Event{}, false, store.ErrNotFound
	}
	now := time.Now().UTC()
	tr := domain.ComputeCheckTransition(ev, rec, now)
	ev.IsOn = tr.IsOn
	ev.NotifiedOn = tr.NotifiedOn
	ev.Enabled = tr.Enabled
	ev.PausedReason = tr.PausedReason
	ev.ConsecutiveFailures = tr.ConsecutiveFailures
	ev.ConsecutiveBlocks = tr.ConsecutiveBlocks
	ev.NextCheckAt = tr.NextCheckAt
	ev.LastOutcome = tr.Outcome
	ev.LastDetail = rec.Detail
	ev.LastError = rec.ErrorText
	ev.CheckCount++
	ev.LastCheckedAt = &now
	f.events[id] = ev
	f.checks = append(f.checks, domain.EventCheckLog{
		ID:         int64(len(f.checks) + 1),
		EventID:    ev.ID,
		EventName:  ev.Name,
		CheckedAt:  now,
		Trigger:    rec.Trigger,
		Outcome:    tr.Outcome,
		IsOn:       tr.IsOn,
		Detail:     rec.Detail,
		DurationMs: rec.DurationMs,
	})
	return ev, tr.JustAutoPaused, nil
}
func (f *fakeAdminStore) MarkNotified(context.Context, string, string) error { return nil }
func (f *fakeAdminStore) CountActiveEvents(context.Context, string) (int, error) {
	return len(f.events), nil
}

// Implement AdminStore on fakeAdminStore.
func (f *fakeAdminStore) AdminOverview(context.Context) (domain.AdminOverview, error) {
	return domain.AdminOverview{
		TotalWatchers: len(f.events),
		TotalUsers:    len(f.users),
		ChecksLast24h: int64(len(f.checks)),
	}, nil
}
func (f *fakeAdminStore) toAdminEvent(ev domain.Event) domain.AdminEvent {
	u := f.users[ev.UserID]
	return domain.AdminEvent{
		Event:       ev,
		OwnerID:     ev.UserID,
		OwnerEmail:  u.Email,
		OwnerRole:   u.Role,
		OwnerStatus: u.Status,
	}
}
func (f *fakeAdminStore) AdminListEvents(context.Context, int) ([]domain.AdminEvent, error) {
	out := make([]domain.AdminEvent, 0, len(f.events))
	for _, ev := range f.events {
		out = append(out, f.toAdminEvent(ev))
	}
	return out, nil
}
func (f *fakeAdminStore) AdminGetEvent(_ context.Context, id string) (domain.AdminEvent, error) {
	ev, ok := f.events[id]
	if !ok {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	return f.toAdminEvent(ev), nil
}
func (f *fakeAdminStore) AdminUpdateEvent(_ context.Context, id string, in domain.EventInput) (domain.AdminEvent, error) {
	ev, ok := f.events[id]
	if !ok {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	ev.Name, ev.URL, ev.CheckIntervalSeconds, ev.Enabled = in.Name, in.URL, in.CheckIntervalSeconds, in.Enabled
	f.events[id] = ev
	return f.toAdminEvent(ev), nil
}
func (f *fakeAdminStore) AdminResetEventBackoff(_ context.Context, id string, enable bool) (domain.AdminEvent, error) {
	ev, ok := f.events[id]
	if !ok {
		return domain.AdminEvent{}, store.ErrNotFound
	}
	ev.ConsecutiveFailures = 0
	ev.ConsecutiveBlocks = 0
	ev.PausedReason = ""
	if enable {
		ev.Enabled = true
	}
	f.events[id] = ev
	return f.toAdminEvent(ev), nil
}
func (f *fakeAdminStore) AdminDeleteEvent(_ context.Context, id string) (bool, error) {
	if _, ok := f.events[id]; !ok {
		return false, nil
	}
	delete(f.events, id)
	return true, nil
}
func (f *fakeAdminStore) ListEventChecks(_ context.Context, eventID string, _ int) ([]domain.EventCheckLog, error) {
	out := make([]domain.EventCheckLog, 0)
	for _, c := range f.checks {
		if c.EventID == eventID {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeAdminStore) ListRecentChecks(context.Context, int) ([]domain.EventCheckLog, error) {
	return f.checks, nil
}
func (f *fakeAdminStore) RecordAdminAudit(_ context.Context, actorID, action, targetID, reqID string, meta map[string]any) error {
	f.audits = append(f.audits, domain.AdminAuditEntry{
		ID:        int64(len(f.audits) + 1),
		ActorID:   actorID,
		Action:    action,
		TargetID:  targetID,
		RequestID: reqID,
		Metadata:  meta,
		CreatedAt: time.Now().UTC(),
	})
	return nil
}
func (f *fakeAdminStore) ListAdminAudit(context.Context, int) ([]domain.AdminAuditEntry, error) {
	return f.audits, nil
}
func (f *fakeAdminStore) AdminListUsers(context.Context, int) ([]domain.AdminUser, error) {
	out := make([]domain.AdminUser, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}
func (f *fakeAdminStore) AdminSetUserStatus(_ context.Context, userID string, status domain.UserStatus) (domain.AdminUser, error) {
	u, ok := f.users[userID]
	if !ok {
		return domain.AdminUser{}, store.ErrNotFound
	}
	if u.Role == domain.RoleAdmin {
		return domain.AdminUser{}, store.ErrProtectedAdmin
	}
	u.Status = status
	f.users[userID] = u
	return u, nil
}
func (f *fakeAdminStore) AdminRemoveUser(_ context.Context, userID string) error {
	u, ok := f.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	if u.Role == domain.RoleAdmin {
		return store.ErrProtectedAdmin
	}
	delete(f.users, userID)
	return nil
}
func (f *fakeAdminStore) GetUser(_ context.Context, id string) (domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return domain.User{}, store.ErrNotFound
	}
	return u.User, nil
}

type stubChecker struct {
	result watch.Result
}

func (c *stubChecker) Check(context.Context, string) watch.Result {
	return c.result
}

func newAdminTestServer(t *testing.T, role domain.Role) (*Server, *fakeAdminStore, http.Handler) {
	t.Helper()
	st := newFakeAdminStore()
	avail := true
	checker := &stubChecker{
		result: watch.Result{
			Outcome:   domain.OutcomeOK,
			Available: &avail,
			Detail:    "IMAX VOLVO · 2026-12-18 19:30",
			Duration:  120 * time.Millisecond,
			CheckedAt: time.Now().UTC(),
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	actor := domain.User{ID: adminActorID, Email: "actor@example.com", Role: role, Status: domain.StatusActive}
	svc := events.NewService(st, checker, nil, logger, actor, "https://dp.example/app/")

	apiURL, _ := url.Parse("http://localhost:8090")
	cfg := config.Config{
		SingleUserMode:    true,
		PublicAPIURL:      apiURL,
		WebAppURL:         apiURL,
		AllowedOrigins:    []string{"http://localhost:8090"},
		APIRatePerMinute:  600,
		MaxRequestBytes:   1 << 20,
		SMTPFrom:          "alerts@example.com",
		SMTPHost:          "127.0.0.1",
		NotificationEmail: "alerts@example.com",
	}
	srv := &Server{
		cfg:        cfg,
		adminStore: st,
		events:     svc,
		logger:     logger,
		limiter:    NewLimiter(time.Now),
		now:        time.Now,
		admin:      actor,
	}
	return srv, st, srv.Handler()
}

func adminReq(method, path string, body any) *http.Request {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "http://localhost:8090"+path, reader)
	req.Host = "localhost:8090"
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://localhost:8090")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	return req
}

func TestAdminEndpointsRejectNonAdminRoles(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleVendor, domain.RoleAgent, domain.RoleAI} {
		t.Run(string(role), func(t *testing.T) {
			_, _, handler := newAdminTestServer(t, role)
			endpoints := []struct{ method, path string }{
				{http.MethodGet, "/api/v1/admin/overview"},
				{http.MethodGet, "/api/v1/admin/watchers"},
				{http.MethodPost, "/api/v1/admin/watchers"},
				{http.MethodPost, "/api/v1/admin/watchers/" + sampleEventID + "/fetch"},
				{http.MethodGet, "/api/v1/admin/users"},
				{http.MethodPost, "/api/v1/admin/users/" + regularUserID + "/ban"},
				{http.MethodDelete, "/api/v1/admin/users/" + regularUserID},
				{http.MethodGet, "/api/v1/admin/logs/checks"},
				{http.MethodGet, "/api/v1/admin/logs/audit"},
			}
			for _, ep := range endpoints {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, adminReq(ep.method, ep.path, nil))
				if rec.Code != http.StatusForbidden {
					t.Errorf("role=%s %s %s = %d, want 403", role, ep.method, ep.path, rec.Code)
				}
			}
		})
	}
}

func TestAdminWatcherOperationsAndOutOfOrderFetch(t *testing.T) {
	_, st, handler := newAdminTestServer(t, domain.RoleAdmin)

	// 1. Dry-run probe does not mutate state or record event_checks.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, adminReq(http.MethodPost, "/api/v1/admin/watchers/"+sampleEventID+"/fetch", map[string]any{"dry_run": true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run fetch = %d: %s", rec.Code, rec.Body.String())
	}
	if st.events[sampleEventID].CheckCount != 0 || len(st.checks) != 0 {
		t.Fatal("dry-run probe must not mutate watcher state or check history")
	}

	// 2. Live out-of-order fetch runs and records admin_manual trigger.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, adminReq(http.MethodPost, "/api/v1/admin/watchers/"+sampleEventID+"/fetch", map[string]any{"dry_run": false}))
	if rec.Code != http.StatusOK {
		t.Fatalf("live fetch = %d: %s", rec.Code, rec.Body.String())
	}
	if !st.events[sampleEventID].IsOn || len(st.checks) != 1 || st.checks[0].Trigger != domain.TriggerAdminManual {
		t.Fatalf("unexpected post-fetch state: event=%+v checks=%+v", st.events[sampleEventID], st.checks)
	}

	// 3. Reset backoff re-enables and clears counters.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, adminReq(http.MethodPost, "/api/v1/admin/watchers/"+sampleEventID+"/reset-backoff", map[string]any{"enable": true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset backoff = %d: %s", rec.Code, rec.Body.String())
	}
	if !st.events[sampleEventID].Enabled || st.events[sampleEventID].ConsecutiveBlocks != 0 {
		t.Fatalf("reset backoff did not clear/enable: %+v", st.events[sampleEventID])
	}

	// 4. Admin can create any event assigned to any user.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, adminReq(http.MethodPost, "/api/v1/admin/watchers", map[string]any{
		"user_id":                regularUserID,
		"name":                   "Custom monitor",
		"url":                    "https://example.com/drop",
		"check_interval_seconds": 120,
		"enabled":                true,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create watcher = %d: %s", rec.Code, rec.Body.String())
	}
	if st.events[createdEventID].UserID != regularUserID {
		t.Fatalf("created watcher owner = %q, want %q", st.events[createdEventID].UserID, regularUserID)
	}

	if len(st.audits) != 4 {
		t.Fatalf("expected 4 audit records, got %d", len(st.audits))
	}
}

func TestAdminUserManagementProtectsAdmins(t *testing.T) {
	_, st, handler := newAdminTestServer(t, domain.RoleAdmin)

	// Cannot ban self or another admin.
	for _, targetID := range []string{adminActorID, otherAdminID} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, adminReq(http.MethodPost, "/api/v1/admin/users/"+targetID+"/ban", nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("ban admin %s = %d, want 403", targetID, rec.Code)
		}
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, adminReq(http.MethodDelete, "/api/v1/admin/users/"+targetID, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("remove admin %s = %d, want 403", targetID, rec.Code)
		}
	}

	// Can ban, unban, and remove a non-admin user.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, adminReq(http.MethodPost, "/api/v1/admin/users/"+regularUserID+"/ban", nil))
	if rec.Code != http.StatusOK || st.users[regularUserID].Status != domain.StatusSuspended {
		t.Fatalf("ban regular user failed: code=%d status=%s", rec.Code, st.users[regularUserID].Status)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, adminReq(http.MethodPost, "/api/v1/admin/users/"+regularUserID+"/unban", nil))
	if rec.Code != http.StatusOK || st.users[regularUserID].Status != domain.StatusActive {
		t.Fatalf("unban regular user failed: code=%d status=%s", rec.Code, st.users[regularUserID].Status)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, adminReq(http.MethodDelete, "/api/v1/admin/users/"+regularUserID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove regular user = %d, want 204", rec.Code)
	}
	if _, exists := st.users[regularUserID]; exists {
		t.Fatal("removed regular user still exists")
	}
}
