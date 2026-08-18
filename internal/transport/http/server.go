// Package http wires the public JSON API. Phase 1 has no auth yet — every
// request implicitly targets the single seeded "default" project; Phase 3
// replaces resolveProject with API-key-based project resolution.
package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/dispatch"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const defaultProjectSlug = "default"

type Server struct {
	DB       *postgres.Queries
	Dispatch *dispatch.Handlers
	Logger   zerolog.Logger
}

func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/devices", s.handleRegisterDevice)
	r.Post("/api/v1/notifications", s.handleCreateNotification)
	r.Get("/api/v1/notifications/{id}", s.handleGetNotification)
	r.Get("/api/v1/notifications/{id}/recipients", s.handleListRecipients)
	return r
}

func (s *Server) resolveProject(ctx context.Context) (postgres.Project, error) {
	return s.DB.GetProjectBySlug(ctx, defaultProjectSlug)
}
