package dashboard

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// visibleProjects returns every project this dashboard user may see. Every
// dashboard user currently sees every project in the deployment (the
// operator-control-panel model) — this is the single seam a future
// per-customer scoping layer would filter, rather than a query scattered
// across handlers.
func (s *Server) visibleProjects(ctx context.Context) ([]postgres.Project, error) {
	return s.DB.ListProjects(ctx)
}

// visibleProject resolves one project by id, subject to the same
// visibility rule as visibleProjects.
func (s *Server) visibleProject(ctx context.Context, id uuid.UUID) (postgres.Project, error) {
	return s.DB.GetProjectByID(ctx, postgres.UUIDFrom(id))
}

// currentUserEmail resolves the authenticated dashboard user's email, for
// display in the page header. Requires requireSession to have already run.
func (s *Server) currentUserEmail(r *http.Request) (string, error) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		return "", errors.New("no authenticated user in request context")
	}
	user, err := s.DB.GetUserByID(r.Context(), postgres.UUIDFrom(userID))
	if err != nil {
		return "", err
	}
	return user.Email, nil
}

func toProjectView(p postgres.Project) templates.Project {
	return templates.Project{
		ID:        postgres.UUIDTo(p.ID).String(),
		Name:      p.Name,
		Slug:      p.Slug,
		Status:    p.Status,
		CreatedAt: postgres.TimeTo(p.CreatedAt),
	}
}

// projectIDFromRoute parses the {projectID} chi route param.
func projectIDFromRoute(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "projectID"))
	return id, err == nil
}

func (s *Server) renderProjectsList(w http.ResponseWriter, r *http.Request, message string) {
	email, err := s.currentUserEmail(r)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to resolve dashboard user")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projects, err := s.visibleProjects(r.Context())
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to list projects")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	views := make([]templates.Project, 0, len(projects))
	for _, p := range projects {
		views = append(views, toProjectView(p))
	}
	renderPage(w, r, templates.ProjectsList(email, views, message))
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	s.renderProjectsList(w, r, "")
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderProjectsList(w, r, "invalid form submission")
		return
	}
	name := r.FormValue("name")
	slug := r.FormValue("slug")
	if name == "" || slug == "" {
		s.renderProjectsList(w, r, "name and slug are required")
		return
	}

	if _, err := s.DB.CreateProject(r.Context(), postgres.CreateProjectParams{Name: name, Slug: slug}); err != nil {
		s.renderProjectsList(w, r, "failed to create project (slug may already be in use)")
		return
	}

	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

func (s *Server) handleProjectOverview(w http.ResponseWriter, r *http.Request) {
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

	email, err := s.currentUserEmail(r)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to resolve dashboard user")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	renderPage(w, r, templates.ProjectOverview(email, toProjectView(project)))
}
