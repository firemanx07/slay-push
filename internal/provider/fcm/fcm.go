// Package fcm sends push notifications directly to Firebase Cloud
// Messaging's HTTP v1 API.
package fcm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/firemanx07/slay-push/internal/provider"
)

func init() {
	provider.Register("fcm", New)
}

const messagingScope = "https://www.googleapis.com/auth/firebase.messaging"

type credentialJSON struct {
	ProjectID string `json:"project_id"`
}

type adapter struct {
	httpClient *http.Client
	baseURL    string // https://fcm.googleapis.com; overridable in tests

	mu           sync.Mutex
	tokenSources map[[32]byte]oauth2.TokenSource // keyed by sha256(credential bytes)
}

func New() provider.Adapter {
	return &adapter{
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		baseURL:      "https://fcm.googleapis.com",
		tokenSources: make(map[[32]byte]oauth2.TokenSource),
	}
}

func (a *adapter) Name() string { return "fcm" }

// tokenSourceFor returns a cached, self-refreshing OAuth2 token source for
// the given credential, keyed by a hash of the credential bytes.
func (a *adapter) tokenSourceFor(ctx context.Context, credential json.RawMessage) (oauth2.TokenSource, error) {
	key := sha256.Sum256(credential)

	a.mu.Lock()
	defer a.mu.Unlock()
	if ts, ok := a.tokenSources[key]; ok {
		return ts, nil
	}

	cfg, err := google.JWTConfigFromJSON(credential, messagingScope)
	if err != nil {
		return nil, fmt.Errorf("parse fcm service account: %w", err)
	}
	ts := cfg.TokenSource(ctx) // already caches + refreshes internally
	a.tokenSources[key] = ts
	return ts, nil
}

type sendEnvelope struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification *fcmNotification  `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type fcmSuccessResponse struct {
	Name string `json:"name"`
}

type fcmErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

func (a *adapter) Send(ctx context.Context, credential json.RawMessage, req provider.SendRequest) (provider.SendResult, error) {
	var cred credentialJSON
	if err := json.Unmarshal(credential, &cred); err != nil || cred.ProjectID == "" {
		return provider.SendResult{}, fmt.Errorf("fcm: invalid service account credential: %w", err)
	}

	ts, err := a.tokenSourceFor(ctx, credential)
	if err != nil {
		return provider.SendResult{}, err
	}
	token, err := ts.Token()
	if err != nil {
		return provider.SendResult{Status: provider.StatusTransientError}, fmt.Errorf("fcm: fetch oauth2 token: %w", err)
	}

	body := sendEnvelope{Message: fcmMessage{
		Token: req.Token,
		Data:  stringifyData(req.Data),
	}}
	if req.Title != "" || req.Body != "" {
		body.Message.Notification = &fcmNotification{Title: req.Title, Body: req.Body}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return provider.SendResult{}, fmt.Errorf("fcm: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/projects/%s/messages:send", a.baseURL, cred.ProjectID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return provider.SendResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return provider.SendResult{Status: provider.StatusTransientError}, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		var ok fcmSuccessResponse
		if err := json.Unmarshal(respBody, &ok); err != nil {
			return provider.SendResult{}, fmt.Errorf("fcm: parse success response: %w", err)
		}
		return provider.SendResult{ProviderMessageID: ok.Name, Status: provider.StatusSent}, nil
	}

	return classifyError(resp, respBody), nil
}

// classifyError maps an FCM HTTP v1 error response to the coarse
// provider.Status the dispatch worker retries (or doesn't retry) on.
func classifyError(resp *http.Response, body []byte) provider.SendResult {
	var errResp fcmErrorResponse
	_ = json.Unmarshal(body, &errResp)

	var errorCode string
	for _, d := range errResp.Error.Details {
		if d.ErrorCode != "" {
			errorCode = d.ErrorCode
		}
	}

	result := provider.SendResult{
		Err: fmt.Errorf("fcm: %d %s: %s", resp.StatusCode, errResp.Error.Status, errResp.Error.Message),
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		result.Status = provider.StatusThrottled
		result.RetryAfter = retryAfter(resp)
	case errorCode == "UNREGISTERED" || errorCode == "SENDER_ID_MISMATCH":
		result.Status = provider.StatusInvalidToken
	case resp.StatusCode == http.StatusBadRequest && errorCode == "INVALID_ARGUMENT":
		result.Status = provider.StatusInvalidToken
	case resp.StatusCode >= http.StatusInternalServerError:
		result.Status = provider.StatusTransientError
	default:
		result.Status = provider.StatusTransientError
	}
	return result
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

// stringifyData converts arbitrary values to strings: FCM's HTTP v1 `data`
// payload requires map[string]string.
func stringifyData(data map[string]any) map[string]string {
	if len(data) == 0 {
		return nil
	}
	out := make(map[string]string, len(data))
	for k, v := range data {
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			out[k] = fmt.Sprintf("%v", v)
			continue
		}
		out[k] = string(b)
	}
	return out
}
