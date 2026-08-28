package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/apikey"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// newAuthedRequest builds a real POST /api/v1/devices request carrying the
// given bearer token, for tests that go through the full router.
func newAuthedRequest(token string) *http.Request {
	body := `{"token":"tok-` + uuid.NewString() + `","provider":"expo","platform":"android"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/devices", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestRequireScope_MissingHeader(t *testing.T) {
	h := newLiveHarness(t)
	h.server.RateLimiter = newRateLimiter(t, rawRedisURL, 100)
	router := NewRouter(h.server)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newAuthedRequest(""))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestRequireScope_MalformedHeader(t *testing.T) {
	h := newLiveHarness(t)
	h.server.RateLimiter = newRateLimiter(t, rawRedisURL, 100)
	router := NewRouter(h.server)

	req := newAuthedRequest("")
	req.Header.Set("Authorization", "not-a-bearer-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_UnknownKey(t *testing.T) {
	h := newLiveHarness(t)
	h.server.RateLimiter = newRateLimiter(t, rawRedisURL, 100)
	router := NewRouter(h.server)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newAuthedRequest("sp_live_this-key-does-not-exist"))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_RevokedKey(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	h.server.RateLimiter = newRateLimiter(t, rawRedisURL, 100)
	router := NewRouter(h.server)

	raw, prefix, err := apikey.Generate()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	key, err := h.queries.CreateAPIKey(ctx, postgres.CreateAPIKeyParams{
		ProjectID: postgres.UUIDFrom(projectID),
		Name:      "revoked test key",
		KeyPrefix: prefix,
		KeyHash:   apikey.Hash(raw),
		Scope:     string(apikey.ScopeSend),
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if err := h.queries.RevokeAPIKey(ctx, postgres.RevokeAPIKeyParams{ID: key.ID, ProjectID: key.ProjectID}); err != nil {
		t.Fatalf("revoke api key: %v", err)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newAuthedRequest(raw))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_InsufficientScope(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	h.server.RateLimiter = newRateLimiter(t, rawRedisURL, 100)
	router := NewRouter(h.server)

	readOnlyKey := mustCreateAPIKey(t, ctx, h.queries, projectID, apikey.ScopeRead)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newAuthedRequest(readOnlyKey)) // register-device requires ScopeSend

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestRequireScope_ValidKeySucceeds(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	h.server.RateLimiter = newRateLimiter(t, rawRedisURL, 100)
	router := NewRouter(h.server)

	sendKey := mustCreateAPIKey(t, ctx, h.queries, projectID, apikey.ScopeSend)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newAuthedRequest(sendKey))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestRequireScope_RateLimited(t *testing.T) {
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	// A ceiling of 1 rps guarantees the second rapid request trips it.
	h.server.RateLimiter = newRateLimiter(t, rawRedisURL, 1)
	router := NewRouter(h.server)

	sendKey := mustCreateAPIKey(t, ctx, h.queries, projectID, apikey.ScopeSend)

	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, newAuthedRequest(sendKey))
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d; body: %s", w1.Code, http.StatusOK, w1.Body.String())
	}

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, newAuthedRequest(sendKey))
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want %d; body: %s", w2.Code, http.StatusTooManyRequests, w2.Body.String())
	}
	if w2.Header().Get("Retry-After") == "" {
		t.Error("second request missing Retry-After header")
	}
}
