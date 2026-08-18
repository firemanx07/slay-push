// Package http wires the public JSON API.
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/apikey"
	"github.com/firemanx07/slay-push/internal/dispatch"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// Server holds the dependencies the public JSON API's handlers need.
type Server struct {
	DB          *postgres.Queries
	Dispatch    *dispatch.Handlers
	RateLimiter *apikey.RateLimiter
	Logger      zerolog.Logger
}

// NewRouter builds the public JSON API's chi.Router.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()

	requireRead := s.requireScope(apikey.ScopeRead)
	requireSend := s.requireScope(apikey.ScopeSend)

	r.With(requireSend).Post("/api/v1/devices", s.handleRegisterDevice)
	r.With(requireSend).Post("/api/v1/notifications", s.handleCreateNotification)
	r.With(requireRead).Get("/api/v1/notifications/{id}", s.handleGetNotification)
	r.With(requireRead).Get("/api/v1/notifications/{id}/recipients", s.handleListRecipients)
	return r
}
