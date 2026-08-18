package fcm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/firemanx07/slay-push/internal/provider"
)

// newTestCredential builds a fake, throwaway service-account credential
// whose token_uri points at the given test server, so the OAuth2 JWT
// exchange itself is exercised against a mock rather than real Google
// endpoints. No real Google project or key is ever involved.
func newTestCredential(t *testing.T, tokenURL string) []byte {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	cred := map[string]string{
		"type":           "service_account",
		"project_id":     "test-project",
		"private_key_id": "test-key-id",
		"private_key":    string(pemKey),
		"client_email":   "test@test-project.iam.gserviceaccount.com",
		"client_id":      "1234567890",
		"token_uri":      tokenURL,
	}
	b, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	return b
}

// newTestServer serves both the fake OAuth2 token endpoint and the fake FCM
// send endpoint, picking a scenario based on the token in the send request
// so one server can drive every test case.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-access-token","token_type":"Bearer","expires_in":3600}`))
	})

	mux.HandleFunc("/v1/projects/test-project/messages:send", func(w http.ResponseWriter, r *http.Request) {
		var envelope sendEnvelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		switch envelope.Message.Token {
		case "token-success":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/0:1234567890"}`))
		case "token-invalid":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
		case "token-throttled":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded.","status":"RESOURCE_EXHAUSTED"}}`))
		case "token-server-error":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":500,"message":"Internal error.","status":"INTERNAL"}}`))
		default:
			t.Fatalf("unexpected token in test request: %q", envelope.Message.Token)
		}
	})

	return httptest.NewServer(mux)
}

func newTestAdapter(server *httptest.Server) *adapter {
	a := New().(*adapter)
	a.baseURL = server.URL
	return a
}

func TestSend_Success(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential(t, server.URL+"/token")

	result, err := a.Send(context.Background(), cred, provider.SendRequest{
		Token: "token-success", Title: "Hi", Body: "there",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != provider.StatusSent {
		t.Errorf("status = %v, want StatusSent", result.Status)
	}
	if result.ProviderMessageID == "" {
		t.Error("expected a non-empty provider message id")
	}
}

func TestSend_InvalidToken(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential(t, server.URL+"/token")

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "token-invalid"})
	if result.Status != provider.StatusInvalidToken {
		t.Errorf("status = %v, want StatusInvalidToken", result.Status)
	}
}

func TestSend_Throttled(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential(t, server.URL+"/token")

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "token-throttled"})
	if result.Status != provider.StatusThrottled {
		t.Errorf("status = %v, want StatusThrottled", result.Status)
	}
	if result.RetryAfter.Seconds() != 30 {
		t.Errorf("RetryAfter = %v, want 30s", result.RetryAfter)
	}
}

func TestSend_TransientServerError(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential(t, server.URL+"/token")

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "token-server-error"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestStringifyData(t *testing.T) {
	out := stringifyData(map[string]any{
		"str":    "hello",
		"num":    42,
		"nested": map[string]any{"a": 1},
	})
	if out["str"] != "hello" {
		t.Errorf("str = %q, want %q", out["str"], "hello")
	}
	if out["num"] != "42" {
		t.Errorf("num = %q, want %q", out["num"], "42")
	}
	if out["nested"] == "" {
		t.Error("expected nested value to be JSON-stringified, got empty")
	}
}

func TestTokenSourceCaching(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	var tokenRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"access_token":"tok-%d","token_type":"Bearer","expires_in":3600}`, tokenRequests)))
	})
	mux.HandleFunc("/v1/projects/test-project/messages:send", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/0:x"}`))
	})
	tokenServer := httptest.NewServer(mux)
	defer tokenServer.Close()

	a := newTestAdapter(tokenServer)
	cred := newTestCredential(t, tokenServer.URL+"/token")

	for i := 0; i < 3; i++ {
		if _, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "token-success"}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	if tokenRequests != 1 {
		t.Errorf("token endpoint hit %d times, want 1 (token source should cache)", tokenRequests)
	}
}
