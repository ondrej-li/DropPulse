package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// EffectiveEntitlements resolves the user's active subscription, falling back
// to the free plan, and returns typed limits. The query only accepts active
// plans; a subscription pointing at a disabled plan does not grant anything.
func (s *Store) EffectiveEntitlements(ctx context.Context, userID string) (domain.Entitlements, error) {
	if !domain.ValidUUID(userID) {
		return domain.Entitlements{}, ErrNotFound
	}
	planCode := "free"
	var overrides []byte
	err := s.pool.QueryRow(ctx, `
		select s.plan_code, s.overrides
		from subscriptions s
		join plans p on p.code = s.plan_code and p.active
		where s.user_id = $1::uuid
		  and s.status in ('active', 'trialing')
		limit 1`,
		userID,
	).Scan(&planCode, &overrides)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.Entitlements{}, fmt.Errorf("resolve subscription: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		planCode = "free"
		overrides = nil
	}

	var limits []byte
	if err := s.pool.QueryRow(ctx,
		"select limits from plans where code = $1 and active",
		planCode,
	).Scan(&limits); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Entitlements{}, fmt.Errorf("plan %q is missing or inactive", planCode)
		}
		return domain.Entitlements{}, fmt.Errorf("load plan limits: %w", err)
	}
	ent, err := domain.MergeLimits(planCode, limits, overrides)
	if err != nil {
		return domain.Entitlements{}, err
	}
	return ent, nil
}
