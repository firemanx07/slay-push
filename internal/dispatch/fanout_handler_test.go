package dispatch

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
	"github.com/firemanx07/slay-push/internal/targeting"
)

func TestHandleFanout_NotificationNotFound(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: uuid.New(), ProjectID: projectID})
	if err == nil {
		t.Fatal("expected an error for a nonexistent notification")
	}
}

func TestHandleFanout_InvalidTargetSpec(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	// target_spec is a jsonb column, so Postgres itself rejects invalid
	// JSON syntax at insert time — this needs valid JSON that fails to
	// unmarshal into targetSpecJSON's shape instead (a non-UUID string
	// where a device id is expected).
	n, err := h.queries.CreateNotification(ctx, postgres.CreateNotificationParams{
		ProjectID:  postgres.UUIDFrom(projectID),
		Data:       []byte(`{}`),
		TargetSpec: []byte(`{"device_ids":["not-a-uuid"]}`),
	})
	if err != nil {
		t.Fatalf("create test notification: %v", err)
	}

	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err == nil {
		t.Fatal("expected an error for a target_spec with a malformed device id")
	}

	got, err := h.queries.GetNotification(ctx, postgres.GetNotificationParams{ID: n.ID, ProjectID: postgres.UUIDFrom(projectID)})
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("notification status = %q, want %q", got.Status, "failed")
	}
}

func TestHandleFanout_EmptyTargetSpec_MarksFailed(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	n, err := h.queries.CreateNotification(ctx, postgres.CreateNotificationParams{
		ProjectID:  postgres.UUIDFrom(projectID),
		Data:       []byte(`{}`),
		TargetSpec: []byte(`{"device_ids":[],"external_user_ids":[]}`),
	})
	if err != nil {
		t.Fatalf("create test notification: %v", err)
	}

	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err == nil {
		t.Fatal("expected an error for an empty target_spec (targeting.ErrNoTargetsSpecified)")
	}

	got, err := h.queries.GetNotification(ctx, postgres.GetNotificationParams{ID: n.ID, ProjectID: postgres.UUIDFrom(projectID)})
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("notification status = %q, want %q", got.Status, "failed")
	}
}

func TestHandleFanout_NoDevicesResolved_CompletesNotification(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	// "ghost-user" matches no subscriber at all — ByGroup.Resolve returns
	// an empty, non-error result in that case (distinct from
	// ErrNoTargetsSpecified, which needs both fields empty).
	n, err := h.queries.CreateNotification(ctx, postgres.CreateNotificationParams{
		ProjectID:  postgres.UUIDFrom(projectID),
		Data:       []byte(`{}`),
		TargetSpec: []byte(`{"external_user_ids":["ghost-user"]}`),
	})
	if err != nil {
		t.Fatalf("create test notification: %v", err)
	}

	if err := h.handlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err != nil {
		t.Fatalf("HandleFanout: %v", err)
	}

	got, err := h.queries.GetNotification(ctx, postgres.GetNotificationParams{ID: n.ID, ProjectID: postgres.UUIDFrom(projectID)})
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("notification status = %q, want %q", got.Status, "completed")
	}
}

func TestHandleFanout_EnqueueError(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{
		DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)},
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	brokenClient := asynq.NewClient(h.redisOpt)
	_ = brokenClient.Close()
	brokenHandlers := &Handlers{
		DB:        h.queries,
		Queue:     brokenClient,
		Targeting: targeting.NewRegistry(h.queries),
		Crypto:    h.handlers.Crypto,
		Logger:    zerolog.Nop(),
	}

	if err := brokenHandlers.HandleFanout(ctx, queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}); err == nil {
		t.Fatal("expected an error when the queue client is closed")
	}
}
