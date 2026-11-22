package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// GetUser loads one account by internal ID.
func (s *Store) GetUser(ctx context.Context, id string) (domain.User, error) {
	if !domain.ValidUUID(id) {
		return domain.User{}, ErrNotFound
	}
	user, err := scanUser(s.pool.QueryRow(ctx,
		"select "+userColumns+" from users u where u.id = $1::uuid and u.deleted_at is null",
		id,
	))
	if err != nil {
		return domain.User{}, err
	}
	return user, nil
}

// UpsertOAuthUser finds or creates the account for an external identity. It
// serialises concurrent first logins for the same provider subject, and for
// verified email auto-linking it also serialises on the email address.
//
// Auto-linking is allowed only when the provider asserts a verified email and
// the existing account also has a verified email. Facebook emails are never
// treated as verified. An unverified email is kept on the identity row but not
// promoted to users.email, so it cannot collide with somebody else's account.
func (s *Store) UpsertOAuthUser(ctx context.Context, ext domain.ExternalIdentity, admin bool) (domain.User, error) {
	ext.Provider = strings.ToLower(strings.TrimSpace(ext.Provider))
	ext.Subject = strings.TrimSpace(ext.Subject)
	ext.Email = domain.NormaliseEmail(ext.Email)
	if ext.Provider == "" || ext.Subject == "" || len(ext.Subject) > 512 {
		return domain.User{}, errors.New("invalid external identity")
	}
	if ext.Provider == "facebook" {
		ext.EmailVerified = false
	}
	if ext.Email == "" {
		ext.EmailVerified = false
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, fmt.Errorf("begin OAuth user transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"select pg_advisory_xact_lock(hashtextextended($1, 0))",
		"oauth:"+ext.Provider+":"+ext.Subject,
	); err != nil {
		return domain.User{}, fmt.Errorf("lock OAuth identity: %w", err)
	}

	user, err := scanUser(tx.QueryRow(ctx,
		"select "+userColumns+`
		 from users u
		 join user_identities i on i.user_id = u.id
		 where i.provider = $1 and i.subject = $2`,
		ext.Provider, ext.Subject,
	))
	switch {
	case err == nil:
		if err := updateOAuthLogin(ctx, tx, user.ID, ext); err != nil {
			return domain.User{}, err
		}
		// Deliberately do not filter deleted_at here: the auth service turns a
		// deleted or suspended account into ErrAccountInactive, whereas hiding
		// the row would surface an unexpected not-found error.
		user, err = scanUser(tx.QueryRow(ctx,
			"select "+userColumns+" from users u where u.id = $1::uuid",
			user.ID,
		))
		if err != nil {
			return domain.User{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.User{}, fmt.Errorf("commit OAuth login: %w", err)
		}
		return user, nil
	case !errors.Is(err, ErrNotFound):
		return domain.User{}, fmt.Errorf("find OAuth identity: %w", err)
	}

	// The identity is new. A verified email can safely attach it to an
	// existing verified account. Lock the email first so two providers logging
	// in at once cannot create duplicate accounts.
	if ext.EmailVerified && ext.Email != "" {
		if _, err := tx.Exec(ctx,
			"select pg_advisory_xact_lock(hashtextextended($1, 0))",
			"email:"+ext.Email,
		); err != nil {
			return domain.User{}, fmt.Errorf("lock verified email: %w", err)
		}
		user, err = scanUser(tx.QueryRow(ctx,
			"select "+userColumns+`
			 from users u
			 where lower(u.email) = $1
			   and u.email_verified = true
			   and u.status = 'active'
			   and u.deleted_at is null
			 limit 1`,
			ext.Email,
		))
		switch {
		case err == nil:
			if err := insertIdentity(ctx, tx, user.ID, ext); err != nil {
				return domain.User{}, err
			}
			if err := updateOAuthLogin(ctx, tx, user.ID, ext); err != nil {
				return domain.User{}, err
			}
			user, err = scanUser(tx.QueryRow(ctx,
				"select "+userColumns+" from users u where u.id = $1::uuid and u.deleted_at is null",
				user.ID,
			))
			if err != nil {
				return domain.User{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.User{}, fmt.Errorf("commit linked OAuth login: %w", err)
			}
			return user, nil
		case !errors.Is(err, ErrNotFound):
			return domain.User{}, fmt.Errorf("find account by verified email: %w", err)
		}
	}

	role := domain.RoleUser
	if admin && ext.EmailVerified {
		role = domain.RoleAdmin
	}
	var email any
	if ext.EmailVerified && ext.Email != "" {
		email = ext.Email
	}
	var newID string
	if err := tx.QueryRow(ctx, `
		insert into users (email, email_verified, display_name, avatar_url, role, last_login_at)
		values ($1, $2, nullif($3, ''), nullif($4, ''), $5, now())
		returning id::text`,
		email, ext.EmailVerified, ext.DisplayName, ext.AvatarURL, string(role),
	).Scan(&newID); err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	if err := insertIdentity(ctx, tx, newID, ext); err != nil {
		return domain.User{}, err
	}
	user, err = scanUser(tx.QueryRow(ctx,
		"select "+userColumns+" from users u where u.id = $1::uuid",
		newID,
	))
	if err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit new OAuth user: %w", err)
	}
	return user, nil
}

func insertIdentity(ctx context.Context, tx pgx.Tx, userID string, ext domain.ExternalIdentity) error {
	email := nullableString(ext.Email)
	if _, err := tx.Exec(ctx, `
		insert into user_identities
			(user_id, provider, subject, email, email_verified, display_name, avatar_url, last_login_at)
		values ($1::uuid, $2, $3, $4, $5, nullif($6, ''), nullif($7, ''), now())`,
		userID, ext.Provider, ext.Subject, email, ext.EmailVerified, ext.DisplayName, ext.AvatarURL,
	); err != nil {
		return fmt.Errorf("link OAuth identity: %w", err)
	}
	return nil
}

func updateOAuthLogin(ctx context.Context, tx pgx.Tx, userID string, ext domain.ExternalIdentity) error {
	if _, err := tx.Exec(ctx, `
		update user_identities
		set last_login_at = now(),
		    email = coalesce($3, email),
		    email_verified = email_verified or $4,
		    display_name = coalesce(nullif($5, ''), display_name),
		    avatar_url = coalesce(nullif($6, ''), avatar_url)
		where provider = $1 and subject = $2`,
		ext.Provider, ext.Subject, nullableString(ext.Email), ext.EmailVerified, ext.DisplayName, ext.AvatarURL,
	); err != nil {
		return fmt.Errorf("update OAuth identity: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		update users
		set last_login_at = now(),
		    updated_at = now(),
		    display_name = coalesce(nullif($2, ''), display_name),
		    avatar_url = coalesce(nullif($3, ''), avatar_url)
		where id = $1::uuid and deleted_at is null`,
		userID, ext.DisplayName, ext.AvatarURL,
	); err != nil {
		return fmt.Errorf("update user login: %w", err)
	}

	// Promote a newly verified email onto the account if it is still unset and
	// no other live account owns it. A conflict is not an error: the identity
	// keeps the address, and support can resolve the duplicate deliberately.
	if ext.EmailVerified && ext.Email != "" {
		if _, err := tx.Exec(ctx, `
			update users u
			set email = $2, email_verified = true, updated_at = now()
			where u.id = $1::uuid
			  and u.email is null
			  and u.deleted_at is null
			  and not exists (
			      select 1 from users other
			      where other.id <> u.id
			        and lower(other.email) = $2
			        and other.deleted_at is null
			  )`,
			userID, ext.Email,
		); err != nil {
			return fmt.Errorf("promote verified email: %w", err)
		}
	}
	return nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
