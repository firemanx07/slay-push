package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// CreateNotificationRequest: exactly one of DeviceIDs (explicit targets) or
// ExternalUserIDs (group push, every active device under each subscriber)
// must be set.
type CreateNotificationRequest struct {
	IdempotencyKey  string         `json:"idempotency_key,omitempty"`
	DeviceIDs       []uuid.UUID    `json:"device_ids"`
	ExternalUserIDs []string       `json:"external_user_ids"`
	Title           string         `json:"title,omitempty"`
	Body            string         `json:"body,omitempty"`
	Data            map[string]any `json:"data,omitempty"`
}

// targetSpecJSON is persisted into notifications.target_spec and re-parsed
// by the fanout handler.
type targetSpecJSON struct {
	DeviceIDs       []uuid.UUID `json:"device_ids"`
	ExternalUserIDs []string    `json:"external_user_ids"`
}

var ErrEmptyTargets = errors.New("dispatch: request specifies no targets")

// CreateNotification persists the notification (status=pending) and
// enqueues one fanout job. Audience resolution happens only in the fanout
// handler, in the worker.
func (h *Handlers) CreateNotification(ctx context.Context, projectID uuid.UUID, req CreateNotificationRequest) (postgres.Notification, error) {
	if len(req.DeviceIDs) == 0 && len(req.ExternalUserIDs) == 0 {
		return postgres.Notification{}, ErrEmptyTargets
	}

	if req.IdempotencyKey != "" {
		existing, err := h.DB.GetNotificationByIdempotencyKey(ctx, postgres.GetNotificationByIdempotencyKeyParams{
			ProjectID:      postgres.UUIDFrom(projectID),
			IdempotencyKey: postgres.TextFrom(req.IdempotencyKey),
		})
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return postgres.Notification{}, fmt.Errorf("check idempotency key: %w", err)
		}
	}

	dataJSON, err := json.Marshal(req.Data)
	if err != nil {
		return postgres.Notification{}, fmt.Errorf("marshal data: %w", err)
	}
	targetSpec, err := json.Marshal(targetSpecJSON{DeviceIDs: req.DeviceIDs, ExternalUserIDs: req.ExternalUserIDs})
	if err != nil {
		return postgres.Notification{}, fmt.Errorf("marshal target_spec: %w", err)
	}

	n, err := h.DB.CreateNotification(ctx, postgres.CreateNotificationParams{
		ProjectID:      postgres.UUIDFrom(projectID),
		IdempotencyKey: postgres.TextFrom(req.IdempotencyKey),
		Title:          postgres.TextFrom(req.Title),
		Body:           postgres.TextFrom(req.Body),
		Data:           dataJSON,
		TargetSpec:     targetSpec,
	})
	if err != nil {
		return postgres.Notification{}, fmt.Errorf("create notification: %w", err)
	}

	if err := queue.EnqueueFanout(h.Queue, queue.FanoutPayload{
		NotificationID: postgres.UUIDTo(n.ID),
		ProjectID:      projectID,
	}); err != nil {
		return postgres.Notification{}, fmt.Errorf("enqueue fanout: %w", err)
	}

	return n, nil
}

// RecipientStatusCounts aggregates a notification's per-recipient statuses,
// e.g. for the GET /notifications/{id} status endpoint.
func (h *Handlers) RecipientStatusCounts(ctx context.Context, notificationID uuid.UUID) (map[string]int64, error) {
	rows, err := h.DB.CountRecipientStatuses(ctx, postgres.UUIDFrom(notificationID))
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.Status] = r.Count
	}
	return counts, nil
}
