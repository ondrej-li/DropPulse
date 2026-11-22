package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/config"
)

func TestSessionCookiesAreHostPrefixedAndHttpOnly(t *testing.T) {
	s := &Server{cfg: config.Config{CookieSecure: true}}
	rec := httptest.NewRecorder()
	s.setSessionCookies(rec, "session-token", "csrf-token", time.Hour)
	cookies := rec.Result().Cookies()
	byName := make(map[string]*http.Cookie, len(cookies))
	for _, cookie := range cookies {
		byName[cookie.Name] = cookie
	}
	session := byName["__Host-dp_session"]
	if session == nil {
		t.Fatalf("missing session cookie: %v", cookies)
	}
	if !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" {
		t.Fatalf("unexpected session cookie: %+v", session)
	}
	csrf := byName["__Host-dp_csrf"]
	if csrf == nil {
		t.Fatalf("missing CSRF cookie: %v", cookies)
	}
	if csrf.HttpOnly || !csrf.Secure || csrf.Path != "/" {
		t.Fatalf("unexpected CSRF cookie: %+v", csrf)
	}
}

func TestDevelopmentCookieNamesHaveNoHostPrefix(t *testing.T) {
	s := &Server{cfg: config.Config{CookieSecure: false}}
	rec := httptest.NewRecorder()
	s.setSessionCookies(rec, "session-token", "csrf-token", time.Hour)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "__Host-dp_session" || cookie.Name == "__Host-dp_csrf" {
			t.Fatalf("insecure development cookie must not use __Host-: %s", cookie.Name)
		}
	}
}

func TestStateCookieUsesNoneOnlyWithSecure(t *testing.T) {
	secure := &Server{cfg: config.Config{CookieSecure: true}}
	rec := httptest.NewRecorder()
	secure.setStateCookie(rec, "browser-binding", 10*time.Minute)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].SameSite != http.SameSiteNoneMode || !cookies[0].HttpOnly {
		t.Fatalf("unexpected state cookie: %+v", cookies)
	}
}
