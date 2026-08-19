// Package provider defines the contract every push provider (Expo, FCM,
// APNs, HMS) implements, plus a self-registering factory registry. A new
// provider registers itself by calling Register from an init().
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Status is the outcome of a single send attempt.
type Status int

// The possible outcomes of a single send attempt.
const (
	StatusUnknown Status = iota // zero value
	StatusSent
	StatusInvalidToken   // terminal, no retry; caller marks the device invalid
	StatusTransientError // retryable with standard backoff
	StatusThrottled      // retryable, honors RetryAfter when provided
)

// SendRequest is the provider-agnostic input to Adapter.Send.
type SendRequest struct {
	Token string
	Title string
	Body  string
	Data  map[string]any
}

// SendResult is the provider-agnostic outcome of Adapter.Send.
type SendResult struct {
	ProviderMessageID string
	Status            Status
	RetryAfter        time.Duration
	Err               error
}

// Adapter sends a single push through one provider. credential is the
// provider's raw stored credential JSON (service-account JSON, APNs key
// material, HMS app id/secret, Expo access token); each adapter decodes its
// own shape.
type Adapter interface {
	Name() string
	Send(ctx context.Context, credential json.RawMessage, req SendRequest) (SendResult, error)
}

// CredentialTester is implemented by adapters that can validate a decrypted
// credential locally (parse it, build a client from it) without sending a
// push. Adapters that don't implement it fall back to a bare JSON-shape
// check by the caller.
type CredentialTester interface {
	TestCredential(ctx context.Context, credential json.RawMessage) error
}

// Factory constructs an Adapter.
type Factory func() Adapter

var (
	mu        sync.Mutex
	factories = map[string]Factory{}
	instances = map[string]Adapter{} // lazily built, one singleton per provider type
)

// Register is called from each provider package's init(). Panics if name
// is already registered.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[name]; exists {
		panic(fmt.Sprintf("provider: %q already registered", name))
	}
	factories[name] = f
}

// Get returns the adapter registered for name, building and caching it on
// first use (one instance per provider type).
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

// Known reports whether name is a registered provider type.
func Known(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	_, ok := factories[name]
	return ok
}
