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

type createNotificationRequest struct {
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	DeviceIDs      []string       `json:"device_ids"`
	Title          string         `json:"title,omitempty"`
	Body           string         `json:"body,omitempty"`
	Data           map[string]any `json:"data,omitempty"`
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
	if len(req.DeviceIDs) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "device_ids must be non-empty (Phase 1: no group/segment targeting yet)")
		return
	}
	if len(req.DeviceIDs) > maxDeviceIDsPerRequest {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("device_ids must contain at most %d entries per request, got %d", maxDeviceIDsPerRequest, len(req.DeviceIDs)))
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

	deviceIDs := make([]uuid.UUID, 0, len(req.DeviceIDs))
	for _, idStr := range req.DeviceIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "device_ids must be valid UUIDs, got: "+idStr)
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
		IdempotencyKey: req.IdempotencyKey,
		DeviceIDs:      deviceIDs,
		Title:          req.Title,
		Body:           req.Body,
		Data:           req.Data,
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
