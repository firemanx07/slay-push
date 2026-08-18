package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, keySize)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestLoadMasterKey(t *testing.T) {
	if _, err := LoadMasterKey(""); err == nil {
		t.Error("expected error for empty key")
	}
	if _, err := LoadMasterKey("not-base64!!!"); err == nil {
		t.Error("expected error for invalid base64")
	}
	if _, err := LoadMasterKey(base64.StdEncoding.EncodeToString([]byte("too-short"))); err == nil {
		t.Error("expected error for wrong-length key")
	}
	if _, err := LoadMasterKey(testKey(t)); err != nil {
		t.Errorf("unexpected error for valid key: %v", err)
	}
}

func TestSealOpen_RoundTrip(t *testing.T) {
	mk, err := LoadMasterKey(testKey(t))
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}

	plaintext := []byte(`{"project_id":"my-fcm-project"}`)
	wrappedDEK, ciphertext, err := mk.Seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if string(ciphertext) == string(plaintext) {
		t.Error("ciphertext should not equal plaintext")
	}

	got, err := mk.Open(wrappedDEK, ciphertext)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestSeal_UniqueDEKPerCall(t *testing.T) {
	mk, err := LoadMasterKey(testKey(t))
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}

	wrappedDEK1, _, err := mk.Seal([]byte("a"))
	if err != nil {
		t.Fatalf("seal 1: %v", err)
	}
	wrappedDEK2, _, err := mk.Seal([]byte("a"))
	if err != nil {
		t.Fatalf("seal 2: %v", err)
	}
	if string(wrappedDEK1) == string(wrappedDEK2) {
		t.Error("expected a distinct DEK per Seal call")
	}
}

func TestOpen_WrongMasterKeyFails(t *testing.T) {
	mk1, err := LoadMasterKey(testKey(t))
	if err != nil {
		t.Fatalf("load master key 1: %v", err)
	}
	mk2, err := LoadMasterKey(testKey(t))
	if err != nil {
		t.Fatalf("load master key 2: %v", err)
	}

	wrappedDEK, ciphertext, err := mk1.Seal([]byte("secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := mk2.Open(wrappedDEK, ciphertext); err == nil {
		t.Error("expected an error decrypting with the wrong master key")
	}
}
