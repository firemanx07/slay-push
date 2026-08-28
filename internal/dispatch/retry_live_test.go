package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// TestLiveWorker_Retries exercises retry-count-aware behavior
// (isLastAttempt), which only works through real asynq task-execution
// machinery — calling HandleSend directly with a bare context.Background()
// never populates the retry count. One live asynq.Server backs both
// subtests; standing one up twice would buy nothing.
func TestLiveWorker_Retries(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.SendTypeFor("expo"), h.handlers.AsynqSendHandler)

	srv := asynq.NewServer(h.redisOpt, asynq.Config{
		Queues:         map[string]int{queue.SendTypeFor("expo"): 1},
		RetryDelayFunc: func(int, error, *asynq.Task) time.Duration { return 10 * time.Millisecond },
		// Both default to 1s/5s — real exponential backoff would make this
		// test take minutes. DelayedTaskCheckInterval controls how often
		// due retries get swept back into the active queue;
		// TaskCheckInterval controls how often the processor itself polls
		// a queue for new work. RetryDelayFunc alone controls neither.
		DelayedTaskCheckInterval: 20 * time.Millisecond,
		TaskCheckInterval:        20 * time.Millisecond,
		LogLevel:                 asynq.ErrorLevel,
	})
	if err := srv.Start(mux); err != nil {
		t.Fatalf("start live asynq server: %v", err)
	}
	t.Cleanup(srv.Shutdown)

	inspector := asynq.NewInspector(h.redisOpt)
	t.Cleanup(func() { _ = inspector.Close() })

	// enqueueRecipient creates a notification + recipient row directly
	// (skipping fanout, already covered by fanout_test.go) and enqueues its
	// send task through the real queue.
	enqueueRecipient := func(t *testing.T, token string) pgtype.UUID {
		t.Helper()
		n, err := h.queries.CreateNotification(ctx, postgres.CreateNotificationParams{
			ProjectID:  postgres.UUIDFrom(projectID),
			Data:       []byte(`{}`),
			TargetSpec: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("create notification fixture: %v", err)
		}
		device := mustCreateDevice(t, ctx, h.queries, projectID, token)
		recipient, err := h.queries.InsertNotificationRecipient(ctx, postgres.InsertNotificationRecipientParams{
			NotificationID: n.ID,
			DeviceID:       device.ID,
			ProviderType:   "expo",
		})
		if err != nil {
			t.Fatalf("insert recipient fixture: %v", err)
		}
		t.Cleanup(func() { _ = inspector.DeleteTask(queue.SendTypeFor("expo"), postgres.UUIDTo(recipient.ID).String()) })

		if err := queue.EnqueueSend(h.client, queue.SendPayload{
			NotificationID: postgres.UUIDTo(n.ID),
			RecipientID:    postgres.UUIDTo(recipient.ID),
			DeviceID:       postgres.UUIDTo(device.ID),
			ProjectID:      projectID,
			ProviderType:   "expo",
			Token:          token,
		}); err != nil {
			t.Fatalf("enqueue send: %v", err)
		}
		return recipient.ID
	}

	waitForTerminal := func(t *testing.T, recipientID pgtype.UUID) postgres.NotificationRecipient {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			r, err := h.queries.GetNotificationRecipient(ctx, recipientID)
			if err != nil {
				t.Fatalf("GetNotificationRecipient: %v", err)
			}
			if terminalRecipientStatuses[r.Status] {
				return r
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for recipient %s to reach a terminal status (last status=%s)",
					postgres.UUIDTo(recipientID), r.Status)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	t.Run("EventualSuccess", func(t *testing.T) {
		token := "expo-tok-" + uuid.NewString()
		testAdapter.Program(token,
			fakeStep{result: provider.SendResult{Status: provider.StatusTransientError}, err: nil},
			fakeStep{result: provider.SendResult{Status: provider.StatusTransientError}, err: nil},
			fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "eventual-success"}},
		)

		recipientID := enqueueRecipient(t, token)
		got := waitForTerminal(t, recipientID)

		if got.Status != "sent" {
			t.Errorf("recipient status = %q, want %q", got.Status, "sent")
		}
		if calls := testAdapter.CallCount(token); calls != 3 {
			t.Errorf("adapter call count = %d, want 3 (2 failures + 1 success)", calls)
		}
	})

	t.Run("EventualExhaustion", func(t *testing.T) {
		token := "expo-tok-" + uuid.NewString()
		testAdapter.Program(token, fakeStep{result: provider.SendResult{Status: provider.StatusTransientError}})

		recipientID := enqueueRecipient(t, token)
		got := waitForTerminal(t, recipientID)

		if got.Status != "failed" {
			t.Errorf("recipient status = %q, want %q", got.Status, "failed")
		}
		if got.ErrorCode == nil || *got.ErrorCode != "transient_error_exhausted" {
			t.Errorf("recipient error_code = %v, want %q", got.ErrorCode, "transient_error_exhausted")
		}
	})
}
