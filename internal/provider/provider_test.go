package provider

import (
	"context"
	"encoding/json"
	"testing"
)

type stubAdapter struct{ name string }

func (s *stubAdapter) Name() string { return s.name }
func (s *stubAdapter) Send(context.Context, json.RawMessage, SendRequest) (SendResult, error) {
	return SendResult{}, nil
}

func TestRegisterAndGet(t *testing.T) {
	built := 0
	Register("test-register-and-get", func() Adapter {
		built++
		return &stubAdapter{name: "test-register-and-get"}
	})

	if Known("test-register-and-get") != true {
		t.Fatal("Known() = false, want true for a registered name")
	}
	if Known("test-unregistered-name") != false {
		t.Error("Known() = true, want false for an unregistered name")
	}

	a1, ok := Get("test-register-and-get")
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if a1.Name() != "test-register-and-get" {
		t.Errorf("Name() = %q, want %q", a1.Name(), "test-register-and-get")
	}

	a2, ok := Get("test-register-and-get")
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
	Register("test-duplicate", func() Adapter { return &stubAdapter{name: "test-duplicate"} })

	defer func() {
		if recover() == nil {
			t.Error("Register() with a duplicate name did not panic")
		}
	}()
	Register("test-duplicate", func() Adapter { return &stubAdapter{name: "test-duplicate"} })
}
