package queue

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestThrottledError(t *testing.T) {
	inner := errors.New("rate limited")
	te := &ThrottledError{RetryAfter: 5 * time.Second, Err: inner}

	if got := te.Error(); got != inner.Error() {
		t.Errorf("Error() = %q, want %q", got, inner.Error())
	}
	if !errors.Is(te, inner) {
		t.Error("errors.Is(te, inner) = false, want true (Unwrap should expose the wrapped error)")
	}
}

func TestIsFailure(t *testing.T) {
	t.Run("ThrottledError is not a failure", func(t *testing.T) {
		err := &ThrottledError{RetryAfter: time.Second, Err: errors.New("throttled")}
		if IsFailure(err) {
			t.Error("IsFailure(ThrottledError) = true, want false — must not count against MaxRetry")
		}
	})

	t.Run("wrapped ThrottledError is still not a failure", func(t *testing.T) {
		err := fmt.Errorf("wrapped: %w", &ThrottledError{RetryAfter: time.Second, Err: errors.New("throttled")})
		if IsFailure(err) {
			t.Error("IsFailure(wrapped ThrottledError) = true, want false")
		}
	})

	t.Run("other errors are failures", func(t *testing.T) {
		if !IsFailure(errors.New("transient send error")) {
			t.Error("IsFailure(plain error) = false, want true")
		}
	})
}

func TestNewClient(t *testing.T) {
	t.Run("invalid redis url returns an error", func(t *testing.T) {
		if _, err := NewClient("not-a-redis-url"); err == nil {
			t.Fatal("NewClient with an invalid URL: got nil error, want non-nil")
		}
	})

	t.Run("valid redis url succeeds", func(t *testing.T) {
		redisURL := os.Getenv("REDIS_URL")
		if redisURL == "" {
			t.Skip("REDIS_URL not set")
		}
		client, err := NewClient(redisURL)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		t.Cleanup(func() { _ = client.Close() })
	})
}

// TestEnqueueFanout confirms a fanout task can be enqueued and is visible to
// the inspector under the fanout queue/type.
func TestEnqueueFanout(t *testing.T) {
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

	payload := FanoutPayload{NotificationID: uuid.New(), ProjectID: uuid.New()}
	if err := EnqueueFanout(client, payload); err != nil {
		t.Fatalf("EnqueueFanout: %v", err)
	}

	inspector := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = inspector.Close() })

	// PageSize is generous: ListPendingTasks defaults to 30, easily crowded
	// out by leftover fanout tasks accumulated in a shared dev Redis with no
	// worker consuming them.
	tasks, err := inspector.ListPendingTasks(QueueFanout, asynq.PageSize(10000))
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	var found bool
	for _, task := range tasks {
		var p FanoutPayload
		if err := json.Unmarshal(task.Payload, &p); err == nil && p.NotificationID == payload.NotificationID {
			found = true
			t.Cleanup(func() { _ = inspector.DeleteTask(QueueFanout, task.ID) })
			break
		}
	}
	if !found {
		t.Fatal("enqueued fanout task not found among pending tasks")
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
	t.Cleanup(func() { _ = inspector.Close() })
	t.Cleanup(func() { _ = inspector.DeleteTask(SendTypeFor("expo"), payload.RecipientID.String()) })

	if _, err := inspector.GetTaskInfo(SendTypeFor("expo"), payload.RecipientID.String()); err != nil {
		t.Fatalf("GetTaskInfo: expected exactly one task at this TaskID, got error: %v", err)
	}
}
