package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// CreateOAuthFlow stores a pending OAuth transaction. The state and browser
// values are already hashed by the caller.
func (s *Store) CreateOAuthFlow(ctx context.Context, flow domain.OAuthFlow) error {
	if len(flow.StateHash) != 32 || len(flow.BrowserHash) != 32 {
		return errors.New("OAuth flow hashes must be SHA-256")
	}
	if flow.Provider == "" || flow.Nonce == "" || flow.CodeVerifier == "" || flow.RedirectURI == "" {
		return errors.New("incomplete OAuth flow")
	}
	_, err := s.pool.Exec(ctx, `
		insert into oauth_flows
			(provider, intent, user_id, state_hash, browser_hash, nonce, code_verifier,
			 redirect_uri, return_to, expires_at)
		values ($1, $2, nullif($3, '')::uuid, $4, $5, $6, $7, $8, nullif($9, ''),
		        now() + ($10 * interval '1 second'))`,
		flow.Provider, flow.Intent, flow.UserID, flow.StateHash, flow.BrowserHash,
		flow.Nonce, flow.CodeVerifier, flow.RedirectURI, flow.ReturnTo,
		flow.ExpiresAt.Sub(flow.CreatedAt).Seconds(),
	)
	if err != nil {
		return fmt.Errorf("create OAuth flow: %w", err)
	}
	return nil
}

// ConsumeOAuthFlow atomically marks a state value used and returns the flow.
// A missing, expired or already consumed row is ErrNotFound.
func (s *Store) ConsumeOAuthFlow(ctx context.Context, provider string, stateHash []byte) (domain.OAuthFlow, error) {
	if len(stateHash) != 32 {
		return domain.OAuthFlow{}, ErrNotFound
	}
	var flow domain.OAuthFlow
	err := s.pool.QueryRow(ctx, `
		update oauth_flows
		set consumed_at = now()
		where state_hash = $1
		  and provider = $2
		  and consumed_at is null
		  and expires_at > now()
		returning id::text, provider, intent, coalesce(user_id::text, ''),
		          state_hash, browser_hash, nonce, code_verifier, redirect_uri,
		          coalesce(return_to, ''), created_at, expires_at`,
		stateHash, provider,
	).Scan(
		&flow.ID,
		&flow.Provider,
		&flow.Intent,
		&flow.UserID,
		&flow.StateHash,
		&flow.BrowserHash,
		&flow.Nonce,
		&flow.CodeVerifier,
		&flow.RedirectURI,
		&flow.ReturnTo,
		&flow.CreatedAt,
		&flow.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OAuthFlow{}, ErrNotFound
		}
		return domain.OAuthFlow{}, fmt.Errorf("consume OAuth flow: %w", err)
	}
	return flow, nil
}

// DeleteExpiredOAuthFlows removes abandoned and expired transactions.
func (s *Store) DeleteExpiredOAuthFlows(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, "delete from oauth_flows where expires_at < now()")
	if err != nil {
		return 0, fmt.Errorf("delete expired OAuth flows: %w", err)
	}
	return tag.RowsAffected(), nil
}
