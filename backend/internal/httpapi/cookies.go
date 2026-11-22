package httpapi

import (
	"net/http"
	"time"
)

const (
	sessionCookieBase = "dp_session"
	csrfCookieBase    = "dp_csrf"
	stateCookieBase   = "dp_oauth"
)

func (s *Server) cookieName(base string) string {
	if s.cfg.CookieSecure {
		// __Host- cookies must be Secure, host-only and use Path=/.
		return "__Host-" + base
	}
	return base
}

func (s *Server) setSessionCookies(w http.ResponseWriter, sessionToken, csrfToken string, maxAge time.Duration) {
	seconds := int(maxAge.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(sessionCookieBase),
		Value:    sessionToken,
		Path:     "/",
		MaxAge:   seconds,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	s.setCSRFCookie(w, csrfToken, maxAge)
}

func (s *Server) setCSRFCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	seconds := int(maxAge.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(csrfCookieBase),
		Value:    token,
		Path:     "/",
		MaxAge:   seconds,
		HttpOnly: false,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) setStateCookie(w http.ResponseWriter, value string, maxAge time.Duration) {
	seconds := int(maxAge.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	sameSite := http.SameSiteLaxMode
	if s.cfg.CookieSecure {
		// Apple posts the callback cross-site; a Lax cookie is not sent on that
		// POST. This is the only SameSite=None cookie in the service and it is
		// short-lived, HttpOnly and bound to one OAuth flow.
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(stateCookieBase),
		Value:    value,
		Path:     "/",
		MaxAge:   seconds,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: sameSite,
	})
}

func (s *Server) clearAuthCookies(w http.ResponseWriter) {
	s.clearCookie(w, s.cookieName(sessionCookieBase), true, http.SameSiteLaxMode)
	s.clearCookie(w, s.cookieName(csrfCookieBase), false, http.SameSiteLaxMode)
	s.clearCookie(w, s.cookieName(stateCookieBase), true, http.SameSiteLaxMode)
}

func (s *Server) clearStateCookie(w http.ResponseWriter) {
	s.clearCookie(w, s.cookieName(stateCookieBase), true, http.SameSiteLaxMode)
}

func (s *Server) clearCookie(w http.ResponseWriter, name string, httpOnly bool, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   s.cfg.CookieSecure,
		SameSite: sameSite,
	})
}

func cookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}
