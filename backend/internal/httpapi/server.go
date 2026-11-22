// Package httpapi wires the public HTTP surface. Every product route is
// authenticated; the only public routes are health, the OAuth/session
// establishing endpoints and signed provider webhooks (not yet implemented).
package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/auth"
	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

// Server contains the HTTP dependencies. Handlers never reach for globals.
type Server struct {
	cfg     config.Config
	store   *store.Store
	auth    *auth.Service
	logger  *slog.Logger
	limiter *Limiter
	now     func() time.Time
}

// New creates the API server.
func New(cfg config.Config, st *store.Store, authSvc *auth.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:     cfg,
		store:   st,
		auth:    authSvc,
		logger:  logger,
		limiter: NewLimiter(time.Now),
		now:     time.Now,
	}
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("GET /api/v1/auth/{provider}/start", s.handleAuthStart)
	mux.HandleFunc("GET /api/v1/auth/{provider}/callback", s.handleAuthCallback)
	mux.HandleFunc("POST /api/v1/auth/{provider}/callback", s.handleAuthCallback)
	mux.Handle("POST /api/v1/auth/refresh", s.requireAuth(http.HandlerFunc(s.handleAuthRefresh)))
	mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /api/v1/auth/csrf", s.requireAuth(http.HandlerFunc(s.handleCSRF)))

	mux.Handle("GET /api/v1/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("GET /api/v1/me/sessions", s.requireAuth(http.HandlerFunc(s.handleListSessions)))
	mux.Handle("DELETE /api/v1/me/sessions/{id}", s.requireAuth(http.HandlerFunc(s.handleRevokeSession)))

	return s.requestID(s.accessLog(s.recoverer(s.securityHeaders(s.cors(mux)))))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// allowAuthRate applies a per-IP, per-scope limit to session-establishing
// endpoints. A rejected request gets Retry-After.
func (s *Server) allowAuthRate(w http.ResponseWriter, r *http.Request, scope string) bool {
	ok, retry := s.limiter.Allow("auth:"+scope+":"+s.clientIP(r), s.cfg.AuthRatePerMinute, time.Minute)
	if ok {
		return true
	}
	seconds := int(retry.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many authentication attempts; try again shortly")
	return false
}
