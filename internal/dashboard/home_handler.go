package dashboard

import (
	"net/http"

	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	user, err := s.DB.GetUserByID(r.Context(), postgres.UUIDFrom(userID))
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to resolve dashboard user")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	renderPage(w, r, templates.Home(user.Email))
}
