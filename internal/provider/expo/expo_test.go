package expo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/firemanx07/slay-push/internal/provider"
)

func newTestAdapter(server *httptest.Server) *adapter {
	a := New().(*adapter)
	a.baseURL = server.URL
	return a
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "expo" {
		t.Errorf("Name() = %q, want %q", got, "expo")
	}
}

func TestSend_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msgs []message
		if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(msgs) != 1 || msgs[0].To != "ExponentPushToken[abc]" {
			t.Fatalf("unexpected request body: %+v", msgs)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"receipt-1"}]}`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	result, err := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{ //nolint:gosec // fake test token
		Token: "ExponentPushToken[abc]", Title: "Hi", Body: "there",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != provider.StatusSent || result.ProviderMessageID != "receipt-1" {
		t.Errorf("got %+v, want StatusSent with id receipt-1", result)
	}
}

func TestSend_DeviceNotRegistered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"error","message":"not registered","details":{"error":"DeviceNotRegistered"}}]}`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "bad-token"})
	if result.Status != provider.StatusInvalidToken {
		t.Errorf("status = %v, want StatusInvalidToken", result.Status)
	}
}

func TestSend_MessageRateExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"error","message":"rate exceeded","details":{"error":"MessageRateExceeded"}}]}`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusThrottled {
		t.Errorf("status = %v, want StatusThrottled", result.Status)
	}
}

func TestSend_HTTP429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "15")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":[{"code":"RATE_LIMITED","message":"too many"}]}`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusThrottled {
		t.Errorf("status = %v, want StatusThrottled", result.Status)
	}
	if result.RetryAfter.Seconds() != 15 {
		t.Errorf("RetryAfter = %v, want 15s", result.RetryAfter)
	}
}

func TestSend_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestSend_ConnectionError(t *testing.T) {
	a := New().(*adapter)
	a.baseURL = "http://127.0.0.1:1" // nothing listens here — connection refused

	result, err := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestSend_MalformedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	_, err := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a parse error for a malformed response body")
	}
}

func TestSend_RequestLevelErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"code":"BAD_REQUEST","message":"invalid request"}]}`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	result, _ := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestSend_EmptyResponseData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	_, err := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected an error for empty response data")
	}
}

func TestClassifyTicket_UnknownErrorDetail(t *testing.T) {
	result := classifyTicket(ticket{Status: "error", Message: "something else went wrong"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestClassifyTicket_UnknownStatus(t *testing.T) {
	result := classifyTicket(ticket{Status: "pending"})
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

func TestSend_UsesAccessTokenWhenProvided(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"x"}]}`))
	}))
	defer server.Close()

	a := newTestAdapter(server)
	_, err := a.Send(context.Background(), []byte(`{"access_token":"secret-token"}`), provider.SendRequest{Token: "tok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-token")
	}
}
