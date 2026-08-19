package dashboard

import (
	"encoding/json"
	"net/http"

	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const defaultCredentialEnvironment = "production"

func toProviderCredentialView(c postgres.ListProviderCredentialsByProjectRow) templates.ProviderCredential {
	return templates.ProviderCredential{
		ProviderType: c.ProviderType,
		Environment:  c.Environment,
		IsActive:     c.IsActive,
		UpdatedAt:    postgres.TimeTo(c.UpdatedAt),
	}
}

func (s *Server) renderProvidersTab(w http.ResponseWriter, r *http.Request, project postgres.Project, message string) {
	email, err := s.currentUserEmail(r)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to resolve dashboard user")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	credentials, err := s.DB.ListProviderCredentialsByProject(r.Context(), project.ID)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to list provider credentials")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	views := make([]templates.ProviderCredential, 0, len(credentials))
	for _, c := range credentials {
		views = append(views, toProviderCredentialView(c))
	}
	renderPage(w, r, templates.ProvidersTab(email, toProjectView(project), views, message))
}

func (s *Server) handleProvidersTab(w http.ResponseWriter, r *http.Request) {
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
	s.renderProvidersTab(w, r, project, "")
}

func (s *Server) handleUpsertProviderCredential(w http.ResponseWriter, r *http.Request) {
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
		s.renderProvidersTab(w, r, project, "invalid form submission")
		return
	}
	providerType := r.FormValue("provider")
	credentialText := r.FormValue("credential")

	if !provider.Known(providerType) {
		s.renderProvidersTab(w, r, project, "unknown provider type")
		return
	}
	if !json.Valid([]byte(credentialText)) {
		s.renderProvidersTab(w, r, project, "credential must be valid JSON")
		return
	}

	wrappedDEK, ciphertext, err := s.MasterKey.Seal([]byte(credentialText))
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to encrypt provider credential")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := s.DB.UpsertProviderCredential(r.Context(), postgres.UpsertProviderCredentialParams{
		ProjectID:    project.ID,
		ProviderType: providerType,
		Environment:  defaultCredentialEnvironment,
		Credential:   ciphertext,
		WrappedDek:   wrappedDEK,
	}); err != nil {
		s.Logger.Error().Err(err).Msg("failed to upsert provider credential")
		s.renderProvidersTab(w, r, project, "failed to save credential")
		return
	}

	s.renderProvidersTab(w, r, project, "credential saved")
}

func (s *Server) handleTestProviderCredential(w http.ResponseWriter, r *http.Request) {
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
		s.renderProvidersTab(w, r, project, "invalid form submission")
		return
	}
	providerType := r.FormValue("provider")

	stored, err := s.DB.GetActiveProviderCredential(r.Context(), postgres.GetActiveProviderCredentialParams{
		ProjectID:    project.ID,
		ProviderType: providerType,
		Environment:  defaultCredentialEnvironment,
	})
	if err != nil {
		s.renderProvidersTab(w, r, project, providerType+": no active credential configured")
		return
	}

	plaintext, err := s.MasterKey.Open(stored.WrappedDek, stored.Credential)
	if err != nil {
		s.renderProvidersTab(w, r, project, providerType+": failed to decrypt stored credential")
		return
	}

	adapter, ok := provider.Get(providerType)
	if !ok {
		s.renderProvidersTab(w, r, project, providerType+": unknown provider")
		return
	}

	tester, ok := adapter.(provider.CredentialTester)
	if !ok {
		if !json.Valid(plaintext) {
			s.renderProvidersTab(w, r, project, providerType+": stored credential is not valid JSON")
			return
		}
		s.renderProvidersTab(w, r, project, providerType+": credential shape looks valid (no live check available for this provider)")
		return
	}

	if err := tester.TestCredential(r.Context(), plaintext); err != nil {
		s.renderProvidersTab(w, r, project, providerType+": test failed — "+err.Error())
		return
	}

	s.renderProvidersTab(w, r, project, providerType+": credential test passed")
}
