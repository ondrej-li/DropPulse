// Package httpapi wires the public HTTP surface. Every product route is
// authenticated; the only public routes are health, the OAuth/session
// establishing endpoints and signed provider webhooks (not yet implemented).
package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/auth"
	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/events"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

// Server contains the HTTP dependencies. Handlers never reach for globals.
type Server struct {
	cfg     config.Config
	store   *store.Store
	auth    *auth.Service
	events  *events.Service
	logger  *slog.Logger
	limiter *Limiter
	now     func() time.Time

	admin             domain.User
	frontend          []byte
	localEntitlements domain.Entitlements
}

// New creates the API server. In single-user mode it loads the fixed local
// account and the frontend file once at start-up.
func New(cfg config.Config, st *store.Store, authSvc *auth.Service, eventsSvc *events.Service, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:     cfg,
		store:   st,
		auth:    authSvc,
		events:  eventsSvc,
		logger:  logger,
		limiter: NewLimiter(time.Now),
		now:     time.Now,
	}
	if !cfg.SingleUserMode {
		return s, nil
	}
	if eventsSvc == nil {
		return nil, errors.New("single-user mode requires the events service")
	}
	s.admin = eventsSvc.Admin()
	s.localEntitlements = domain.Entitlements{
		PlanCode:           "local",
		MaxActiveWatches:   100,
		AllowedModes:       []domain.Mode{domain.ModeBasic},
		MinIntervalSeconds: domain.MinCheckInterval,
		HistoryDays:        365,
	}
	if cfg.FrontendFile == "" {
		return nil, errors.New("single-user mode requires FRONTEND_FILE")
	}
	frontend, err := os.ReadFile(cfg.FrontendFile)
	if err != nil {
		return nil, fmt.Errorf("read FRONTEND_FILE: %w", err)
	}
	if len(frontend) == 0 {
		return nil, errors.New("FRONTEND_FILE is empty")
	}
	if len(frontend) > 1<<20 {
		return nil, errors.New("FRONTEND_FILE must be at most 1 MiB")
	}
	s.frontend = frontend
	return s, nil
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)

	if s.cfg.SingleUserMode {
		local := s.singleUser
		mux.Handle("GET /api/v1/me", local(http.HandlerFunc(s.handleMe)))
		mux.Handle("GET /api/v1/events", local(http.HandlerFunc(s.handleListEvents)))
		mux.Handle("POST /api/v1/events", local(http.HandlerFunc(s.handleCreateEvent)))
		mux.Handle("GET /api/v1/events/{id}", local(http.HandlerFunc(s.handleGetEvent)))
		mux.Handle("PUT /api/v1/events/{id}", local(http.HandlerFunc(s.handleUpdateEvent)))
		mux.Handle("DELETE /api/v1/events/{id}", local(http.HandlerFunc(s.handleDeleteEvent)))
		mux.Handle("POST /api/v1/events/{id}/check", local(http.HandlerFunc(s.handleCheckEvent)))
		mux.HandleFunc("GET /", s.handleFrontend)
	} else {
		mux.HandleFunc("GET /api/v1/auth/{provider}/start", s.handleAuthStart)
		mux.HandleFunc("GET /api/v1/auth/{provider}/callback", s.handleAuthCallback)
		mux.HandleFunc("POST /api/v1/auth/{provider}/callback", s.handleAuthCallback)
		mux.Handle("POST /api/v1/auth/refresh", s.requireAuth(http.HandlerFunc(s.handleAuthRefresh)))
		mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
		mux.Handle("GET /api/v1/auth/csrf", s.requireAuth(http.HandlerFunc(s.handleCSRF)))

		mux.Handle("GET /api/v1/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
		mux.Handle("GET /api/v1/me/sessions", s.requireAuth(http.HandlerFunc(s.handleListSessions)))
		mux.Handle("DELETE /api/v1/me/sessions/{id}", s.requireAuth(http.HandlerFunc(s.handleRevokeSession)))
	}

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
