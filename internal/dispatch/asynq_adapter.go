package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/firemanx07/slay-push/internal/queue"
)

// AsynqFanoutHandler and AsynqSendHandler adapt the typed HandleFanout/
// HandleSend methods to asynq.HandlerFunc's (ctx, *asynq.Task) signature —
// kept as a thin translation layer so the handlers themselves stay
// testable without constructing an *asynq.Task.

func (h *Handlers) AsynqFanoutHandler(ctx context.Context, task *asynq.Task) error {
	var payload queue.FanoutPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: unmarshal fanout payload: %v", asynq.SkipRetry, err)
	}
	return h.HandleFanout(ctx, payload)
}

func (h *Handlers) AsynqSendHandler(ctx context.Context, task *asynq.Task) error {
	var payload queue.SendPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: unmarshal send payload: %v", asynq.SkipRetry, err)
	}
	return h.HandleSend(ctx, payload)
}
