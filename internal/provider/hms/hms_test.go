package hms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/firemanx07/slay-push/internal/provider"
)

func newTestCredential() []byte {
	return []byte(`{"app_id":"test-app","app_secret":"test-secret"}`)
}

// newTestServer serves both the fake OAuth2 token endpoint and the fake HMS
// send endpoint, returning sendResponse for every send. tokenRequests, if
// non-nil, counts how many times the token endpoint was hit.
func newTestServer(t *testing.T, tokenRequests *int, sendResponse string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if tokenRequests != nil {
			*tokenRequests++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-token","token_type":"Bearer","expires_in":3600}`))
	})

	mux.HandleFunc("/v1/test-app/messages:send", func(w http.ResponseWriter, r *http.Request) {
		var envelope sendEnvelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(envelope.Message.Token) != 1 {
			t.Fatalf("expected exactly one token, got %d", len(envelope.Message.Token))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sendResponse))
	})

	return httptest.NewServer(mux)
}

func newTestAdapter(server *httptest.Server) *adapter {
	a := New().(*adapter)
	a.tokenURL = server.URL + "/token"
	a.baseURL = server.URL
	return a
}

func TestSend_Success(t *testing.T) {
	server := newTestServer(t, nil, `{"code":"80000000","msg":"Success","requestId":"req-1"}`)
	defer server.Close()

	a := newTestAdapter(server)
	result, err := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "token-success", Title: "Hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != provider.StatusSent || result.ProviderMessageID != "req-1" {
		t.Errorf("got %+v, want StatusSent with id req-1", result)
	}
}

func TestSend_AllTokensUnregistered(t *testing.T) {
	server := newTestServer(t, nil, `{"code":"80300007","msg":"all tokens are unregistered"}`)
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "bad-token"})
	if result.Status != provider.StatusInvalidToken {
		t.Errorf("status = %v, want StatusInvalidToken", result.Status)
	}
}

func TestSend_InternalServerError(t *testing.T) {
	server := newTestServer(t, nil, `{"code":"81000001","msg":"internal error"}`)
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestSend_OAuthTokenExpired_InvalidatesCache(t *testing.T) {
	tokenRequests := 0
	server := newTestServer(t, &tokenRequests, `{"code":"80200003","msg":"token expired"}`)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential()

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}

	// The cached token should have been evicted.
	if _, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"}); err != nil {
		t.Fatalf("unexpected error on second send: %v", err)
	}
	if tokenRequests != 2 {
		t.Errorf("token endpoint hit %d times, want 2 (cache should have been invalidated)", tokenRequests)
	}
}

func TestSend_TokenCaching(t *testing.T) {
	tokenRequests := 0
	server := newTestServer(t, &tokenRequests, `{"code":"80000000","msg":"Success","requestId":"req"}`)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential()

	for i := 0; i < 3; i++ {
		if _, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if tokenRequests != 1 {
		t.Errorf("token endpoint hit %d times, want 1 (token should cache)", tokenRequests)
	}
}

func TestTestCredential_Success(t *testing.T) {
	server := newTestServer(t, nil, `{"code":"80000000","msg":"Success","requestId":"req-1"}`)
	defer server.Close()

	a := newTestAdapter(server)
	if err := a.TestCredential(context.Background(), newTestCredential()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTestCredential_InvalidCredential(t *testing.T) {
	a := New().(*adapter)
	if err := a.TestCredential(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error for credential missing app_id/app_secret")
	}
}

func TestStringifyData(t *testing.T) {
	got := stringifyData(map[string]any{"key": "value"})
	if got != `{"key":"value"}` {
		t.Errorf("got %q", got)
	}
	if stringifyData(nil) != "" {
		t.Error("expected empty string for nil data")
	}
}
