// Package store contains the Postgres persistence layer. Queries are written
// by hand against pgx; there is no ORM and no generated query layer.
package store

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

var (
	// ErrNotFound means the requested row does not exist or is not visible to
	// the caller.
	ErrNotFound = errors.New("not found")
	// ErrUnauthenticated means a token or session is missing, expired or
	// revoked. Callers must not reveal which of those is true.
	ErrUnauthenticated = errors.New("unauthenticated")
)

// Store owns the database pool. It is safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps an existing pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Pool exposes the pool for migrations and health checks in wiring code.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

type rowScanner interface {
	Scan(dest ...any) error
}

const userColumns = `
	u.id::text,
	coalesce(u.username, ''),
	coalesce(u.email, ''),
	u.email_verified,
	coalesce(u.display_name, ''),
	coalesce(u.avatar_url, ''),
	u.role,
	u.status,
	coalesce(u.terms_version, ''),
	u.terms_accepted_at,
	u.created_at,
	u.updated_at,
	u.last_login_at,
	u.deleted_at`

func scanUser(row rowScanner) (domain.User, error) {
	var (
		user          domain.User
		role, status  string
		termsAccepted *time.Time
		lastLogin     *time.Time
		deleted       *time.Time
	)
	if err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.EmailVerified,
		&user.DisplayName,
		&user.AvatarURL,
		&role,
		&status,
		&user.TermsVersion,
		&termsAccepted,
		&user.CreatedAt,
		&user.UpdatedAt,
		&lastLogin,
		&deleted,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, ErrNotFound
		}
		return domain.User{}, err
	}
	user.Role = domain.Role(role)
	user.Status = domain.UserStatus(status)
	user.TermsAccepted = termsAccepted
	user.LastLoginAt = lastLogin
	user.DeletedAt = deleted
	return user, nil
}
