// Package apikey generates and hashes machine-to-machine API keys and
// defines their scopes.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const (
	livePrefix       = "sp_live_"
	randomBytes      = 32
	displayPrefixLen = len(livePrefix) + 6
)

type Scope string

const (
	ScopeRead Scope = "read"
	ScopeSend Scope = "send"
)

func ParseScope(s string) (Scope, bool) {
	switch Scope(s) {
	case ScopeRead, ScopeSend:
		return Scope(s), true
	default:
		return "", false
	}
}

// Satisfies reports whether s grants the required scope. ScopeSend
// satisfies both ScopeSend and ScopeRead requirements.
func (s Scope) Satisfies(required Scope) bool {
	if s == ScopeSend {
		return true
	}
	return s == required
}

// Generate returns a new raw key and its display prefix (the part shown
// back to the operator after creation, e.g. in a key list UI).
func Generate() (raw, prefix string, err error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate api key: %w", err)
	}
	raw = livePrefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, raw[:displayPrefixLen], nil
}

// Hash returns the value stored in api_keys.key_hash for a raw key.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
