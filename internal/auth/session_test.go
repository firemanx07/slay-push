package auth

import "testing"

func TestGenerateSessionToken_HasPrefix(t *testing.T) {
	raw, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if len(raw) <= len(sessionTokenPrefix) || raw[:len(sessionTokenPrefix)] != sessionTokenPrefix {
		t.Errorf("token %q should start with %q", raw, sessionTokenPrefix)
	}
}

func TestHashSessionToken_DeterministicAndDistinct(t *testing.T) {
	raw, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	first := HashSessionToken(raw)
	second := HashSessionToken(raw)
	if first != second {
		t.Error("HashSessionToken should be deterministic for the same input")
	}
	if HashSessionToken(raw) == HashSessionToken(raw+"x") {
		t.Error("HashSessionToken should differ for different input")
	}
}
