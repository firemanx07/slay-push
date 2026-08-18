package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/firemanx07/slay-push/internal/dispatch"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// createNotificationRequest uses OneSignal-style targeting field names
// (include_player_ids/include_external_user_ids). included_segments/filters
// are accepted on the wire shape but rejected for now.
type createNotificationRequest struct {
	IdempotencyKey         string           `json:"idempotency_key,omitempty"`
	IncludePlayerIDs       []string         `json:"include_player_ids,omitempty"`
	IncludeExternalUserIDs []string         `json:"include_external_user_ids,omitempty"`
	IncludedSegments       []string         `json:"included_segments,omitempty"`
	Filters                []map[string]any `json:"filters,omitempty"`
	Title                  string           `json:"title,omitempty"`
	Body                   string           `json:"body,omitempty"`
	Data                   map[string]any   `json:"data,omitempty"`
}

type createNotificationResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (s *Server) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var req createNotificationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if len(req.IncludedSegments) > 0 || len(req.Filters) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "included_segments/filters (segmentation) are not yet supported — use include_player_ids or include_external_user_ids")
		return
	}
	hasPlayerIDs := len(req.IncludePlayerIDs) > 0
	hasExternalUserIDs := len(req.IncludeExternalUserIDs) > 0
	if hasPlayerIDs == hasExternalUserIDs {
		writeError(w, http.StatusUnprocessableEntity, "exactly one of include_player_ids or include_external_user_ids is required")
		return
	}
	if len(req.IncludePlayerIDs) > maxDeviceIDsPerRequest {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("include_player_ids must contain at most %d entries per request, got %d", maxDeviceIDsPerRequest, len(req.IncludePlayerIDs)))
		return
	}
	if len(req.IncludeExternalUserIDs) > maxDeviceIDsPerRequest {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("include_external_user_ids must contain at most %d entries per request, got %d", maxDeviceIDsPerRequest, len(req.IncludeExternalUserIDs)))
		return
	}
	if len(req.IdempotencyKey) > maxIdempotencyKeyLength {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("idempotency_key must be at most %d characters", maxIdempotencyKeyLength))
		return
	}
	if len(req.Title) > maxTitleLength {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("title must be at most %d characters", maxTitleLength))
		return
	}
	if len(req.Body) > maxBodyLength {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("body must be at most %d characters", maxBodyLength))
		return
	}
	if req.Data != nil {
		dataJSON, err := json.Marshal(req.Data)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "data must be valid JSON-serializable content")
			return
		}
		if len(dataJSON) > maxDataBytes {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("data must be at most %d bytes serialized", maxDataBytes))
			return
		}
	}

	deviceIDs := make([]uuid.UUID, 0, len(req.IncludePlayerIDs))
	for _, idStr := range req.IncludePlayerIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "include_player_ids must be valid UUIDs, got: "+idStr)
			return
		}
		deviceIDs = append(deviceIDs, id)
	}

	project, err := s.resolveProject(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve project")
		return
	}

	n, err := s.Dispatch.CreateNotification(r.Context(), postgres.UUIDTo(project.ID), dispatch.CreateNotificationRequest{
		IdempotencyKey:  req.IdempotencyKey,
		DeviceIDs:       deviceIDs,
		ExternalUserIDs: req.IncludeExternalUserIDs,
		Title:           req.Title,
		Body:            req.Body,
		Data:            req.Data,
	})
	if err != nil {
		if errors.Is(err, dispatch.ErrEmptyTargets) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.Logger.Error().Err(err).Msg("create notification failed")
		writeError(w, http.StatusInternalServerError, "failed to create notification")
		return
	}

	writeJSON(w, http.StatusAccepted, createNotificationResponse{
		ID:     postgres.UUIDTo(n.ID).String(),
		Status: n.Status,
	})
}

type notificationStatusResponse struct {
	ID              string           `json:"id"`
	Status          string           `json:"status"`
	TotalRecipients int32            `json:"total_recipients"`
	Counts          map[string]int64 `json:"counts"`
}

func (s *Server) handleGetNotification(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid notification id")
		return
	}

	project, err := s.resolveProject(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve project")
		return
	}

	n, err := s.DB.GetNotification(r.Context(), postgres.GetNotificationParams{
		ID:        postgres.UUIDFrom(id),
		ProjectID: project.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "notification not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch notification")
		return
	}

	counts, err := s.Dispatch.RecipientStatusCounts(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch recipient counts")
		return
	}

	writeJSON(w, http.StatusOK, notificationStatusResponse{
		ID:              postgres.UUIDTo(n.ID).String(),
		Status:          n.Status,
		TotalRecipients: n.TotalRecipients,
		Counts:          counts,
	})
}

type recipientResponse struct {
	ID                string  `json:"id"`
	DeviceID          string  `json:"device_id"`
	ProviderType      string  `json:"provider_type"`
	Status            string  `json:"status"`
	ProviderMessageID *string `json:"provider_message_id,omitempty"`
	ErrorCode         *string `json:"error_code,omitempty"`
	ErrorMessage      *string `json:"error_message,omitempty"`
	AttemptCount      int32   `json:"attempt_count"`
}

func (s *Server) handleListRecipients(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid notification id")
		return
	}

	recipients, err := s.DB.ListNotificationRecipients(r.Context(), postgres.UUIDFrom(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list recipients")
		return
	}

	out := make([]recipientResponse, 0, len(recipients))
	for _, rec := range recipients {
		out = append(out, recipientResponse{
			ID:                postgres.UUIDTo(rec.ID).String(),
			DeviceID:          postgres.UUIDTo(rec.DeviceID).String(),
			ProviderType:      rec.ProviderType,
			Status:            rec.Status,
			ProviderMessageID: rec.ProviderMessageID,
			ErrorCode:         rec.ErrorCode,
			ErrorMessage:      rec.ErrorMessage,
			AttemptCount:      rec.AttemptCount,
		})
	}

	writeJSON(w, http.StatusOK, out)
}
