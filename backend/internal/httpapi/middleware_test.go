package httpapi

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

func TestClientIPUsesLastForwardedHopOnlyFromTrustedProxy(t *testing.T) {
	s := &Server{cfg: config.Config{
		TrustedProxyPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
	}}
	req := httptest.NewRequest(http.MethodGet, "http://api.example/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.5")
	if got := s.clientIP(req); got != "203.0.113.5" {
		t.Fatalf("clientIP = %q, want the last forwarded hop", got)
	}
	req.Header.Set("X-Real-IP", "192.0.2.9")
	if got := s.clientIP(req); got != "192.0.2.9" {
		t.Fatalf("clientIP = %q, want X-Real-IP", got)
	}

	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	if got := s.clientIP(req); got != "198.51.100.7" {
		t.Fatalf("untrusted remote must not be overridden: %q", got)
	}
}

func TestValidCSRFRequiresOriginCookieHeaderAndSessionBinding(t *testing.T) {
	origin := "https://app.example"
	s := &Server{cfg: config.Config{AllowedOrigins: []string{origin}}}
	token := "csrf-token-value"
	sum := sha256.Sum256([]byte(token))
	principal := domain.Principal{Session: domain.Session{CSRFTokenHash: sum[:]}}

	req := httptest.NewRequest(http.MethodPost, "http://api.example/api/v1/me", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "dp_csrf", Value: token})
	if !s.validCSRF(req, principal) {
		t.Fatal("well-formed CSRF request was rejected")
	}

	req.Header.Set("Origin", "https://evil.example")
	if s.validCSRF(req, principal) {
		t.Fatal("wrong origin was accepted")
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", "different")
	if s.validCSRF(req, principal) {
		t.Fatal("mismatched header was accepted")
	}
}

func TestSafeMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if !isSafeMethod(method) {
			t.Errorf("%s should be safe", method)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		if isSafeMethod(method) {
			t.Errorf("%s should be unsafe", method)
		}
	}
}
