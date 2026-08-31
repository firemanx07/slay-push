package fcm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/firemanx07/slay-push/internal/provider"
)

// newTestCredential builds a fake service-account credential whose
// token_uri points at the given test server.
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
		_, _ = fmt.Fprintf(w, `{"access_token":"tok-%d","token_type":"Bearer","expires_in":3600}`, tokenRequests)
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

func TestTestCredential_Success(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential(t, server.URL+"/token")

	if err := a.TestCredential(context.Background(), cred); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTestCredential_InvalidCredential(t *testing.T) {
	a := New().(*adapter)
	if err := a.TestCredential(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error for credential missing project_id")
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "fcm" {
		t.Errorf("Name() = %q, want %q", got, "fcm")
	}
}

// newInvalidKeyCredential has a valid project_id (so field validation
// passes) but a private_key that can't be parsed, to exercise
// tokenSourceFor's own error path (distinct from the field-validation
// error path already covered above).
func newInvalidKeyCredential(t *testing.T, tokenURL string) []byte {
	t.Helper()
	cred := map[string]string{
		"type":         "service_account",
		"project_id":   "test-project",
		"private_key":  "not a valid pem key",
		"client_email": "test@test-project.iam.gserviceaccount.com",
		"token_uri":    tokenURL,
	}
	b, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	return b
}

func TestSend_MissingProjectID(t *testing.T) {
	a := New().(*adapter)
	_, err := a.Send(context.Background(), []byte(`{}`), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected error for credential missing project_id")
	}
}

func TestSend_InvalidPrivateKey(t *testing.T) {
	a := New().(*adapter)
	cred := newInvalidKeyCredential(t, "http://unused")
	_, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected error for an unparseable private key")
	}
}

func TestTestCredential_InvalidPrivateKey(t *testing.T) {
	a := New().(*adapter)
	cred := newInvalidKeyCredential(t, "http://unused")
	if err := a.TestCredential(context.Background(), cred); err == nil {
		t.Fatal("expected error for an unparseable private key")
	}
}

func TestSend_TokenFetchError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer tokenServer.Close()

	a := New().(*adapter)
	cred := newTestCredential(t, tokenServer.URL)
	_, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected error when the oauth2 token endpoint fails")
	}
}

func TestTestCredential_TokenFetchError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer tokenServer.Close()

	a := New().(*adapter)
	cred := newTestCredential(t, tokenServer.URL)
	if err := a.TestCredential(context.Background(), cred); err == nil {
		t.Fatal("expected error when the oauth2 token endpoint fails")
	}
}

func TestSend_ConnectionError(t *testing.T) {
	tokenServer := newTestServer(t)
	defer tokenServer.Close()

	a := New().(*adapter)
	a.baseURL = "http://127.0.0.1:1" // nothing listens here — connection refused
	cred := newTestCredential(t, tokenServer.URL+"/token")

	result, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestSend_MalformedSuccessResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-access-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/v1/projects/test-project/messages:send", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not json`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	a := newTestAdapter(server)
	cred := newTestCredential(t, server.URL+"/token")
	_, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a parse error for a malformed success response")
	}
}

func TestClassifyError_BadRequestInvalidArgument(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}}
	body := []byte(`{"error":{"code":400,"message":"bad token","status":"INVALID_ARGUMENT","details":[{"errorCode":"INVALID_ARGUMENT"}]}}`)
	result := classifyError(resp, body)
	if result.Status != provider.StatusInvalidToken {
		t.Errorf("status = %v, want StatusInvalidToken", result.Status)
	}
}

func TestClassifyError_DefaultBranch(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
	body := []byte(`{"error":{"code":403,"message":"forbidden","status":"PERMISSION_DENIED"}}`)
	result := classifyError(resp, body)
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

func TestStringifyData_MarshalError(t *testing.T) {
	out := stringifyData(map[string]any{"bad": math.NaN()})
	if out["bad"] == "" {
		t.Error("expected a fallback fmt.Sprintf value for a value json.Marshal can't encode")
	}
}
