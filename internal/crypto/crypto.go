// Package crypto implements envelope encryption for provider credentials:
// each credential gets its own random AES-256-GCM Data Encryption Key
// (DEK); the DEK is wrapped by a single master key (APP_MASTER_KEY).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const keySize = 32 // AES-256

// MasterKey wraps/unwraps per-credential DEKs.
type MasterKey struct {
	key [keySize]byte
}

// LoadMasterKey decodes a base64-encoded 32-byte key.
func LoadMasterKey(base64Key string) (MasterKey, error) {
	if base64Key == "" {
		return MasterKey{}, errors.New("APP_MASTER_KEY is not set")
	}
	raw, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return MasterKey{}, fmt.Errorf("APP_MASTER_KEY is not valid base64: %w", err)
	}
	if len(raw) != keySize {
		return MasterKey{}, fmt.Errorf("APP_MASTER_KEY must decode to %d bytes, got %d", keySize, len(raw))
	}
	var mk MasterKey
	copy(mk.key[:], raw)
	return mk, nil
}

// Seal generates a random DEK, encrypts plaintext with it, and wraps the
// DEK with the master key.
func (mk MasterKey) Seal(plaintext []byte) (wrappedDEK, ciphertext []byte, err error) {
	dek := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, nil, err
	}
	ciphertext, err = seal(dek, plaintext)
	if err != nil {
		return nil, nil, err
	}
	wrappedDEK, err = seal(mk.key[:], dek)
	if err != nil {
		return nil, nil, err
	}
	return wrappedDEK, ciphertext, nil
}

// Open unwraps the DEK with the master key, then decrypts ciphertext.
func (mk MasterKey) Open(wrappedDEK, ciphertext []byte) ([]byte, error) {
	dek, err := open(mk.key[:], wrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("unwrap dek: %w", err)
	}
	plaintext, err := open(dek, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	return plaintext, nil
}

func seal(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func open(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
