// Package provider defines the contract every push provider (Expo, FCM,
// APNs, HMS) implements, plus a self-registering factory registry. Adding a
// provider means adding one new package that calls Register in an init() —
// nothing here or in the dispatch/queue layers changes.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Status is the outcome of a single send attempt, coarse enough that the
// dispatch worker can decide retry behavior without knowing provider-specific
// error codes.
type Status int

const (
	StatusSent           Status = iota
	StatusInvalidToken          // terminal — mark the device stale/invalid, no retry
	StatusTransientError        // retryable with standard backoff
	StatusThrottled             // retryable, honor RetryAfter when provided
)

type SendRequest struct {
	Token string
	Title string
	Body  string
	Data  map[string]any
}

type SendResult struct {
	ProviderMessageID string
	Status            Status
	RetryAfter        time.Duration
	Err               error
}

// Adapter is implemented once per provider. Credential is the provider's raw
// stored credential JSON (service-account JSON, APNs key material, HMS
// app id/secret, Expo access token) — each adapter decodes its own shape.
type Adapter interface {
	Name() string
	Send(ctx context.Context, credential json.RawMessage, req SendRequest) (SendResult, error)
}

// Factory constructs an Adapter. Kept separate from Adapter itself so a
// provider package can do one-time setup (e.g. an HTTP client) in New.
type Factory func() Adapter

var (
	mu        sync.Mutex
	factories = map[string]Factory{}
	instances = map[string]Adapter{} // lazily built, one singleton per provider type
)

// Register is called from each provider package's init(). Registering the
// same name twice is a programming error (two providers claiming the same
// device_type), so it panics at startup rather than silently overwriting.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[name]; exists {
		panic(fmt.Sprintf("provider: %q already registered", name))
	}
	factories[name] = f
}

// Get resolves device_type directly to a shared adapter instance — no
// classification logic exists anywhere in this codebase; device_type is
// always supplied explicitly by the caller at registration time. The
// instance is a singleton per provider type (built lazily, once) so a
// provider like FCM can cache per-credential OAuth2 tokens across calls
// instead of re-authenticating on every send.
func Get(name string) (Adapter, bool) {
	mu.Lock()
	defer mu.Unlock()

	if a, ok := instances[name]; ok {
		return a, true
	}
	f, ok := factories[name]
	if !ok {
		return nil, false
	}
	a := f()
	instances[name] = a
	return a, true
}

// Known reports whether name is a registered provider type, for request
// validation before anything is persisted.
func Known(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := factories[name]
	return ok
}
