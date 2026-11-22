package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

var (
	ErrInvalidRequest   = errors.New("invalid authentication request")
	ErrInvalidState     = errors.New("invalid OAuth state")
	ErrExchangeFailed   = errors.New("OAuth exchange failed")
	ErrAccountInactive  = errors.New("account is not active")
	ErrUnsupportedGrant = errors.New("unsupported grant")
)

// Service owns OAuth transaction creation and session issuance.
type Service struct {
	cfg       config.Config
	store     *store.Store
	providers map[string]Provider
	now       func() time.Time
}

// NewService builds the auth service.
func NewService(cfg config.Config, st *store.Store, providers map[string]Provider) *Service {
	return &Service{
		cfg:       cfg,
		store:     st,
		providers: providers,
		now:       time.Now,
	}
}

// StartResult is what the HTTP layer needs to begin a browser login.
type StartResult struct {
	RedirectURL       string
	StateCookie       string
	StateCookieMaxAge int
}

// ClientInfo is request metadata recorded on a session.
type ClientInfo struct {
	UserAgent string
	IP        string
}

// FinishResult is a completed browser login.
type FinishResult struct {
	User         domain.User
	Session      domain.Session
	SessionToken string
	CSRFToken    string
	ReturnTo     string
}

// RefreshResult is a renewed browser session.
type RefreshResult struct {
	Session      domain.Session
	SessionToken string
	CSRFToken    string
}

// StartWeb creates a state-bound OAuth transaction and returns the provider
// redirect. The caller must set StateCookie to the returned value.
func (s *Service) StartWeb(ctx context.Context, providerName, returnTo string) (StartResult, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	provider, ok := s.providers[providerName]
	if !ok {
		return StartResult{}, ErrProviderNotFound
	}
	returnTo, err := SafeReturnTo(returnTo)
	if err != nil {
		return StartResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	state, err := randomToken(32)
	if err != nil {
		return StartResult{}, fmt.Errorf("create OAuth state: %w", err)
	}
	browserToken, err := randomToken(32)
	if err != nil {
		return StartResult{}, fmt.Errorf("create OAuth browser binding: %w", err)
	}
	nonce, err := randomToken(32)
	if err != nil {
		return StartResult{}, fmt.Errorf("create OAuth nonce: %w", err)
	}
	verifier, _, err := newPKCE()
	if err != nil {
		return StartResult{}, fmt.Errorf("create PKCE verifier: %w", err)
	}
	now := s.now().UTC()
	redirectURI := s.RedirectURI(providerName)
	flow := domain.OAuthFlow{
		Provider:     providerName,
		Intent:       "login",
		StateHash:    sha256Bytes(state),
		BrowserHash:  sha256Bytes(browserToken),
		Nonce:        nonce,
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		ReturnTo:     returnTo,
		CreatedAt:    now,
		ExpiresAt:    now.Add(s.cfg.OAuthFlowTTL),
	}
	if err := s.store.CreateOAuthFlow(ctx, flow); err != nil {
		return StartResult{}, err
	}
	return StartResult{
		RedirectURL:       provider.AuthCodeURL(state, nonce, verifier, redirectURI),
		StateCookie:       browserToken,
		StateCookieMaxAge: int(s.cfg.OAuthFlowTTL.Seconds()),
	}, nil
}

// FinishWeb consumes a callback, verifies the provider response and creates a
// browser session.
func (s *Service) FinishWeb(ctx context.Context, providerName string, params url.Values, browserToken string, client ClientInfo) (FinishResult, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	provider, ok := s.providers[providerName]
	if !ok {
		return FinishResult{}, ErrProviderNotFound
	}
	if params.Get("error") != "" {
		return FinishResult{}, fmt.Errorf("%w: provider returned an error", ErrExchangeFailed)
	}
	state := params.Get("state")
	code := params.Get("code")
	if state == "" || len(state) > 512 || code == "" || len(code) > 4096 || browserToken == "" {
		return FinishResult{}, ErrInvalidRequest
	}
	flow, err := s.store.ConsumeOAuthFlow(ctx, providerName, sha256Bytes(state))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return FinishResult{}, ErrInvalidState
		}
		return FinishResult{}, err
	}
	if flow.Intent != "login" {
		return FinishResult{}, ErrInvalidState
	}
	if !equalBytes(flow.BrowserHash, sha256Bytes(browserToken)) {
		return FinishResult{}, ErrInvalidState
	}
	if flow.RedirectURI != s.RedirectURI(providerName) {
		return FinishResult{}, ErrInvalidState
	}
	ret, err := SafeReturnTo(flow.ReturnTo)
	if err != nil {
		return FinishResult{}, ErrInvalidState
	}

	external, err := provider.Exchange(ctx, code, flow.CodeVerifier, flow.RedirectURI, flow.Nonce)
	if err != nil {
		return FinishResult{}, fmt.Errorf("%w: %v", ErrExchangeFailed, err)
	}
	external.Provider = providerName
	if external.Subject == "" {
		return FinishResult{}, fmt.Errorf("%w: provider returned no subject", ErrExchangeFailed)
	}
	_, admin := s.cfg.AdminEmails[external.Email]
	user, err := s.store.UpsertOAuthUser(ctx, external, admin && external.EmailVerified)
	if err != nil {
		return FinishResult{}, err
	}
	if user.Status != domain.StatusActive || user.DeletedAt != nil {
		return FinishResult{}, ErrAccountInactive
	}

	sessionToken, err := randomToken(32)
	if err != nil {
		return FinishResult{}, err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return FinishResult{}, err
	}
	session, err := s.store.CreateWebSession(
		ctx, user.ID,
		sha256Bytes(sessionToken), sha256Bytes(csrfToken),
		client.UserAgent, client.IP,
		s.cfg.SessionIdleTTL, s.cfg.SessionAbsoluteTTL,
	)
	if err != nil {
		return FinishResult{}, err
	}
	return FinishResult{
		User:         user,
		Session:      session,
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
		ReturnTo:     ret,
	}, nil
}

// RefreshWeb rotates the current browser token and CSRF token.
func (s *Service) RefreshWeb(ctx context.Context, principal domain.Principal) (RefreshResult, error) {
	if principal.AuthKind != "web" {
		return RefreshResult{}, ErrUnsupportedGrant
	}
	sessionToken, err := randomToken(32)
	if err != nil {
		return RefreshResult{}, err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return RefreshResult{}, err
	}
	session, err := s.store.RotateWebToken(
		ctx,
		principal.Session.ID,
		principal.TokenID,
		sha256Bytes(sessionToken),
		sha256Bytes(csrfToken),
		s.cfg.SessionIdleTTL,
	)
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{
		Session:      session,
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
	}, nil
}

// RotateCSRF issues a new session-bound CSRF token and returns the raw value.
func (s *Service) RotateCSRF(ctx context.Context, sessionID string) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	if err := s.store.RotateCSRF(ctx, sessionID, sha256Bytes(token)); err != nil {
		return "", err
	}
	return token, nil
}

// Logout revokes the current session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.store.RevokeSession(ctx, sessionID, "logout")
}

// RedirectURI is the exact callback registered with every provider.
func (s *Service) RedirectURI(providerName string) string {
	return strings.TrimRight(s.cfg.PublicAPIURL.String(), "/") +
		"/api/v1/auth/" + url.PathEscape(providerName) + "/callback"
}

// Providers returns the configured provider names for diagnostics and /me
// clients. It does not expose credentials.
func (s *Service) Providers() []string {
	return s.cfg.ProviderNames()
}
