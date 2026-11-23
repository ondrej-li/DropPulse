package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

const eventColumns = `
	e.id::text,
	e.user_id::text,
	e.name,
	e.url,
	e.check_interval_seconds,
	e.enabled,
	e.is_on,
	e.notified_on,
	e.last_checked_at,
	e.last_status_code,
	e.last_error,
	e.last_notified_at,
	e.last_notification_error,
	e.created_at,
	e.updated_at,
	coalesce((select u.email
	          from users u
	          where u.id = e.user_id
	            and u.email_verified
	            and u.deleted_at is null), '')`

func scanEvent(row rowScanner) (domain.Event, error) {
	var (
		event          domain.Event
		lastChecked    *time.Time
		lastStatusCode *int
		lastNotified   *time.Time
	)
	if err := row.Scan(
		&event.ID,
		&event.UserID,
		&event.Name,
		&event.URL,
		&event.CheckIntervalSeconds,
		&event.Enabled,
		&event.IsOn,
		&event.NotifiedOn,
		&lastChecked,
		&lastStatusCode,
		&event.LastError,
		&lastNotified,
		&event.LastNotificationErr,
		&event.CreatedAt,
		&event.UpdatedAt,
		&event.UserEmail,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Event{}, ErrNotFound
		}
		return domain.Event{}, err
	}
	event.LastCheckedAt = lastChecked
	event.LastStatusCode = lastStatusCode
	event.LastNotifiedAt = lastNotified
	return event, nil
}

// EnsureLocalAdmin creates the fixed single-user account, or repairs its role
// and status if the row already exists. It is idempotent and serialised so two
// starts cannot race on the partial unique username index.
func (s *Store) EnsureLocalAdmin(ctx context.Context, username string) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, fmt.Errorf("begin local admin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"select pg_advisory_xact_lock(hashtextextended($1, 0))",
		"local-admin:"+username,
	); err != nil {
		return domain.User{}, fmt.Errorf("lock local admin: %w", err)
	}

	user, err := scanUser(tx.QueryRow(ctx,
		"select "+userColumns+" from users u where u.username = $1 and u.deleted_at is null for update",
		username,
	))
	switch {
	case err == nil:
		if user.Role != domain.RoleAdmin || user.Status != domain.StatusActive {
			if _, err := tx.Exec(ctx, `
				update users
				set role = 'admin', status = 'active', updated_at = now()
				where id = $1::uuid`,
				user.ID,
			); err != nil {
				return domain.User{}, fmt.Errorf("repair local admin: %w", err)
			}
			user.Role = domain.RoleAdmin
			user.Status = domain.StatusActive
		}
	case !errors.Is(err, ErrNotFound):
		return domain.User{}, fmt.Errorf("find local admin: %w", err)
	default:
		user, err = scanUser(tx.QueryRow(ctx, `
			insert into users as u (username, display_name, role, status)
			values ($1, 'Admin', 'admin', 'active')
			returning `+userColumns,
			username,
		))
		if err != nil {
			return domain.User{}, fmt.Errorf("create local admin: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit local admin: %w", err)
	}
	return user, nil
}

// ListEvents returns a user's events, newest first.
func (s *Store) ListEvents(ctx context.Context, userID string) ([]domain.Event, error) {
	rows, err := s.pool.Query(ctx,
		"select "+eventColumns+" from events e where e.user_id = $1::uuid order by e.created_at desc",
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.Event, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

// GetEvent loads one event belonging to userID.
func (s *Store) GetEvent(ctx context.Context, userID, id string) (domain.Event, error) {
	if !domain.ValidUUID(id) {
		return domain.Event{}, ErrNotFound
	}
	return scanEvent(s.pool.QueryRow(ctx,
		"select "+eventColumns+" from events e where e.id = $1::uuid and e.user_id = $2::uuid",
		id, userID,
	))
}

// GetEventByID loads one event without a user filter. The scheduler is the
// only caller; user-facing reads use GetEvent.
func (s *Store) GetEventByID(ctx context.Context, id string) (domain.Event, error) {
	if !domain.ValidUUID(id) {
		return domain.Event{}, ErrNotFound
	}
	return scanEvent(s.pool.QueryRow(ctx,
		"select "+eventColumns+" from events e where e.id = $1::uuid",
		id,
	))
}

// CreateEvent inserts a validated event. Its first check is due immediately.
func (s *Store) CreateEvent(ctx context.Context, userID string, input domain.EventInput) (domain.Event, error) {
	return scanEvent(s.pool.QueryRow(ctx, `
		insert into events as e (user_id, name, url, check_interval_seconds, enabled)
		values ($1::uuid, $2, $3, $4, $5)
		returning `+eventColumns,
		userID, input.Name, input.URL, input.CheckIntervalSeconds, input.Enabled,
	))
}

// UpdateEvent replaces the editable fields. A URL change re-arms both the
// availability state and the notification edge, and makes the next check
// immediate. Re-enabling a disabled event also checks it immediately.
func (s *Store) UpdateEvent(ctx context.Context, userID, id string, input domain.EventInput) (domain.Event, error) {
	if !domain.ValidUUID(id) {
		return domain.Event{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Event{}, fmt.Errorf("begin update event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanEvent(tx.QueryRow(ctx,
		"select "+eventColumns+" from events e where e.id = $1::uuid and e.user_id = $2::uuid for update",
		id, userID,
	))
	if err != nil {
		return domain.Event{}, err
	}
	urlChanged := current.URL != input.URL
	reenabled := !current.Enabled && input.Enabled
	updated, err := scanEvent(tx.QueryRow(ctx, `
		update events as e
		set name = $2,
		    url = $3,
		    check_interval_seconds = $4,
		    enabled = $5,
		    is_on = case when $6 then false else e.is_on end,
		    notified_on = case when $6 then false else e.notified_on end,
		    last_checked_at = case when $6 or $7 then null else e.last_checked_at end,
		    last_status_code = case when $6 then null else e.last_status_code end,
		    last_error = case when $6 then '' else e.last_error end,
		    last_notification_error = case when $6 then '' else e.last_notification_error end,
		    updated_at = now()
		where e.id = $1::uuid
		returning `+eventColumns,
		id, input.Name, input.URL, input.CheckIntervalSeconds, input.Enabled, urlChanged, reenabled,
	))
	if err != nil {
		return domain.Event{}, fmt.Errorf("update event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Event{}, fmt.Errorf("commit update event: %w", err)
	}
	return updated, nil
}

// DeleteEvent removes one event belonging to userID.
func (s *Store) DeleteEvent(ctx context.Context, userID, id string) (bool, error) {
	if !domain.ValidUUID(id) {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx,
		"delete from events where id = $1::uuid and user_id = $2::uuid",
		id, userID,
	)
	if err != nil {
		return false, fmt.Errorf("delete event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DueEvents returns enabled events for active accounts whose interval has
// elapsed, oldest check first. The limit bounds one scheduler tick.
func (s *Store) DueEvents(ctx context.Context, limit int) ([]domain.Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		select `+eventColumns+`
		from events e
		join users u on u.id = e.user_id
		where e.enabled
		  and u.status = 'active'
		  and u.deleted_at is null
		  and (e.last_checked_at is null
		       or e.last_checked_at <= now() - (e.check_interval_seconds * interval '1 second'))
		order by e.last_checked_at nulls first, e.created_at
		limit $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.Event, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due events: %w", err)
	}
	return events, nil
}

// RecordCheck stores one result and computes the notification edge under a row
// lock. notified_on stays true only while the event has been continuously on
// since the last email, so every off->on transition re-arms it. available is
// non-nil for a provider check and overrides the HTTP-status interpretation.
func (s *Store) RecordCheck(ctx context.Context, eventID string, statusCode *int, available *bool, errText string) (domain.Event, error) {
	if !domain.ValidUUID(eventID) {
		return domain.Event{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Event{}, fmt.Errorf("begin record check: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanEvent(tx.QueryRow(ctx,
		"select "+eventColumns+" from events e where e.id = $1::uuid for update",
		eventID,
	))
	if err != nil {
		return domain.Event{}, err
	}
	isOn := statusCode != nil && *statusCode == httpStatusOK
	if available != nil {
		isOn = *available
	}
	notifiedOn := isOn && current.IsOn && current.NotifiedOn
	var status any
	if statusCode != nil {
		status = *statusCode
	}

	updated, err := scanEvent(tx.QueryRow(ctx, `
		update events as e
		set is_on = $2,
		    notified_on = $3,
		    last_status_code = $4,
		    last_checked_at = now(),
		    last_error = $5,
		    updated_at = now()
		where e.id = $1::uuid
		returning `+eventColumns,
		eventID, isOn, notifiedOn, status, errText,
	))
	if err != nil {
		return domain.Event{}, fmt.Errorf("record event check: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Event{}, fmt.Errorf("commit event check: %w", err)
	}
	return updated, nil
}

// MarkNotified records a successful email, or the failure that should be
// retried on the next check while the event is still on.
func (s *Store) MarkNotified(ctx context.Context, eventID, notificationErr string) error {
	if !domain.ValidUUID(eventID) {
		return ErrNotFound
	}
	_, err := s.pool.Exec(ctx, `
		update events
		set notified_on = case when $2 = '' then true else notified_on end,
		    last_notified_at = case when $2 = '' then now() else last_notified_at end,
		    last_notification_error = $2,
		    updated_at = now()
		where id = $1::uuid`,
		eventID, notificationErr,
	)
	if err != nil {
		return fmt.Errorf("mark event notified: %w", err)
	}
	return nil
}

// CountActiveEvents is the usage counter exposed by /api/v1/me.
func (s *Store) CountActiveEvents(ctx context.Context, userID string) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx,
		"select count(*) from events where user_id = $1::uuid and enabled",
		userID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active events: %w", err)
	}
	return count, nil
}

const httpStatusOK = 200
