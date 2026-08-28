package dispatch

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// TestCreateNotification_IdempotencyKeyRace pins down the actual current
// behavior of two concurrent CreateNotification calls sharing an
// idempotency key. CreateNotification does a SELECT-then-INSERT, not an
// atomic upsert, so which of two outcomes happens depends on timing:
//   - benign: the second call's SELECT lands after the first has already
//     committed, finds its row, and returns it — both calls succeed with
//     the same notification ID.
//   - adversarial: both calls' SELECTs miss (neither sees the other's
//     uncommitted insert), both INSERT, one wins and one surfaces a raw
//     Postgres unique-violation error — not gracefully recovered.
//
// Both are real, reproducible outcomes (confirmed by running this race
// repeatedly), not just a hypothetical worst case. Exactly one
// notification row must exist afterward either way.
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

	var successes, uniqueViolations int
	var successfulIDs []postgres.Notification
	for _, r := range results {
		switch r.err {
		case nil:
			successes++
			successfulIDs = append(successfulIDs, r.n)
		default:
			var pgErr *pgconn.PgError
			if errors.As(r.err, &pgErr) && pgErr.Code == "23505" {
				uniqueViolations++
			} else {
				t.Errorf("unexpected error from concurrent CreateNotification: %v", r.err)
			}
		}
	}
	switch {
	case successes == 2 && uniqueViolations == 0:
		// Benign timing: both calls returned successfully, so they must
		// have returned the same, already-created notification.
		if successfulIDs[0].ID != successfulIDs[1].ID {
			t.Errorf("both calls succeeded but returned different notification IDs: %v vs %v",
				successfulIDs[0].ID, successfulIDs[1].ID)
		}
	case successes == 1 && uniqueViolations == 1:
		// Adversarial timing: the current, not-gracefully-recovered
		// outcome — one caller gets a raw unique-violation error.
	default:
		t.Errorf("unexpected outcome: %d successes, %d unique violations (want 2/0 or 1/1)", successes, uniqueViolations)
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
