// Package dispatch orchestrates the notification pipeline: creating a
// notification (HTTP layer), resolving its audience and enqueueing sends
// (fanout, runs in the worker), and calling the right provider adapter for
// each recipient (send, runs in the worker).
package dispatch

import (
	"context"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/store/postgres"
	"github.com/firemanx07/slay-push/internal/targeting"
)

type Handlers struct {
	DB        *postgres.Queries
	Queue     *asynq.Client
	Targeting *targeting.Registry
	Logger    zerolog.Logger
}

func NewHandlers(db *postgres.Queries, q *asynq.Client, targetingRegistry *targeting.Registry, logger zerolog.Logger) *Handlers {
	return &Handlers{DB: db, Queue: q, Targeting: targetingRegistry, Logger: logger}
}

// terminalRecipientStatuses are statuses HandleSend must never act on again
// — the DB-level half of the pipeline's idempotency guarantee (the other
// half is the queue-level asynq.TaskID dedupe in queue.EnqueueSend).
var terminalRecipientStatuses = map[string]bool{
	"sent":      true,
	"delivered": true,
	"failed":    true,
}

// failRecipient is the one place notification_recipients transitions to
// "failed" from, so every failure path (invalid token, exhausted retries,
// missing credential, unknown provider) records it identically.
func (h *Handlers) failRecipient(ctx context.Context, recipientID uuid.UUID, code, message string) {
	if err := h.DB.MarkRecipientFailed(ctx, postgres.MarkRecipientFailedParams{
		ID:           postgres.UUIDFrom(recipientID),
		ErrorCode:    postgres.TextFrom(code),
		ErrorMessage: postgres.TextFrom(message),
	}); err != nil {
		h.Logger.Error().Err(err).Str("recipient_id", recipientID.String()).Msg("failed to mark recipient failed")
	}
}

func isLastAttempt(ctx context.Context) bool {
	retryCount, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	if !ok1 || !ok2 {
		return false
	}
	return retryCount >= maxRetry
}

func firstNonNilErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
