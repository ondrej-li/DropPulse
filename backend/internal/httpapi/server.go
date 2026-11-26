// Package httpapi wires the public HTTP surface. The landing page, health check,
// configured-provider list and OAuth endpoints are public; every route that
// reads or changes a user's watches is authenticated.
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
	cfg        config.Config
	store      *store.Store
	adminStore AdminStore
	auth       *auth.Service
	events     *events.Service
	logger     *slog.Logger
	limiter    *Limiter
	now        func() time.Time

	admin             domain.User
	frontend          []byte
	localEntitlements domain.Entitlements
}

// New creates the API server. It loads the frontend file once at start-up when
// one is configured; single-user mode additionally requires the fixed local
// account and the frontend.
func New(cfg config.Config, st *store.Store, authSvc *auth.Service, eventsSvc *events.Service, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if eventsSvc == nil {
		return nil, errors.New("the events service is required")
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
	if st != nil {
		s.adminStore = st
	}
	if cfg.SingleUserMode {
		s.admin = eventsSvc.Admin()
		s.localEntitlements = domain.Entitlements{
			PlanCode:           "local",
			MaxActiveWatches:   100,
			AllowedModes:       []domain.Mode{domain.ModeBasic},
			MinIntervalSeconds: domain.MinCheckInterval,
			HistoryDays:        365,
		}
	}
	if cfg.FrontendFile == "" {
		if cfg.SingleUserMode {
			return nil, errors.New("single-user mode requires FRONTEND_FILE")
		}
		return s, nil
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
		s.mountUserRoutes(mux, s.singleUser)
		mux.HandleFunc("GET /{$}", s.handleFrontend)
	} else {
		mux.HandleFunc("GET /api/v1/auth/providers", s.handleAuthProviders)
		mux.HandleFunc("GET /api/v1/auth/{provider}/start", s.handleAuthStart)
		mux.HandleFunc("GET /api/v1/auth/{provider}/callback", s.handleAuthCallback)
		mux.HandleFunc("POST /api/v1/auth/{provider}/callback", s.handleAuthCallback)
		mux.Handle("POST /api/v1/auth/refresh", s.requireAuth(http.HandlerFunc(s.handleAuthRefresh)))
		mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
		mux.Handle("GET /api/v1/auth/csrf", s.requireAuth(http.HandlerFunc(s.handleCSRF)))

		s.mountUserRoutes(mux, s.requireAuth)
		mux.Handle("GET /api/v1/me/sessions", s.requireAuth(http.HandlerFunc(s.handleListSessions)))
		mux.Handle("DELETE /api/v1/me/sessions/{id}", s.requireAuth(http.HandlerFunc(s.handleRevokeSession)))
		if len(s.frontend) > 0 {
			mux.HandleFunc("GET /{$}", s.handleFrontend)
			mux.HandleFunc("GET /app/{$}", s.handleFrontend)
		}
	}

	return s.requestID(s.accessLog(s.recoverer(s.securityHeaders(s.cors(mux)))))
}

// mountUserRoutes attaches the account, watch, and admin routes behind the
// mode's authentication boundary: local loopback checks or a real session.
func (s *Server) mountUserRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mount := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, wrap(handler))
	}
	mountAdmin := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, wrap(s.requireAdmin(handler)))
	}
	mount("GET /api/v1/me", s.handleMe)
	mount("GET /api/v1/events", s.handleListEvents)
	mount("POST /api/v1/events", s.handleCreateEvent)
	mount("GET /api/v1/events/{id}", s.handleGetEvent)
	mount("PUT /api/v1/events/{id}", s.handleUpdateEvent)
	mount("DELETE /api/v1/events/{id}", s.handleDeleteEvent)
	mount("POST /api/v1/events/{id}/check", s.handleCheckEvent)
	mount("GET /api/v1/providers/cinemacity/shows", s.handleCinemaCityShows)

	mountAdmin("GET /api/v1/admin/overview", s.handleAdminOverview)
	mountAdmin("GET /api/v1/admin/watchers", s.handleAdminListWatchers)
	mountAdmin("POST /api/v1/admin/watchers", s.handleAdminCreateWatcher)
	mountAdmin("GET /api/v1/admin/watchers/{id}", s.handleAdminGetWatcher)
	mountAdmin("PUT /api/v1/admin/watchers/{id}", s.handleAdminUpdateWatcher)
	mountAdmin("DELETE /api/v1/admin/watchers/{id}", s.handleAdminDeleteWatcher)
	mountAdmin("POST /api/v1/admin/watchers/{id}/fetch", s.handleAdminFetchWatcher)
	mountAdmin("POST /api/v1/admin/watchers/{id}/reset-backoff", s.handleAdminResetBackoff)
	mountAdmin("GET /api/v1/admin/users", s.handleAdminListUsers)
	mountAdmin("POST /api/v1/admin/users/{id}/ban", s.handleAdminBanUser)
	mountAdmin("POST /api/v1/admin/users/{id}/unban", s.handleAdminUnbanUser)
	mountAdmin("DELETE /api/v1/admin/users/{id}", s.handleAdminRemoveUser)
	mountAdmin("GET /api/v1/admin/logs/checks", s.handleAdminCheckLogs)
	mountAdmin("GET /api/v1/admin/logs/audit", s.handleAdminAuditLogs)
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
