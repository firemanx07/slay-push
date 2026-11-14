package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/firemanx07/slay-push/internal/provider"
)

// fakeStep is one canned response in a fakeAdapter script.
type fakeStep struct {
	result provider.SendResult
	err    error
}

// fakeAdapter is a process-global test double for provider.Adapter,
// registered under "expo" in this file's init() below. Safe because this
// package's test binary never imports the real internal/provider/expo,
// which would double-register "expo" and panic.
//
// Per-test isolation comes from each test using a unique push token, not
// from resetting the adapter — it's one shared singleton across the whole
// package's test run.
type fakeAdapter struct {
	mu       sync.Mutex
	scripts  map[string][]fakeStep
	calls    map[string]int
	behavior func(provider.SendRequest) (provider.SendResult, error)
}

func (f *fakeAdapter) Name() string { return "expo" }

// SetBehavior makes Send return fn's result for every request, skipping
// scripts and call counting, until it is called again with nil. fn runs
// without the adapter's lock held.
func (f *fakeAdapter) SetBehavior(fn func(req provider.SendRequest) (provider.SendResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.behavior = fn
}

// Send returns the behavior's result when one is set. Otherwise it returns
// the next canned step in token's script, consumed in call order; the last
// step repeats for any further calls once the script is exhausted.
func (f *fakeAdapter) Send(_ context.Context, _ json.RawMessage, req provider.SendRequest) (provider.SendResult, error) {
	f.mu.Lock()
	if behavior := f.behavior; behavior != nil {
		f.mu.Unlock()
		return behavior(req)
	}
	defer f.mu.Unlock()
	f.calls[req.Token]++
	steps := f.scripts[req.Token]
	if len(steps) == 0 {
		return provider.SendResult{Status: provider.StatusTransientError},
			fmt.Errorf("fakeAdapter: no script programmed for token %q", req.Token)
	}
	idx := f.calls[req.Token] - 1
	if idx >= len(steps) {
		idx = len(steps) - 1
	}
	step := steps[idx]
	return step.result, step.err
}

// Program registers the canned response sequence for token.
func (f *fakeAdapter) Program(token string, steps ...fakeStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[token] = steps
	f.calls[token] = 0
}

// CallCount reports how many times Send has been called for token.
func (f *fakeAdapter) CallCount(token string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[token]
}

var testAdapter = &fakeAdapter{scripts: map[string][]fakeStep{}, calls: map[string]int{}}

func init() {
	provider.Register("expo", func() provider.Adapter { return testAdapter })
}

func TestFakeAdapter_BehaviorOverridesScript(t *testing.T) {
	f := &fakeAdapter{scripts: map[string][]fakeStep{}, calls: map[string]int{}}
	f.Program("tok", fakeStep{result: provider.SendResult{Status: provider.StatusTransientError}})
	req := provider.SendRequest{Token: "tok"}

	f.SetBehavior(func(provider.SendRequest) (provider.SendResult, error) {
		return provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "hook"}, nil
	})
	got, err := f.Send(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("Send with behavior: %v", err)
	}
	if got.Status != provider.StatusSent || got.ProviderMessageID != "hook" {
		t.Errorf("Send with behavior = %+v, want StatusSent with message id %q", got, "hook")
	}
	if calls := f.CallCount("tok"); calls != 0 {
		t.Errorf("CallCount with behavior set = %d, want 0 (behavior skips call counting)", calls)
	}

	f.SetBehavior(nil)
	got, err = f.Send(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("Send after clearing behavior: %v", err)
	}
	if got.Status != provider.StatusTransientError {
		t.Errorf("Send after clearing behavior status = %v, want StatusTransientError", got.Status)
	}
}
