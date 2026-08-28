package dispatch

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// TestCreateNotification_IdempotencyKeyRace confirms two concurrent
// CreateNotification calls sharing an idempotency key both succeed and
// return the same notification — the insert itself is conflict-safe
// (ON CONFLICT ... DO NOTHING + a fallback fetch on the loser), so timing
// can no longer surface a raw unique-violation error to either caller.
func TestCreateNotification_IdempotencyKeyRace(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	token := "expo-tok-" + uuid.NewString()
	device := mustCreateDevice(t, ctx, h.queries, projectID, token)

	key := "race-key-" + uuid.NewString()
	req := CreateNotificationRequest{
		IdempotencyKey: key,
		DeviceIDs:      []uuid.UUID{postgres.UUIDTo(device.ID)},
	}

	type result struct {
		n   postgres.Notification
		err error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, err := h.handlers.CreateNotification(ctx, projectID, req)
			results[i] = result{n: n, err: err}
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("call %d: CreateNotification returned an error: %v", i, r.err)
		}
	}
	if results[0].n.ID != results[1].n.ID {
		t.Errorf("both calls succeeded but returned different notification IDs: %v vs %v",
			results[0].n.ID, results[1].n.ID)
	}

	var count int
	if err := h.pool.QueryRow(ctx,
		`select count(*) from notifications where project_id = $1 and idempotency_key = $2`,
		projectID, key,
	).Scan(&count); err != nil {
		t.Fatalf("count notifications for key: %v", err)
	}
	if count != 1 {
		t.Errorf("notification rows for idempotency key %q = %d, want 1", key, count)
	}
}
