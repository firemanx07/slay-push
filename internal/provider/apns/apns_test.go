package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/net/http2"

	"github.com/firemanx07/slay-push/internal/provider"
)

// newTestCredential builds a fake token-auth credential with a real ECDSA key.
func newTestCredential(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	cred := credentialJSON{
		KeyID:      "TESTKEYID",
		TeamID:     "TESTTEAMID",
		BundleID:   "com.example.app",
		PrivateKey: string(pemKey),
	}
	b, err := json.Marshal(cred) //nolint:gosec // test fixture, not a real credential
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	return b
}

// newTestServer starts a TLS test server with HTTP/2 enabled.
func newTestServer(handler http.Handler) *httptest.Server {
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	return server
}

// newTestAdapter builds an adapter pointed at the test server, accepting
// its self-signed certificate.
func newTestAdapter(t *testing.T, server *httptest.Server, credential []byte) *adapter {
	t.Helper()
	a := New().(*adapter)
	cc, err := a.clientFor(credential)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	cc.client.Host = server.URL
	cc.client.HTTPClient = &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server uses a self-signed cert
		},
	}
	return a
}

func TestSend_Success(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("apns-id", "test-apns-id")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cred := newTestCredential(t)
	a := newTestAdapter(t, server, cred)

	result, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "devicetoken", Title: "Hi", Body: "there"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != provider.StatusSent || result.ProviderMessageID != "test-apns-id" {
		t.Errorf("got %+v, want StatusSent with id test-apns-id", result)
	}
}

func TestSend_Unregistered(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
	}))
	defer server.Close()

	cred := newTestCredential(t)
	a := newTestAdapter(t, server, cred)

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "gone-token"})
	if result.Status != provider.StatusInvalidToken {
		t.Errorf("status = %v, want StatusInvalidToken", result.Status)
	}
}

func TestSend_BadDeviceToken(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"BadDeviceToken"}`))
	}))
	defer server.Close()

	cred := newTestCredential(t)
	a := newTestAdapter(t, server, cred)

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "bad-token"})
	if result.Status != provider.StatusInvalidToken {
		t.Errorf("status = %v, want StatusInvalidToken", result.Status)
	}
}

func TestSend_TooManyRequests(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"reason":"TooManyRequests"}`))
	}))
	defer server.Close()

	cred := newTestCredential(t)
	a := newTestAdapter(t, server, cred)

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusThrottled {
		t.Errorf("status = %v, want StatusThrottled", result.Status)
	}
}

func TestSend_InternalServerError(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"reason":"InternalServerError"}`))
	}))
	defer server.Close()

	cred := newTestCredential(t)
	a := newTestAdapter(t, server, cred)

	result, _ := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestClientFor_CachesPerCredential(t *testing.T) {
	a := New().(*adapter)
	cred := newTestCredential(t)

	cc1, err := a.clientFor(cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cc2, err := a.clientFor(cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cc1 != cc2 {
		t.Error("expected the same cached client for the same credential bytes")
	}
}

func TestClientFor_MissingBundleID(t *testing.T) {
	a := New().(*adapter)
	_, err := a.clientFor([]byte(`{"key_id":"x","team_id":"y","private_key":""}`))
	if err == nil {
		t.Fatal("expected an error for missing bundle_id")
	}
}
