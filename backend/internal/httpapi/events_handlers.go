package httpapi

import (
	"errors"
	"net/http"

	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/events"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

type eventRequest struct {
	Name                 string `json:"name"`
	URL                  string `json:"url"`
	CheckIntervalSeconds int    `json:"check_interval_seconds"`
	Enabled              *bool  `json:"enabled"`
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	list, err := s.events.List(r.Context(), principal.User.ID)
	if err != nil {
		s.logger.Error("list events", "err", err, "user_id", principal.User.ID, "request_id", requestIDFrom(r.Context()))
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": list})
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	event, err := s.events.Get(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	var request eventRequest
	if err := decodeJSON(w, r, s.cfg.MaxRequestBytes, &request); err != nil {
		badRequest(w, err)
		return
	}
	event, err := s.events.Create(r.Context(), principal.User.ID, request.input())
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, event)
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	var request eventRequest
	if err := decodeJSON(w, r, s.cfg.MaxRequestBytes, &request); err != nil {
		badRequest(w, err)
		return
	}
	event, err := s.events.Update(r.Context(), principal.User.ID, r.PathValue("id"), request.input())
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	if err := s.events.Delete(r.Context(), principal.User.ID, r.PathValue("id")); err != nil {
		s.writeEventError(w, r, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleCheckEvent(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.unauthenticated(w, r)
		return
	}
	event, err := s.events.CheckNow(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		s.writeEventError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) handleCinemaCityShows(w http.ResponseWriter, r *http.Request) {
	if _, ok := principalFrom(r.Context()); !ok {
		s.unauthenticated(w, r)
		return
	}
	cinemaID := r.URL.Query().Get("cinema")
	if cinemaID == "" {
		cinemaID = "1052"
	}
	attr := r.URL.Query().Get("attr")
	if attr == "" {
		attr = "imax"
	}
	shows, err := s.events.ListCinemaCityShows(r.Context(), cinemaID, attr)
	if err != nil {
		s.logger.Warn("fetch Cinema City shows", "cinema", cinemaID, "attr", attr, "err", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusBadGateway, "upstream_unavailable", "Cinema City programme could not be loaded right now")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shows": shows})
}

func (request eventRequest) input() domain.EventInput {
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return domain.EventInput{
		Name:                 request.Name,
		URL:                  request.URL,
		CheckIntervalSeconds: request.CheckIntervalSeconds,
		Enabled:              enabled,
	}
}

func (s *Server) writeEventError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *events.ValidationError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, "invalid_event", invalid.Message)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "event_not_found", "event not found")
	default:
		s.logger.Error("event request", "err", err, "path", r.URL.Path, "request_id", requestIDFrom(r.Context()))
		internalError(w)
	}
}
