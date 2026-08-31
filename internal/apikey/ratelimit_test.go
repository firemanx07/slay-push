package apikey

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

const defaultTestRedisURL = "redis://localhost:6379"

func newTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = defaultTestRedisURL
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis unreachable (set REDIS_URL to run): %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rdb := newTestRedisClient(t)
	rl := NewRateLimiter(rdb, 100, zerolog.Nop())

	allowed, retryAfter := rl.Allow(context.Background(), uuid.New(), uuid.New())
	if !allowed {
		t.Error("Allow() = false, want true (well under both ceilings)")
	}
	if retryAfter != 0 {
		t.Errorf("retryAfter = %v, want 0", retryAfter)
	}
}

func TestRateLimiter_BlocksPerKeyCeiling(t *testing.T) {
	rdb := newTestRedisClient(t)
	rl := NewRateLimiter(rdb, 1, zerolog.Nop())

	keyID := uuid.New()
	projectID := uuid.New()

	if allowed, _ := rl.Allow(context.Background(), keyID, projectID); !allowed {
		t.Fatal("first call: Allow() = false, want true")
	}
	allowed, retryAfter := rl.Allow(context.Background(), keyID, projectID)
	if allowed {
		t.Error("second rapid call for the same key: Allow() = true, want false (per-key ceiling exceeded)")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want > 0", retryAfter)
	}
}

func TestRateLimiter_BlocksPerProjectCeiling(t *testing.T) {
	rdb := newTestRedisClient(t)
	rl := NewRateLimiter(rdb, 1, zerolog.Nop()) // per-project ceiling = 5x rps = 5

	projectID := uuid.New()

	var lastAllowed bool
	for i := 0; i < 6; i++ {
		// A distinct key each call, so the per-key ceiling (checked first)
		// never trips — only the shared per-project ceiling can.
		allowed, _ := rl.Allow(context.Background(), uuid.New(), projectID)
		lastAllowed = allowed
	}
	if lastAllowed {
		t.Error("6th call sharing one project across 6 distinct keys: Allow() = true, want false (per-project ceiling exceeded)")
	}
}

func TestRateLimiter_FailsOpenOnRedisError(t *testing.T) {
	rdb := newTestRedisClient(t)
	_ = rdb.Close() // any subsequent command now fails immediately.

	rl := NewRateLimiter(rdb, 1, zerolog.Nop())
	allowed, retryAfter := rl.Allow(context.Background(), uuid.New(), uuid.New())
	if !allowed {
		t.Error("Allow() = false on a Redis error, want true (must fail open)")
	}
	if retryAfter != 0 {
		t.Errorf("retryAfter = %v, want 0", retryAfter)
	}
}
