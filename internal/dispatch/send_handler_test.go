package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

func TestHandleSend_RecipientNotFound(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	err := h.handlers.HandleSend(ctx, queue.SendPayload{
		RecipientID: uuid.New(), ProjectID: projectID, ProviderType: "expo", Token: "irrelevant",
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent recipient")
	}
}

func TestHandleSend_TerminalStatusIsNoOp(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)}})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}
	recipient, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: n.ID, DeviceID: device.ID,
	})
	if err != nil {
		t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
	}
	cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(recipient.ID))

	if err := h.queries.MarkRecipientSent(ctx, postgres.MarkRecipientSentParams{ID: recipient.ID}); err != nil {
		t.Fatalf("MarkRecipientSent: %v", err)
	}

	before := testAdapter.CallCount(token)
	if err := h.handlers.HandleSend(ctx, queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID), RecipientID: postgres.UUIDTo(recipient.ID),
		DeviceID: postgres.UUIDTo(device.ID), ProjectID: projectID, ProviderType: "expo", Token: token,
	}); err != nil {
		t.Fatalf("HandleSend on an already-terminal recipient: %v", err)
	}
	if got := testAdapter.CallCount(token); got != before {
		t.Errorf("adapter was called %d time(s) for an already-terminal recipient, want %d (no-op)", got, before)
	}
}

func TestHandleSend_NoActiveCredential(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	// Deliberately no mustCreateCredential call — no active credential row.

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)}})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}
	recipient, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: n.ID, DeviceID: device.ID,
	})
	if err != nil {
		t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
	}
	cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(recipient.ID))

	err = h.handlers.HandleSend(ctx, queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID), RecipientID: postgres.UUIDTo(recipient.ID),
		DeviceID: postgres.UUIDTo(device.ID), ProjectID: projectID, ProviderType: "expo", Token: token,
	})
	if err == nil {
		t.Fatal("expected an error when no active credential exists")
	}

	got, err := h.queries.GetNotificationRecipient(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetNotificationRecipient: %v", err)
	}
	if got.Status != "failed" || got.ErrorCode == nil || *got.ErrorCode != "no_credential" {
		t.Errorf("got status=%q error_code=%v, want failed/no_credential", got.Status, got.ErrorCode)
	}
}

func TestHandleSend_CredentialDecryptFailure(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)

	// Seal with a *different* master key than h.handlers.Crypto uses — the
	// stored credential can never decrypt under the handler's key.
	wrongKey := testMasterKey(t)
	wrappedDEK, ciphertext, err := wrongKey.Seal([]byte(`{"access_token":"fake"}`))
	if err != nil {
		t.Fatalf("seal with wrong key: %v", err)
	}
	if _, err := h.queries.UpsertProviderCredential(ctx, postgres.UpsertProviderCredentialParams{
		ProjectID: postgres.UUIDFrom(projectID), ProviderType: "expo", Environment: "production",
		Credential: ciphertext, WrappedDek: wrappedDEK,
	}); err != nil {
		t.Fatalf("create test credential: %v", err)
	}

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)}})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}
	recipient, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: n.ID, DeviceID: device.ID,
	})
	if err != nil {
		t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
	}
	cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(recipient.ID))

	err = h.handlers.HandleSend(ctx, queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID), RecipientID: postgres.UUIDTo(recipient.ID),
		DeviceID: postgres.UUIDTo(device.ID), ProjectID: projectID, ProviderType: "expo", Token: token,
	})
	if err == nil {
		t.Fatal("expected a decrypt error")
	}

	got, err := h.queries.GetNotificationRecipient(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetNotificationRecipient: %v", err)
	}
	if got.Status != "failed" || got.ErrorCode == nil || *got.ErrorCode != "credential_decrypt_failed" {
		t.Errorf("got status=%q error_code=%v, want failed/credential_decrypt_failed", got.Status, got.ErrorCode)
	}
}

func TestHandleSend_UnknownProvider(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	// "fcm" is a valid provider_type per the devices table's check
	// constraint, but this package's test binary never imports the real
	// internal/provider/fcm (only the fake "expo" adapter registers
	// itself here) — so it's genuinely unregistered in provider.Get.
	const fakeProviderType = "fcm"
	token := "unknown-provider-tok-" + uuid.NewString()
	device, err := h.queries.UpsertDevice(ctx, postgres.UpsertDeviceParams{
		ProjectID: postgres.UUIDFrom(projectID), Token: token, Platform: "android",
		ProviderType: fakeProviderType, Metadata: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create test device: %v", err)
	}

	wrappedDEK, ciphertext, err := h.handlers.Crypto.Seal([]byte(`{}`))
	if err != nil {
		t.Fatalf("seal test credential: %v", err)
	}
	if _, err := h.queries.UpsertProviderCredential(ctx, postgres.UpsertProviderCredentialParams{
		ProjectID: postgres.UUIDFrom(projectID), ProviderType: fakeProviderType, Environment: "production",
		Credential: ciphertext, WrappedDek: wrappedDEK,
	}); err != nil {
		t.Fatalf("create test credential: %v", err)
	}

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)}})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}
	recipient, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: n.ID, DeviceID: device.ID,
	})
	if err != nil {
		t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
	}
	// HandleFanout above enqueued a send:fcm task (cleanupSendTask only
	// covers send:expo) — the direct HandleSend call below never consumes
	// it, so it'd otherwise sit in shared Redis and could be picked up by
	// a real worker later, retrying against this test's rolled-back data.
	t.Cleanup(func() {
		inspector := asynq.NewInspector(h.redisOpt)
		defer func() { _ = inspector.Close() }()
		_ = inspector.DeleteTask(queue.SendTypeFor(fakeProviderType), postgres.UUIDTo(recipient.ID).String())
	})

	err = h.handlers.HandleSend(ctx, queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID), RecipientID: postgres.UUIDTo(recipient.ID),
		DeviceID: postgres.UUIDTo(device.ID), ProjectID: projectID, ProviderType: fakeProviderType, Token: token,
	})
	if err == nil {
		t.Fatal("expected an error for an unregistered provider type")
	}

	got, err := h.queries.GetNotificationRecipient(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetNotificationRecipient: %v", err)
	}
	if got.Status != "failed" || got.ErrorCode == nil || *got.ErrorCode != "unknown_provider" {
		t.Errorf("got status=%q error_code=%v, want failed/unknown_provider", got.Status, got.ErrorCode)
	}
}

func TestHandleSend_Throttled(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)
	testAdapter.Program(token, fakeStep{result: provider.SendResult{Status: provider.StatusThrottled, RetryAfter: 42 * time.Second}})

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)}})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}
	recipient, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{
		NotificationID: n.ID, DeviceID: device.ID,
	})
	if err != nil {
		t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
	}
	cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(recipient.ID))

	err = h.handlers.HandleSend(ctx, queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID), RecipientID: postgres.UUIDTo(recipient.ID),
		DeviceID: postgres.UUIDTo(device.ID), ProjectID: projectID, ProviderType: "expo", Token: token,
	})
	if err == nil {
		t.Fatal("expected a *queue.ThrottledError")
	}
	var te *queue.ThrottledError
	if !errors.As(err, &te) {
		t.Fatalf("error type = %T, want *queue.ThrottledError", err)
	}
	if te.RetryAfter != 42*time.Second {
		t.Errorf("RetryAfter = %v, want %v", te.RetryAfter, 42*time.Second)
	}

	// Throttled is retryable — the recipient must NOT be marked terminal.
	got, err := h.queries.GetNotificationRecipient(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetNotificationRecipient: %v", err)
	}
	if terminalRecipientStatuses[got.Status] {
		t.Errorf("recipient status = %q, want a non-terminal status after a throttled attempt", got.Status)
	}
}
