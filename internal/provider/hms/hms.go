// Package hms sends push notifications directly to Huawei's HMS Push Kit.
package hms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/firemanx07/slay-push/internal/provider"
)

func init() {
	provider.Register("hms", New)
}

type credentialJSON struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

type cachedToken struct {
	accessToken string
	expiresAt   time.Time
}

type adapter struct {
	httpClient *http.Client
	tokenURL   string // https://oauth-login.cloud.huawei.com/oauth2/v3/token; overridable in tests
	baseURL    string // https://push-api.cloud.huawei.com; overridable in tests

	mu     sync.Mutex
	tokens map[[32]byte]*cachedToken // keyed by sha256(credential bytes)
}

// New returns an HMS provider.Adapter.
func New() provider.Adapter {
	return &adapter{ //nolint:gosec // tokenURL/baseURL below are public endpoint constants, not credentials
		httpClient: &http.Client{Timeout: 10 * time.Second},
		tokenURL:   "https://oauth-login.cloud.huawei.com/oauth2/v3/token",
		baseURL:    "https://push-api.cloud.huawei.com",
		tokens:     make(map[[32]byte]*cachedToken),
	}
}

func (a *adapter) Name() string { return "hms" }

// accessTokenFor returns a cached OAuth2 access token for the given
// credential, refreshed shortly before expiry.
func (a *adapter) accessTokenFor(ctx context.Context, credential json.RawMessage, cred credentialJSON) (string, error) {
	key := sha256.Sum256(credential)

	a.mu.Lock()
	if t, ok := a.tokens[key]; ok && time.Now().Before(t.expiresAt) {
		tok := t.accessToken
		a.mu.Unlock()
		return tok, nil
	}
	a.mu.Unlock()

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {cred.AppID},
		"client_secret": {cred.AppSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("hms: fetch oauth2 token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hms: oauth2 token request failed: %d %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("hms: parse oauth2 token response: %w", err)
	}

	a.mu.Lock()
	a.tokens[key] = &cachedToken{
		accessToken: tokenResp.AccessToken,
		expiresAt:   time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second),
	}
	a.mu.Unlock()

	return tokenResp.AccessToken, nil
}

// TestCredential implements provider.CredentialTester by exchanging the
// app id/secret for an OAuth2 access token, without sending a push.
func (a *adapter) TestCredential(ctx context.Context, credential json.RawMessage) error {
	var cred credentialJSON
	if err := json.Unmarshal(credential, &cred); err != nil || cred.AppID == "" || cred.AppSecret == "" {
		return fmt.Errorf("hms: invalid credential: %w", err)
	}
	_, err := a.accessTokenFor(ctx, credential, cred)
	return err
}

func (a *adapter) invalidateToken(credential json.RawMessage) {
	key := sha256.Sum256(credential)
	a.mu.Lock()
	delete(a.tokens, key)
	a.mu.Unlock()
}

type sendEnvelope struct {
	Message hmsMessage `json:"message"`
}

type hmsMessage struct {
	Notification *hmsNotification `json:"notification,omitempty"`
	Data         string           `json:"data,omitempty"`
	Token        []string         `json:"token"`
}

type hmsNotification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// sendResponse: HMS returns HTTP 200 for most outcomes; the actual result
// is in Code, not the HTTP status.
type sendResponse struct {
	Code      string `json:"code"`
	Msg       string `json:"msg"`
	RequestID string `json:"requestId"`
}

const (
	codeSuccess               = "80000000"
	codeSomeTokensIllegal     = "80100000" // with a single token per call, this means that token failed
	codeOAuthAuthError        = "80200001"
	codeOAuthTokenExpired     = "80200003"
	codeAllTokensUnregistered = "80300007"
	codeInternalServerError   = "81000001"
)

func (a *adapter) Send(ctx context.Context, credential json.RawMessage, req provider.SendRequest) (provider.SendResult, error) {
	var cred credentialJSON
	if err := json.Unmarshal(credential, &cred); err != nil || cred.AppID == "" || cred.AppSecret == "" {
		return provider.SendResult{}, fmt.Errorf("hms: invalid credential: %w", err)
	}

	token, err := a.accessTokenFor(ctx, credential, cred)
	if err != nil {
		return provider.SendResult{Status: provider.StatusTransientError}, err
	}

	var notification *hmsNotification
	if req.Title != "" || req.Body != "" {
		notification = &hmsNotification{Title: req.Title, Body: req.Body}
	}

	payload, err := json.Marshal(sendEnvelope{Message: hmsMessage{
		Notification: notification,
		Data:         stringifyData(req.Data),
		Token:        []string{req.Token},
	}})
	if err != nil {
		return provider.SendResult{}, fmt.Errorf("hms: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/%s/messages:send", a.baseURL, cred.AppID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return provider.SendResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return provider.SendResult{Status: provider.StatusTransientError}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusTooManyRequests {
		return provider.SendResult{Status: provider.StatusThrottled, RetryAfter: retryAfter(resp),
			Err: fmt.Errorf("hms: rate limited: %s", string(body))}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return provider.SendResult{Status: provider.StatusTransientError,
			Err: fmt.Errorf("hms: unexpected http status %d: %s", resp.StatusCode, string(body))}, nil
	}

	var parsed sendResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return provider.SendResult{}, fmt.Errorf("hms: parse response: %w", err)
	}

	return a.classify(credential, parsed), nil
}

// classify maps HMS's response `code` to a provider.Status. Codes not
// listed explicitly fall through to the transient default.
func (a *adapter) classify(credential json.RawMessage, parsed sendResponse) provider.SendResult {
	switch parsed.Code {
	case codeSuccess:
		return provider.SendResult{ProviderMessageID: parsed.RequestID, Status: provider.StatusSent}
	case codeAllTokensUnregistered, codeSomeTokensIllegal:
		return provider.SendResult{Status: provider.StatusInvalidToken,
			Err: fmt.Errorf("hms: %s: %s", parsed.Code, parsed.Msg)}
	case codeOAuthAuthError, codeOAuthTokenExpired:
		// Evict the cached token; the next retry fetches a fresh one.
		a.invalidateToken(credential)
		return provider.SendResult{Status: provider.StatusTransientError,
			Err: fmt.Errorf("hms: %s: %s (token invalidated, will refresh on retry)", parsed.Code, parsed.Msg)}
	case codeInternalServerError:
		return provider.SendResult{Status: provider.StatusTransientError,
			Err: fmt.Errorf("hms: %s: %s", parsed.Code, parsed.Msg)}
	default:
		return provider.SendResult{Status: provider.StatusTransientError,
			Err: fmt.Errorf("hms: %s: %s", parsed.Code, parsed.Msg)}
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

// stringifyData JSON-encodes the data payload into a single string, the
// shape HMS's `message.data` field requires.
func stringifyData(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(b)
}
