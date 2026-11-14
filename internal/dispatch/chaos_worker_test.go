package dispatch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/crypto"
	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

const (
	envChaos             = "SLAYPUSH_CHAOS"
	envWorkerMode        = "SLAYPUSH_TEST_WORKER"
	envWorkerMasterKey   = "SLAYPUSH_TEST_MASTER_KEY"
	envWorkerLedger      = "SLAYPUSH_TEST_LEDGER"
	envWorkerID          = "SLAYPUSH_TEST_WORKER_ID"
	envWorkerSendMode    = "SLAYPUSH_TEST_SEND_MODE"
	envWorkerConcurrency = "SLAYPUSH_TEST_CONCURRENCY"
	workerReadyLine      = "ready"
)

// TestMain turns the test binary into a send worker when
// SLAYPUSH_TEST_WORKER=1, so chaos tests can start and kill real worker
// processes. Otherwise it runs the package's tests.
func TestMain(m *testing.M) {
	if os.Getenv(envWorkerMode) == "1" {
		os.Exit(runWorkerProcess())
	}
	os.Exit(m.Run())
}

// runWorkerProcess runs an asynq worker on the send:expo queue until its
// stdin closes, printing workerReadyLine once it is polling. Every provider
// call is appended to the ledger file as "<worker id>\t<token>\n".
func runWorkerProcess() int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "worker process:", err)
		return 1
	}
	concurrency, err := strconv.Atoi(os.Getenv(envWorkerConcurrency))
	if err != nil || concurrency <= 0 {
		return fail(fmt.Errorf("invalid %s %q", envWorkerConcurrency, os.Getenv(envWorkerConcurrency)))
	}
	mk, err := crypto.LoadMasterKey(os.Getenv(envWorkerMasterKey))
	if err != nil {
		return fail(fmt.Errorf("load master key: %w", err))
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv(envDatabaseURL))
	if err != nil {
		return fail(fmt.Errorf("connect postgres: %w", err))
	}
	defer pool.Close()
	redisOpt, err := queue.ParseRedisOpt(os.Getenv(envRedisURL))
	if err != nil {
		return fail(fmt.Errorf("parse redis url: %w", err))
	}
	behavior, err := workerBehavior(os.Getenv(envWorkerLedger), os.Getenv(envWorkerID), os.Getenv(envWorkerSendMode))
	if err != nil {
		return fail(err)
	}
	testAdapter.SetBehavior(behavior)

	h := &Handlers{DB: postgres.New(pool), Crypto: mk, Logger: zerolog.Nop()}
	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.SendTypeFor("expo"), h.AsynqSendHandler)
	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:              concurrency,
		Queues:                   map[string]int{queue.SendTypeFor("expo"): 1},
		RetryDelayFunc:           queue.RetryDelayFunc,
		IsFailure:                queue.IsFailure,
		DelayedTaskCheckInterval: 100 * time.Millisecond,
		TaskCheckInterval:        20 * time.Millisecond,
		LogLevel:                 asynq.ErrorLevel,
	})
	if err := srv.Start(mux); err != nil {
		return fail(fmt.Errorf("start asynq server: %w", err))
	}
	fmt.Println(workerReadyLine)
	_, _ = io.Copy(io.Discard, os.Stdin)
	srv.Shutdown()
	return 0
}

// workerBehavior returns the provider behavior for a worker process: append
// one ledger line, then act on mode. "record" returns sent, "block" never
// returns, "delay:<ms>" sleeps that long and returns sent.
func workerBehavior(ledgerPath, workerID, mode string) (func(provider.SendRequest) (provider.SendResult, error), error) {
	f, err := os.OpenFile(ledgerPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // ledger path is set by the parent test
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	var (
		block bool
		delay time.Duration
	)
	switch {
	case mode == "record":
	case mode == "block":
		block = true
	case strings.HasPrefix(mode, "delay:"):
		ms, err := strconv.Atoi(strings.TrimPrefix(mode, "delay:"))
		if err != nil || ms < 0 {
			return nil, fmt.Errorf("invalid send mode %q", mode)
		}
		delay = time.Duration(ms) * time.Millisecond
	default:
		return nil, fmt.Errorf("invalid send mode %q", mode)
	}
	return func(req provider.SendRequest) (provider.SendResult, error) {
		if _, err := f.Write([]byte(workerID + "\t" + req.Token + "\n")); err != nil {
			return provider.SendResult{Status: provider.StatusTransientError}, err
		}
		if block {
			select {}
		}
		time.Sleep(delay)
		return provider.SendResult{Status: provider.StatusSent, ProviderMessageID: "chaos-" + req.Token}, nil
	}, nil
}

// requireChaos skips t unless SLAYPUSH_CHAOS=1.
func requireChaos(t *testing.T) {
	t.Helper()
	if os.Getenv(envChaos) != "1" {
		t.Skipf("set %s=1 to run chaos tests", envChaos)
	}
}

// workerProc is a running worker child process.
type workerProc struct {
	cmd   *exec.Cmd
	id    string
	stdin io.WriteCloser
	once  sync.Once
}

// startWorkerProc starts a worker child process that records its provider
// calls in ledger and acts on mode (see workerBehavior), and waits until it
// reports ready. The process is killed and reaped when t finishes.
func startWorkerProc(t *testing.T, h *testHarness, ledger, mode string, concurrency int) *workerProc {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	id := "w-" + uuid.NewString()[:8]
	cmd := exec.CommandContext(context.Background(), exe, "-test.run=^$") //nolint:gosec // re-executes this test binary
	cmd.Env = append(os.Environ(),
		envWorkerMode+"=1",
		envWorkerMasterKey+"="+h.masterKeyB64,
		envWorkerLedger+"="+ledger,
		envWorkerID+"="+id,
		envWorkerSendMode+"="+mode,
		envWorkerConcurrency+"="+strconv.Itoa(concurrency),
	)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("worker stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("worker stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker process: %v", err)
	}
	w := &workerProc{cmd: cmd, id: id, stdin: stdin}
	t.Cleanup(func() { w.Kill(t) })

	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		switch {
		case err != nil:
			ready <- fmt.Errorf("read ready line: %w", err)
		case strings.TrimSpace(line) != workerReadyLine:
			ready <- fmt.Errorf("unexpected first line %q", line)
		default:
			ready <- nil
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("worker %s failed to start: %v", id, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("worker %s did not report ready within 30s", id)
	}
	return w
}

// Kill sends SIGKILL to the worker process and waits for it to be reaped.
// Calling it again is a no-op.
func (w *workerProc) Kill(t *testing.T) {
	t.Helper()
	w.once.Do(func() {
		_ = w.cmd.Process.Kill()
		_ = w.cmd.Wait()
	})
}

// ledgerEntry is one provider call recorded by a worker process.
type ledgerEntry struct {
	WorkerID string
	Token    string
}

// readLedger returns every complete line of the ledger file; a missing file
// is an empty ledger and a trailing partial line is ignored.
func readLedger(t *testing.T, path string) []ledgerEntry {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // ledger path is created by the calling test
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(string(b), "\n")
	lines = lines[:len(lines)-1]
	entries := make([]ledgerEntry, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		entries = append(entries, ledgerEntry{WorkerID: parts[0], Token: parts[1]})
	}
	return entries
}

func TestWorkerProc_ProcessesRecordsAndIsReaped(t *testing.T) {
	requireChaos(t)
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	const total = 5
	ids, tokens := seedDevices(t, ctx, h, projectID, total)
	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: ids, Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := fanoutNotification(ctx, h, n); err != nil {
		t.Fatalf("fanout: %v", err)
	}

	ledger := filepath.Join(t.TempDir(), "ledger.log")
	w := startWorkerProc(t, h, ledger, "record", 4)
	waitForTerminalRecipients(t, ctx, h, projectID, total, 30*time.Second)

	entries := readLedger(t, ledger)
	if len(entries) != total {
		t.Fatalf("ledger entries = %d, want %d", len(entries), total)
	}
	seen := map[string]int{}
	for _, e := range entries {
		if e.WorkerID != w.id {
			t.Errorf("ledger worker id = %q, want %q", e.WorkerID, w.id)
		}
		seen[e.Token]++
	}
	for _, token := range tokens {
		if seen[token] != 1 {
			t.Errorf("ledger calls for %s = %d, want 1", token, seen[token])
		}
	}

	w.Kill(t)
	if w.cmd.ProcessState == nil {
		t.Fatal("worker process was not reaped")
	}
	if w.cmd.ProcessState.Exited() {
		t.Errorf("worker exited normally (%v), want killed by signal", w.cmd.ProcessState)
	}
}

func TestWorkerProc_ExitsWhenStdinCloses(t *testing.T) {
	requireChaos(t)
	h := newLiveHarness(t)
	w := startWorkerProc(t, h, filepath.Join(t.TempDir(), "ledger.log"), "record", 2)

	if err := w.stdin.Close(); err != nil {
		t.Fatalf("close worker stdin: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- w.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("worker exit = %v, want a clean exit", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("worker still running 20s after its stdin closed")
	}
}
