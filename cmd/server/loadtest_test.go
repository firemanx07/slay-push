package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/config"
)

func TestPercentile(t *testing.T) {
	sorted := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond, 40 * time.Millisecond, 50 * time.Millisecond}
	tests := []struct {
		p    int
		want time.Duration
	}{
		{50, 30 * time.Millisecond},
		{95, 50 * time.Millisecond},
		{100, 50 * time.Millisecond},
	}
	for _, tt := range tests {
		if got := percentile(sorted, tt.p); got != tt.want {
			t.Errorf("percentile(_, %d) = %v, want %v", tt.p, got, tt.want)
		}
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("percentile(nil, 50) = %v, want 0", got)
	}
}

func TestLatencyResult_Record(t *testing.T) {
	r := newLatencyResult()
	r.record(10*time.Millisecond, nil)
	r.record(20*time.Millisecond, nil)
	r.record(0, errors.New("boom"))

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.latencies) != 2 {
		t.Errorf("latencies = %d, want 2", len(r.latencies))
	}
	if r.errors != 1 {
		t.Errorf("errors = %d, want 1", r.errors)
	}
	if len(r.sampleErrs) != 1 || r.sampleErrs[0] != "boom" {
		t.Errorf("sampleErrs = %v, want [\"boom\"]", r.sampleErrs)
	}
}

func TestLatencyResult_SampleErrsCappedAtFive(t *testing.T) {
	r := newLatencyResult()
	for i := 0; i < 10; i++ {
		r.record(0, fmt.Errorf("err-%d", i))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sampleErrs) != 5 {
		t.Errorf("sampleErrs = %d, want 5 (capped)", len(r.sampleErrs))
	}
	if r.errors != 10 {
		t.Errorf("errors = %d, want 10", r.errors)
	}
}

func TestLatencyResult_Print(t *testing.T) {
	t.Helper()
	// print() only writes to stdout — just confirm it doesn't panic on both
	// an empty result and one with recorded latencies/errors.
	newLatencyResult().print("empty", time.Second)

	r := newLatencyResult()
	r.record(10*time.Millisecond, nil)
	r.record(0, errors.New("boom"))
	r.print("mixed", time.Second)
}

func TestRunConcurrent(t *testing.T) {
	const n = 50
	const concurrency = 5

	var called int32
	var maxInFlight int32
	var inFlight int32

	runConcurrent(n, concurrency, func(_ int) {
		cur := atomic.AddInt32(&inFlight, 1)
		defer atomic.AddInt32(&inFlight, -1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
				break
			}
		}
		atomic.AddInt32(&called, 1)
	})

	if called != n {
		t.Errorf("called %d times, want %d", called, n)
	}
	if maxInFlight > concurrency {
		t.Errorf("max in-flight = %d, want <= %d", maxInFlight, concurrency)
	}
}

func TestLoadTestClient_PostAndDecodeID(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"abc-123"}`))
		}))
		defer server.Close()

		c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
		id, err := c.postAndDecodeID(context.Background(), "/api/v1/devices", []byte(`{}`))
		if err != nil {
			t.Fatalf("postAndDecodeID: %v", err)
		}
		if id != "abc-123" {
			t.Errorf("id = %q, want %q", id, "abc-123")
		}
	})

	t.Run("http error status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":"bad request"}`))
		}))
		defer server.Close()

		c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
		if _, err := c.postAndDecodeID(context.Background(), "/api/v1/devices", []byte(`{}`)); err == nil {
			t.Fatal("expected an error for a non-2xx response")
		}
	})

	t.Run("malformed response body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{not json`))
		}))
		defer server.Close()

		c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
		if _, err := c.postAndDecodeID(context.Background(), "/api/v1/devices", []byte(`{}`)); err == nil {
			t.Fatal("expected an error for a malformed response body")
		}
	})
}

func TestLoadTestClient_NotificationDone(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		statusCode int
		wantDone   bool
		wantErr    bool
	}{
		{"zero total recipients", `{"total_recipients":0,"counts":{}}`, http.StatusOK, false, false},
		{"partial completion", `{"total_recipients":5,"counts":{"sent":2,"failed":1}}`, http.StatusOK, false, false},
		{"fully terminal via sent+failed", `{"total_recipients":5,"counts":{"sent":3,"failed":2}}`, http.StatusOK, true, false},
		{"fully terminal via delivered", `{"total_recipients":3,"counts":{"delivered":3}}`, http.StatusOK, true, false},
		{"http error", `{"error":"not found"}`, http.StatusNotFound, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
			done, err := c.notificationDone(context.Background(), "notif-1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if done != tt.wantDone {
				t.Errorf("done = %v, want %v", done, tt.wantDone)
			}
		})
	}
}

func TestLoadTestClient_RegisterDevices(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n%5 == 0 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"device-%d"}`, n)
	}))
	defer server.Close()

	c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
	ids, result := c.registerDevices(20, 5)

	result.mu.Lock()
	defer result.mu.Unlock()
	if len(ids) != len(result.latencies) {
		t.Errorf("returned %d ids but recorded %d successes", len(ids), len(result.latencies))
	}
	if result.errors == 0 {
		t.Error("expected some rate-limited errors given the 1-in-5 failure pattern")
	}
	if len(ids)+result.errors != 20 {
		t.Errorf("ids(%d) + errors(%d) = %d, want 20", len(ids), result.errors, len(ids)+result.errors)
	}
}

func TestLoadTestClient_CreateNotifications(t *testing.T) {
	var mu sync.Mutex
	var seenTargets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IncludePlayerIDs []string `json:"include_player_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		mu.Lock()
		seenTargets = append(seenTargets, body.IncludePlayerIDs...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"notif-1","status":"pending"}`))
	}))
	defer server.Close()

	c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
	deviceIDs := []string{"dev-1", "dev-2", "dev-3"}
	refs, result := c.createNotifications(10, 3, deviceIDs)

	if len(refs) != 10 {
		t.Fatalf("refs = %d, want 10", len(refs))
	}
	for _, ref := range refs {
		if ref.createErr != nil {
			t.Errorf("unexpected create error: %v", ref.createErr)
		}
		if ref.id != "notif-1" {
			t.Errorf("id = %q, want %q", ref.id, "notif-1")
		}
	}
	result.mu.Lock()
	if len(result.latencies) != 10 {
		t.Errorf("recorded %d successes, want 10", len(result.latencies))
	}
	result.mu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	if len(seenTargets) != 10 {
		t.Fatalf("saw %d targets, want 10", len(seenTargets))
	}
	validTargets := map[string]bool{"dev-1": true, "dev-2": true, "dev-3": true}
	for _, target := range seenTargets {
		if !validTargets[target] {
			t.Errorf("unexpected target device %q", target)
		}
	}
}

func TestLoadTestClient_PollCompletion(t *testing.T) {
	t.Run("completes after a few polls", func(t *testing.T) {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			if n < 3 {
				_, _ = w.Write([]byte(`{"total_recipients":1,"counts":{}}`))
				return
			}
			_, _ = w.Write([]byte(`{"total_recipients":1,"counts":{"sent":1}}`))
		}))
		defer server.Close()

		c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
		refs := []notificationRef{{id: "notif-1", createdAt: time.Now()}}
		result := c.pollCompletion(refs, 5*time.Second)

		result.mu.Lock()
		defer result.mu.Unlock()
		if len(result.latencies) != 1 || result.errors != 0 {
			t.Errorf("got %d successes, %d errors, want 1/0", len(result.latencies), result.errors)
		}
	})

	t.Run("times out if never terminal", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total_recipients":1,"counts":{}}`))
		}))
		defer server.Close()

		c := &loadTestClient{httpClient: server.Client(), baseURL: server.URL, apiKey: "test-key"}
		refs := []notificationRef{{id: "notif-1", createdAt: time.Now()}}
		result := c.pollCompletion(refs, 400*time.Millisecond)

		result.mu.Lock()
		defer result.mu.Unlock()
		if result.errors != 1 || len(result.latencies) != 0 {
			t.Errorf("got %d successes, %d errors, want 0/1", len(result.latencies), result.errors)
		}
	})

	t.Run("skips refs that failed to create", func(t *testing.T) {
		c := &loadTestClient{httpClient: http.DefaultClient, baseURL: "http://unused", apiKey: "test-key"}
		refs := []notificationRef{{id: "", createdAt: time.Now(), createErr: errors.New("boom")}}
		result := c.pollCompletion(refs, time.Second)
		result.mu.Lock()
		defer result.mu.Unlock()
		if result.errors != 0 || len(result.latencies) != 0 {
			t.Errorf("expected the failed-to-create ref to be skipped entirely, got %d errors, %d successes", result.errors, len(result.latencies))
		}
	})
}

func TestRunLoadTest_MissingAPIKey(t *testing.T) {
	err := runLoadTest(config.Config{}, zerolog.Nop(), []string{"--base-url", "http://unused"})
	if err == nil {
		t.Fatal("expected an error for a missing --api-key")
	}
}

// TestRunLoadTest_FullRun drives all three phases against a fake instance
// implementing just enough of the public API to resolve every notification
// instantly, exercising the whole orchestration path end to end.
func TestRunLoadTest_FullRun(t *testing.T) {
	var notifSeq int64
	var mu sync.Mutex
	notifTotals := map[string]int32{}
	notifCounts := map[string]map[string]int64{}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q}`, uuid.New().String())
	})
	mux.HandleFunc("/api/v1/notifications", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IncludePlayerIDs []string `json:"include_player_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, target := range body.IncludePlayerIDs {
			if _, err := uuid.Parse(target); err != nil {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = fmt.Fprintf(w, `{"error":"include_player_ids must be valid UUIDs, got: %s"}`, target)
				return
			}
		}
		id := fmt.Sprintf("notif-%d", atomic.AddInt64(&notifSeq, 1))
		mu.Lock()
		notifTotals[id] = 1
		notifCounts[id] = map[string]int64{"sent": 1} // resolves instantly for this test
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":"pending"}`, id)
	})
	mux.HandleFunc("/api/v1/notifications/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/notifications/")
		mu.Lock()
		total, counts := notifTotals[id], notifCounts[id]
		mu.Unlock()
		resp := struct {
			TotalRecipients int32            `json:"total_recipients"`
			Counts          map[string]int64 `json:"counts"`
		}{TotalRecipients: total, Counts: counts}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	err := runLoadTest(config.Config{}, zerolog.Nop(), []string{
		"--base-url", server.URL,
		"--api-key", "test-key",
		"--devices", "5",
		"--notifications", "10",
		"--concurrency", "3",
	})
	if err != nil {
		t.Fatalf("runLoadTest: %v", err)
	}
}
