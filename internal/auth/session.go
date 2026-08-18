package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	sessionTokenPrefix = "sd_"
	sessionRandomBytes = 32
)

// SessionTTL is how long a dashboard session is valid from creation.
// Sessions don't silently extend on use; logging in again mints a new one.
const SessionTTL = 7 * 24 * time.Hour

// GenerateSessionToken returns a new high-entropy dashboard session token.
func GenerateSessionToken() (string, error) {
	b := make([]byte, sessionRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return sessionTokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashSessionToken returns the value stored in sessions.token_hash for a
// raw session token.
func HashSessionToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
