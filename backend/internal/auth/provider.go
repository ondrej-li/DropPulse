// Package auth implements OAuth/OIDC sign-in and DropPulse session issuance.
package auth

import (
	"context"
	"errors"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// ErrProviderNotFound means the requested provider is not configured. The HTTP
// layer turns this into a generic 404 rather than exposing configuration.
var ErrProviderNotFound = errors.New("provider not configured")

// Provider is one external identity provider.
type Provider interface {
	Name() string
	AuthCodeURL(state, nonce, verifier, redirectURI string) string
	Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (domain.ExternalIdentity, error)
}
