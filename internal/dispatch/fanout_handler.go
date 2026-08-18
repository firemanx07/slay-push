package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
	"github.com/firemanx07/slay-push/internal/targeting"
)

// HandleFanout resolves a notification's audience and enqueues one send
// task per recipient. Runs in the worker, never in the HTTP request path.
func (h *Handlers) HandleFanout(ctx context.Context, payload queue.FanoutPayload) error {
	n, err := h.DB.GetNotification(ctx, postgres.GetNotificationParams{
		ID:        postgres.UUIDFrom(payload.NotificationID),
		ProjectID: postgres.UUIDFrom(payload.ProjectID),
	})
	if err != nil {
		return fmt.Errorf("fetch notification: %w", err)
	}

	var spec targetSpecJSON
	if err := json.Unmarshal(n.TargetSpec, &spec); err != nil {
		_ = h.DB.SetNotificationStatus(ctx, postgres.SetNotificationStatusParams{ID: n.ID, Status: "failed"})
		// target_spec is malformed; not retryable.
		return fmt.Errorf("invalid target_spec (not retryable): %w", err)
	}

	deviceIDs, err := h.Targeting.Resolve(ctx, payload.ProjectID, targeting.Spec{
		DeviceIDs:       spec.DeviceIDs,
		ExternalUserIDs: spec.ExternalUserIDs,
	})
	if err != nil {
		_ = h.DB.SetNotificationStatus(ctx, postgres.SetNotificationStatusParams{ID: n.ID, Status: "failed"})
		return fmt.Errorf("resolve targets (not retryable): %w", err)
	}
	if len(deviceIDs) == 0 {
		return h.DB.CompleteNotification(ctx, postgres.CompleteNotificationParams{ID: n.ID, Status: "completed"})
	}

	devices, err := h.DB.GetDevicesByIDs(ctx, postgres.GetDevicesByIDsParams{
		ProjectID: n.ProjectID,
		Ids:       postgres.UUIDsFrom(deviceIDs),
	})
	if err != nil {
		return fmt.Errorf("fetch devices: %w", err)
	}

	var data map[string]any
	if len(n.Data) > 0 {
		if err := json.Unmarshal(n.Data, &data); err != nil {
			return fmt.Errorf("unmarshal notification data: %w", err)
		}
	}

	var title, body string
	if n.Title != nil {
		title = *n.Title
	}
	if n.Body != nil {
		body = *n.Body
	}

	enqueued := 0
	for _, d := range devices {
		recipient, err := h.getOrCreateRecipient(ctx, n.ID, d.ID, d.ProviderType)
		if err != nil {
			return fmt.Errorf("insert recipient for device %s: %w", postgres.UUIDTo(d.ID), err)
		}

		if err := queue.EnqueueSend(h.Queue, queue.SendPayload{
			NotificationID: payload.NotificationID,
			RecipientID:    postgres.UUIDTo(recipient.ID),
			DeviceID:       postgres.UUIDTo(d.ID),
			ProjectID:      payload.ProjectID,
			ProviderType:   d.ProviderType,
			Token:          d.Token,
			Title:          title,
			Body:           body,
			Data:           data,
		}); err != nil {
			return fmt.Errorf("enqueue send for recipient %s: %w", postgres.UUIDTo(recipient.ID), err)
		}
		enqueued++
	}

	if err := h.DB.SetNotificationTotals(ctx, postgres.SetNotificationTotalsParams{
		ID: n.ID, TotalRecipients: int32(enqueued),
	}); err != nil {
		return fmt.Errorf("set notification totals: %w", err)
	}
	return h.DB.SetNotificationStatus(ctx, postgres.SetNotificationStatusParams{ID: n.ID, Status: "processing"})
}

// getOrCreateRecipient inserts a recipient row, or fetches the existing one
// on a (notification_id, device_id) conflict.
func (h *Handlers) getOrCreateRecipient(ctx context.Context, notificationID, deviceID pgtype.UUID, providerType string) (postgres.NotificationRecipient, error) {
	recipient, err := h.DB.InsertNotificationRecipient(ctx, postgres.InsertNotificationRecipientParams{
		NotificationID: notificationID,
		DeviceID:       deviceID,
		ProviderType:   providerType,
	})
	if err == nil {
		return recipient, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return postgres.NotificationRecipient{}, err
	}

	return h.DB.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: notificationID,
		DeviceID:       deviceID,
	})
}
