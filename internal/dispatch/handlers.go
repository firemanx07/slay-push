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

	"github.com/firemanx07/slay-push/internal/crypto"
	"github.com/firemanx07/slay-push/internal/store/postgres"
	"github.com/firemanx07/slay-push/internal/targeting"
)

// Handlers holds the dependencies shared by the HTTP and worker layers of
// the notification pipeline.
type Handlers struct {
	DB        *postgres.Queries
	Queue     *asynq.Client
	Targeting *targeting.Registry
	Crypto    crypto.MasterKey
	Logger    zerolog.Logger

	// OutboundLimiter, if set, caps HandleSend's rate of calls to each
	// provider on behalf of each project. Only meaningful in the worker
	// process — the HTTP layer never calls HandleSend — so it's left unset
	// there rather than added to NewHandlers's required parameters.
	OutboundLimiter *OutboundRateLimiter
}

// NewHandlers builds a Handlers from its dependencies.
func NewHandlers(db *postgres.Queries, q *asynq.Client, targetingRegistry *targeting.Registry, masterKey crypto.MasterKey, logger zerolog.Logger) *Handlers {
	return &Handlers{DB: db, Queue: q, Targeting: targetingRegistry, Crypto: masterKey, Logger: logger}
}

// terminalRecipientStatuses are statuses HandleSend must never act on again.
var terminalRecipientStatuses = map[string]bool{
	"sent":      true,
	"delivered": true,
	"failed":    true,
}

// failRecipient marks a recipient failed with the given error code/message.
func (h *Handlers) failRecipient(ctx context.Context, recipientID uuid.UUID, code, message string) {
	if err := h.DB.MarkRecipientFailed(ctx, postgres.MarkRecipientFailedParams{
		ID:           postgres.UUIDFrom(recipientID),
		ErrorCode:    postgres.TextFrom(code),
		ErrorMessage: postgres.TextFrom(message),
	}); err != nil {
		h.Logger.Error().Err(err).Str("recipient_id", recipientID.String()).Msg("failed to mark recipient failed")
	}
}

// isLastAttempt reports whether the current asynq task is on its final retry.
func isLastAttempt(ctx context.Context) bool {
	retryCount, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	if !ok1 || !ok2 {
		return false
	}
	return retryCount >= maxRetry
}

// firstNonNilErr returns the first non-nil error from errs, or nil if all are nil.
func firstNonNilErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
