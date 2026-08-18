package dashboard

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/firemanx07/slay-push/internal/auth"
	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const minPasswordLength = 8

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, templates.Setup(""))
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, r, templates.Setup("invalid form submission"))
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")

	if email == "" || !strings.Contains(email, "@") {
		renderPage(w, r, templates.Setup("a valid email is required"))
		return
	}
	if len(password) < minPasswordLength {
		renderPage(w, r, templates.Setup(fmt.Sprintf("password must be at least %d characters", minPasswordLength)))
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to hash password")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := s.DB.CreateUser(r.Context(), postgres.CreateUserParams{Email: email, PasswordHash: hash}); err != nil {
		renderPage(w, r, templates.Setup("failed to create admin account (email may already be in use)"))
		return
	}

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
