package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// ErrProtectedAdmin is returned when an admin attempts to ban, unban, or remove
// an administrator account.
var ErrProtectedAdmin = errors.New("admin accounts cannot be modified or removed")

const adminEventColumns = eventColumns + `,
	u.id::text,
	coalesce(u.email, ''),
	coalesce(u.display_name, ''),
	coalesce(u.username, ''),
	u.role,
	u.status`

func scanAdminEvent(row rowScanner) (domain.AdminEvent, error) {
	var (
		ae             domain.AdminEvent
		lastChecked    *time.Time
		nextCheck      *time.Time
		lastStatusCode *int
		lastOutcome    string
		lastDurationMs *int
		lastNotified   *time.Time
		ownerRole      string
		ownerStatus    string
	)
	if err := row.Scan(
		&ae.ID,
		&ae.UserID,
		&ae.Name,
		&ae.URL,
		&ae.CheckIntervalSeconds,
		&ae.Enabled,
		&ae.IsOn,
		&ae.NotifiedOn,
		&lastChecked,
		&nextCheck,
		&lastStatusCode,
		&lastOutcome,
		&ae.LastDetail,
		&ae.LastError,
		&ae.PausedReason,
		&ae.ConsecutiveFailures,
		&ae.ConsecutiveBlocks,
		&ae.CheckCount,
		&ae.ErrorCount,
		&lastDurationMs,
		&lastNotified,
		&ae.LastNotificationErr,
		&ae.CreatedAt,
		&ae.UpdatedAt,
		&ae.UserEmail,
		&ae.OwnerID,
		&ae.OwnerEmail,
		&ae.OwnerDisplayName,
		&ae.OwnerUsername,
		&ownerRole,
		&ownerStatus,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AdminEvent{}, ErrNotFound
		}
		return domain.AdminEvent{}, err
	}
	ae.LastCheckedAt = lastChecked
	ae.NextCheckAt = nextCheck
	ae.LastStatusCode = lastStatusCode
	ae.LastOutcome = domain.CheckOutcome(lastOutcome)
	ae.LastDurationMs = lastDurationMs
	ae.LastNotifiedAt = lastNotified
	ae.OwnerRole = domain.Role(ownerRole)
	ae.OwnerStatus = domain.UserStatus(ownerStatus)
	return ae, nil
}

// AdminOverview returns fleet-wide watcher, user, and 24-hour check statistics.
func (s *Store) AdminOverview(ctx context.Context) (domain.AdminOverview, error) {
	var ov domain.AdminOverview
	if err := s.pool.QueryRow(ctx, `
		select
			count(*),
			count(*) filter (where enabled),
			count(*) filter (where is_on),
			count(*) filter (where enabled and consecutive_failures > 0),
			count(*) filter (where last_outcome = 'blocked'),
			count(*) filter (where not enabled and paused_reason <> '')
		from events`,
	).Scan(
		&ov.TotalWatchers,
		&ov.EnabledWatchers,
		&ov.OnWatchers,
		&ov.BackingOffWatchers,
		&ov.BlockedWatchers,
		&ov.AutoPausedWatchers,
	); err != nil {
		return domain.AdminOverview{}, fmt.Errorf("admin watcher overview: %w", err)
	}

	if err := s.pool.QueryRow(ctx, `
		select
			count(*),
			count(*) filter (where status = 'active'),
			count(*) filter (where status = 'suspended')
		from users
		where deleted_at is null`,
	).Scan(&ov.TotalUsers, &ov.ActiveUsers, &ov.SuspendedUsers); err != nil {
		return domain.AdminOverview{}, fmt.Errorf("admin user overview: %w", err)
	}

	if err := s.pool.QueryRow(ctx, `
		select
			count(*),
			count(*) filter (where outcome = 'error'),
			count(*) filter (where outcome = 'blocked')
		from event_checks
		where checked_at >= now() - interval '24 hours'`,
	).Scan(&ov.ChecksLast24h, &ov.ErrorsLast24h, &ov.BlocksLast24h); err != nil {
		return domain.AdminOverview{}, fmt.Errorf("admin check overview: %w", err)
	}
	return ov, nil
}

// AdminListEvents returns all watchers across all accounts, newest first.
func (s *Store) AdminListEvents(ctx context.Context, limit int) ([]domain.AdminEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		select `+adminEventColumns+`
		from events e
		join users u on u.id = e.user_id
		order by e.created_at desc
		limit $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("admin list events: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AdminEvent, 0)
	for rows.Next() {
		ev, err := scanAdminEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin list events: %w", err)
	}
	return out, nil
}

// AdminGetEvent loads any watcher by ID along with owner metadata.
func (s *Store) AdminGetEvent(ctx context.Context, id string) (domain.AdminEvent, error) {
	if !domain.ValidUUID(id) {
		return domain.AdminEvent{}, ErrNotFound
	}
	return scanAdminEvent(s.pool.QueryRow(ctx, `
		select `+adminEventColumns+`
		from events e
		join users u on u.id = e.user_id
		where e.id = $1::uuid`,
		id,
	))
}

// AdminUpdateEvent updates any watcher regardless of owner.
func (s *Store) AdminUpdateEvent(ctx context.Context, id string, input domain.EventInput) (domain.AdminEvent, error) {
	if !domain.ValidUUID(id) {
		return domain.AdminEvent{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AdminEvent{}, fmt.Errorf("begin admin update event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanEvent(tx.QueryRow(ctx,
		"select "+eventColumns+" from events e where e.id = $1::uuid for update",
		id,
	))
	if err != nil {
		return domain.AdminEvent{}, err
	}
	urlChanged := current.URL != input.URL
	reenabled := !current.Enabled && input.Enabled
	if _, err := tx.Exec(ctx, `
		update events as e
		set name = $2,
		    url = $3,
		    check_interval_seconds = $4,
		    enabled = $5,
		    is_on = case when $6 then false else e.is_on end,
		    notified_on = case when $6 then false else e.notified_on end,
		    last_checked_at = case when $6 or $7 then null else e.last_checked_at end,
		    next_check_at = case when $6 or $7 then null else e.next_check_at end,
		    last_status_code = case when $6 then null else e.last_status_code end,
		    last_outcome = case when $6 then '' else e.last_outcome end,
		    last_detail = case when $6 then '' else e.last_detail end,
		    last_error = case when $6 then '' else e.last_error end,
		    paused_reason = case when $6 or $7 then '' else e.paused_reason end,
		    consecutive_failures = case when $6 or $7 then 0 else e.consecutive_failures end,
		    consecutive_blocks = case when $6 or $7 then 0 else e.consecutive_blocks end,
		    last_duration_ms = case when $6 then null else e.last_duration_ms end,
		    last_notification_error = case when $6 then '' else e.last_notification_error end,
		    updated_at = now()
		where e.id = $1::uuid`,
		id, input.Name, input.URL, input.CheckIntervalSeconds, input.Enabled, urlChanged, reenabled,
	); err != nil {
		return domain.AdminEvent{}, fmt.Errorf("admin update event: %w", err)
	}
	updated, err := scanAdminEvent(tx.QueryRow(ctx, `
		select `+adminEventColumns+`
		from events e
		join users u on u.id = e.user_id
		where e.id = $1::uuid`,
		id,
	))
	if err != nil {
		return domain.AdminEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminEvent{}, fmt.Errorf("commit admin update event: %w", err)
	}
	return updated, nil
}

// AdminResetEventBackoff clears failure/block backoff counters and makes the
// watcher immediately eligible for scheduling (optionally re-enabling it).
func (s *Store) AdminResetEventBackoff(ctx context.Context, id string, enable bool) (domain.AdminEvent, error) {
	if !domain.ValidUUID(id) {
		return domain.AdminEvent{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AdminEvent{}, fmt.Errorf("begin reset backoff: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		update events
		set consecutive_failures = 0,
		    consecutive_blocks = 0,
		    paused_reason = '',
		    next_check_at = now(),
		    enabled = case when $2 then true else enabled end,
		    updated_at = now()
		where id = $1::uuid`,
		id, enable,
	)
	if err != nil {
		return domain.AdminEvent{}, fmt.Errorf("reset backoff: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.AdminEvent{}, ErrNotFound
	}
	updated, err := scanAdminEvent(tx.QueryRow(ctx, `
		select `+adminEventColumns+`
		from events e
		join users u on u.id = e.user_id
		where e.id = $1::uuid`,
		id,
	))
	if err != nil {
		return domain.AdminEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminEvent{}, fmt.Errorf("commit reset backoff: %w", err)
	}
	return updated, nil
}

// AdminDeleteEvent deletes any watcher by ID.
func (s *Store) AdminDeleteEvent(ctx context.Context, id string) (bool, error) {
	if !domain.ValidUUID(id) {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, "delete from events where id = $1::uuid", id)
	if err != nil {
		return false, fmt.Errorf("admin delete event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListEventChecks returns recent check history for a single watcher.
func (s *Store) ListEventChecks(ctx context.Context, eventID string, limit int) ([]domain.EventCheckLog, error) {
	if !domain.ValidUUID(eventID) {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		select c.id, c.event_id::text, e.name, e.url, c.checked_at, c.trigger,
		       c.outcome, c.is_on, c.status_code, c.detail, c.error, c.duration_ms
		from event_checks c
		join events e on e.id = c.event_id
		where c.event_id = $1::uuid
		order by c.checked_at desc, c.id desc
		limit $2`,
		eventID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list event checks: %w", err)
	}
	defer rows.Close()
	return scanEventChecks(rows)
}

// ListRecentChecks returns the newest check logs across all watchers.
func (s *Store) ListRecentChecks(ctx context.Context, limit int) ([]domain.EventCheckLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		select c.id, c.event_id::text, e.name, e.url, c.checked_at, c.trigger,
		       c.outcome, c.is_on, c.status_code, c.detail, c.error, c.duration_ms
		from event_checks c
		join events e on e.id = c.event_id
		order by c.checked_at desc, c.id desc
		limit $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list recent checks: %w", err)
	}
	defer rows.Close()
	return scanEventChecks(rows)
}

func scanEventChecks(rows pgx.Rows) ([]domain.EventCheckLog, error) {
	out := make([]domain.EventCheckLog, 0)
	for rows.Next() {
		var (
			entry   domain.EventCheckLog
			trigger string
			outcome string
		)
		if err := rows.Scan(
			&entry.ID,
			&entry.EventID,
			&entry.EventName,
			&entry.EventURL,
			&entry.CheckedAt,
			&trigger,
			&outcome,
			&entry.IsOn,
			&entry.StatusCode,
			&entry.Detail,
			&entry.Error,
			&entry.DurationMs,
		); err != nil {
			return nil, err
		}
		entry.Trigger = domain.CheckTrigger(trigger)
		entry.Outcome = domain.CheckOutcome(outcome)
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// PurgeOldEventChecks removes historical check logs older than retention.
func (s *Store) PurgeOldEventChecks(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		retention = 90 * 24 * time.Hour
	}
	tag, err := s.pool.Exec(ctx,
		"delete from event_checks where checked_at < now() - ($1 * interval '1 second')",
		retention.Seconds(),
	)
	if err != nil {
		return 0, fmt.Errorf("purge old event checks: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RecordAdminAudit appends an entry to admin_audit_log.
func (s *Store) RecordAdminAudit(ctx context.Context, actorID, action, targetID, requestID string, metadata map[string]any) error {
	var actor any
	if domain.ValidUUID(actorID) {
		actor = actorID
	}
	var target any
	if domain.ValidUUID(targetID) {
		target = targetID
	}
	encoded := []byte("{}")
	if len(metadata) > 0 {
		if raw, err := json.Marshal(metadata); err == nil {
			encoded = raw
		}
	}
	if _, err := s.pool.Exec(ctx, `
		insert into admin_audit_log (actor_id, action, target_id, request_id, metadata)
		values ($1::uuid, $2, $3::uuid, $4, $5::jsonb)`,
		actor, truncate(action, 80), target, truncate(requestID, 64), encoded,
	); err != nil {
		return fmt.Errorf("record admin audit: %w", err)
	}
	return nil
}

// ListAdminAudit returns the newest administrator audit log entries.
func (s *Store) ListAdminAudit(ctx context.Context, limit int) ([]domain.AdminAuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		select a.id,
		       coalesce(a.actor_id::text, ''),
		       coalesce(u.email, ''),
		       coalesce(nullif(u.display_name, ''), nullif(u.username, ''), ''),
		       a.action,
		       coalesce(a.target_id::text, ''),
		       a.request_id,
		       a.metadata,
		       a.created_at
		from admin_audit_log a
		left join users u on u.id = a.actor_id
		order by a.created_at desc, a.id desc
		limit $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AdminAuditEntry, 0)
	for rows.Next() {
		var (
			entry domain.AdminAuditEntry
			raw   []byte
		)
		if err := rows.Scan(
			&entry.ID,
			&entry.ActorID,
			&entry.ActorEmail,
			&entry.ActorName,
			&entry.Action,
			&entry.TargetID,
			&entry.RequestID,
			&raw,
			&entry.CreatedAt,
		); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			var meta map[string]any
			if err := json.Unmarshal(raw, &meta); err == nil {
				entry.Metadata = meta
			}
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	return out, nil
}

// AdminListUsers returns all non-deleted accounts along with watch and session
// counts.
func (s *Store) AdminListUsers(ctx context.Context, limit int) ([]domain.AdminUser, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		select `+userColumns+`,
		       (select count(*) from events e where e.user_id = u.id and e.enabled),
		       (select count(*) from events e where e.user_id = u.id),
		       (select count(*) from sessions s
		        where s.user_id = u.id
		          and s.revoked_at is null
		          and s.idle_expires_at > now()
		          and s.absolute_expires_at > now())
		from users u
		where u.deleted_at is null
		order by u.created_at desc
		limit $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("admin list users: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AdminUser, 0)
	for rows.Next() {
		au, err := scanAdminUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, au)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin list users: %w", err)
	}
	return out, nil
}

func scanAdminUser(row rowScanner) (domain.AdminUser, error) {
	var (
		au            domain.AdminUser
		role, status  string
		termsAccepted *time.Time
		lastLogin     *time.Time
		deleted       *time.Time
	)
	if err := row.Scan(
		&au.ID,
		&au.Username,
		&au.Email,
		&au.EmailVerified,
		&au.DisplayName,
		&au.AvatarURL,
		&role,
		&status,
		&au.TermsVersion,
		&termsAccepted,
		&au.CreatedAt,
		&au.UpdatedAt,
		&lastLogin,
		&deleted,
		&au.ActiveWatches,
		&au.TotalWatches,
		&au.ActiveSessions,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AdminUser{}, ErrNotFound
		}
		return domain.AdminUser{}, err
	}
	au.Role = domain.Role(role)
	au.Status = domain.UserStatus(status)
	au.TermsAccepted = termsAccepted
	au.LastLoginAt = lastLogin
	au.DeletedAt = deleted
	return au, nil
}

// AdminSetUserStatus bans (suspends) or unbans (reactivates) a non-admin
// account. Admin accounts can never be banned or modified through this path.
// Suspending an account immediately revokes all of its live sessions.
func (s *Store) AdminSetUserStatus(ctx context.Context, userID string, status domain.UserStatus) (domain.AdminUser, error) {
	if !domain.ValidUUID(userID) {
		return domain.AdminUser{}, ErrNotFound
	}
	if status != domain.StatusActive && status != domain.StatusSuspended {
		return domain.AdminUser{}, errors.New("status must be active or suspended")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AdminUser{}, fmt.Errorf("begin admin user status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	target, err := scanUser(tx.QueryRow(ctx,
		"select "+userColumns+" from users u where u.id = $1::uuid and u.deleted_at is null for update",
		userID,
	))
	if err != nil {
		return domain.AdminUser{}, err
	}
	if target.Role == domain.RoleAdmin {
		return domain.AdminUser{}, ErrProtectedAdmin
	}

	tag, err := tx.Exec(ctx, `
		update users
		set status = $2, updated_at = now()
		where id = $1::uuid and deleted_at is null and role <> 'admin'`,
		userID, string(status),
	)
	if err != nil {
		return domain.AdminUser{}, fmt.Errorf("update user status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.AdminUser{}, ErrProtectedAdmin
	}

	if status == domain.StatusSuspended {
		if err := revokeAllUserSessionsTx(ctx, tx, userID, "admin_ban"); err != nil {
			return domain.AdminUser{}, err
		}
	}

	updated, err := scanAdminUser(tx.QueryRow(ctx, `
		select `+userColumns+`,
		       (select count(*) from events e where e.user_id = u.id and e.enabled),
		       (select count(*) from events e where e.user_id = u.id),
		       (select count(*) from sessions s
		        where s.user_id = u.id
		          and s.revoked_at is null
		          and s.idle_expires_at > now()
		          and s.absolute_expires_at > now())
		from users u
		where u.id = $1::uuid`,
		userID,
	))
	if err != nil {
		return domain.AdminUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AdminUser{}, fmt.Errorf("commit admin user status: %w", err)
	}
	return updated, nil
}

// AdminRemoveUser deletes a non-admin account: revokes all sessions, removes
// linked OAuth identities and watches, and soft-deletes/anonymises the user
// row. Admin accounts can never be removed through this path.
func (s *Store) AdminRemoveUser(ctx context.Context, userID string) error {
	if !domain.ValidUUID(userID) {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin admin remove user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	target, err := scanUser(tx.QueryRow(ctx,
		"select "+userColumns+" from users u where u.id = $1::uuid and u.deleted_at is null for update",
		userID,
	))
	if err != nil {
		return err
	}
	if target.Role == domain.RoleAdmin {
		return ErrProtectedAdmin
	}

	if err := revokeAllUserSessionsTx(ctx, tx, userID, "admin_removed"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "delete from events where user_id = $1::uuid", userID); err != nil {
		return fmt.Errorf("delete user events: %w", err)
	}
	if _, err := tx.Exec(ctx, "delete from user_identities where user_id = $1::uuid", userID); err != nil {
		return fmt.Errorf("delete user identities: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		update users
		set status = 'deleted',
		    deleted_at = now(),
		    email = null,
		    email_verified = false,
		    username = null,
		    display_name = null,
		    avatar_url = null,
		    updated_at = now()
		where id = $1::uuid and deleted_at is null and role <> 'admin'`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("anonymise removed user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProtectedAdmin
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit admin remove user: %w", err)
	}
	return nil
}

func revokeAllUserSessionsTx(ctx context.Context, tx pgx.Tx, userID, reason string) error {
	if _, err := tx.Exec(ctx, `
		update session_tokens
		set revoked_at = now()
		where revoked_at is null
		  and session_id in (select id from sessions where user_id = $1::uuid)`,
		userID,
	); err != nil {
		return fmt.Errorf("revoke user session tokens: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update sessions
		set revoked_at = coalesce(revoked_at, now()),
		    revoked_reason = coalesce(revoked_reason, $2)
		where user_id = $1::uuid and revoked_at is null`,
		userID, truncate(reason, 100),
	); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}
