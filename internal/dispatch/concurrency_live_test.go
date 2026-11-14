package dispatch

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/firemanx07/slay-push/internal/platform"
	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const envLoadRecipients = "SLAYPUSH_LOAD_RECIPIENTS"

// loadRecipients returns the recipient count for load-shaped tests:
// SLAYPUSH_LOAD_RECIPIENTS when set, def otherwise.
func loadRecipients(t *testing.T, def int) int {
	t.Helper()
	raw := os.Getenv(envLoadRecipients)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", envLoadRecipients, raw)
	}
	return n
}

// startLiveWorker runs an asynq.Server on the send:expo queue, wired like
// cmd/server's worker (same retry delay function, same IsFailure, same send
// handler) with timing intervals shortened for tests. It does not consume
// the fanout queue, which other packages' tests inspect in the same Redis;
// tests run fanout themselves through fanoutNotification.
func startLiveWorker(t *testing.T, h *testHarness, concurrency int) {
	t.Helper()
	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.SendTypeFor("expo"), h.handlers.AsynqSendHandler)

	srv := asynq.NewServer(h.redisOpt, asynq.Config{
		Concurrency:              concurrency,
		Queues:                   map[string]int{queue.SendTypeFor("expo"): 10},
		RetryDelayFunc:           queue.RetryDelayFunc,
		IsFailure:                queue.IsFailure,
		DelayedTaskCheckInterval: 100 * time.Millisecond,
		TaskCheckInterval:        20 * time.Millisecond,
		LogLevel:                 asynq.ErrorLevel,
	})
	if err := srv.Start(mux); err != nil {
		t.Fatalf("start live asynq server: %v", err)
	}
	t.Cleanup(srv.Shutdown)
}

// fanoutNotification runs the fanout stage for n in the calling goroutine.
func fanoutNotification(ctx context.Context, h *testHarness, n postgres.Notification) error {
	return h.handlers.HandleFanout(ctx, queue.FanoutPayload{
		NotificationID: postgres.UUIDTo(n.ID),
		ProjectID:      postgres.UUIDTo(n.ProjectID),
	})
}

// seedDevices creates n expo devices under projectID, each programmed to
// send successfully, and returns their ids and tokens in matching order.
func seedDevices(t *testing.T, ctx context.Context, h *testHarness, projectID uuid.UUID, n int) ([]uuid.UUID, []string) {
	t.Helper()
	ids := make([]uuid.UUID, n)
	tokens := make([]string, n)
	for i := range n {
		token := "expo-tok-" + uuid.NewString()
		d := mustCreateDevice(t, ctx, h.queries, projectID, token)
		testAdapter.Program(token, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "msg-" + token}})
		ids[i] = postgres.UUIDTo(d.ID)
		tokens[i] = token
	}
	return ids, tokens
}

// recipientStatusCounts returns the per-status recipient counts across
// every notification under projectID.
func recipientStatusCounts(t *testing.T, ctx context.Context, h *testHarness, projectID uuid.UUID) map[string]int {
	t.Helper()
	rows, err := h.pool.Query(ctx,
		`select r.status, count(*) from notification_recipients r
		 join notifications n on n.id = r.notification_id
		 where n.project_id = $1 group by r.status`, projectID)
	if err != nil {
		t.Fatalf("count recipient statuses: %v", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var c int
		if err := rows.Scan(&status, &c); err != nil {
			t.Fatalf("scan recipient status count: %v", err)
		}
		counts[status] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recipient status counts: %v", err)
	}
	return counts
}

// waitForTerminalRecipients polls until want recipients under projectID are
// all in a terminal status, then returns the final counts. It fails the
// test with the last observed counts on timeout.
func waitForTerminalRecipients(t *testing.T, ctx context.Context, h *testHarness, projectID uuid.UUID, want int, timeout time.Duration) map[string]int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		counts := recipientStatusCounts(t, ctx, h, projectID)
		terminal, total := 0, 0
		for status, c := range counts {
			total += c
			if terminalRecipientStatuses[status] {
				terminal += c
			}
		}
		if terminal == want && total == want {
			return counts
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s: want %d terminal recipients, counts=%v", timeout, want, counts)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForCompletedNotifications polls for up to 10 seconds until every
// notification under projectID has status "completed", failing the test on
// timeout.
func waitForCompletedNotifications(t *testing.T, ctx context.Context, h *testHarness, projectID uuid.UUID) {
	t.Helper()
	const timeout = 10 * time.Second
	deadline := time.Now().Add(timeout)
	for {
		var open int
		if err := h.pool.QueryRow(ctx, `select count(*) from notifications where project_id = $1 and status <> 'completed'`, projectID).Scan(&open); err != nil {
			t.Fatalf("count open notifications: %v", err)
		}
		if open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s: %d notifications are not completed", timeout, open)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestLive_BurstDrainsExactlyOnce creates notifications from many
// goroutines at once, with fanout run concurrently and sends processed by a
// live worker pool, and asserts every recipient
// reaches "sent" with exactly one provider call each and every notification
// reaches "completed".
func TestLive_BurstDrainsExactlyOnce(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	total := loadRecipients(t, 300)
	const creators = 10
	ids, tokens := seedDevices(t, ctx, h, projectID, total)
	startLiveWorker(t, h, 20)

	start := time.Now()
	var wg sync.WaitGroup
	for c := range creators {
		lo, hi := c*total/creators, (c+1)*total/creators
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{
				DeviceIDs: ids[lo:hi],
				Title:     "burst",
				Body:      "burst",
			})
			if err != nil {
				t.Errorf("CreateNotification (creator %d): %v", c, err)
				return
			}
			if err := fanoutNotification(ctx, h, n); err != nil {
				t.Errorf("fanout (creator %d): %v", c, err)
			}
		}()
	}
	wg.Wait()

	counts := waitForTerminalRecipients(t, ctx, h, projectID, total, 60*time.Second)
	elapsed := time.Since(start)
	waitForCompletedNotifications(t, ctx, h, projectID)
	t.Logf("burst: %d recipients drained in %s (%.0f recipients/s), counts=%v",
		total, elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds(), counts)

	if counts["sent"] != total {
		t.Errorf("sent recipients = %d, want %d (counts=%v)", counts["sent"], total, counts)
	}
	for _, token := range tokens {
		if calls := testAdapter.CallCount(token); calls != 1 {
			t.Errorf("provider calls for %s = %d, want exactly 1", token, calls)
		}
	}
}

// TestLive_ThrottledBacklogDrainsWithoutFailures pushes more recipients than
// the outbound rate limit allows in one burst and asserts the backlog
// drains to "sent" with one provider call each: throttling delays sends and
// never fails or abandons them.
func TestLive_ThrottledBacklogDrainsWithoutFailures(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	rdb, err := platform.NewRedisClient(os.Getenv(envRedisURL))
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	const rps = 40
	h.handlers.OutboundLimiter = NewOutboundRateLimiter(rdb, rps, h.handlers.Logger)

	total := loadRecipients(t, 120)
	ids, tokens := seedDevices(t, ctx, h, projectID, total)
	startLiveWorker(t, h, 20)

	start := time.Now()
	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{
		DeviceIDs: ids,
		Title:     "throttled",
		Body:      "throttled",
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := fanoutNotification(ctx, h, n); err != nil {
		t.Fatalf("fanout: %v", err)
	}

	// The limiter admits rps sends at once, then about rps sends per second.
	drainBound := 30 * time.Second
	if total > rps {
		drainBound += time.Duration(total-rps) * time.Second / rps
	}
	counts := waitForTerminalRecipients(t, ctx, h, projectID, total, drainBound)
	elapsed := time.Since(start)
	waitForCompletedNotifications(t, ctx, h, projectID)
	t.Logf("throttled: %d recipients at %d rps drained in %s, counts=%v",
		total, rps, elapsed.Round(time.Millisecond), counts)

	if counts["sent"] != total {
		t.Errorf("sent recipients = %d, want %d (counts=%v)", counts["sent"], total, counts)
	}
	for _, token := range tokens {
		if calls := testAdapter.CallCount(token); calls != 1 {
			t.Errorf("provider calls for %s = %d, want exactly 1", token, calls)
		}
	}
}

// TestHandleSend_RetryAfterCrashBetweenSendAndMark_ResendsToProvider asserts
// the at-least-once behavior: a recipient left in "sending" by a worker that
// died after the provider call returned is sent to the provider again when
// its task is retried.
func TestHandleSend_RetryAfterCrashBetweenSendAndMark_ResendsToProvider(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)
	testAdapter.Program(token, fakeStep{result: provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "crash-replay"}})

	n, err := h.queries.CreateNotification(ctx, postgres.CreateNotificationParams{
		ProjectID:  postgres.UUIDFrom(projectID),
		Data:       []byte(`{}`),
		TargetSpec: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create notification fixture: %v", err)
	}
	recipient, err := h.queries.InsertNotificationRecipient(ctx, postgres.InsertNotificationRecipientParams{
		NotificationID: n.ID,
		DeviceID:       device.ID,
		ProviderType:   "expo",
	})
	if err != nil {
		t.Fatalf("insert recipient fixture: %v", err)
	}
	payload := queue.SendPayload{
		NotificationID: postgres.UUIDTo(n.ID),
		RecipientID:    postgres.UUIDTo(recipient.ID),
		DeviceID:       postgres.UUIDTo(device.ID),
		ProjectID:      projectID,
		ProviderType:   "expo",
		Token:          token,
	}

	if err := h.handlers.HandleSend(ctx, payload); err != nil {
		t.Fatalf("first HandleSend: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `update notification_recipients set status = 'sending', provider_message_id = null, sent_at = null where id = $1`, payload.RecipientID); err != nil {
		t.Fatalf("simulate crash before MarkRecipientSent: %v", err)
	}
	if err := h.handlers.HandleSend(ctx, payload); err != nil {
		t.Fatalf("retried HandleSend: %v", err)
	}

	if calls := testAdapter.CallCount(token); calls != 2 {
		t.Errorf("provider calls = %d, want 2 (one before the simulated crash, one on retry)", calls)
	}
	got, err := h.queries.GetNotificationRecipient(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetNotificationRecipient: %v", err)
	}
	if got.Status != "sent" {
		t.Errorf("recipient status = %q, want %q", got.Status, "sent")
	}
}
