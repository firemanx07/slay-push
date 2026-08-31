package hms

import (
	"context"
	"encoding/json"
	"math"
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

func TestName(t *testing.T) {
	if got := New().Name(); got != "hms" {
		t.Errorf("Name() = %q, want %q", got, "hms")
	}
}

func TestSend_TokenFetchConnectionError(t *testing.T) {
	a := New().(*adapter)
	a.tokenURL = "http://127.0.0.1:1" // nothing listens here — connection refused

	_, err := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a connection error fetching the oauth2 token")
	}
}

func TestSend_TokenFetchNon200(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	a := newTestAdapter(server)
	_, err := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected error when the oauth2 token endpoint returns non-200")
	}
}

func TestSend_TokenFetchMalformedResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	a := newTestAdapter(server)
	_, err := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected error when the oauth2 token response body is malformed")
	}
}

func TestSend_ConnectionError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-token","token_type":"Bearer","expires_in":3600}`))
	})
	tokenServer := httptest.NewServer(mux)
	defer tokenServer.Close()

	a := New().(*adapter)
	a.tokenURL = tokenServer.URL + "/token"
	a.baseURL = "http://127.0.0.1:1" // nothing listens here — connection refused

	result, err := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestSend_RateLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/v1/test-app/messages:send", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "20")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"80100000","msg":"rate limited"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusThrottled {
		t.Errorf("status = %v, want StatusThrottled", result.Status)
	}
	if result.RetryAfter.Seconds() != 20 {
		t.Errorf("RetryAfter = %v, want 20s", result.RetryAfter)
	}
}

func TestSend_UnexpectedHTTPStatus(t *testing.T) {
	server := newTestServerWithStatus(t, http.StatusServiceUnavailable, `{"code":"","msg":"unavailable"}`)
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestSend_MalformedSendResponse(t *testing.T) {
	server := newTestServerWithStatus(t, http.StatusOK, `{not json`)
	defer server.Close()

	a := newTestAdapter(server)
	_, err := a.Send(context.Background(), newTestCredential(), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a parse error for a malformed send response")
	}
}

func TestClassify_SomeTokensIllegal(t *testing.T) {
	a := New().(*adapter)
	result := a.classify(newTestCredential(), sendResponse{Code: codeSomeTokensIllegal, Msg: "illegal"})
	if result.Status != provider.StatusInvalidToken {
		t.Errorf("status = %v, want StatusInvalidToken", result.Status)
	}
}

func TestClassify_OAuthAuthError(t *testing.T) {
	a := New().(*adapter)
	result := a.classify(newTestCredential(), sendResponse{Code: codeOAuthAuthError, Msg: "bad auth"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestClassify_UnknownCode(t *testing.T) {
	a := New().(*adapter)
	result := a.classify(newTestCredential(), sendResponse{Code: "99999999", Msg: "unknown"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestRetryAfter_NoHeader(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	if got := retryAfter(resp); got != 0 {
		t.Errorf("retryAfter = %v, want 0", got)
	}
}

// newTestServerWithStatus serves the OAuth2 token endpoint normally, and
// the send endpoint with a fixed status/body regardless of request content.
func newTestServerWithStatus(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/v1/test-app/messages:send", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return httptest.NewServer(mux)
}

func TestStringifyData(t *testing.T) {
	got := stringifyData(map[string]any{"key": "value"})
	if got != `{"key":"value"}` {
		t.Errorf("got %q", got)
	}
	if stringifyData(nil) != "" {
		t.Error("expected empty string for nil data")
	}
	if got := stringifyData(map[string]any{"bad": math.NaN()}); got != "" {
		t.Errorf("expected empty string when json.Marshal can't encode the value, got %q", got)
	}
}
