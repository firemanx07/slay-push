package dashboard

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/apikey"
	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

func toAPIKeyView(k postgres.ApiKey) templates.APIKey {
	return templates.APIKey{
		ID:         postgres.UUIDTo(k.ID).String(),
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		Scope:      k.Scope,
		CreatedAt:  postgres.TimeTo(k.CreatedAt),
		LastUsedAt: postgres.TimeToPtr(k.LastUsedAt),
		Revoked:    k.RevokedAt.Valid,
	}
}

func (s *Server) renderAPIKeysTab(w http.ResponseWriter, r *http.Request, project postgres.Project, revealedKey, message string) {
	email, err := s.currentUserEmail(r)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to resolve dashboard user")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	keys, err := s.DB.ListAPIKeysByProject(r.Context(), project.ID)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to list api keys")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	views := make([]templates.APIKey, 0, len(keys))
	for _, k := range keys {
		views = append(views, toAPIKeyView(k))
	}
	renderPage(w, r, templates.APIKeysTab(email, toProjectView(project), views, revealedKey, message))
}

func (s *Server) handleAPIKeysTab(w http.ResponseWriter, r *http.Request) {
	projectID, ok := projectIDFromRoute(r)
	if !ok {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}
	project, err := s.visibleProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	s.renderAPIKeysTab(w, r, project, "", "")
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := projectIDFromRoute(r)
	if !ok {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}
	project, err := s.visibleProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderAPIKeysTab(w, r, project, "", "invalid form submission")
		return
	}
	name := r.FormValue("name")
	scope, scopeOK := apikey.ParseScope(r.FormValue("scope"))
	if name == "" || !scopeOK {
		s.renderAPIKeysTab(w, r, project, "", "name is required and scope must be read or send")
		return
	}

	raw, prefix, err := apikey.Generate()
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to generate api key")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := s.DB.CreateAPIKey(r.Context(), postgres.CreateAPIKeyParams{
		ProjectID: project.ID,
		Name:      name,
		KeyPrefix: prefix,
		KeyHash:   apikey.Hash(raw),
		Scope:     string(scope),
	}); err != nil {
		s.Logger.Error().Err(err).Msg("failed to create api key")
		s.renderAPIKeysTab(w, r, project, "", "failed to create api key")
		return
	}

	s.renderAPIKeysTab(w, r, project, raw, "")
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := projectIDFromRoute(r)
	if !ok {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}
	keyID, err := uuid.Parse(chi.URLParam(r, "keyID"))
	if err != nil {
		http.Error(w, "invalid api key id", http.StatusBadRequest)
		return
	}

	if err := s.DB.RevokeAPIKey(r.Context(), postgres.RevokeAPIKeyParams{
		ID:        postgres.UUIDFrom(keyID),
		ProjectID: postgres.UUIDFrom(projectID),
	}); err != nil {
		s.Logger.Error().Err(err).Msg("failed to revoke api key")
	}

	http.Redirect(w, r, "/projects/"+projectID.String()+"/api-keys", http.StatusSeeOther) //nolint:gosec // projectID is a parsed uuid.UUID, not raw attacker-controlled text
}
