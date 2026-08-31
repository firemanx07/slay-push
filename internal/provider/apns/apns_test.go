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
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sideshow/apns2"
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

func TestTestCredential_Success(t *testing.T) {
	a := New().(*adapter)
	cred := newTestCredential(t)
	if err := a.TestCredential(context.Background(), cred); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTestCredential_MissingBundleID(t *testing.T) {
	a := New().(*adapter)
	err := a.TestCredential(context.Background(), []byte(`{"key_id":"x","team_id":"y","private_key":""}`))
	if err == nil {
		t.Fatal("expected an error for missing bundle_id")
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "apns" {
		t.Errorf("Name() = %q, want %q", got, "apns")
	}
}

func TestClientFor_MalformedCredentialJSON(t *testing.T) {
	a := New().(*adapter)
	_, err := a.clientFor([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected an error for malformed credential JSON")
	}
}

func TestClientFor_InvalidPrivateKey(t *testing.T) {
	a := New().(*adapter)
	cred, err := json.Marshal(credentialJSON{ //nolint:gosec // test fixture, not a real credential
		KeyID: "x", TeamID: "y", BundleID: "com.example.app", PrivateKey: "not a valid p8 key",
	})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if _, err := a.clientFor(cred); err == nil {
		t.Fatal("expected an error for an unparseable .p8 private key")
	}
}

func TestClientFor_SandboxEnvironment(t *testing.T) {
	a := New().(*adapter)
	cred, err := json.Marshal(credentialJSON{
		KeyID: "x", TeamID: "y", BundleID: "com.example.app",
		PrivateKey:  string(newTestPrivateKeyPEM(t)),
		Environment: "sandbox",
	})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	cc, err := a.clientFor(cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cc.client.Host != apns2.HostDevelopment {
		t.Errorf("Host = %q, want %q (sandbox environment)", cc.client.Host, apns2.HostDevelopment)
	}
}

// newTestPrivateKeyPEM returns a fresh PEM-encoded ECDSA private key, reused
// as a stand-in .p8 key across tests that don't need a distinct one.
func newTestPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// newTestCertCredential builds a fake cert-auth credential with a
// self-signed cert/key pair, valid input for tls.X509KeyPair.
func newTestCertCredential(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	cred, err := json.Marshal(credentialJSON{ //nolint:gosec // test fixture, not a real credential
		AuthType: "cert", BundleID: "com.example.app",
		CertPEM: string(certPEM), KeyPEM: string(keyPEM),
	})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	return cred
}

func TestClientFor_CertAuth_Success(t *testing.T) {
	a := New().(*adapter)
	if _, err := a.clientFor(newTestCertCredential(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientFor_CertAuth_InvalidCert(t *testing.T) {
	a := New().(*adapter)
	cred, err := json.Marshal(credentialJSON{ //nolint:gosec // test fixture, not a real credential
		AuthType: "cert", BundleID: "com.example.app", CertPEM: "not a cert", KeyPEM: "not a key",
	})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if _, err := a.clientFor(cred); err == nil {
		t.Fatal("expected an error for an invalid cert/key pair")
	}
}

func TestSend_ClientForError(t *testing.T) {
	a := New().(*adapter)
	_, err := a.Send(context.Background(), []byte(`{"key_id":"x","team_id":"y","private_key":""}`), provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected clientFor's error (missing bundle_id) to propagate")
	}
}

func TestSend_WithCustomData(t *testing.T) {
	var gotBody map[string]any
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("apns-id", "test-apns-id")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cred := newTestCredential(t)
	a := newTestAdapter(t, server, cred)

	_, err := a.Send(context.Background(), cred, provider.SendRequest{
		Token: "devicetoken", Data: map[string]any{"custom_key": "custom_value"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["custom_key"] != "custom_value" {
		t.Errorf("custom data not merged into payload, got: %+v", gotBody)
	}
}

func TestSend_PushError(t *testing.T) {
	cred := newTestCredential(t)
	a := New().(*adapter)
	cc, err := a.clientFor(cred)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	cc.client.Host = "https://127.0.0.1:1" // nothing listens here — connection refused
	cc.client.HTTPClient = &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only, no real server involved
		},
	}

	result, err := a.Send(context.Background(), cred, provider.SendRequest{Token: "tok"})
	if err == nil {
		t.Fatal("expected a push error")
	}
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}

func TestClassifyResponse_DefaultReason(t *testing.T) {
	result := classifyResponse(&apns2.Response{StatusCode: http.StatusBadRequest, Reason: "PayloadTooLarge"})
	if result.Status != provider.StatusTransientError {
		t.Errorf("status = %v, want StatusTransientError", result.Status)
	}
}
