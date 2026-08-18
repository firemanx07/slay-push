// Package dashboard serves the operator-facing htmx dashboard: local login,
// first-run setup, and (in later phases) project/provider/device/notification
// management pages. It mounts alongside, not inside, internal/transport/http's
// API router.
package dashboard

import (
	"embed"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

//go:embed static
var staticFS embed.FS

// Server holds the dependencies the dashboard's handlers need.
type Server struct {
	DB           *postgres.Queries
	Logger       zerolog.Logger
	CookieSecure bool
}

// NewRouter builds the dashboard's chi.Router.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(s.setupGate)

	r.Handle("/static/*", http.FileServer(http.FS(staticFS)))

	r.Get("/setup", s.handleSetupPage)
	r.Post("/setup", s.handleSetup)

	r.Get("/login", s.handleLoginPage)
	r.Post("/login", s.handleLogin)
	r.Post("/logout", s.handleLogout)

	r.With(s.requireSession).Get("/", s.handleHome)

	return r
}
