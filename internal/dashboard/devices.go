package dashboard

import (
	"net/http"

	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const deviceBrowserPageSize = 50

func toDeviceView(d postgres.ListDevicesByProjectRow) templates.Device {
	return templates.Device{
		ID:           postgres.UUIDTo(d.ID).String(),
		ExternalID:   d.ExternalID,
		Platform:     d.Platform,
		ProviderType: d.ProviderType,
		Status:       d.Status,
		CreatedAt:    postgres.TimeTo(d.CreatedAt),
	}
}

func (s *Server) handleDevicesTab(w http.ResponseWriter, r *http.Request) {
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

	externalID := r.URL.Query().Get("external_id")
	status := r.URL.Query().Get("status")

	devices, err := s.DB.ListDevicesByProject(r.Context(), postgres.ListDevicesByProjectParams{
		ProjectID:  project.ID,
		ExternalID: externalID,
		Status:     status,
		PageOffset: 0,
		PageLimit:  deviceBrowserPageSize,
	})
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to list devices")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	views := make([]templates.Device, 0, len(devices))
	for _, d := range devices {
		views = append(views, toDeviceView(d))
	}
	renderPage(w, r, templates.DevicesTab(toProjectView(project), views, externalID, status))
}
