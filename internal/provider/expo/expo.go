// Package expo sends push notifications directly to Expo's push service.
package expo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/firemanx07/slay-push/internal/provider"
)

func init() {
	provider.Register("expo", New)
}

// credentialJSON's access_token is optional.
type credentialJSON struct {
	AccessToken string `json:"access_token"`
}

type adapter struct {
	httpClient *http.Client
	baseURL    string // https://exp.host; overridable in tests
}

func New() provider.Adapter {
	return &adapter{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    "https://exp.host",
	}
}

func (a *adapter) Name() string { return "expo" }

type message struct {
	To    string         `json:"to"`
	Title string         `json:"title,omitempty"`
	Body  string         `json:"body,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

type ticket struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

type sendResponse struct {
	Data   []ticket `json:"data"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

func (a *adapter) Send(ctx context.Context, credential json.RawMessage, req provider.SendRequest) (provider.SendResult, error) {
	var cred credentialJSON
	if len(credential) > 0 {
		_ = json.Unmarshal(credential, &cred) // access_token is optional; a missing/empty credential is valid
	}

	// Expo's API accepts a batch array; this is a single-element batch.
	payload, err := json.Marshal([]message{{To: req.Token, Title: req.Title, Body: req.Body, Data: req.Data}})
	if err != nil {
		return provider.SendResult{}, fmt.Errorf("expo: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/--/api/v2/push/send", bytes.NewReader(payload))
	if err != nil {
		return provider.SendResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if cred.AccessToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return provider.SendResult{Status: provider.StatusTransientError}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusTooManyRequests {
		return provider.SendResult{
			Status:     provider.StatusThrottled,
			RetryAfter: retryAfter(resp),
			Err:        fmt.Errorf("expo: rate limited: %s", string(body)),
		}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return provider.SendResult{
			Status: provider.StatusTransientError,
			Err:    fmt.Errorf("expo: unexpected status %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	var parsed sendResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return provider.SendResult{}, fmt.Errorf("expo: parse response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		// Request-level error, not a per-ticket outcome.
		return provider.SendResult{
			Status: provider.StatusTransientError,
			Err:    fmt.Errorf("expo: request-level error: %s", parsed.Errors[0].Message),
		}, nil
	}
	if len(parsed.Data) == 0 {
		return provider.SendResult{}, fmt.Errorf("expo: empty response data")
	}

	return classifyTicket(parsed.Data[0]), nil
}

func classifyTicket(t ticket) provider.SendResult {
	switch t.Status {
	case "ok":
		return provider.SendResult{ProviderMessageID: t.ID, Status: provider.StatusSent}
	case "error":
		result := provider.SendResult{Err: fmt.Errorf("expo: %s (%s)", t.Message, t.Details.Error)}
		switch t.Details.Error {
		case "DeviceNotRegistered":
			result.Status = provider.StatusInvalidToken
		case "MessageRateExceeded":
			result.Status = provider.StatusThrottled
		default:
			result.Status = provider.StatusTransientError
		}
		return result
	default:
		return provider.SendResult{Status: provider.StatusTransientError, Err: fmt.Errorf("expo: unknown ticket status %q", t.Status)}
	}
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}
