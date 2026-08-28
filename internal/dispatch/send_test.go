package dispatch

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

func TestHandleSend_InvalidToken(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)
	testAdapter.Program(token, fakeStep{result: provider.SendResult{Status: provider.StatusInvalidToken}})

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{
		DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)},
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}
	recipient, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: n.ID,
		DeviceID:       device.ID,
	})
	if err != nil {
		t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
	}
	cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(recipient.ID))

	if err := h.handlers.HandleSend(ctx, queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID),
		RecipientID:    postgres.UUIDTo(recipient.ID),
		DeviceID:       postgres.UUIDTo(device.ID),
		ProjectID:      projectID,
		ProviderType:   "expo",
		Token:          token,
	}); err != nil {
		t.Fatalf("HandleSend: %v", err)
	}

	t.Run("recipient marked failed with invalid_token", func(t *testing.T) {
		got, err := h.queries.GetNotificationRecipient(ctx, recipient.ID)
		if err != nil {
			t.Fatalf("GetNotificationRecipient: %v", err)
		}
		if got.Status != "failed" {
			t.Errorf("recipient status = %q, want %q", got.Status, "failed")
		}
		if got.ErrorCode == nil || *got.ErrorCode != "invalid_token" {
			t.Errorf("recipient error_code = %v, want %q", got.ErrorCode, "invalid_token")
		}
	})

	t.Run("device marked invalid", func(t *testing.T) {
		devices, err := h.queries.GetDevicesByIDs(ctx, postgres.GetDevicesByIDsParams{
			ProjectID: postgres.UUIDFrom(projectID),
			Ids:       []pgtype.UUID{device.ID},
		})
		if err != nil {
			t.Fatalf("GetDevicesByIDs: %v", err)
		}
		if len(devices) != 1 {
			t.Fatalf("GetDevicesByIDs returned %d devices, want 1", len(devices))
		}
		if devices[0].Status != "invalid" {
			t.Errorf("device status = %q, want %q", devices[0].Status, "invalid")
		}
	})
}
