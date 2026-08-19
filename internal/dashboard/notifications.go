package dashboard

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/dashboard/templates"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const notificationHistoryPageSize = 50

func toNotificationView(n postgres.Notification) templates.Notification {
	return templates.Notification{
		ID:              postgres.UUIDTo(n.ID).String(),
		Status:          n.Status,
		TotalRecipients: n.TotalRecipients,
		CreatedAt:       postgres.TimeTo(n.CreatedAt),
	}
}

func toRecipientView(rec postgres.NotificationRecipient) templates.Recipient {
	v := templates.Recipient{
		ID:           postgres.UUIDTo(rec.ID).String(),
		DeviceID:     postgres.UUIDTo(rec.DeviceID).String(),
		ProviderType: rec.ProviderType,
		Status:       rec.Status,
		AttemptCount: rec.AttemptCount,
	}
	if rec.ProviderMessageID != nil {
		v.ProviderMessageID = *rec.ProviderMessageID
	}
	if rec.ErrorMessage != nil {
		v.ErrorMessage = *rec.ErrorMessage
	}
	return v
}

func toRecipientCounts(counts map[string]int64) templates.RecipientCounts {
	return templates.RecipientCounts{
		Queued:    counts["queued"],
		Sending:   counts["sending"],
		Sent:      counts["sent"],
		Delivered: counts["delivered"],
		Failed:    counts["failed"],
	}
}

func (s *Server) handleNotificationsTab(w http.ResponseWriter, r *http.Request) {
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

	notifications, err := s.DB.ListNotificationsByProject(r.Context(), postgres.ListNotificationsByProjectParams{
		ProjectID:  project.ID,
		PageOffset: 0,
		PageLimit:  notificationHistoryPageSize,
	})
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to list notifications")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	views := make([]templates.Notification, 0, len(notifications))
	for _, n := range notifications {
		views = append(views, toNotificationView(n))
	}
	renderPage(w, r, templates.NotificationsTab(toProjectView(project), views))
}

// notificationRecipients resolves a notification (unscoped by project — the
// dashboard is visible to every project) and its recipients.
func (s *Server) notificationRecipients(r *http.Request, id uuid.UUID) (postgres.Notification, []postgres.NotificationRecipient, error) {
	n, err := s.DB.GetNotificationByID(r.Context(), postgres.UUIDFrom(id))
	if err != nil {
		return postgres.Notification{}, nil, err
	}
	recipients, err := s.DB.ListNotificationRecipients(r.Context(), postgres.ListNotificationRecipientsParams{
		NotificationID: n.ID,
		ProjectID:      n.ProjectID,
	})
	if err != nil {
		return postgres.Notification{}, nil, err
	}
	return n, recipients, nil
}

func (s *Server) handleNotificationDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid notification id", http.StatusBadRequest)
		return
	}

	n, recipients, err := s.notificationRecipients(r, id)
	if err != nil {
		http.Error(w, "notification not found", http.StatusNotFound)
		return
	}

	counts, err := s.Dispatch.RecipientStatusCounts(r.Context(), id)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to fetch recipient counts")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	recipientViews := make([]templates.Recipient, 0, len(recipients))
	for _, rec := range recipients {
		recipientViews = append(recipientViews, toRecipientView(rec))
	}
	recipientCounts := toRecipientCounts(counts)

	renderPage(w, r, templates.NotificationDetail(
		id.String(), n.Status, n.TotalRecipients, recipientCounts, recipientViews,
		recipientCounts.Terminal(n.TotalRecipients),
	))
}

func (s *Server) handleRecipientsFragment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid notification id", http.StatusBadRequest)
		return
	}

	n, recipients, err := s.notificationRecipients(r, id)
	if err != nil {
		http.Error(w, "notification not found", http.StatusNotFound)
		return
	}

	counts, err := s.Dispatch.RecipientStatusCounts(r.Context(), id)
	if err != nil {
		s.Logger.Error().Err(err).Msg("failed to fetch recipient counts")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	recipientViews := make([]templates.Recipient, 0, len(recipients))
	for _, rec := range recipients {
		recipientViews = append(recipientViews, toRecipientView(rec))
	}
	recipientCounts := toRecipientCounts(counts)

	renderPage(w, r, templates.RecipientsFragment(id.String(), recipientViews, recipientCounts.Terminal(n.TotalRecipients)))
}
