package apikey

import (
	"context"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// RateLimiter enforces a per-key ceiling and a looser per-project ceiling.
// A Redis error fails open.
type RateLimiter struct {
	limiter    *redis_rate.Limiter
	perKey     redis_rate.Limit
	perProject redis_rate.Limit
	logger     zerolog.Logger
}

// NewRateLimiter builds a RateLimiter with a per-key ceiling of rps and a
// per-project ceiling of 5x rps.
func NewRateLimiter(rdb *redis.Client, rps int, logger zerolog.Logger) *RateLimiter {
	return &RateLimiter{
		limiter:    redis_rate.NewLimiter(rdb),
		perKey:     redis_rate.PerSecond(rps),
		perProject: redis_rate.PerSecond(rps * 5),
		logger:     logger,
	}
}

// Allow checks both ceilings, key first. allowed is false only when a
// ceiling was genuinely exceeded (not on a Redis error).
func (r *RateLimiter) Allow(ctx context.Context, apiKeyID, projectID uuid.UUID) (allowed bool, retryAfter time.Duration) {
	if ok, ra := r.check(ctx, "apikey:"+apiKeyID.String(), r.perKey); !ok {
		return false, ra
	}
	if ok, ra := r.check(ctx, "project:"+projectID.String(), r.perProject); !ok {
		return false, ra
	}
	return true, 0
}

// check enforces a single rate limit on key, failing open on Redis errors.
func (r *RateLimiter) check(ctx context.Context, key string, limit redis_rate.Limit) (bool, time.Duration) {
	res, err := r.limiter.Allow(ctx, key, limit)
	if err != nil {
		r.logger.Warn().Err(err).Str("key", key).Msg("rate limiter error, failing open")
		return true, 0
	}
	if res.Allowed == 0 {
		return false, res.RetryAfter
	}
	return true, 0
}
