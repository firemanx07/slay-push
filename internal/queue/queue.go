// Package queue defines the asynq task types and payloads shared between
// the HTTP layer and the worker. One queue per provider (send:fcm,
// send:expo, send:apns, send:hms).
package queue

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// Queue and task type names for the fanout stage.
const (
	QueueFanout = "fanout"

	TypeFanout = "fanout"
)

// SendTypeFor returns the per-provider task/queue name, e.g. "send:fcm".
func SendTypeFor(providerType string) string { return "send:" + providerType }

// FanoutPayload is the task payload for the fanout queue.
type FanoutPayload struct {
	NotificationID uuid.UUID `json:"notification_id"`
	ProjectID      uuid.UUID `json:"project_id"`
}

// SendPayload is the task payload for a per-provider send queue.
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

// ParseRedisOpt is shared by NewClient and cmd/server's asynq.Server
// construction.
func ParseRedisOpt(redisURL string) (asynq.RedisConnOpt, error) {
	return asynq.ParseRedisURI(redisURL)
}

// NewClient builds an asynq.Client from a redis:// URL.
func NewClient(redisURL string) (*asynq.Client, error) {
	opt, err := ParseRedisOpt(redisURL)
	if err != nil {
		return nil, err
	}
	return asynq.NewClient(opt), nil
}

// EnqueueFanout enqueues a fanout task for the given notification.
func EnqueueFanout(client *asynq.Client, payload FanoutPayload) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeFanout, b)
	_, err = client.Enqueue(task, asynq.Queue(QueueFanout), asynq.MaxRetry(3))
	return err
}

// EnqueueSend is keyed by recipient id (asynq.TaskID): re-enqueueing the
// same recipient while a task is pending/in-flight is a no-op.
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

// ThrottledError carries a provider-supplied Retry-After through to
// asynq's retry scheduling (see RetryDelayFunc).
type ThrottledError struct {
	RetryAfter time.Duration
	Err        error
}

func (e *ThrottledError) Error() string { return e.Err.Error() }
func (e *ThrottledError) Unwrap() error { return e.Err }

// RetryDelayFunc honors Retry-After on a *ThrottledError, falling back to
// asynq's default exponential backoff otherwise.
func RetryDelayFunc(n int, err error, task *asynq.Task) time.Duration {
	var te *ThrottledError
	if errors.As(err, &te) && te.RetryAfter > 0 {
		return te.RetryAfter
	}
	return asynq.DefaultRetryDelayFunc(n, err, task)
}
