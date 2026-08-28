package dispatch

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

func TestHandleFanout_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)
	testAdapter.Program(token, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "test-msg-1"}})

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{
		DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)},
		Title:     "hello",
		Body:      "world",
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
	inspector := asynq.NewInspector(h.redisOpt)
	t.Cleanup(func() { _ = inspector.DeleteTask(queue.SendTypeFor("expo"), postgres.UUIDTo(recipient.ID).String()) })

	if err := h.handlers.HandleSend(ctx, queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID),
		RecipientID:    postgres.UUIDTo(recipient.ID),
		DeviceID:       postgres.UUIDTo(device.ID),
		ProjectID:      projectID,
		ProviderType:   "expo",
		Token:          token,
		Title:          "hello",
		Body:           "world",
	}); err != nil {
		t.Fatalf("HandleSend: %v", err)
	}

	t.Run("recipient reaches sent", func(t *testing.T) {
		got, err := h.queries.GetNotificationRecipient(ctx, recipient.ID)
		if err != nil {
			t.Fatalf("GetNotificationRecipient: %v", err)
		}
		if got.Status != "sent" {
			t.Errorf("recipient status = %q, want %q", got.Status, "sent")
		}
	})

	t.Run("notification status stays processing after all recipients terminal", func(t *testing.T) {
		got, err := h.queries.GetNotification(ctx, postgres.GetNotificationParams{ID: n.ID, ProjectID: postgres.UUIDFrom(projectID)})
		if err != nil {
			t.Fatalf("GetNotification: %v", err)
		}
		if got.Status != "processing" {
			t.Errorf("notification status = %q, want %q (nothing transitions it to completed on the non-zero-recipient path — if this now fails, that behavior changed and this assertion should be updated deliberately)", got.Status, "processing")
		}
	})
}

func TestHandleFanout_Idempotent(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)
	testAdapter.Program(token, fakeStep{result: provider.SendResult{Status: provider.StatusSent}})

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{
		DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)},
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	payload := queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}
	if err := h.handlers.HandleFanout(ctx, payload); err != nil {
		t.Fatalf("first HandleFanout: %v", err)
	}
	recipient, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: n.ID,
		DeviceID:       device.ID,
	})
	if err != nil {
		t.Fatalf("GetRecipientByNotificationAndDevice after first fanout: %v", err)
	}
	t.Cleanup(func() {
		_ = asynq.NewInspector(h.redisOpt).DeleteTask(queue.SendTypeFor("expo"), postgres.UUIDTo(recipient.ID).String())
	})

	// Re-running fanout for the same notification must not insert a second
	// recipient row for the same device.
	if err := h.handlers.HandleFanout(ctx, payload); err != nil {
		t.Fatalf("second HandleFanout: %v", err)
	}

	counts, err := h.handlers.RecipientStatusCounts(ctx, postgres.UUIDTo(n.ID))
	if err != nil {
		t.Fatalf("RecipientStatusCounts: %v", err)
	}
	var total int64
	for _, c := range counts {
		total += c
	}
	if total != 1 {
		t.Errorf("total recipient rows after two fanouts = %d, want 1", total)
	}
}
