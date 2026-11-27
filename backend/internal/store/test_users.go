package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// EnsureTestUser resolves or creates a local test account for headless testing
// when LOCAL_TEST_AUTH=true. It must never be called outside local test mode.
func (s *Store) EnsureTestUser(ctx context.Context, ident, email string, role domain.Role, explicitRole bool) (domain.User, domain.Entitlements, error) {
	ident = strings.TrimSpace(ident)
	email = domain.NormaliseEmail(email)
	if ident == "" {
		ident = "admin"
	}
	if explicitRole && !role.Valid() {
		return domain.User{}, domain.Entitlements{}, errors.New("invalid role")
	}

	if domain.ValidUUID(ident) {
		user, err := s.GetUser(ctx, ident)
		if err != nil {
			return domain.User{}, domain.Entitlements{}, err
		}
		if explicitRole && user.Role != role && user.Username != "admin" {
			if _, err := s.pool.Exec(ctx,
				"update users set role = $2, updated_at = now() where id = $1::uuid and deleted_at is null",
				user.ID, string(role),
			); err != nil {
				return domain.User{}, domain.Entitlements{}, fmt.Errorf("update test user role: %w", err)
			}
			user.Role = role
		}
		ent, err := s.EffectiveEntitlements(ctx, user.ID)
		if err != nil {
			return domain.User{}, domain.Entitlements{}, err
		}
		return user, ent, nil
	}

	var username string
	if strings.Contains(ident, "@") {
		if email == "" {
			email = domain.NormaliseEmail(ident)
		}
		localPart := strings.SplitN(email, "@", 2)[0]
		username = sanitiseTestUsername(localPart)
	} else {
		username = sanitiseTestUsername(ident)
		if email == "" {
			email = username + "@local.test"
		}
	}
	if username == "" {
		return domain.User{}, domain.Entitlements{}, errors.New("invalid test user identifier")
	}

	targetRole := domain.RoleUser
	if username == "admin" {
		targetRole = domain.RoleAdmin
	}
	if explicitRole {
		targetRole = role
		if username == "admin" {
			targetRole = domain.RoleAdmin
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, domain.Entitlements{}, fmt.Errorf("begin test user tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"select pg_advisory_xact_lock(hashtextextended($1, 0))",
		"test-user:"+username,
	); err != nil {
		return domain.User{}, domain.Entitlements{}, fmt.Errorf("lock test user: %w", err)
	}

	user, err := scanUser(tx.QueryRow(ctx, `
		select `+userColumns+`
		from users u
		where u.deleted_at is null
		  and (u.username = $1 or (u.email is not null and lower(u.email) = $2))
		order by case when u.username = $1 then 0 else 1 end
		limit 1
		for update`,
		username, email,
	))
	switch {
	case err == nil:
		newRole := user.Role
		if explicitRole && user.Username != "admin" {
			newRole = targetRole
		}
		if newRole != user.Role {
			if _, err := tx.Exec(ctx,
				"update users set role = $2, last_login_at = now(), updated_at = now() where id = $1::uuid",
				user.ID, string(newRole),
			); err != nil {
				return domain.User{}, domain.Entitlements{}, fmt.Errorf("update test user: %w", err)
			}
			user.Role = newRole
		}
	case !errors.Is(err, ErrNotFound):
		return domain.User{}, domain.Entitlements{}, fmt.Errorf("lookup test user: %w", err)
	default:
		displayName := strings.Title(strings.ReplaceAll(username, "-", " "))
		user, err = scanUser(tx.QueryRow(ctx, `
			insert into users as u (username, email, email_verified, display_name, role, status, last_login_at)
			values ($1, $2, true, $3, $4, 'active', now())
			returning `+userColumns,
			username, email, displayName, string(targetRole),
		))
		if err != nil {
			return domain.User{}, domain.Entitlements{}, fmt.Errorf("create test user: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, domain.Entitlements{}, fmt.Errorf("commit test user: %w", err)
	}

	ent, err := s.EffectiveEntitlements(ctx, user.ID)
	if err != nil {
		return domain.User{}, domain.Entitlements{}, err
	}
	return user, ent, nil
}

func sanitiseTestUsername(raw string) string {
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
