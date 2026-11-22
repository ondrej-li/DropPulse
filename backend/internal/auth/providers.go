package auth

import (
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/ondrej-li/DropPulse/backend/internal/config"
)

// NewProviders builds the configured OAuth providers. It validates the Apple
// signing key at startup so a malformed key is an immediate configuration
// error rather than a broken first login.
func NewProviders(cfg config.Config, client *http.Client) (map[string]Provider, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	providers := make(map[string]Provider)

	if pc, ok := cfg.Provider("google"); ok {
		providers["google"] = &oidcProvider{
			name:     "google",
			issuer:   "https://accounts.google.com",
			clientID: pc.ClientID,
			endpoint: oauth2.Endpoint{
				AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL:  "https://oauth2.googleapis.com/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
			scopes:     []string{"openid", "email", "profile"},
			httpClient: client,
			clientSecret: func() (string, error) {
				return pc.ClientSecret, nil
			},
		}
	}

	if pc, ok := cfg.Provider("apple"); ok {
		if _, err := AppleClientSecret(pc.AppleTeamID, pc.AppleKeyID, pc.ClientID, pc.ApplePrivateKey, time.Now()); err != nil {
			return nil, fmt.Errorf("validate Apple client secret: %w", err)
		}
		providers["apple"] = &oidcProvider{
			name:     "apple",
			issuer:   "https://appleid.apple.com",
			clientID: pc.ClientID,
			endpoint: oauth2.Endpoint{
				AuthURL:   "https://appleid.apple.com/auth/authorize",
				TokenURL:  "https://appleid.apple.com/auth/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
			scopes: []string{"name", "email"},
			extraAuthParams: map[string]string{
				"response_mode": "form_post",
			},
			httpClient: client,
			clientSecret: func() (string, error) {
				return AppleClientSecret(pc.AppleTeamID, pc.AppleKeyID, pc.ClientID, pc.ApplePrivateKey, time.Now())
			},
		}
	}

	if pc, ok := cfg.Provider("facebook"); ok {
		providers["facebook"] = &facebookProvider{
			clientID:     pc.ClientID,
			clientSecret: pc.ClientSecret,
			graphVersion: pc.FacebookGraphVersion,
			httpClient:   client,
		}
	}
	return providers, nil
}
