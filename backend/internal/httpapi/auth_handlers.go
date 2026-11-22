package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/ondrej-li/DropPulse/backend/internal/auth"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

// handleAuthProviders is public because the landing page needs to know which
// buttons to render before the user has a session. It exposes names only.
func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	if !s.allowAuthRate(w, r, "providers") {
		return
	}
	providers := []string{}
	if s.auth != nil {
		providers = s.auth.Providers()
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Server) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if !s.allowAuthRate(w, r, "start:"+provider) {
		return
	}
	result, err := s.auth.StartWeb(r.Context(), provider, r.URL.Query().Get("return_to"))
	switch {
	case errors.Is(err, auth.ErrProviderNotFound):
		writeError(w, http.StatusNotFound, "provider_not_configured", "that sign-in provider is not available")
		return
	case errors.Is(err, auth.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid_request", "the return path is invalid")
		return
	case err != nil:
		s.logger.Error("start OAuth flow", "provider", provider, "err", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, "auth_start_failed", "sign-in could not be started")
		return
	}
	s.setStateCookie(w, result.StateCookie, s.cfg.OAuthFlowTTL)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, result.RedirectURL, http.StatusFound)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if !s.allowAuthRate(w, r, "callback:"+provider) {
		return
	}
	if err := parseForm(w, r, s.cfg.MaxRequestBytes); err != nil {
		s.clearStateCookie(w)
		s.redirectAuthError(w, r)
		return
	}
	browserToken := cookieValue(r, s.cookieName(stateCookieBase))
	s.clearStateCookie(w)
	result, err := s.auth.FinishWeb(
		r.Context(), provider, r.Form, browserToken,
		auth.ClientInfo{UserAgent: r.UserAgent(), IP: s.clientIP(r)},
	)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidRequest) && !errors.Is(err, auth.ErrInvalidState) &&
			!errors.Is(err, auth.ErrExchangeFailed) && !errors.Is(err, auth.ErrAccountInactive) &&
			!errors.Is(err, auth.ErrProviderNotFound) {
			s.logger.Error("finish OAuth flow", "provider", provider, "err", err, "request_id", requestIDFrom(r.Context()))
		}
		s.redirectAuthError(w, r)
		return
	}
	s.setSessionCookies(w, result.SessionToken, result.CSRFToken, s.cfg.SessionIdleTTL)
	http.Redirect(w, r, s.webRedirect(result.ReturnTo), http.StatusSeeOther)
}

func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	result, err := s.auth.RefreshWeb(r.Context(), principal)
	if err != nil {
		if errors.Is(err, auth.ErrUnsupportedGrant) {
			writeError(w, http.StatusBadRequest, "unsupported_grant", "this session cannot be refreshed this way")
			return
		}
		if errors.Is(err, store.ErrUnauthenticated) || errors.Is(err, store.ErrNotFound) {
			s.clearAuthCookies(w)
			writeError(w, http.StatusUnauthorized, "not_authenticated", "authentication is required")
			return
		}
		s.logger.Error("refresh web session", "err", err, "user_id", principal.User.ID, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	s.setSessionCookies(w, result.SessionToken, result.CSRFToken, s.cfg.SessionIdleTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"expires_at": result.Session.IdleExpiresAt,
		"csrf_token": result.CSRFToken,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if err := s.auth.Logout(r.Context(), principal.Session.ID); err != nil {
		s.logger.Error("logout", "err", err, "user_id", principal.User.ID, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	s.clearAuthCookies(w)
	writeNoContent(w)
}

func (s *Server) handleCSRF(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	token, err := s.auth.RotateCSRF(r.Context(), principal.Session.ID)
	if err != nil {
		if errors.Is(err, store.ErrUnauthenticated) {
			s.clearAuthCookies(w)
			writeError(w, http.StatusUnauthorized, "not_authenticated", "authentication is required")
			return
		}
		s.logger.Error("rotate CSRF token", "err", err, "user_id", principal.User.ID, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	s.setCSRFCookie(w, token, s.cfg.SessionIdleTTL)
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": token})
}

func (s *Server) redirectAuthError(w http.ResponseWriter, r *http.Request) {
	values := url.Values{}
	values.Set("auth_error", "auth_failed")
	target := s.webRedirect("/?" + values.Encode())
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) webRedirect(returnTo string) string {
	if returnTo == "" || !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") {
		returnTo = "/"
	}
	return strings.TrimRight(s.cfg.WebAppURL.String(), "/") + returnTo
}
