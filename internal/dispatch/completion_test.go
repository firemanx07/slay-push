package dispatch

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// completionFixture is a notification fanned out to n devices, with each
// recipient's send payload ready to hand to HandleSend.
type completionFixture struct {
	notification postgres.Notification
	payloads     []queue.SendPayload
}

func newCompletionFixture(t *testing.T, ctx context.Context, h *testHarness, n int, step fakeStep) completionFixture {
	t.Helper()
	projectID := postgres.UUIDTo(h.project.ID)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	deviceIDs := make([]uuid.UUID, n)
	tokens := make([]string, n)
	for i := range n {
		tokens[i] = "expo-tok-" + uuid.NewString()
		d := mustCreateDevice(t, ctx, h.queries, projectID, tokens[i])
		testAdapter.Program(tokens[i], step)
		deviceIDs[i] = postgres.UUIDTo(d.ID)
	}

	notification, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: deviceIDs, Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(notification.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}

	payloads := make([]queue.SendPayload, n)
	for i := range n {
		r, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
			NotificationID: notification.ID,
			DeviceID:       postgres.UUIDFrom(deviceIDs[i]),
		})
		if err != nil {
			t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
		}
		cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(r.ID))
		payloads[i] = queue.SendPayload{
			NotificationID: postgres.UUIDTo(notification.ID),
			RecipientID:    postgres.UUIDTo(r.ID),
			DeviceID:       deviceIDs[i],
			ProjectID:      projectID,
			ProviderType:   "expo",
			Token:          tokens[i],
		}
	}
	return completionFixture{notification: notification, payloads: payloads}
}

func notificationStatus(t *testing.T, ctx context.Context, h *testHarness, id postgres.Notification) postgres.Notification {
	t.Helper()
	got, err := h.queries.GetNotification(ctx, postgres.GetNotificationParams{ID: id.ID, ProjectID: id.ProjectID})
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	return got
}

func TestCompletion_OnlyAfterLastRecipientIsTerminal(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 3, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "m"}})

	for i, p := range f.payloads {
		if got := notificationStatus(t, ctx, h, f.notification); got.Status != "processing" {
			t.Fatalf("before send %d: status = %q, want %q", i, got.Status, "processing")
		}
		if err := h.handlers.HandleSend(ctx, p); err != nil {
			t.Fatalf("HandleSend %d: %v", i, err)
		}
	}

	got := notificationStatus(t, ctx, h, f.notification)
	if got.Status != "completed" {
		t.Errorf("status = %q, want %q", got.Status, "completed")
	}
	if !got.CompletedAt.Valid {
		t.Error("completed_at is unset, want set")
	}
}

func TestCompletion_FailedRecipientsCountAsTerminal(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 2, fakeStep{result: provider.SendResult{Status: provider.StatusInvalidToken}})

	for i, p := range f.payloads {
		if err := h.handlers.HandleSend(ctx, p); err != nil {
			t.Fatalf("HandleSend %d: %v", i, err)
		}
	}

	if got := notificationStatus(t, ctx, h, f.notification); got.Status != "completed" {
		t.Errorf("status = %q, want %q", got.Status, "completed")
	}
}

func TestCompletion_RetriedTerminalSendCompletesNotification(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 1, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "m"}})

	if err := h.handlers.HandleSend(ctx, f.payloads[0]); err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `update notifications set status = 'processing', completed_at = null where id = $1`, f.notification.ID); err != nil {
		t.Fatalf("simulate a lost completion: %v", err)
	}
	if err := h.handlers.HandleSend(ctx, f.payloads[0]); err != nil {
		t.Fatalf("retried HandleSend: %v", err)
	}

	if got := notificationStatus(t, ctx, h, f.notification); got.Status != "completed" {
		t.Errorf("status = %q, want %q", got.Status, "completed")
	}
}

func TestCompletion_FanoutCompletesWhenSendsFinishedFirst(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	notification, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{
		DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)},
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	recipient, err := h.queries.InsertNotificationRecipient(ctx, postgres.InsertNotificationRecipientParams{
		NotificationID: notification.ID,
		DeviceID:       device.ID,
		ProviderType:   "expo",
	})
	if err != nil {
		t.Fatalf("insert recipient: %v", err)
	}
	cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(recipient.ID))
	if err := h.queries.MarkRecipientSent(ctx, postgres.MarkRecipientSentParams{ID: recipient.ID, ProviderMessageID: postgres.TextFrom("early")}); err != nil {
		t.Fatalf("mark recipient sent: %v", err)
	}

	if got := notificationStatus(t, ctx, h, notification); got.Status != "pending" {
		t.Fatalf("before fanout: status = %q, want %q", got.Status, "pending")
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(notification.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}

	if got := notificationStatus(t, ctx, h, notification); got.Status != "completed" {
		t.Errorf("status = %q, want %q", got.Status, "completed")
	}
}
