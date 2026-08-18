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
	result, err := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{
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
