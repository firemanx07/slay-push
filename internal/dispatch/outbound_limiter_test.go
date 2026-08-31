package dispatch

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

const defaultTestRedisURL = "redis://localhost:6379"

func newRawRedisClient(t *testing.T) *redis.Client {
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

func TestOutboundRateLimiter_AllowsUnderLimit(t *testing.T) {
	rdb := newRawRedisClient(t)
	rl := NewOutboundRateLimiter(rdb, 100, zerolog.Nop())

	allowed, retryAfter := rl.Allow(context.Background(), uuid.New(), "expo")
	if !allowed {
		t.Error("Allow() = false, want true (well under the ceiling)")
	}
	if retryAfter != 0 {
		t.Errorf("retryAfter = %v, want 0", retryAfter)
	}
}

func TestOutboundRateLimiter_BlocksCeiling(t *testing.T) {
	rdb := newRawRedisClient(t)
	rl := NewOutboundRateLimiter(rdb, 1, zerolog.Nop())

	projectID := uuid.New()
	if allowed, _ := rl.Allow(context.Background(), projectID, "expo"); !allowed {
		t.Fatal("first call: Allow() = false, want true")
	}
	allowed, retryAfter := rl.Allow(context.Background(), projectID, "expo")
	if allowed {
		t.Error("second rapid call for the same (project, provider): Allow() = true, want false")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want > 0", retryAfter)
	}
}

func TestOutboundRateLimiter_DistinctProvidersDontShareCeiling(t *testing.T) {
	rdb := newRawRedisClient(t)
	rl := NewOutboundRateLimiter(rdb, 1, zerolog.Nop())

	projectID := uuid.New()
	if allowed, _ := rl.Allow(context.Background(), projectID, "expo"); !allowed {
		t.Fatal("expo call: Allow() = false, want true")
	}
	// A different provider under the same project must have its own ceiling.
	if allowed, _ := rl.Allow(context.Background(), projectID, "fcm"); !allowed {
		t.Error("fcm call: Allow() = false, want true (distinct provider, should not share expo's ceiling)")
	}
}

func TestOutboundRateLimiter_FailsOpenOnRedisError(t *testing.T) {
	rdb := newRawRedisClient(t)
	_ = rdb.Close() // any subsequent command now fails immediately.

	rl := NewOutboundRateLimiter(rdb, 1, zerolog.Nop())
	allowed, retryAfter := rl.Allow(context.Background(), uuid.New(), "expo")
	if !allowed {
		t.Error("Allow() = false on a Redis error, want true (must fail open)")
	}
	if retryAfter != 0 {
		t.Errorf("retryAfter = %v, want 0", retryAfter)
	}
}
