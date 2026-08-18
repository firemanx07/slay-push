// Package queue defines the asynq task types and payloads shared between
// the HTTP layer (which enqueues) and the worker (which consumes). One
// queue per provider (send:fcm, send:expo, ... added as each adapter lands)
// so a slow/down provider never starves the others.
package queue

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	QueueFanout = "fanout"

	TypeFanout = "fanout"
)

// SendTypeFor returns the per-provider task/queue name, e.g. "send:fcm".
func SendTypeFor(providerType string) string { return "send:" + providerType }

type FanoutPayload struct {
	NotificationID uuid.UUID `json:"notification_id"`
	ProjectID      uuid.UUID `json:"project_id"`
}

type SendPayload struct {
	NotificationID uuid.UUID      `json:"notification_id"`
	RecipientID    uuid.UUID      `json:"recipient_id"`
	DeviceID       uuid.UUID      `json:"device_id"`
	ProjectID      uuid.UUID      `json:"project_id"`
	ProviderType   string         `json:"provider_type"`
	Token          string         `json:"token"`
	Title          string         `json:"title"`
	Body           string         `json:"body"`
	Data           map[string]any `json:"data,omitempty"`
}

// ParseRedisOpt is shared by the HTTP-side client (NewClient) and the
// worker's asynq.Server construction (cmd/server), so the URL-parsing logic
// exists exactly once.
func ParseRedisOpt(redisURL string) (asynq.RedisConnOpt, error) {
	return asynq.ParseRedisURI(redisURL)
}

func NewClient(redisURL string) (*asynq.Client, error) {
	opt, err := ParseRedisOpt(redisURL)
	if err != nil {
		return nil, err
	}
	return asynq.NewClient(opt), nil
}

func EnqueueFanout(client *asynq.Client, payload FanoutPayload) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeFanout, b)
	_, err = client.Enqueue(task, asynq.Queue(QueueFanout), asynq.MaxRetry(3))
	return err
}

// EnqueueSend is keyed by recipient id (asynq.TaskID), so re-enqueueing the
// same recipient while a task is already pending/in-flight is a no-op
// rather than a duplicate send — the queue-layer half of the idempotency
// story (the DB-status check in the send handler is the other half).
func EnqueueSend(client *asynq.Client, payload SendPayload) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	taskType := SendTypeFor(payload.ProviderType)
	task := asynq.NewTask(taskType, b)
	_, err = client.Enqueue(task,
		asynq.Queue(taskType),
		asynq.MaxRetry(5),
		asynq.TaskID(payload.RecipientID.String()),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// ThrottledError lets a provider adapter's StatusThrottled result carry a
// provider-supplied Retry-After through to asynq's retry scheduling —
// honored opportunistically (see RetryDelayFunc), never assumed present.
type ThrottledError struct {
	RetryAfter time.Duration
	Err        error
}

func (e *ThrottledError) Error() string { return e.Err.Error() }
func (e *ThrottledError) Unwrap() error { return e.Err }

// RetryDelayFunc honors a provider's Retry-After when the failing error is
// a *ThrottledError with one set, falling back to asynq's default
// exponential backoff otherwise — none of the four providers is confirmed
// to reliably send Retry-After, so the static backoff is the real backstop.
func RetryDelayFunc(n int, err error, task *asynq.Task) time.Duration {
	var te *ThrottledError
	if errors.As(err, &te) && te.RetryAfter > 0 {
		return te.RetryAfter
	}
	return asynq.DefaultRetryDelayFunc(n, err, task)
}
