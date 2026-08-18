package http

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/firemanx07/slay-push/internal/apikey"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const touchAPIKeyTimeout = 5 * time.Second

type ctxKey int

const ctxKeyProjectID ctxKey = iota

func contextWithProjectID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyProjectID, id)
}

func projectIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyProjectID).(uuid.UUID)
	return id, ok
}

// requireScope resolves the API key from the Authorization header, checks
// its scope and rate limit, and injects the resolved project id into the
// request context.
func (s *Server) requireScope(minScope apikey.Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
				return
			}

			key, err := s.DB.GetActiveAPIKeyByHash(r.Context(), apikey.Hash(raw))
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeError(w, http.StatusUnauthorized, "invalid or revoked API key")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to resolve API key")
				return
			}

			scope, ok := apikey.ParseScope(key.Scope)
			if !ok || !scope.Satisfies(minScope) {
				writeError(w, http.StatusForbidden, "API key does not have the required scope")
				return
			}

			projectID := postgres.UUIDTo(key.ProjectID)
			if allowed, retryAfter := s.RateLimiter.Allow(r.Context(), postgres.UUIDTo(key.ID), projectID); !allowed {
				// Round up to at least 1 second.
				seconds := int(math.Ceil(retryAfter.Seconds()))
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}

			go s.touchAPIKeyLastUsed(key.ID) //nolint:gosec,contextcheck // deliberately detached from the request context: this write must outlive the response

			next.ServeHTTP(w, r.WithContext(contextWithProjectID(r.Context(), projectID)))
		})
	}
}

func (s *Server) touchAPIKeyLastUsed(id pgtype.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), touchAPIKeyTimeout)
	defer cancel()
	if err := s.DB.TouchAPIKeyLastUsed(ctx, id); err != nil {
		s.Logger.Warn().Err(err).Msg("failed to update api key last_used_at")
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}
