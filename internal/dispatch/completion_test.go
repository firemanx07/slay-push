package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

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

var errInjected = errors.New("injected database failure")

// faultyDB passes every statement through to its DBTX except the sqlc
// queries named in failQueries, whose Exec fails with errInjected.
type faultyDB struct {
	postgres.DBTX
	failQueries []string
}

func (f faultyDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	for _, name := range f.failQueries {
		if strings.HasPrefix(sql, "-- name: "+name+" ") {
			return pgconn.CommandTag{}, errInjected
		}
	}
	return f.DBTX.Exec(ctx, sql, args...)
}

// handlersFailing returns a copy of h's Handlers whose database fails the
// named sqlc queries.
func handlersFailing(h *testHarness, queries ...string) *Handlers {
	fh := *h.handlers
	fh.DB = postgres.New(faultyDB{DBTX: h.pool, failQueries: queries})
	return &fh
}

const completeQuery = "CompleteNotificationIfAllRecipientsTerminal"

func recipientStatus(t *testing.T, ctx context.Context, h *testHarness, id uuid.UUID) string {
	t.Helper()
	r, err := h.queries.GetNotificationRecipient(ctx, postgres.UUIDFrom(id))
	if err != nil {
		t.Fatalf("GetNotificationRecipient: %v", err)
	}
	return r.Status
}

func TestCompletion_SentPath_ErrorIsReturnedAndRetryCompletes(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 1, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "m"}})
	p := f.payloads[0]

	err := handlersFailing(h, completeQuery).HandleSend(ctx, p)
	if !errors.Is(err, errInjected) {
		t.Fatalf("HandleSend error = %v, want the injected completion failure", err)
	}
	if got := recipientStatus(t, ctx, h, p.RecipientID); got != "sent" {
		t.Errorf("recipient status = %q, want %q", got, "sent")
	}
	if got := notificationStatus(t, ctx, h, f.notification); got.Status != "processing" {
		t.Errorf("notification status = %q, want %q", got.Status, "processing")
	}

	if err := h.handlers.HandleSend(ctx, p); err != nil {
		t.Fatalf("retried HandleSend: %v", err)
	}
	if got := notificationStatus(t, ctx, h, f.notification); got.Status != "completed" {
		t.Errorf("notification status after retry = %q, want %q", got.Status, "completed")
	}
	if calls := testAdapter.CallCount(p.Token); calls != 1 {
		t.Errorf("provider calls = %d, want 1 (the retry must not send again)", calls)
	}
}

func TestCompletion_InvalidTokenPath_ErrorIsReturnedAndRetryCompletes(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 1, fakeStep{result: provider.SendResult{Status: provider.StatusInvalidToken}})
	p := f.payloads[0]

	err := handlersFailing(h, completeQuery).HandleSend(ctx, p)
	if !errors.Is(err, errInjected) {
		t.Fatalf("HandleSend error = %v, want the injected completion failure", err)
	}
	if got := recipientStatus(t, ctx, h, p.RecipientID); got != "failed" {
		t.Errorf("recipient status = %q, want %q", got, "failed")
	}

	if err := h.handlers.HandleSend(ctx, p); err != nil {
		t.Fatalf("retried HandleSend: %v", err)
	}
	if got := notificationStatus(t, ctx, h, f.notification); got.Status != "completed" {
		t.Errorf("notification status after retry = %q, want %q", got.Status, "completed")
	}
	if calls := testAdapter.CallCount(p.Token); calls != 1 {
		t.Errorf("provider calls = %d, want 1", calls)
	}
}

func TestCompletion_MarkSentFailureIsReturned(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 1, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "m"}})
	p := f.payloads[0]

	if err := handlersFailing(h, "MarkRecipientSent").HandleSend(ctx, p); !errors.Is(err, errInjected) {
		t.Fatalf("HandleSend error = %v, want the injected failure", err)
	}
	if got := recipientStatus(t, ctx, h, p.RecipientID); got != "sending" {
		t.Errorf("recipient status = %q, want %q", got, "sending")
	}
}

func TestCompletion_MarkFailedFailureIsReturnedAndRetryCompletes(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 1, fakeStep{result: provider.SendResult{Status: provider.StatusInvalidToken}})
	p := f.payloads[0]

	if err := handlersFailing(h, "MarkRecipientFailed").HandleSend(ctx, p); !errors.Is(err, errInjected) {
		t.Fatalf("HandleSend error = %v, want the injected failure", err)
	}
	if got := recipientStatus(t, ctx, h, p.RecipientID); got != "sending" {
		t.Errorf("recipient status = %q, want %q", got, "sending")
	}

	if err := h.handlers.HandleSend(ctx, p); err != nil {
		t.Fatalf("retried HandleSend: %v", err)
	}
	if got := notificationStatus(t, ctx, h, f.notification); got.Status != "completed" {
		t.Errorf("notification status after retry = %q, want %q", got.Status, "completed")
	}
}

func TestCompletion_TerminalRecipientRetryReturnsCompletionError(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	f := newCompletionFixture(t, ctx, h, 1, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "m"}})
	p := f.payloads[0]

	if err := h.handlers.HandleSend(ctx, p); err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `update notifications set status = 'processing', completed_at = null where id = $1`, f.notification.ID); err != nil {
		t.Fatalf("simulate a lost completion: %v", err)
	}

	if err := handlersFailing(h, completeQuery).HandleSend(ctx, p); !errors.Is(err, errInjected) {
		t.Fatalf("HandleSend error = %v, want the injected completion failure", err)
	}
	if calls := testAdapter.CallCount(p.Token); calls != 1 {
		t.Errorf("provider calls = %d, want 1", calls)
	}
}

func TestCompletion_FanoutTailErrorsAreReturned(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"completion fails", completeQuery},
		{"status update fails", "SetNotificationStatus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveHarness(t)
			ctx := context.Background()
			projectID := postgres.UUIDTo(h.project.ID)

			device := mustCreateDevice(t, ctx, h.queries, projectID, "expo-tok-"+uuid.NewString())
			n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: []uuid.UUID{postgres.UUIDTo(device.ID)}})
			if err != nil {
				t.Fatalf("CreateNotification: %v", err)
			}
			r, err := h.queries.InsertNotificationRecipient(ctx, postgres.InsertNotificationRecipientParams{
				NotificationID: n.ID, DeviceID: device.ID, ProviderType: "expo",
			})
			if err != nil {
				t.Fatalf("insert recipient: %v", err)
			}
			cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(r.ID))
			if err := h.queries.MarkRecipientSent(ctx, postgres.MarkRecipientSentParams{ID: r.ID, ProviderMessageID: postgres.TextFrom("early")}); err != nil {
				t.Fatalf("mark recipient sent: %v", err)
			}

			payload := queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}
			if err := handlersFailing(h, tc.query).HandleFanout(ctx, payload); !errors.Is(err, errInjected) {
				t.Fatalf("HandleFanout error = %v, want the injected failure", err)
			}
			if err := h.handlers.HandleFanout(ctx, payload); err != nil {
				t.Fatalf("retried HandleFanout: %v", err)
			}
			if got := notificationStatus(t, ctx, h, n); got.Status != "completed" {
				t.Errorf("notification status after retry = %q, want %q", got.Status, "completed")
			}
		})
	}
}

func TestFanoutRetry_GroupTargetWithInvalidatedDevice_StillCompletes(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	sub, err := h.queries.UpsertSubscriber(ctx, postgres.UpsertSubscriberParams{ProjectID: postgres.UUIDFrom(projectID), ExternalID: "user-" + uuid.NewString()})
	if err != nil {
		t.Fatalf("UpsertSubscriber: %v", err)
	}
	var devices []postgres.Device
	for range 2 {
		d, err := h.queries.UpsertDevice(ctx, postgres.UpsertDeviceParams{
			ProjectID:    postgres.UUIDFrom(projectID),
			Token:        "expo-tok-" + uuid.NewString(),
			Platform:     "android",
			ProviderType: "expo",
			Metadata:     []byte(`{}`),
			SubscriberID: sub.ID,
		})
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		devices = append(devices, d)
	}

	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{ExternalUserIDs: []string{sub.ExternalID}})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	payload := queue.FanoutPayload{NotificationID: postgres.UUIDTo(n.ID), ProjectID: projectID}
	if err := h.handlers.HandleFanout(ctx, payload); err != nil {
		t.Fatalf("first HandleFanout: %v", err)
	}

	if err := h.queries.MarkDeviceStatus(ctx, postgres.MarkDeviceStatusParams{ID: devices[0].ID, Status: "invalid"}); err != nil {
		t.Fatalf("MarkDeviceStatus: %v", err)
	}
	if err := h.handlers.HandleFanout(ctx, payload); err != nil {
		t.Fatalf("retried HandleFanout: %v", err)
	}

	got := notificationStatus(t, ctx, h, n)
	if got.TotalRecipients != 2 {
		t.Errorf("total_recipients = %d, want 2 (both persisted recipient rows)", got.TotalRecipients)
	}
	for _, d := range devices {
		r, err := h.queries.GetRecipientByNotificationAndDevice(ctx, postgres.GetRecipientByNotificationAndDeviceParams{NotificationID: n.ID, DeviceID: d.ID})
		if err != nil {
			t.Fatalf("GetRecipientByNotificationAndDevice: %v", err)
		}
		cleanupSendTask(t, h.redisOpt, postgres.UUIDTo(r.ID))
		if err := h.queries.MarkRecipientSent(ctx, postgres.MarkRecipientSentParams{ID: r.ID, ProviderMessageID: postgres.TextFrom("m")}); err != nil {
			t.Fatalf("MarkRecipientSent: %v", err)
		}
	}
	if err := h.handlers.completeIfDone(ctx, postgres.UUIDTo(n.ID)); err != nil {
		t.Fatalf("completeIfDone: %v", err)
	}
	if got := notificationStatus(t, ctx, h, n); got.Status != "completed" {
		t.Errorf("notification status = %q, want %q", got.Status, "completed")
	}
}
