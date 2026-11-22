package httpapi

import (
	"net/http"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
)

type meResponse struct {
	User                 domain.User         `json:"user"`
	Plan                 planResponse        `json:"plan"`
	Entitlements         domain.Entitlements `json:"entitlements"`
	Usage                usageResponse       `json:"usage"`
	Session              *sessionResponse    `json:"session,omitempty"`
	Providers            []string            `json:"providers"`
	SingleUser           bool                `json:"single_user"`
	NotificationsEnabled bool                `json:"notifications_enabled"`
}

type planResponse struct {
	Code string `json:"code"`
}

type usageResponse struct {
	ActiveWatches        int `json:"active_watches"`
	AISetupPreviewsToday int `json:"ai_setup_previews_today"`
	AIChecksToday        int `json:"ai_checks_today"`
}

type sessionResponse struct {
	ID                string             `json:"id"`
	Kind              domain.SessionKind `json:"kind"`
	Current           bool               `json:"current"`
	UserAgent         string             `json:"user_agent,omitempty"`
	IP                string             `json:"ip,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	LastUsedAt        time.Time          `json:"last_used_at"`
	IdleExpiresAt     time.Time          `json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time          `json:"absolute_expires_at"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	active := 0
	if s.events != nil {
		var err error
		active, err = s.events.CountActive(r.Context(), principal.User.ID)
		if err != nil {
			s.logger.Error("count active events", "err", err, "user_id", principal.User.ID, "request_id", requestIDFrom(r.Context()))
			internalError(w)
			return
		}
	}
	if principal.AuthKind == "local" {
		writeJSON(w, http.StatusOK, meResponse{
			User:                 principal.User,
			Plan:                 planResponse{Code: principal.Entitlements.PlanCode},
			Entitlements:         principal.Entitlements,
			Usage:                usageResponse{ActiveWatches: active},
			Providers:            []string{},
			SingleUser:           true,
			NotificationsEnabled: s.notificationsEnabled(),
		})
		return
	}
	session := sessionForResponse(principal.Session, principal.Session.ID)
	providers := []string{}
	if s.auth != nil {
		providers = s.auth.Providers()
	}
	writeJSON(w, http.StatusOK, meResponse{
		User:                 principal.User,
		Plan:                 planResponse{Code: principal.Entitlements.PlanCode},
		Entitlements:         principal.Entitlements,
		Usage:                usageResponse{ActiveWatches: active},
		Session:              &session,
		Providers:            providers,
		NotificationsEnabled: s.notificationsEnabled(),
	})
}

// notificationsEnabled reports whether the server can submit availability
// mail. In multi-user mode the recipient is the verified account address; a
// fixed NOTIFICATION_EMAIL is a single-user-only convenience.
func (s *Server) notificationsEnabled() bool {
	if s.cfg.SingleUserMode {
		return s.cfg.NotificationEmail != ""
	}
	return s.cfg.SMTPFrom != ""
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	sessions, err := s.store.ListSessions(r.Context(), principal.User.ID)
	if err != nil {
		s.logger.Error("list sessions", "err", err, "user_id", principal.User.ID, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	out := make([]sessionResponse, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, sessionForResponse(session, principal.Session.ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	id := r.PathValue("id")
	if !domain.ValidUUID(id) {
		writeError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	found, err := s.store.RevokeUserSession(r.Context(), principal.User.ID, id)
	if err != nil {
		s.logger.Error("revoke session", "err", err, "user_id", principal.User.ID, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	if id == principal.Session.ID {
		s.clearAuthCookies(w)
	}
	writeNoContent(w)
}

func sessionForResponse(session domain.Session, currentID string) sessionResponse {
	return sessionResponse{
		ID:                session.ID,
		Kind:              session.Kind,
		Current:           session.ID == currentID,
		UserAgent:         session.UserAgent,
		IP:                session.IP,
		CreatedAt:         session.CreatedAt,
		LastUsedAt:        session.LastUsedAt,
		IdleExpiresAt:     session.IdleExpiresAt,
		AbsoluteExpiresAt: session.AbsoluteExpiresAt,
	}
}
