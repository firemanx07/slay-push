package dispatch

import (
	"context"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// OutboundRateLimiter caps how fast the worker calls out to one provider on
// behalf of one project — protecting a project's own FCM/APNs/Expo/HMS
// account from being throttled or banned by a large fanout burst. A Redis
// error fails open, same policy as apikey.RateLimiter.
type OutboundRateLimiter struct {
	limiter *redis_rate.Limiter
	limit   redis_rate.Limit
	logger  zerolog.Logger
}

// NewOutboundRateLimiter builds an OutboundRateLimiter enforcing rps per
// (project, provider) pair.
func NewOutboundRateLimiter(rdb *redis.Client, rps int, logger zerolog.Logger) *OutboundRateLimiter {
	return &OutboundRateLimiter{
		limiter: redis_rate.NewLimiter(rdb),
		limit:   redis_rate.PerSecond(rps),
		logger:  logger,
	}
}

// Allow reports whether projectID may send another providerType push right
// now. allowed is false only when the ceiling was genuinely exceeded (not on
// a Redis error).
func (r *OutboundRateLimiter) Allow(ctx context.Context, projectID uuid.UUID, providerType string) (allowed bool, retryAfter time.Duration) {
	key := "outbound:" + providerType + ":" + projectID.String()
	res, err := r.limiter.Allow(ctx, key, r.limit)
	if err != nil {
		r.logger.Warn().Err(err).Str("key", key).Msg("outbound rate limiter error, failing open")
		return true, 0
	}
	if res.Allowed == 0 {
		return false, res.RetryAfter
	}
	return true, 0
}
