package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

// singleUser is the authentication boundary of the local proof-of-concept
// mode. There is no login, so the listener must be loopback-only (validated in
// config) and every request must additionally:
//
//   - use a loopback Host header, which defeats DNS rebinding;
//   - carry a same-origin Origin header on unsafe methods;
//   - not be marked cross-site by the browser;
//   - use application/json when it has a body.
//
// This is a deliberate, narrow bypass for one operator on one machine, not a
// general unauthenticated mode.
func (s *Server) singleUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "host_not_allowed", "this server only accepts loopback hosts")
			return
		}
		if !isSafeMethod(r.Method) {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" || !sameOrigin(origin, r.Host) {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
				return
			}
			if r.ContentLength != 0 {
				contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
				if !strings.HasPrefix(contentType, "application/json") {
					writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "request body must be application/json")
					return
				}
			}
		}
		if ok, retry := s.limiter.Allow("local:"+s.clientIP(r), s.cfg.APIRatePerMinute, time.Minute); !ok {
			seconds := int(retry.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; try again shortly")
			return
		}
		principal := domain.Principal{
			User:         s.admin,
			AuthKind:     "local",
			Entitlements: s.localEntitlements,
		}
		ctx := context.WithValue(r.Context(), principalContextKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; "+
			"connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.frontend)
}

// isLoopbackHost accepts localhost and loopback IP literals with any port.
func isLoopbackHost(hostPort string) bool {
	host := hostPort
	if parsed, _, err := net.SplitHostPort(hostPort); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// sameOrigin reports whether origin is exactly the request's http origin.
// Origin must have no path, query, fragment or user info.
func sameOrigin(origin, hostPort string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return canonicalHostPort(u.Host, "http") == canonicalHostPort(hostPort, "http")
}

func canonicalHostPort(hostPort, scheme string) string {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		port = ""
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.String()
	}
	return net.JoinHostPort(host, port)
}
