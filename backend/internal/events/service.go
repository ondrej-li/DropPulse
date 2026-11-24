// Package events owns the event lifecycle: validated CRUD, the due-check
// scheduler, and the off->on notification edge. Every operation is scoped to a
// user; single-user mode simply supplies one fixed local principal.
package events

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
	"github.com/ondrej-li/DropPulse/backend/internal/watch"
)

// Repository is the subset of the store used by the service. It is an
// interface so the transition rules can be unit-tested without Postgres.
type Repository interface {
	ListEvents(ctx context.Context, userID string) ([]domain.Event, error)
	GetEvent(ctx context.Context, userID, id string) (domain.Event, error)
	GetEventByID(ctx context.Context, id string) (domain.Event, error)
	CreateEvent(ctx context.Context, userID string, input domain.EventInput) (domain.Event, error)
	UpdateEvent(ctx context.Context, userID, id string, input domain.EventInput) (domain.Event, error)
	DeleteEvent(ctx context.Context, userID, id string) (bool, error)
	DueEvents(ctx context.Context, limit int) ([]domain.Event, error)
	RecordCheck(ctx context.Context, eventID string, statusCode *int, available *bool, errText string) (domain.Event, error)
	MarkNotified(ctx context.Context, eventID, notificationErr string) error
	CountActiveEvents(ctx context.Context, userID string) (int, error)
}

// Checker performs one availability check: a generic HTTPS status request or a
// provider lookup, depending on the target.
type Checker interface {
	Check(ctx context.Context, target string) watch.Result
}

// Mailer sends one availability email.
type Mailer interface {
	SendEventOn(ctx context.Context, event domain.Event, uiURL string) error
}

// ValidationError is safe to show to the local UI.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Service coordinates storage, checking and notification.
type Service struct {
	repo    Repository
	checker Checker
	mailer  Mailer
	logger  *slog.Logger
	admin   domain.User
	uiURL   string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewService builds the event service. admin is the fixed single-user account
// and may be zero in multi-user mode; uiURL is the frontend link used in email.
func NewService(repo Repository, checker Checker, mailer Mailer, logger *slog.Logger, admin domain.User, uiURL string) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:    repo,
		checker: checker,
		mailer:  mailer,
		logger:  logger,
		admin:   admin,
		uiURL:   uiURL,
		locks:   make(map[string]*sync.Mutex),
	}
}

// Admin returns the fixed single-user account.
func (s *Service) Admin() domain.User { return s.admin }

// List returns the user's events.
func (s *Service) List(ctx context.Context, userID string) ([]domain.Event, error) {
	return s.repo.ListEvents(ctx, userID)
}

// Get returns one event belonging to the user.
func (s *Service) Get(ctx context.Context, userID, id string) (domain.Event, error) {
	return s.repo.GetEvent(ctx, userID, id)
}

// CountActive returns the number of enabled events.
func (s *Service) CountActive(ctx context.Context, userID string) (int, error) {
	return s.repo.CountActiveEvents(ctx, userID)
}

// Create validates and inserts an event.
func (s *Service) Create(ctx context.Context, userID string, input domain.EventInput) (domain.Event, error) {
	validated, err := validateInput(input)
	if err != nil {
		return domain.Event{}, err
	}
	return s.repo.CreateEvent(ctx, userID, validated)
}

// Update validates and replaces an event's editable fields.
func (s *Service) Update(ctx context.Context, userID, id string, input domain.EventInput) (domain.Event, error) {
	validated, err := validateInput(input)
	if err != nil {
		return domain.Event{}, err
	}
	return s.repo.UpdateEvent(ctx, userID, id, validated)
}

// Delete removes an event.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	found, err := s.repo.DeleteEvent(ctx, userID, id)
	if err != nil {
		return err
	}
	if !found {
		return store.ErrNotFound
	}
	return nil
}

// CheckNow checks one event immediately, outside the scheduler.
func (s *Service) CheckNow(ctx context.Context, userID, id string) (domain.Event, error) {
	event, err := s.repo.GetEvent(ctx, userID, id)
	if err != nil {
		return domain.Event{}, err
	}
	return s.check(ctx, event.ID)
}

// Run performs due checks until ctx is cancelled. The tick is short relative
// to the minimum interval so a newly created event is checked promptly; the
// due predicate in SQL decides what actually runs.
func (s *Service) Run(ctx context.Context) {
	const tick = 15 * time.Second
	s.runDue(ctx)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(ctx)
		}
	}
}

func (s *Service) runDue(ctx context.Context) {
	due, err := s.repo.DueEvents(ctx, 50)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("list due events", "err", err)
		}
		return
	}
	if len(due) == 0 {
		return
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, event := range due {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(eventID string) {
			defer func() {
				<-sem
				wg.Done()
			}()
			if _, err := s.check(ctx, eventID); err != nil {
				if ctx.Err() == nil && !errors.Is(err, store.ErrNotFound) {
					s.logger.Warn("scheduled event check", "event_id", eventID, "err", err)
				}
			}
		}(event.ID)
	}
	wg.Wait()
}

// check serialises all checks of one event, records the result, and sends one
// email when the event has just become available. A failed email leaves
// notified_on false, so the next check retries while the event remains on.
func (s *Service) check(ctx context.Context, eventID string) (domain.Event, error) {
	lock := s.eventLock(eventID)
	lock.Lock()
	defer lock.Unlock()

	event, err := s.repo.GetEventByID(ctx, eventID)
	if err != nil {
		return domain.Event{}, err
	}
	result := s.checker.Check(ctx, event.URL)
	var (
		status    *int
		available *bool
		errText   string
	)
	if result.Err != nil {
		errText = domain.TruncateError(result.Err.Error())
	} else {
		status = result.StatusCode
		available = result.Available
	}
	updated, err := s.repo.RecordCheck(ctx, event.ID, status, available, errText)
	if err != nil {
		return domain.Event{}, err
	}

	if updated.IsOn && !updated.NotifiedOn && updated.Enabled && s.mailer != nil {
		notificationErr := ""
		if err := s.mailer.SendEventOn(ctx, updated, s.uiURL); err != nil {
			notificationErr = domain.TruncateError(err.Error())
			s.logger.Warn("send availability notification", "event_id", updated.ID, "err", err)
		}
		if err := s.repo.MarkNotified(ctx, updated.ID, notificationErr); err != nil {
			s.logger.Error("record notification result", "event_id", updated.ID, "err", err)
		} else if refreshed, err := s.repo.GetEventByID(ctx, updated.ID); err == nil {
			updated = refreshed
		}
	}
	return updated, nil
}

func (s *Service) eventLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.locks[id]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[id] = lock
	}
	return lock
}

func validateInput(input domain.EventInput) (domain.EventInput, error) {
	name, err := domain.NormaliseEventName(input.Name)
	if err != nil {
		return domain.EventInput{}, &ValidationError{Message: err.Error()}
	}
	target, err := watch.ValidateTargetURL(input.URL)
	if err != nil {
		return domain.EventInput{}, &ValidationError{Message: err.Error()}
	}
	if err := watch.ValidateProviderTarget(target); err != nil {
		return domain.EventInput{}, &ValidationError{Message: err.Error()}
	}
	interval, err := domain.NormaliseCheckInterval(input.CheckIntervalSeconds)
	if err != nil {
		return domain.EventInput{}, &ValidationError{Message: err.Error()}
	}
	return domain.EventInput{
		Name:                 name,
		URL:                  target,
		CheckIntervalSeconds: interval,
		Enabled:              input.Enabled,
	}, nil
}

// Compile-time check that the concrete store satisfies the service's needs.
var _ Repository = (*store.Store)(nil)
