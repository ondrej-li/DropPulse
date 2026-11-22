package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

type oidcProvider struct {
	name            string
	issuer          string
	clientID        string
	endpoint        oauth2.Endpoint
	scopes          []string
	extraAuthParams map[string]string
	clientSecret    func() (string, error)
	httpClient      *http.Client

	verifierMu sync.Mutex
	verifier   *oidc.IDTokenVerifier
}

func (p *oidcProvider) Name() string { return p.name }

func (p *oidcProvider) AuthCodeURL(state, nonce, verifier, redirectURI string) string {
	cfg := &oauth2.Config{
		ClientID:    p.clientID,
		RedirectURL: redirectURI,
		Endpoint:    p.endpoint,
		Scopes:      p.scopes,
	}
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge(verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	for key, value := range p.extraAuthParams {
		opts = append(opts, oauth2.SetAuthURLParam(key, value))
	}
	return cfg.AuthCodeURL(state, opts...)
}

func pkceChallenge(verifier string) string {
	sum := sha256Bytes(verifier)
	return base64RawURL(sum)
}

func (p *oidcProvider) Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (domain.ExternalIdentity, error) {
	secret := ""
	if p.clientSecret != nil {
		var err error
		secret, err = p.clientSecret()
		if err != nil {
			return domain.ExternalIdentity{}, fmt.Errorf("provider client secret: %w", err)
		}
	}
	cfg := &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: secret,
		RedirectURL:  redirectURI,
		Endpoint:     p.endpoint,
		Scopes:       p.scopes,
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.httpClient)
	token, err := cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return domain.ExternalIdentity{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return domain.ExternalIdentity{}, errors.New("provider returned no ID token")
	}

	verifierForProvider, err := p.idTokenVerifier(ctx)
	if err != nil {
		return domain.ExternalIdentity{}, err
	}
	ctx = oidc.ClientContext(ctx, p.httpClient)
	idToken, err := verifierForProvider.Verify(ctx, rawIDToken)
	if err != nil {
		return domain.ExternalIdentity{}, fmt.Errorf("verify ID token: %w", err)
	}
	if nonce == "" || !equalStrings(idToken.Nonce, nonce) {
		return domain.ExternalIdentity{}, errors.New("ID token nonce mismatch")
	}
	if idToken.Subject == "" {
		return domain.ExternalIdentity{}, errors.New("ID token has no subject")
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return domain.ExternalIdentity{}, fmt.Errorf("read ID token claims: %w", err)
	}
	if claims.Subject != "" && claims.Subject != idToken.Subject {
		return domain.ExternalIdentity{}, errors.New("ID token subject mismatch")
	}
	return domain.ExternalIdentity{
		Provider:      p.name,
		Subject:       idToken.Subject,
		Email:         domain.NormaliseEmail(claims.Email),
		EmailVerified: claimBool(claims.EmailVerified),
		DisplayName:   strings.TrimSpace(claims.Name),
		AvatarURL:     strings.TrimSpace(claims.Picture),
	}, nil
}

func (p *oidcProvider) idTokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	p.verifierMu.Lock()
	defer p.verifierMu.Unlock()
	if p.verifier != nil {
		return p.verifier, nil
	}
	ctx = oidc.ClientContext(ctx, p.httpClient)
	provider, err := oidc.NewProvider(ctx, p.issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider %s: %w", p.name, err)
	}
	p.verifier = provider.Verifier(&oidc.Config{ClientID: p.clientID})
	return p.verifier, nil
}

type oidcClaims struct {
	Subject       string          `json:"sub"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
	Name          string          `json:"name"`
	Picture       string          `json:"picture"`
}

// claimBool accepts the two shapes providers use for email_verified: a JSON
// boolean (Google) and the string "true" (Apple).
func claimBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var boolean bool
	if err := json.Unmarshal(raw, &boolean); err == nil {
		return boolean
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.EqualFold(strings.TrimSpace(text), "true")
	}
	return false
}

// AppleClientSecret creates the short-lived ES256 client secret Apple requires
// for the token endpoint. Apple caps these at six months; ten minutes is plenty
// and keeps a leaked value useless.
func AppleClientSecret(teamID, keyID, clientID, privateKeyPEM string, now time.Time) (string, error) {
	key, err := jwt.ParseECPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse Apple private key: %w", err)
	}
	claims := jwt.MapClaims{
		"iss": teamID,
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": clientID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = keyID
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign Apple client secret: %w", err)
	}
	return signed, nil
}
