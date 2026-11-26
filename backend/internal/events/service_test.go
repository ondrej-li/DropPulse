package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
	"github.com/ondrej-li/DropPulse/backend/internal/watch"
)

const (
	testUserID  = "11111111-1111-1111-1111-111111111111"
	testEventID = "22222222-2222-2222-2222-222222222222"
)

type fakeRepo struct {
	events map[string]domain.Event
}

func newFakeRepo(event domain.Event) *fakeRepo {
	return &fakeRepo{events: map[string]domain.Event{event.ID: event}}
}

func (f *fakeRepo) ListEvents(context.Context, string) ([]domain.Event, error) {
	list := make([]domain.Event, 0, len(f.events))
	for _, event := range f.events {
		list = append(list, event)
	}
	return list, nil
}

func (f *fakeRepo) GetEvent(_ context.Context, userID, id string) (domain.Event, error) {
	event, ok := f.events[id]
	if !ok || event.UserID != userID {
		return domain.Event{}, store.ErrNotFound
	}
	return event, nil
}

func (f *fakeRepo) GetEventByID(_ context.Context, id string) (domain.Event, error) {
	event, ok := f.events[id]
	if !ok {
		return domain.Event{}, store.ErrNotFound
	}
	return event, nil
}

func (f *fakeRepo) CreateEvent(_ context.Context, userID string, input domain.EventInput) (domain.Event, error) {
	return domain.Event{ID: testEventID, UserID: userID, Name: input.Name, URL: input.URL}, nil
}

func (f *fakeRepo) UpdateEvent(_ context.Context, _, id string, input domain.EventInput) (domain.Event, error) {
	event, ok := f.events[id]
	if !ok {
		return domain.Event{}, store.ErrNotFound
	}
	event.Name = input.Name
	event.URL = input.URL
	event.CheckIntervalSeconds = input.CheckIntervalSeconds
	event.Enabled = input.Enabled
	f.events[id] = event
	return event, nil
}

func (f *fakeRepo) DeleteEvent(_ context.Context, _, id string) (bool, error) {
	if _, ok := f.events[id]; !ok {
		return false, nil
	}
	delete(f.events, id)
	return true, nil
}

func (f *fakeRepo) DueEvents(context.Context, int) ([]domain.Event, error) {
	return nil, nil
}

func (f *fakeRepo) RecordCheck(_ context.Context, eventID string, rec domain.CheckRecord) (domain.Event, bool, error) {
	event, ok := f.events[eventID]
	if !ok {
		return domain.Event{}, false, store.ErrNotFound
	}
	now := time.Now().UTC()
	tr := domain.ComputeCheckTransition(event, rec, now)
	event.IsOn = tr.IsOn
	event.NotifiedOn = tr.NotifiedOn
	event.Enabled = tr.Enabled
	event.PausedReason = tr.PausedReason
	event.ConsecutiveFailures = tr.ConsecutiveFailures
	event.ConsecutiveBlocks = tr.ConsecutiveBlocks
	event.NextCheckAt = tr.NextCheckAt
	event.LastStatusCode = rec.StatusCode
	event.LastOutcome = tr.Outcome
	if tr.Outcome == domain.OutcomeOK {
		event.LastDetail = rec.Detail
	}
	event.LastError = rec.ErrorText
	event.CheckCount++
	if tr.Outcome != domain.OutcomeOK {
		event.ErrorCount++
	}
	event.LastCheckedAt = &now
	f.events[eventID] = event
	return event, tr.JustAutoPaused, nil
}

func (f *fakeRepo) MarkNotified(_ context.Context, eventID, notificationErr string) error {
	event, ok := f.events[eventID]
	if !ok {
		return store.ErrNotFound
	}
	if notificationErr == "" {
		event.NotifiedOn = true
		event.LastNotificationErr = ""
		now := time.Now()
		event.LastNotifiedAt = &now
	} else {
		event.LastNotificationErr = notificationErr
	}
	f.events[eventID] = event
	return nil
}

func (f *fakeRepo) CountActiveEvents(context.Context, string) (int, error) {
	return len(f.events), nil
}

type queueChecker struct {
	results []watch.Result
}

func (q *queueChecker) Check(context.Context, string) watch.Result {
	if len(q.results) == 0 {
		return watch.Result{Err: errors.New("no fake result queued")}
	}
	result := q.results[0]
	q.results = q.results[1:]
	return result
}

type recordingMailer struct {
	sends      int
	pauseSends int
	failures   int
}

func (m *recordingMailer) SendEventOn(context.Context, domain.Event, string) error {
	m.sends++
	if m.failures > 0 {
		m.failures--
		return errors.New("smtp unavailable")
	}
	return nil
}

func (m *recordingMailer) SendEventPaused(context.Context, domain.Event, string) error {
	m.pauseSends++
	return nil
}

func status(code int) *int { return &code }

func availability(value bool) *bool { return &value }

func testService(t *testing.T, repo *fakeRepo, checker Checker, mailer Mailer) *Service {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := domain.User{ID: testUserID, Username: "admin", Role: domain.RoleAdmin, Status: domain.StatusActive}
	return NewService(repo, checker, mailer, logger, admin, "http://localhost:8090")
}

func baseEvent() domain.Event {
	now := time.Now()
	return domain.Event{
		ID:                   testEventID,
		UserID:               testUserID,
		Name:                 "Saturday show",
		URL:                  "https://example.com/tickets",
		CheckIntervalSeconds: 300,
		Enabled:              true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func TestNotificationEdgeSendsOncePerAvailabilityEpisode(t *testing.T) {
	repo := newFakeRepo(baseEvent())
	checker := &queueChecker{results: []watch.Result{
		{StatusCode: status(200)},
		{StatusCode: status(200)},
		{StatusCode: status(404)},
		{StatusCode: status(200)},
	}}
	mailer := &recordingMailer{}
	service := testService(t, repo, checker, mailer)
	ctx := context.Background()

	first, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.IsOn || !first.NotifiedOn || mailer.sends != 1 {
		t.Fatalf("first on check: on=%v notified=%v sends=%d", first.IsOn, first.NotifiedOn, mailer.sends)
	}

	second, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.IsOn || mailer.sends != 1 {
		t.Fatalf("staying on must not resend: on=%v sends=%d", second.IsOn, mailer.sends)
	}

	off, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if off.IsOn || off.NotifiedOn || mailer.sends != 1 {
		t.Fatalf("off check: on=%v notified=%v sends=%d", off.IsOn, off.NotifiedOn, mailer.sends)
	}

	again, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.IsOn || mailer.sends != 2 {
		t.Fatalf("off->on must notify again: on=%v sends=%d", again.IsOn, mailer.sends)
	}
}

func TestDisabledEventDoesNotNotify(t *testing.T) {
	event := baseEvent()
	event.Enabled = false
	repo := newFakeRepo(event)
	mailer := &recordingMailer{}
	service := testService(t, repo, &queueChecker{results: []watch.Result{{StatusCode: status(200)}}}, mailer)

	checked, err := service.CheckNow(context.Background(), testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if !checked.IsOn || mailer.sends != 0 {
		t.Fatalf("disabled check: on=%v sends=%d", checked.IsOn, mailer.sends)
	}
}

func TestNotificationRetriesAfterSendFailure(t *testing.T) {
	repo := newFakeRepo(baseEvent())
	checker := &queueChecker{results: []watch.Result{
		{StatusCode: status(200)},
		{StatusCode: status(200)},
	}}
	mailer := &recordingMailer{failures: 1}
	service := testService(t, repo, checker, mailer)
	ctx := context.Background()

	first, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if first.NotifiedOn || first.LastNotificationErr == "" || mailer.sends != 1 {
		t.Fatalf("failed notification: notified=%v err=%q sends=%d",
			first.NotifiedOn, first.LastNotificationErr, mailer.sends)
	}

	second, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotifiedOn || second.LastNotificationErr != "" || mailer.sends != 2 {
		t.Fatalf("retry: notified=%v err=%q sends=%d",
			second.NotifiedOn, second.LastNotificationErr, mailer.sends)
	}
}

func TestProviderAvailabilityDrivesNotifications(t *testing.T) {
	repo := newFakeRepo(baseEvent())
	checker := &queueChecker{results: []watch.Result{
		{Available: availability(false)},
		{Available: availability(true)},
		{Available: availability(true)},
		{Available: availability(false)},
		{Available: availability(true)},
	}}
	mailer := &recordingMailer{}
	service := testService(t, repo, checker, mailer)
	ctx := context.Background()

	want := []struct {
		on    bool
		sends int
	}{
		{false, 0},
		{true, 1},
		{true, 1},
		{false, 1},
		{true, 2},
	}
	for i, expected := range want {
		event, err := service.CheckNow(ctx, testUserID, testEventID)
		if err != nil {
			t.Fatal(err)
		}
		if event.IsOn != expected.on || mailer.sends != expected.sends {
			t.Fatalf("check %d: on=%v sends=%d, want on=%v sends=%d",
				i+1, event.IsOn, mailer.sends, expected.on, expected.sends)
		}
		if event.LastStatusCode != nil {
			t.Fatalf("check %d: provider check stored HTTP status %d", i+1, *event.LastStatusCode)
		}
	}
}

func TestCinemaCityTargetValidation(t *testing.T) {
	repo := newFakeRepo(baseEvent())
	service := testService(t, repo, &queueChecker{}, &recordingMailer{})
	ctx := context.Background()

	valid := "https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX"
	created, err := service.Create(ctx, testUserID, domain.EventInput{
		Name:                 "Dune at Flora IMAX",
		URL:                  valid,
		CheckIntervalSeconds: 300,
		Enabled:              true,
	})
	if err != nil {
		t.Fatalf("valid Cinema City target rejected: %v", err)
	}
	if created.URL != valid {
		t.Fatalf("created URL = %q, want %q", created.URL, valid)
	}

	invalid := []string{
		"https://www.cinemacity.cz/films/duna-cast-treti/8105s2r",
		"https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=abc",
		"https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&date=15-12-2026",
		"https://www.cinemacity.cz/cinemas/flora/1052?cinema=1052",
	}
	for _, raw := range invalid {
		_, err := service.Create(ctx, testUserID, domain.EventInput{
			Name:                 "Bad Cinema City target",
			URL:                  raw,
			CheckIntervalSeconds: 300,
			Enabled:              true,
		})
		if err == nil {
			t.Fatalf("Cinema City target %q must be rejected", raw)
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("error %v is not a ValidationError", err)
		}
	}
}

func TestCreateRejectsUnsafeInput(t *testing.T) {
	repo := newFakeRepo(baseEvent())
	service := testService(t, repo, &queueChecker{}, &recordingMailer{})

	cases := []domain.EventInput{
		{Name: "Show", URL: "http://example.com/"},
		{Name: "Show", URL: "https://127.0.0.1/"},
		{Name: "", URL: "https://example.com/"},
		{Name: "Show", URL: "https://example.com/", CheckIntervalSeconds: 30},
	}
	for _, input := range cases {
		if _, err := service.Create(context.Background(), testUserID, input); err == nil {
			t.Fatalf("input %+v must be rejected", input)
		} else {
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error %v is not a ValidationError", err)
			}
		}
	}
}

func TestTransientErrorDoesNotResetOnStateOrDuplicateEmails(t *testing.T) {
	repo := newFakeRepo(baseEvent())
	checker := &queueChecker{results: []watch.Result{
		{Outcome: domain.OutcomeOK, Available: availability(true), Detail: "IMAX VOLVO · 2026-12-18 19:30"},
		{Outcome: domain.OutcomeError, Err: errors.New("Cinema City returned HTTP 503")},
		{Outcome: domain.OutcomeOK, Available: availability(true), Detail: "IMAX VOLVO · 2026-12-18 19:30"},
	}}
	mailer := &recordingMailer{}
	service := testService(t, repo, checker, mailer)
	ctx := context.Background()

	ev, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil || !ev.IsOn || mailer.sends != 1 {
		t.Fatalf("first check: on=%v sends=%d err=%v", ev.IsOn, mailer.sends, err)
	}

	ev, err = service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.IsOn || !ev.NotifiedOn || ev.ConsecutiveFailures != 1 || ev.ErrorCount != 1 {
		t.Fatalf("error check must preserve on/notified and record backoff: %+v", ev)
	}

	ev, err = service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.IsOn || ev.ConsecutiveFailures != 0 || mailer.sends != 1 {
		t.Fatalf("recovery must not duplicate notification: on=%v failures=%d sends=%d",
			ev.IsOn, ev.ConsecutiveFailures, mailer.sends)
	}
}

func TestRepeatedBlocksAutoPauseAndNotifyOwner(t *testing.T) {
	repo := newFakeRepo(baseEvent())
	checker := &queueChecker{results: []watch.Result{
		{Outcome: domain.OutcomeBlocked, Err: errors.New("HTTP 429"), RetryAfter: 20 * time.Minute},
		{Outcome: domain.OutcomeBlocked, Err: errors.New("HTTP 403")},
		{Outcome: domain.OutcomeBlocked, Err: errors.New("HTTP 403")},
	}}
	mailer := &recordingMailer{}
	service := testService(t, repo, checker, mailer)
	ctx := context.Background()

	for i := 1; i <= 2; i++ {
		ev, err := service.CheckNow(ctx, testUserID, testEventID)
		if err != nil {
			t.Fatal(err)
		}
		if !ev.Enabled || mailer.pauseSends != 0 || ev.ConsecutiveBlocks != i {
			t.Fatalf("block %d unexpected state: enabled=%v pauseSends=%d blocks=%d",
				i, ev.Enabled, mailer.pauseSends, ev.ConsecutiveBlocks)
		}
	}

	ev, err := service.CheckNow(ctx, testUserID, testEventID)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Enabled || ev.PausedReason == "" || mailer.pauseSends != 1 {
		t.Fatalf("third block must auto-pause and notify: enabled=%v reason=%q pauseSends=%d",
			ev.Enabled, ev.PausedReason, mailer.pauseSends)
	}
}
