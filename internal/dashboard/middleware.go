package dashboard

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/firemanx07/slay-push/internal/auth"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const sessionCookieName = "slaypush_session"

const touchSessionTimeout = 5 * time.Second

type ctxKey int

const ctxKeyUserID ctxKey = iota

func contextWithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}

func userIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyUserID).(uuid.UUID)
	return id, ok
}

// setupGate redirects "/setup" to "/login" once an admin exists, and
// redirects everything else to "/setup" until one does.
func (s *Server) setupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		count, err := s.DB.CountUsers(r.Context())
		if err != nil {
			s.Logger.Error().Err(err).Msg("failed to check setup status")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		setupComplete := count > 0
		if r.URL.Path == "/setup" {
			if setupComplete {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		} else if !setupComplete {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSession resolves the dashboard session cookie, redirecting to
// "/login" if it's missing, unknown, or expired/revoked.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, err := s.DB.GetActiveSessionByHash(r.Context(), auth.HashSessionToken(cookie.Value))
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		go s.touchSessionLastUsed(session.ID) //nolint:gosec,contextcheck // deliberately detached from the request context: this write must outlive the response

		next.ServeHTTP(w, r.WithContext(contextWithUserID(r.Context(), postgres.UUIDTo(session.UserID))))
	})
}

func (s *Server) touchSessionLastUsed(id pgtype.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), touchSessionTimeout)
	defer cancel()
	if err := s.DB.TouchSessionLastUsed(ctx, id); err != nil {
		s.Logger.Warn().Err(err).Msg("failed to update session last_used_at")
	}
}
