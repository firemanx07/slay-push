package queue

import (
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func TestRetryDelayFunc(t *testing.T) {
	t.Run("honors ThrottledError.RetryAfter", func(t *testing.T) {
		want := 42 * time.Second
		err := &ThrottledError{RetryAfter: want, Err: errors.New("throttled")}
		if got := RetryDelayFunc(0, err, nil); got != want {
			t.Errorf("RetryDelayFunc = %v, want %v", got, want)
		}
	})

	// asynq.DefaultRetryDelayFunc includes real jitter (rand.IntN(30)*(n+1)
	// seconds on top of a deterministic n^4+15 base — see its source), so
	// two independent calls with identical inputs are not expected to
	// match. Assert the result falls in the known range instead of exact
	// equality against a second call.
	t.Run("falls back to the default backoff for other errors", func(t *testing.T) {
		err := errors.New("transient")
		task := asynq.NewTask("send:expo", []byte(`{}`))
		got := RetryDelayFunc(1, err, task)
		assertInDefaultBackoffRange(t, 1, got)
	})

	t.Run("falls back when RetryAfter is zero", func(t *testing.T) {
		err := &ThrottledError{RetryAfter: 0, Err: errors.New("throttled")}
		task := asynq.NewTask("send:expo", []byte(`{}`))
		got := RetryDelayFunc(2, err, task)
		assertInDefaultBackoffRange(t, 2, got)
	})
}

// assertInDefaultBackoffRange checks got against asynq.DefaultRetryDelayFunc's
// known formula (n^4 + 15 + rand.IntN(30)*(n+1) seconds) for retry count n,
// without depending on its specific random draw.
func assertInDefaultBackoffRange(t *testing.T, n int, got time.Duration) {
	t.Helper()
	base := int(math.Pow(float64(n), 4)) + 15
	maxJitter := 30 * (n + 1)
	min := time.Duration(base) * time.Second
	max := time.Duration(base+maxJitter-1) * time.Second
	if got < min || got > max {
		t.Errorf("RetryDelayFunc(%d, ...) = %v, want in [%v, %v] (asynq.DefaultRetryDelayFunc's range)", n, got, min, max)
	}
}

// TestEnqueueSend_Dedup confirms EnqueueSend's queue-level idempotency: a
// second enqueue for the same recipient (same asynq.TaskID) is a silent
// no-op, not an error, and doesn't create a second task.
func TestEnqueueSend_Dedup(t *testing.T) {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		t.Skip("REDIS_URL not set")
	}
	opt, err := ParseRedisOpt(redisURL)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := asynq.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })

	payload := SendPayload{
		RecipientID:  uuid.New(),
		ProviderType: "expo",
		Token:        "queue-test-token",
	}

	if err := EnqueueSend(client, payload); err != nil {
		t.Fatalf("first EnqueueSend: %v", err)
	}
	if err := EnqueueSend(client, payload); err != nil {
		t.Fatalf("second EnqueueSend (should be silently deduped): %v", err)
	}

	inspector := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = inspector.DeleteTask(SendTypeFor("expo"), payload.RecipientID.String()) })

	if _, err := inspector.GetTaskInfo(SendTypeFor("expo"), payload.RecipientID.String()); err != nil {
		t.Fatalf("GetTaskInfo: expected exactly one task at this TaskID, got error: %v", err)
	}
}
