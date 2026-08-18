package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// environment is fixed to "production" for now.
const environment = "production"

// HandleSend calls the right provider adapter for one recipient and
// records the outcome. Runs in the worker, one queue per provider type.
func (h *Handlers) HandleSend(ctx context.Context, payload queue.SendPayload) error {
	recipientID := postgres.UUIDFrom(payload.RecipientID)

	recipient, err := h.DB.GetNotificationRecipient(ctx, recipientID)
	if err != nil {
		return fmt.Errorf("fetch recipient: %w", err)
	}
	if terminalRecipientStatuses[recipient.Status] {
		// Already reached a terminal state.
		return nil
	}

	if _, err := h.DB.MarkRecipientSending(ctx, recipientID); err != nil {
		return fmt.Errorf("mark recipient sending: %w", err)
	}

	credRow, err := h.DB.GetActiveProviderCredential(ctx, postgres.GetActiveProviderCredentialParams{
		ProjectID:    postgres.UUIDFrom(payload.ProjectID),
		ProviderType: payload.ProviderType,
		Environment:  environment,
	})
	if err != nil {
		h.failRecipient(ctx, payload.RecipientID, "no_credential", err.Error())
		// Not retryable: skip asynq's retry/backoff.
		return fmt.Errorf("%w: no active %s credential for project: %w", asynq.SkipRetry, payload.ProviderType, err)
	}

	cred, err := h.Crypto.Open(credRow.WrappedDek, credRow.Credential)
	if err != nil {
		h.failRecipient(ctx, payload.RecipientID, "credential_decrypt_failed", err.Error())
		return fmt.Errorf("%w: decrypt %s credential: %w", asynq.SkipRetry, payload.ProviderType, err)
	}

	adapter, ok := provider.Get(payload.ProviderType)
	if !ok {
		h.failRecipient(ctx, payload.RecipientID, "unknown_provider", "no adapter registered for "+payload.ProviderType)
		return fmt.Errorf("%w: unknown provider %q", asynq.SkipRetry, payload.ProviderType)
	}

	result, sendErr := adapter.Send(ctx, cred, provider.SendRequest{
		Token: payload.Token,
		Title: payload.Title,
		Body:  payload.Body,
		Data:  payload.Data,
	})

	switch result.Status {
	case provider.StatusSent:
		return h.DB.MarkRecipientSent(ctx, postgres.MarkRecipientSentParams{
			ID:                recipientID,
			ProviderMessageID: postgres.TextFrom(result.ProviderMessageID),
		})

	case provider.StatusInvalidToken:
		if err := h.DB.MarkDeviceStatus(ctx, postgres.MarkDeviceStatusParams{
			ID: postgres.UUIDFrom(payload.DeviceID), Status: "invalid",
		}); err != nil {
			h.Logger.Error().Err(err).Str("device_id", payload.DeviceID.String()).Msg("failed to mark device invalid")
		}
		h.failRecipient(ctx, payload.RecipientID, "invalid_token", errorMessage(sendErr, result.Err))
		return nil // terminal — no retry

	case provider.StatusThrottled:
		return &queue.ThrottledError{
			RetryAfter: result.RetryAfter,
			Err:        firstNonNilErr(sendErr, result.Err, errors.New("throttled")),
		}

	default: // provider.StatusTransientError, provider.StatusUnknown
		transientErr := firstNonNilErr(sendErr, result.Err, errors.New("transient send error"))
		if isLastAttempt(ctx) {
			h.failRecipient(ctx, payload.RecipientID, "transient_error_exhausted", transientErr.Error())
		}
		return transientErr
	}
}

func errorMessage(errs ...error) string {
	if err := firstNonNilErr(errs...); err != nil {
		return err.Error()
	}
	return ""
}
