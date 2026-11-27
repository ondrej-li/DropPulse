// Package providers defines DropPulse's two built-in provider kinds:
//
//  1. CapabilitySeats (Kind 1, e.g. cinemacity.cz): providers whose public
//     programme legitimately exposes buyable seat availability without session
//     or anti-bot circumvention (and optionally supports schedule-only mode).
//  2. CapabilityScheduleOnly (Kind 2, e.g. cinestar.cz): providers that do not
//     expose seat counts publicly — DropPulse monitors only whether matching
//     screenings are scheduled on the programme.
package providers

import (
	"context"
	"net/http"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/providers/cinemacity"
	"github.com/ondrej-li/DropPulse/backend/internal/providers/cinestar"
)

// Capability classifies what a built-in provider can monitor legitimately.
type Capability string

const (
	// CapabilitySeats means the provider exposes open seat availability cleanly
	// and can also optionally check schedule presence (&mode=schedule).
	CapabilitySeats Capability = "seats"
	// CapabilityScheduleOnly means the provider only checks whether a show is
	// published on the venue's schedule (never scraping seat maps or queues).
	CapabilityScheduleOnly Capability = "schedule_only"
)

// Doer is the HTTP client subset shared by all providers.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Blocked is implemented by provider errors triggered by HTTP 403/429,
// waiting rooms, or anti-bot challenges.
type Blocked interface {
	error
	BlockedInfo() (statusCode int, retryAfter time.Duration)
}

// CheckResult is the normalised output of a provider check.
type CheckResult struct {
	Kind      domain.CheckKind
	Available bool
	Detail    string
}

// Provider is one built-in event availability adapter.
type Provider interface {
	Name() string
	Capability() Capability
	Match(rawURL string) (kind domain.CheckKind, matched bool, err error)
	Check(ctx context.Context, doer Doer, rawURL string) (CheckResult, error)
}

// Registry dispatches URLs to built-in providers in registration order.
type Registry struct {
	list []Provider
}

// NewRegistry creates a provider registry.
func NewRegistry(list ...Provider) *Registry {
	return &Registry{list: list}
}

var defaultRegistry = NewRegistry(
	cinemaCityProvider{},
	cineStarProvider{},
)

// DefaultRegistry returns the built-in providers (Cinema City + CineStar).
func DefaultRegistry() *Registry {
	return defaultRegistry
}

// Match finds the provider owning rawURL, if any.
func (r *Registry) Match(rawURL string) (Provider, domain.CheckKind, bool, error) {
	for _, p := range r.list {
		kind, matched, err := p.Match(rawURL)
		if matched {
			return p, kind, true, err
		}
	}
	return nil, domain.CheckKindHTTPStatus, false, nil
}

// Providers returns a copy of registered providers.
func (r *Registry) Providers() []Provider {
	out := make([]Provider, len(r.list))
	copy(out, r.list)
	return out
}

// ValidateTarget checks provider-specific URL rules when rawURL belongs to a
// built-in provider; generic HTTPS URLs return nil.
func ValidateTarget(rawURL string) error {
	_, _, matched, err := defaultRegistry.Match(rawURL)
	if matched {
		return err
	}
	return nil
}

// ClassifyURL returns the effective CheckKind ("seats", "schedule", or
// "http_status") for an event URL without requiring database storage.
func ClassifyURL(rawURL string) domain.CheckKind {
	_, kind, matched, err := defaultRegistry.Match(rawURL)
	if matched && err == nil {
		return kind
	}
	return domain.CheckKindHTTPStatus
}

type cinemaCityProvider struct{}

func (cinemaCityProvider) Name() string           { return "cinemacity" }
func (cinemaCityProvider) Capability() Capability { return CapabilitySeats }

func (cinemaCityProvider) Match(rawURL string) (domain.CheckKind, bool, error) {
	target, matched, err := cinemacity.Match(rawURL)
	if !matched {
		return "", false, nil
	}
	if err != nil {
		return domain.CheckKindSeats, true, err
	}
	return target.EffectiveKind(), true, nil
}

func (cinemaCityProvider) Check(ctx context.Context, doer Doer, rawURL string) (CheckResult, error) {
	target, _, err := cinemacity.Match(rawURL)
	if err != nil {
		return CheckResult{}, err
	}
	res, err := cinemacity.CheckDetailed(ctx, doer, target)
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{
		Kind:      res.Kind,
		Available: res.Available,
		Detail:    res.Detail,
	}, nil
}

type cineStarProvider struct{}

func (cineStarProvider) Name() string           { return "cinestar" }
func (cineStarProvider) Capability() Capability { return CapabilityScheduleOnly }

func (cineStarProvider) Match(rawURL string) (domain.CheckKind, bool, error) {
	_, matched, err := cinestar.Match(rawURL)
	if !matched {
		return "", false, nil
	}
	return domain.CheckKindSchedule, true, err
}

func (cineStarProvider) Check(ctx context.Context, doer Doer, rawURL string) (CheckResult, error) {
	target, _, err := cinestar.Match(rawURL)
	if err != nil {
		return CheckResult{}, err
	}
	res, err := cinestar.CheckDetailed(ctx, doer, target)
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{
		Kind:      res.Kind,
		Available: res.Available,
		Detail:    res.Detail,
	}, nil
}
