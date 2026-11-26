package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/events"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

// AdminStore is the subset of store.Store used by the admin HTTP handlers.
// Keeping it as an interface lets RBAC, audit, and user-protection rules be
// unit-tested without Postgres.
type AdminStore interface {
	AdminOverview(ctx context.Context) (domain.AdminOverview, error)
	AdminListEvents(ctx context.Context, limit int) ([]domain.AdminEvent, error)
	AdminGetEvent(ctx context.Context, id string) (domain.AdminEvent, error)
	AdminUpdateEvent(ctx context.Context, id string, input domain.EventInput) (domain.AdminEvent, error)
	AdminResetEventBackoff(ctx context.Context, id string, enable bool) (domain.AdminEvent, error)
	AdminDeleteEvent(ctx context.Context, id string) (bool, error)
	ListEventChecks(ctx context.Context, eventID string, limit int) ([]domain.EventCheckLog, error)
	ListRecentChecks(ctx context.Context, limit int) ([]domain.EventCheckLog, error)
	RecordAdminAudit(ctx context.Context, actorID, action, targetID, requestID string, metadata map[string]any) error
	ListAdminAudit(ctx context.Context, limit int) ([]domain.AdminAuditEntry, error)
	AdminListUsers(ctx context.Context, limit int) ([]domain.AdminUser, error)
	AdminSetUserStatus(ctx context.Context, userID string, status domain.UserStatus) (domain.AdminUser, error)
	AdminRemoveUser(ctx context.Context, userID string) error
	GetUser(ctx context.Context, id string) (domain.User, error)
}

// Compile-time check that *store.Store implements AdminStore.
var _ AdminStore = (*store.Store)(nil)

type adminCreateWatcherRequest struct {
	UserID               string `json:"user_id"`
	Name                 string `json:"name"`
	URL                  string `json:"url"`
	CheckIntervalSeconds int    `json:"check_interval_seconds"`
	Enabled              *bool  `json:"enabled"`
}

type adminFetchRequest struct {
	DryRun bool `json:"dry_run"`
}

type adminResetBackoffRequest struct {
	Enable *bool `json:"enable"`
}

func (s *Server) mailTransportName() string {
	if !s.notificationsEnabled() {
		return "disabled"
	}
	if s.cfg.SMTPHost != "" {
		return "smtp"
	}
	return "sendmail"
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		internalError(w)
		return
	}
	ov, err := s.adminStore.AdminOverview(r.Context())
	if err != nil {
		s.logger.Error("admin overview", "err", err, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	ov.MailTransport = s.mailTransportName()
	writeJSON(w, http.StatusOK, ov)
}

func (s *Server) handleAdminListWatchers(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		internalError(w)
		return
	}
	limit := parseLimitQuery(r, 200, 500)
	list, err := s.adminStore.AdminListEvents(r.Context(), limit)
	if err != nil {
		s.logger.Error("admin list watchers", "err", err, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"watchers": list})
}

func (s *Server) handleAdminCreateWatcher(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if s.adminStore == nil {
		internalError(w)
		return
	}
	var req adminCreateWatcherRequest
	if err := decodeJSON(w, r, s.cfg.MaxRequestBytes, &req); err != nil {
		badRequest(w, err)
		return
	}
	ownerID := strings.TrimSpace(req.UserID)
	if ownerID == "" {
		ownerID = principal.User.ID
	} else {
		if _, err := s.adminStore.GetUser(r.Context(), ownerID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "user_not_found", "target user not found")
				return
			}
			s.logger.Error("admin create watcher lookup user", "err", err, "request_id", requestIDFrom(r.Context()))
			internalError(w)
			return
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	created, err := s.events.Create(r.Context(), ownerID, domain.EventInput{
		Name:                 req.Name,
		URL:                  req.URL,
		CheckIntervalSeconds: req.CheckIntervalSeconds,
		Enabled:              enabled,
	})
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	s.audit(r, principal.User.ID, "create_watcher", created.ID, map[string]any{
		"owner_id": ownerID,
		"name":     created.Name,
		"url":      created.URL,
		"interval": created.CheckIntervalSeconds,
		"enabled":  created.Enabled,
	})
	watcher, err := s.adminStore.AdminGetEvent(r.Context(), created.ID)
	if err != nil {
		writeJSON(w, http.StatusCreated, created)
		return
	}
	writeJSON(w, http.StatusCreated, watcher)
}

func (s *Server) handleAdminGetWatcher(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		internalError(w)
		return
	}
	id := r.PathValue("id")
	watcher, err := s.adminStore.AdminGetEvent(r.Context(), id)
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	limit := parseLimitQuery(r, 50, 200)
	checks, err := s.adminStore.ListEventChecks(r.Context(), id, limit)
	if err != nil {
		s.logger.Error("admin list watcher checks", "err", err, "event_id", id, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"watcher": watcher,
		"checks":  checks,
	})
}

func (s *Server) handleAdminUpdateWatcher(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if s.adminStore == nil {
		internalError(w)
		return
	}
	var req eventRequest
	if err := decodeJSON(w, r, s.cfg.MaxRequestBytes, &req); err != nil {
		badRequest(w, err)
		return
	}
	validated, err := events.ValidateInput(req.input())
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	id := r.PathValue("id")
	updated, err := s.adminStore.AdminUpdateEvent(r.Context(), id, validated)
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	s.audit(r, principal.User.ID, "update_watcher", updated.ID, map[string]any{
		"name":     updated.Name,
		"url":      updated.URL,
		"interval": updated.CheckIntervalSeconds,
		"enabled":  updated.Enabled,
	})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleAdminDeleteWatcher(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if s.adminStore == nil {
		internalError(w)
		return
	}
	id := r.PathValue("id")
	found, err := s.adminStore.AdminDeleteEvent(r.Context(), id)
	if err != nil {
		s.logger.Error("admin delete watcher", "err", err, "event_id", id, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "event_not_found", "event not found")
		return
	}
	s.audit(r, principal.User.ID, "delete_watcher", id, nil)
	writeNoContent(w)
}

func (s *Server) handleAdminFetchWatcher(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if s.adminStore == nil {
		internalError(w)
		return
	}
	var req adminFetchRequest
	if err := decodeOptionalJSON(w, r, s.cfg.MaxRequestBytes, &req); err != nil {
		badRequest(w, err)
		return
	}
	id := r.PathValue("id")
	if req.DryRun {
		result, err := s.events.DryRunCheck(r.Context(), id)
		if err != nil {
			s.writeEventError(w, r, err)
			return
		}
		s.audit(r, principal.User.ID, "dry_run_fetch", id, map[string]any{
			"outcome":     result.Outcome,
			"is_on":       result.IsOn,
			"duration_ms": result.DurationMs,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"dry_run": true,
			"result":  result,
		})
		return
	}

	checked, err := s.events.AdminCheckNow(r.Context(), id)
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	s.audit(r, principal.User.ID, "manual_fetch", checked.ID, map[string]any{
		"outcome": checked.LastOutcome,
		"is_on":   checked.IsOn,
	})
	watcher, err := s.adminStore.AdminGetEvent(r.Context(), checked.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"dry_run": false, "watcher": checked})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dry_run": false, "watcher": watcher})
}

func (s *Server) handleAdminResetBackoff(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if s.adminStore == nil {
		internalError(w)
		return
	}
	var req adminResetBackoffRequest
	if err := decodeOptionalJSON(w, r, s.cfg.MaxRequestBytes, &req); err != nil {
		badRequest(w, err)
		return
	}
	enable := true
	if req.Enable != nil {
		enable = *req.Enable
	}
	id := r.PathValue("id")
	updated, err := s.adminStore.AdminResetEventBackoff(r.Context(), id, enable)
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	s.audit(r, principal.User.ID, "reset_backoff", updated.ID, map[string]any{"enable": enable})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		internalError(w)
		return
	}
	limit := parseLimitQuery(r, 200, 500)
	users, err := s.adminStore.AdminListUsers(r.Context(), limit)
	if err != nil {
		s.logger.Error("admin list users", "err", err, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleAdminBanUser(w http.ResponseWriter, r *http.Request) {
	s.setAdminUserStatus(w, r, domain.StatusSuspended, "ban_user")
}

func (s *Server) handleAdminUnbanUser(w http.ResponseWriter, r *http.Request) {
	s.setAdminUserStatus(w, r, domain.StatusActive, "unban_user")
}

func (s *Server) setAdminUserStatus(w http.ResponseWriter, r *http.Request, status domain.UserStatus, action string) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if s.adminStore == nil {
		internalError(w)
		return
	}
	id := r.PathValue("id")
	if id == principal.User.ID {
		writeError(w, http.StatusForbidden, "cannot_modify_admin", "administrator accounts cannot be banned or removed")
		return
	}
	updated, err := s.adminStore.AdminSetUserStatus(r.Context(), id, status)
	if err != nil {
		s.writeAdminUserError(w, r, err)
		return
	}
	s.audit(r, principal.User.ID, action, updated.ID, map[string]any{
		"email":  updated.Email,
		"status": updated.Status,
	})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleAdminRemoveUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if s.adminStore == nil {
		internalError(w)
		return
	}
	id := r.PathValue("id")
	if id == principal.User.ID {
		writeError(w, http.StatusForbidden, "cannot_modify_admin", "administrator accounts cannot be banned or removed")
		return
	}
	if err := s.adminStore.AdminRemoveUser(r.Context(), id); err != nil {
		s.writeAdminUserError(w, r, err)
		return
	}
	s.audit(r, principal.User.ID, "remove_user", id, nil)
	writeNoContent(w)
}

func (s *Server) handleAdminCheckLogs(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		internalError(w)
		return
	}
	limit := parseLimitQuery(r, 100, 200)
	logs, err := s.adminStore.ListRecentChecks(r.Context(), limit)
	if err != nil {
		s.logger.Error("admin check logs", "err", err, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": logs})
}

func (s *Server) handleAdminAuditLogs(w http.ResponseWriter, r *http.Request) {
	if s.adminStore == nil {
		internalError(w)
		return
	}
	limit := parseLimitQuery(r, 100, 200)
	entries, err := s.adminStore.ListAdminAudit(r.Context(), limit)
	if err != nil {
		s.logger.Error("admin audit logs", "err", err, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) writeAdminUserError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrProtectedAdmin):
		writeError(w, http.StatusForbidden, "cannot_modify_admin", "administrator accounts cannot be banned or removed")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "user not found")
	default:
		s.logger.Error("admin user request", "err", err, "path", r.URL.Path, "request_id", requestIDFrom(r.Context()))
		internalError(w)
	}
}

func (s *Server) audit(r *http.Request, actorID, action, targetID string, metadata map[string]any) {
	reqID := requestIDFrom(r.Context())
	s.logger.Info("admin action",
		"actor_id", actorID,
		"action", action,
		"target_id", targetID,
		"request_id", reqID,
	)
	if s.adminStore == nil {
		return
	}
	if err := s.adminStore.RecordAdminAudit(r.Context(), actorID, action, targetID, reqID, metadata); err != nil {
		s.logger.Warn("record admin audit", "action", action, "err", err, "request_id", reqID)
	}
}

func parseLimitQuery(r *http.Request, fallback, maxLimit int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, out any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	err := decodeJSON(w, r, maxBytes, out)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
