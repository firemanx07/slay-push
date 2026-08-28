package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
)

type stubAdapter struct{ name string }

var testProviderNameSequence uint64

func uniqueTestProviderName(t *testing.T) string {
	return fmt.Sprintf("%s-%d", t.Name(), atomic.AddUint64(&testProviderNameSequence, 1))
}

func (s *stubAdapter) Name() string { return s.name }
func (s *stubAdapter) Send(context.Context, json.RawMessage, SendRequest) (SendResult, error) {
	return SendResult{}, nil
}

func TestRegisterAndGet(t *testing.T) {
	built := 0
	name := uniqueTestProviderName(t)
	Register(name, func() Adapter {
		built++
		return &stubAdapter{name: name}
	})

	if Known(name) != true {
		t.Fatal("Known() = false, want true for a registered name")
	}
	if Known("test-unregistered-name") != false {
		t.Error("Known() = true, want false for an unregistered name")
	}

	a1, ok := Get(name)
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if a1.Name() != name {
		t.Errorf("Name() = %q, want %q", a1.Name(), name)
	}

	a2, ok := Get(name)
	if !ok {
		t.Fatal("second Get() ok = false, want true")
	}
	if a1 != a2 {
		t.Error("Get() returned a different instance on the second call — should be a cached singleton")
	}
	if built != 1 {
		t.Errorf("factory was called %d times, want exactly 1 (lazy, cached)", built)
	}
}

func TestGet_Unregistered(t *testing.T) {
	if _, ok := Get("test-definitely-not-registered"); ok {
		t.Error("Get() ok = true, want false for an unregistered name")
	}
}

func TestRegister_DuplicatePanics(t *testing.T) {
	name := uniqueTestProviderName(t)
	Register(name, func() Adapter { return &stubAdapter{name: name} })

	defer func() {
		if recover() == nil {
			t.Error("Register() with a duplicate name did not panic")
		}
	}()
	Register(name, func() Adapter { return &stubAdapter{name: name} })
}
