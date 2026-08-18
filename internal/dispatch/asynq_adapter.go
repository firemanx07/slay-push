package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/firemanx07/slay-push/internal/queue"
)

// AsynqFanoutHandler adapts HandleFanout to asynq.HandlerFunc's
// (ctx, *asynq.Task) signature.
func (h *Handlers) AsynqFanoutHandler(ctx context.Context, task *asynq.Task) error {
	var payload queue.FanoutPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: unmarshal fanout payload: %w", asynq.SkipRetry, err)
	}
	return h.HandleFanout(ctx, payload)
}

// AsynqSendHandler adapts HandleSend to asynq.HandlerFunc's
// (ctx, *asynq.Task) signature.
func (h *Handlers) AsynqSendHandler(ctx context.Context, task *asynq.Task) error {
	var payload queue.SendPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: unmarshal send payload: %w", asynq.SkipRetry, err)
	}
	return h.HandleSend(ctx, payload)
}
