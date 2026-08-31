package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/firemanx07/slay-push/internal/config"
	"github.com/rs/zerolog"
)

// runLoadTest exercises a running slay-push instance's public API as a
// black-box HTTP client: register devices, create notifications targeting
// them, then poll a sample of those notifications to completion. It has no
// DB/Redis dependency of its own, so it works identically against a local
// dev run or a real deployment — only --base-url/--api-key change.
func runLoadTest(_ config.Config, _ zerolog.Logger, args []string) error {
	fs := flag.NewFlagSet("loadtest", flag.ExitOnError)
	baseURL := fs.String("base-url", "http://localhost:8080", "base URL of a running slay-push instance")
	apiKey := fs.String("api-key", "", "API key with send scope (send satisfies read too)")
	numDevices := fs.Int("devices", 100, "number of devices to register")
	numNotifications := fs.Int("notifications", 200, "number of notifications to create")
	concurrency := fs.Int("concurrency", 20, "max concurrent requests per phase")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *apiKey == "" {
		return errors.New("loadtest: --api-key is required")
	}

	client := &loadTestClient{httpClient: &http.Client{Timeout: 10 * time.Second}, baseURL: *baseURL, apiKey: *apiKey}

	fmt.Printf("Phase 1: registering %d devices (concurrency %d)...\n", *numDevices, *concurrency)
	phase1Start := time.Now()
	deviceIDs, regResult := client.registerDevices(*numDevices, *concurrency)
	regResult.print("device registration", time.Since(phase1Start))
	if len(deviceIDs) == 0 {
		return errors.New("loadtest: no devices registered successfully, aborting")
	}

	fmt.Printf("\nPhase 2: creating %d notifications (concurrency %d)...\n", *numNotifications, *concurrency)
	phase2Start := time.Now()
	refs, createResult := client.createNotifications(*numNotifications, *concurrency, deviceIDs)
	createResult.print("notification creation", time.Since(phase2Start))

	// A random sample, not the first N created — creation order isn't
	// processing order (concurrent creation, and the queue/rate-limiter
	// don't guarantee FIFO), so the first N can just as easily be the last
	// N actually processed.
	sample := append([]notificationRef(nil), refs...)
	rand.Shuffle(len(sample), func(i, j int) { sample[i], sample[j] = sample[j], sample[i] }) //nolint:gosec // sampling which notifications to poll for a load test, not a security-sensitive value
	sampleSize := len(sample)
	if sampleSize > 20 {
		sampleSize = 20
	}
	const pollTimeout = 60 * time.Second
	fmt.Printf("\nPhase 3: polling %d notifications to completion (%v timeout each)...\n", sampleSize, pollTimeout)
	phase3Start := time.Now()
	completionResult := client.pollCompletion(sample[:sampleSize], pollTimeout)
	completionResult.print("end-to-end completion", time.Since(phase3Start))

	return nil
}

type loadTestClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// notificationRef tracks one created notification's id and the instant it
// was created, so phase 3 can measure true end-to-end completion latency.
type notificationRef struct {
	id        string
	createdAt time.Time
	createErr error
}

func (c *loadTestClient) registerDevices(n, concurrency int) ([]string, *latencyResult) {
	result := newLatencyResult()
	var mu sync.Mutex
	var deviceIDs []string
	runConcurrent(n, concurrency, func(i int) {
		body, _ := json.Marshal(map[string]string{
			"token":    fmt.Sprintf("loadtest-token-%d-%d", i, time.Now().UnixNano()),
			"provider": "expo",
			"platform": "android",
		})
		start := time.Now()
		id, err := c.postAndDecodeID(context.Background(), "/api/v1/devices", body)
		result.record(time.Since(start), err)
		if err == nil {
			mu.Lock()
			deviceIDs = append(deviceIDs, id)
			mu.Unlock()
		}
	})
	return deviceIDs, result
}

func (c *loadTestClient) createNotifications(n, concurrency int, deviceIDs []string) ([]notificationRef, *latencyResult) {
	result := newLatencyResult()
	refs := make([]notificationRef, n)
	runConcurrent(n, concurrency, func(i int) {
		target := deviceIDs[rand.IntN(len(deviceIDs))] //nolint:gosec // picking a load-test target device, not a security-sensitive value
		body, _ := json.Marshal(map[string]any{
			"include_player_ids": []string{target},
			"title":              "load test",
			"body":               fmt.Sprintf("notification %d", i),
		})
		start := time.Now()
		id, err := c.postAndDecodeID(context.Background(), "/api/v1/notifications", body)
		result.record(time.Since(start), err)
		refs[i] = notificationRef{id: id, createdAt: start, createErr: err}
	})
	return refs, result
}

func (c *loadTestClient) pollCompletion(refs []notificationRef, timeout time.Duration) *latencyResult {
	result := newLatencyResult()
	var wg sync.WaitGroup
	for _, ref := range refs {
		if ref.createErr != nil {
			continue // never created successfully, nothing to poll
		}
		wg.Add(1)
		go func(ref notificationRef) {
			defer wg.Done()
			deadline := ref.createdAt.Add(timeout)
			for {
				done, err := c.notificationDone(context.Background(), ref.id)
				if err == nil && done {
					result.record(time.Since(ref.createdAt), nil)
					return
				}
				if time.Now().After(deadline) {
					result.record(0, fmt.Errorf("notification %s did not reach a terminal state within %v", ref.id, timeout))
					return
				}
				time.Sleep(250 * time.Millisecond)
			}
		}(ref)
	}
	wg.Wait()
	return result
}

func (c *loadTestClient) postAndDecodeID(ctx context.Context, path string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return out.ID, nil
}

// notificationDone reports whether every recipient under id has reached a
// terminal state. The notification's own status field only ever reaches
// "completed"/"failed" in the zero-recipient edge case — per-recipient
// outcomes never flip it back — so completion has to be read off
// total_recipients vs. the terminal entries in counts instead.
func (c *loadTestClient) notificationDone(ctx context.Context, id string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/notifications/"+id, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		TotalRecipients int32            `json:"total_recipients"`
		Counts          map[string]int64 `json:"counts"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	if out.TotalRecipients == 0 {
		return false, nil
	}
	var terminal int64
	for _, status := range []string{"sent", "delivered", "failed"} {
		terminal += out.Counts[status]
	}
	return terminal >= int64(out.TotalRecipients), nil
}

// runConcurrent calls fn(i) for i in [0,n), bounded to at most concurrency
// goroutines in flight at once.
func runConcurrent(n, concurrency int, fn func(i int)) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// latencyResult accumulates per-request latencies and errors for one phase,
// safe for concurrent use from runConcurrent's goroutines.
type latencyResult struct {
	mu         sync.Mutex
	latencies  []time.Duration
	errors     int
	sampleErrs []string
}

func newLatencyResult() *latencyResult {
	return &latencyResult{}
}

func (r *latencyResult) record(d time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.errors++
		if len(r.sampleErrs) < 5 {
			r.sampleErrs = append(r.sampleErrs, err.Error())
		}
		return
	}
	r.latencies = append(r.latencies, d)
}

func (r *latencyResult) print(label string, wallClock time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := len(r.latencies) + r.errors
	rps := float64(total) / wallClock.Seconds()
	fmt.Printf("  %s: %d ok, %d errors (of %d), %.1f req/s, wall clock %v\n",
		label, len(r.latencies), r.errors, total, rps, wallClock)
	if len(r.latencies) > 0 {
		sorted := append([]time.Duration(nil), r.latencies...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		fmt.Printf("  latency: p50=%v p95=%v p99=%v max=%v\n",
			percentile(sorted, 50), percentile(sorted, 95), percentile(sorted, 99), sorted[len(sorted)-1])
	}
	for _, e := range r.sampleErrs {
		fmt.Printf("  sample error: %s\n", e)
	}
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted) * p) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
