package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// Authenticated is the result of resolving a session token.
type Authenticated struct {
	User      domain.User
	Session   domain.Session
	TokenID   string
	TokenKind string
}

const sessionColumns = `
	s.id::text,
	s.user_id::text,
	s.kind,
	coalesce(s.user_agent, ''),
	coalesce(s.ip, ''),
	s.created_at,
	s.last_used_at,
	s.idle_expires_at,
	s.absolute_expires_at,
	s.revoked_at,
	s.csrf_token_hash`

func scanSession(row rowScanner) (domain.Session, error) {
	var (
		session domain.Session
		kind    string
	)
	if err := row.Scan(
		&session.ID,
		&session.UserID,
		&kind,
		&session.UserAgent,
		&session.IP,
		&session.CreatedAt,
		&session.LastUsedAt,
		&session.IdleExpiresAt,
		&session.AbsoluteExpiresAt,
		&session.RevokedAt,
		&session.CSRFTokenHash,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, ErrNotFound
		}
		return domain.Session{}, err
	}
	session.Kind = domain.SessionKind(kind)
	return session, nil
}

// CreateWebSession creates a browser session and its initial opaque token.
func (s *Store) CreateWebSession(
	ctx context.Context,
	userID string,
	tokenHash, csrfHash []byte,
	userAgent, ip string,
	idleTTL, absoluteTTL time.Duration,
) (domain.Session, error) {
	if !domain.ValidUUID(userID) {
		return domain.Session{}, errors.New("invalid user id")
	}
	if len(tokenHash) != 32 || len(csrfHash) != 32 {
		return domain.Session{}, errors.New("token hashes must be SHA-256")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Session{}, fmt.Errorf("begin session transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	session, err := scanSession(tx.QueryRow(ctx, `
		insert into sessions
			(user_id, kind, user_agent, ip, idle_expires_at, absolute_expires_at, csrf_token_hash)
		values ($1::uuid, 'web', nullif($2, ''), nullif($3, ''),
		        now() + ($4 * interval '1 second'),
		        now() + ($5 * interval '1 second'),
		        $6)
		returning id::text, user_id::text, kind, coalesce(user_agent, ''), coalesce(ip, ''),
		          created_at, last_used_at, idle_expires_at, absolute_expires_at, revoked_at, csrf_token_hash`,
		userID, truncate(userAgent, 512), truncate(ip, 64),
		idleTTL.Seconds(), absoluteTTL.Seconds(), csrfHash,
	))
	if err != nil {
		return domain.Session{}, fmt.Errorf("create session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into session_tokens (session_id, kind, token_hash, expires_at)
		values ($1::uuid, 'web', $2, $3)`,
		session.ID, tokenHash, session.AbsoluteExpiresAt,
	); err != nil {
		return domain.Session{}, fmt.Errorf("create session token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Session{}, fmt.Errorf("commit session: %w", err)
	}
	return session, nil
}

// Authenticate resolves a hashed token to a live session and user. It updates
// the session's sliding idle expiry.
func (s *Store) Authenticate(ctx context.Context, tokenHash []byte, kinds []string, idleTTL time.Duration) (Authenticated, error) {
	if len(tokenHash) != 32 || len(kinds) == 0 {
		return Authenticated{}, ErrUnauthenticated
	}
	var (
		tokenID   string
		tokenKind string
		session   domain.Session
		kind      string
	)
	err := s.pool.QueryRow(ctx, `
		select t.id::text, t.kind, `+sessionColumns+`
		from session_tokens t
		join sessions s on s.id = t.session_id
		where t.token_hash = $1
		  and t.kind = any($2)
		  and t.revoked_at is null
		  and t.expires_at > now()`,
		tokenHash, kinds,
	).Scan(
		&tokenID,
		&tokenKind,
		&session.ID,
		&session.UserID,
		&kind,
		&session.UserAgent,
		&session.IP,
		&session.CreatedAt,
		&session.LastUsedAt,
		&session.IdleExpiresAt,
		&session.AbsoluteExpiresAt,
		&session.RevokedAt,
		&session.CSRFTokenHash,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Authenticated{}, ErrUnauthenticated
		}
		return Authenticated{}, err
	}
	session.Kind = domain.SessionKind(kind)

	now := time.Now().UTC()
	if session.RevokedAt != nil || !now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		_ = s.RevokeSession(ctx, session.ID, "expired")
		return Authenticated{}, ErrUnauthenticated
	}

	user, err := s.GetUser(ctx, session.UserID)
	if err != nil || user.Status != domain.StatusActive {
		_ = s.RevokeSession(ctx, session.ID, "inactive_user")
		return Authenticated{}, ErrUnauthenticated
	}

	newIdle := now.Add(idleTTL)
	if newIdle.After(session.AbsoluteExpiresAt) {
		newIdle = session.AbsoluteExpiresAt
	}
	tag, err := s.pool.Exec(ctx, `
		update sessions
		set last_used_at = now(),
		    idle_expires_at = least(absolute_expires_at, now() + ($2 * interval '1 second'))
		where id = $1::uuid and revoked_at is null`,
		session.ID, idleTTL.Seconds(),
	)
	if err != nil {
		return Authenticated{}, fmt.Errorf("touch session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Authenticated{}, ErrUnauthenticated
	}
	session.LastUsedAt = now
	session.IdleExpiresAt = newIdle

	return Authenticated{
		User:      user,
		Session:   session,
		TokenID:   tokenID,
		TokenKind: tokenKind,
	}, nil
}

// RotateWebToken revokes the current browser token, inserts a replacement and
// rotates the CSRF token in one transaction.
func (s *Store) RotateWebToken(
	ctx context.Context,
	sessionID, oldTokenID string,
	newTokenHash, newCSRFHash []byte,
	idleTTL time.Duration,
) (domain.Session, error) {
	if !domain.ValidUUID(sessionID) || !domain.ValidUUID(oldTokenID) ||
		len(newTokenHash) != 32 || len(newCSRFHash) != 32 {
		return domain.Session{}, ErrUnauthenticated
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Session{}, fmt.Errorf("begin token rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var absolute time.Time
	var revoked *time.Time
	if err := tx.QueryRow(ctx,
		"select absolute_expires_at, revoked_at from sessions where id = $1::uuid for update",
		sessionID,
	).Scan(&absolute, &revoked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, ErrUnauthenticated
		}
		return domain.Session{}, err
	}
	if revoked != nil || !time.Now().UTC().Before(absolute) {
		return domain.Session{}, ErrUnauthenticated
	}

	tag, err := tx.Exec(ctx, `
		update session_tokens
		set revoked_at = now()
		where id = $2::uuid and session_id = $1::uuid and kind = 'web' and revoked_at is null`,
		sessionID, oldTokenID,
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("revoke old session token: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.Session{}, ErrUnauthenticated
	}
	if _, err := tx.Exec(ctx, `
		insert into session_tokens (session_id, kind, token_hash, expires_at)
		values ($1::uuid, 'web', $2, $3)`,
		sessionID, newTokenHash, absolute,
	); err != nil {
		return domain.Session{}, fmt.Errorf("insert rotated session token: %w", err)
	}
	session, err := scanSession(tx.QueryRow(ctx, `
		update sessions
		set csrf_token_hash = $2,
		    last_used_at = now(),
		    idle_expires_at = least(absolute_expires_at, now() + ($3 * interval '1 second'))
		where id = $1::uuid and revoked_at is null
		returning id::text, user_id::text, kind, coalesce(user_agent, ''), coalesce(ip, ''),
		          created_at, last_used_at, idle_expires_at, absolute_expires_at, revoked_at, csrf_token_hash`,
		sessionID, newCSRFHash, idleTTL.Seconds(),
	))
	if err != nil {
		return domain.Session{}, fmt.Errorf("update rotated session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Session{}, fmt.Errorf("commit token rotation: %w", err)
	}
	return session, nil
}

// RotateCSRF stores a new CSRF token hash for a live session.
func (s *Store) RotateCSRF(ctx context.Context, sessionID string, csrfHash []byte) error {
	if !domain.ValidUUID(sessionID) || len(csrfHash) != 32 {
		return errors.New("invalid CSRF rotation")
	}
	tag, err := s.pool.Exec(ctx,
		"update sessions set csrf_token_hash = $2 where id = $1::uuid and revoked_at is null",
		sessionID, csrfHash,
	)
	if err != nil {
		return fmt.Errorf("rotate CSRF token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUnauthenticated
	}
	return nil
}

// RevokeSession revokes a session and all of its tokens. It is idempotent.
func (s *Store) RevokeSession(ctx context.Context, sessionID, reason string) error {
	if !domain.ValidUUID(sessionID) {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		update sessions
		set revoked_at = coalesce(revoked_at, now()),
		    revoked_reason = coalesce(revoked_reason, $2)
		where id = $1::uuid`,
		sessionID, truncate(reason, 100),
	); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update session_tokens set revoked_at = now()
		where session_id = $1::uuid and revoked_at is null`,
		sessionID,
	); err != nil {
		return fmt.Errorf("revoke session tokens: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session revocation: %w", err)
	}
	return nil
}

// ListSessions returns the user's live sessions, newest activity first.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]domain.Session, error) {
	if !domain.ValidUUID(userID) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		select `+sessionColumns+`
		from sessions s
		where s.user_id = $1::uuid
		  and s.revoked_at is null
		  and s.idle_expires_at > now()
		  and s.absolute_expires_at > now()
		order by s.last_used_at desc`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	sessions := make([]domain.Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

// RevokeUserSession revokes one of the user's sessions. It returns false when
// the session does not belong to that user or is already gone.
func (s *Store) RevokeUserSession(ctx context.Context, userID, sessionID string) (bool, error) {
	if !domain.ValidUUID(userID) || !domain.ValidUUID(sessionID) {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin session revoke: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		update sessions
		set revoked_at = coalesce(revoked_at, now()),
		    revoked_reason = coalesce(revoked_reason, 'user_revoked')
		where id = $1::uuid and user_id = $2::uuid`,
		sessionID, userID,
	)
	if err != nil {
		return false, fmt.Errorf("revoke user session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		update session_tokens set revoked_at = now()
		where session_id = $1::uuid and revoked_at is null`,
		sessionID,
	); err != nil {
		return false, fmt.Errorf("revoke user session tokens: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit user session revoke: %w", err)
	}
	return true, nil
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	// Avoid splitting a UTF-8 rune.
	cut := max
	for cut > 0 && (value[cut]&0xC0) == 0x80 {
		cut--
	}
	return value[:cut]
}
