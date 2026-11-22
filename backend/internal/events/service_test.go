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

func (f *fakeRepo) RecordCheck(_ context.Context, eventID string, statusCode *int, errText string) (domain.Event, error) {
	event, ok := f.events[eventID]
	if !ok {
		return domain.Event{}, store.ErrNotFound
	}
	wasOn := event.IsOn
	wasNotified := event.NotifiedOn
	isOn := statusCode != nil && *statusCode == 200
	event.IsOn = isOn
	event.NotifiedOn = isOn && wasOn && wasNotified
	event.LastStatusCode = statusCode
	event.LastError = errText
	now := time.Now()
	event.LastCheckedAt = &now
	f.events[eventID] = event
	return event, nil
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
	sends    int
	failures int
}

func (m *recordingMailer) SendEventOn(context.Context, domain.Event, string) error {
	m.sends++
	if m.failures > 0 {
		m.failures--
		return errors.New("smtp unavailable")
	}
	return nil
}

func status(code int) *int { return &code }

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
