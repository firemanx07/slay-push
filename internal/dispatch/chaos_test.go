package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// waitForLedgerEntries polls until the ledger at path holds at least min
// entries. On timeout it returns an error stating how many it holds.
func waitForLedgerEntries(t *testing.T, path string, min int, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		n := len(readLedger(t, path))
		if n >= min {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no sends in flight: ledger has %d entries, want at least %d after %s", n, min, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWaitForLedgerEntries_EmptyLedgerTimesOutNamingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create ledger: %v", err)
	}

	err := waitForLedgerEntries(t, path, 1, 100*time.Millisecond)
	if err == nil {
		t.Fatal("waitForLedgerEntries on an empty ledger returned nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "no sends in flight") || !strings.Contains(err.Error(), "ledger has 0 entries") {
		t.Errorf("error = %q, want it to name the empty ledger", err)
	}
}

func TestWaitForLedgerEntries_ReturnsOnceEnoughEntriesExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.log")
	if err := os.WriteFile(path, []byte("w-1\ta\nw-1\tb\n"), 0o600); err != nil {
		t.Fatalf("create ledger: %v", err)
	}

	if err := waitForLedgerEntries(t, path, 2, time.Second); err != nil {
		t.Errorf("waitForLedgerEntries with 2 entries present = %v, want nil", err)
	}
}

// TestChaos_KillMidSend kills a worker with SIGKILL while it holds sends in
// flight and asserts a second worker finishes the notification: no recipient
// is lost, and exactly the in-flight recipients receive a second provider
// call.
func TestChaos_KillMidSend(t *testing.T) {
	requireChaos(t)
	const (
		total       = 60
		concurrency = 10
	)
	h := newLiveHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	mustCreateCredential(t, ctx, h.queries, h.handlers.Crypto, projectID)

	ids, tokens := seedDevices(t, ctx, h, projectID, total)
	n, err := h.handlers.CreateNotification(ctx, projectID, CreateNotificationRequest{DeviceIDs: ids, Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if err := fanoutNotification(ctx, h, n); err != nil {
		t.Fatalf("fanout: %v", err)
	}
	ledger := filepath.Join(t.TempDir(), "ledger.log")

	killed := startWorkerProc(t, h, ledger, "block", concurrency)
	if err := waitForLedgerEntries(t, ledger, concurrency, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	killed.Kill(t)
	inFlight := len(readLedger(t, ledger))

	startWorkerProc(t, h, ledger, "record", concurrency)
	t.Logf("waiting up to 4m: asynq's recoverer re-queues a killed worker's tasks about once a minute")
	counts := waitForTerminalRecipients(t, ctx, h, projectID, total, 4*time.Minute)
	waitForCompletedNotifications(t, ctx, h, projectID, 10*time.Second)

	calls := map[string]int{}
	for _, e := range readLedger(t, ledger) {
		calls[e.Token]++
	}
	duplicates := 0
	for _, token := range tokens {
		switch c := calls[token]; c {
		case 0:
			t.Errorf("recipient %s was never sent", token)
		case 1:
		case 2:
			duplicates++
		default:
			t.Errorf("recipient %s was sent %d times, want at most 2", token, c)
		}
	}
	if counts["sent"] != total {
		t.Errorf("sent recipients = %d, want %d (counts=%v)", counts["sent"], total, counts)
	}
	if duplicates != inFlight {
		t.Errorf("recipients sent twice = %d, want %d (the sends in flight when the worker was killed)", duplicates, inFlight)
	}
	t.Logf("kill mid-send: %d of %d recipients were sent twice; the killed worker had %d sends in flight", duplicates, total, inFlight)
}
