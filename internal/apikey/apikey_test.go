package apikey

import (
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	raw, prefix, err := Generate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(raw, livePrefix) {
		t.Errorf("raw key %q missing prefix %q", raw, livePrefix)
	}
	if !strings.HasPrefix(raw, prefix) {
		t.Errorf("prefix %q is not a prefix of raw key %q", prefix, raw)
	}
	if len(prefix) != displayPrefixLen {
		t.Errorf("prefix length = %d, want %d", len(prefix), displayPrefixLen)
	}

	raw2, _, err := Generate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == raw2 {
		t.Error("expected two calls to Generate to produce different keys")
	}
}

func TestHash_Deterministic(t *testing.T) {
	raw, _, err := Generate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if Hash(raw) != Hash(raw) {
		t.Error("Hash should be deterministic for the same input")
	}
	if Hash(raw) == Hash(raw+"x") {
		t.Error("Hash should differ for different input")
	}
}

func TestParseScope(t *testing.T) {
	if _, ok := ParseScope("read"); !ok {
		t.Error("expected \"read\" to parse")
	}
	if _, ok := ParseScope("send"); !ok {
		t.Error("expected \"send\" to parse")
	}
	if _, ok := ParseScope("admin"); ok {
		t.Error("expected \"admin\" to fail to parse")
	}
}

func TestScope_Satisfies(t *testing.T) {
	cases := []struct {
		have, need Scope
		want       bool
	}{
		{ScopeRead, ScopeRead, true},
		{ScopeRead, ScopeSend, false},
		{ScopeSend, ScopeRead, true},
		{ScopeSend, ScopeSend, true},
	}
	for _, c := range cases {
		if got := c.have.Satisfies(c.need); got != c.want {
			t.Errorf("Scope(%q).Satisfies(%q) = %v, want %v", c.have, c.need, got, c.want)
		}
	}
}
