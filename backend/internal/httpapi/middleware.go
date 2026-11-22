package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

type contextKey int

const (
	requestIDContextKey contextKey = iota
	principalContextKey
)

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.status = status
	r.wrote = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic serving request",
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", requestIDFrom(r.Context()),
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				// If the handler already wrote a body this is a no-op for the
				// client, but it prevents the panic from reaching net/http's
				// connection goroutine.
				writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if len(id) < 8 || len(id) > 64 || strings.ContainsAny(id, "\r\n\t ") {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDContextKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "req-" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(buf)
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)
	return id
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", s.clientIP(r),
			"request_id", requestIDFrom(r.Context()),
		)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Add("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.cfg.OriginAllowed(origin) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.Header().Add("Vary", "Origin")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
		next.ServeHTTP(w, r)
	})
}

type credential struct {
	raw       string
	tokenKind string
	authKind  string
	bearer    bool
}

func (s *Server) requestCredential(r *http.Request) (credential, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header != "" {
		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 4096 {
			return credential{}, false
		}
		return credential{raw: parts[1], tokenKind: "access", authKind: "bearer", bearer: true}, true
	}
	raw := cookieValue(r, s.cookieName(sessionCookieBase))
	if raw == "" || len(raw) > 4096 {
		return credential{}, false
	}
	return credential{raw: raw, tokenKind: "web", authKind: "web"}, true
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cred, ok := s.requestCredential(r)
		if !ok {
			s.unauthenticated(w, r)
			return
		}
		sum := sha256.Sum256([]byte(cred.raw))
		authResult, err := s.store.Authenticate(r.Context(), sum[:], []string{cred.tokenKind}, s.cfg.SessionIdleTTL)
		if err != nil {
			if errors.Is(err, store.ErrUnauthenticated) {
				s.unauthenticated(w, r)
				return
			}
			s.logger.Error("authenticate request", "err", err, "request_id", requestIDFrom(r.Context()))
			internalError(w)
			return
		}
		entitlements, err := s.store.EffectiveEntitlements(r.Context(), authResult.User.ID)
		if err != nil {
			s.logger.Error("load entitlements", "err", err, "user_id", authResult.User.ID, "request_id", requestIDFrom(r.Context()))
			internalError(w)
			return
		}
		principal := domain.Principal{
			User:         authResult.User,
			Session:      authResult.Session,
			TokenID:      authResult.TokenID,
			AuthKind:     cred.authKind,
			Entitlements: entitlements,
		}

		if ok, retry := s.limiter.Allow("api:"+authResult.User.ID, s.cfg.APIRatePerMinute, time.Minute); !ok {
			seconds := int(retry.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; try again shortly")
			return
		}

		if !cred.bearer && !isSafeMethod(r.Method) && !s.validCSRF(r, principal) {
			writeError(w, http.StatusForbidden, "csrf_failed", "request could not be verified")
			return
		}

		ctx := context.WithValue(r.Context(), principalContextKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) unauthenticated(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	writeError(w, http.StatusUnauthorized, "not_authenticated", "authentication is required")
}

func (s *Server) validCSRF(r *http.Request, principal domain.Principal) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || !s.cfg.OriginAllowed(origin) {
		return false
	}
	headerToken := r.Header.Get("X-CSRF-Token")
	cookieToken := cookieValue(r, s.cookieName(csrfCookieBase))
	if headerToken == "" || cookieToken == "" || len(headerToken) > 512 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) != 1 {
		return false
	}
	sum := sha256.Sum256([]byte(headerToken))
	return subtle.ConstantTimeCompare(sum[:], principal.Session.CSRFTokenHash) == 1
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func principalFrom(ctx context.Context) (domain.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(domain.Principal)
	return principal, ok
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return host
	}
	remote = remote.Unmap()
	trusted := false
	for _, prefix := range s.cfg.TrustedProxyPrefixes {
		if prefix.Contains(remote) {
			trusted = true
			break
		}
	}
	if trusted {
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			if ip, err := netip.ParseAddr(realIP); err == nil {
				return ip.Unmap().String()
			}
		}
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			// The nearest proxy appends its peer. With one trusted proxy, the
			// last entry is the address nginx actually saw; earlier entries
			// are client-supplied and must not be trusted.
			parts := strings.Split(forwarded, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				candidate := strings.TrimSpace(parts[i])
				if candidate == "" {
					continue
				}
				if ip, err := netip.ParseAddr(candidate); err == nil {
					return ip.Unmap().String()
				}
			}
		}
	}
	return remote.String()
}
