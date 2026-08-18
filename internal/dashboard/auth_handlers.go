package dashboard

import (
	"net/http"
	"time"

	"github.com/firemanx07/slay-push/internal/auth"
	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, templates.Login(""))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, r, templates.Login("invalid form submission"))
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")

	user, err := s.DB.GetUserByEmail(r.Context(), email)
	if err != nil {
		renderPage(w, r, templates.Login("invalid email or password"))
		return
	}

	ok, err := auth.VerifyPassword(user.PasswordHash, password)
	if err != nil || !ok {
		renderPage(w, r, templates.Login("invalid email or password"))
		return
	}

	rawToken, err := auth.GenerateSessionToken()
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to generate session token")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().Add(auth.SessionTTL)
	if _, err := s.DB.CreateSession(r.Context(), postgres.CreateSessionParams{
		UserID:    user.ID,
		TokenHash: auth.HashSessionToken(rawToken),
		ExpiresAt: postgres.TimeFrom(expiresAt),
	}); err != nil {
		s.Logger.Error().Err(err).Msg("failed to create session")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is config-driven (s.CookieSecure), not omitted; false only for local plain-HTTP dev
		Name:     sessionCookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if session, err := s.DB.GetActiveSessionByHash(r.Context(), auth.HashSessionToken(cookie.Value)); err == nil {
			if err := s.DB.RevokeSession(r.Context(), session.ID); err != nil {
				s.Logger.Warn().Err(err).Msg("failed to revoke session")
			}
		}
	}

	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is config-driven (s.CookieSecure), not omitted; false only for local plain-HTTP dev
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
