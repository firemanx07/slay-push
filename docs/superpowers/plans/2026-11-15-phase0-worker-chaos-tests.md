# Phase 0 worker chaos tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in tests that kill a worker process mid-send and that run 1, 2 and 4 worker processes against one workload, and record the measured results in the docs.

**Spec:** `docs/superpowers/specs/2026-11-13-platform-roadmap-design.md` (Phase 0 test plan: "Worker killed mid-send", "Horizontal scaling").

## Global Constraints

- Chaos tests skip unless `SLAYPUSH_CHAOS=1`, and skip when `DATABASE_URL` or `REDIS_URL` is unreachable.
- Worker processes are child processes of the test binary. The test that starts one kills and reaps it in `t.Cleanup`. A child also exits when its stdin closes.
- Child workers consume only the `send:expo` queue. Fanout runs in the test process through `fanoutNotification`.
- Provider calls are recorded in a ledger file, one `<worker-id>\t<token>\n` line per call, written with a single `O_APPEND` write.
- Assertions are scoped to the test project's recipients.
- Code comments are doc-only: behavior and contract, no rationale.
- Each task ends with `go build ./... && go vet ./... && gofmt -l .` (prints nothing), `go test ./internal/dispatch/ -count=1 -race` and `golangci-lint run ./...` clean.

## Review Focus

1. Kill lands when no send is in flight: the test fails with a message naming the empty ledger, and does not hang.
2. Parent test process dies: the child worker exits when stdin closes.
3. Recovery slower than the timeout: asynq's recoverer runs every 1 minute, so the drain timeout is 4 minutes and the failure message names the recoverer interval.
4. Ledger lines interleave: each line is one `O_APPEND` write, and the reader ignores a trailing partial line.
5. Tasks left in `send:expo` by earlier runs: workers fail them harmlessly, and assertions only count this project's recipients.

---

### Task 1: Behavior hook on the fake adapter

**Files:**
- Modify: `internal/dispatch/fake_adapter_test.go`

**Interfaces:**
- Produces: `func (f *fakeAdapter) SetBehavior(fn func(req provider.SendRequest) (provider.SendResult, error))`. While set, `Send` returns `fn(req)` and skips scripts and call counting. `SetBehavior(nil)` restores script behavior.

- [ ] **Step 1: Write the failing test `TestFakeAdapter_BehaviorOverridesScript` in `fake_adapter_test.go`.** Program token T with a transient-error step. Call `SetBehavior` returning `StatusSent` with `ProviderMessageID: "hook"`. Assert `Send` returns that result. Call `SetBehavior(nil)`. Assert `Send` returns the transient-error step.
- [ ] **Step 2: Run `go test ./internal/dispatch/ -run TestFakeAdapter_BehaviorOverridesScript`. Expect a compile failure: `SetBehavior` undefined.**
- [ ] **Step 3: Add the `behavior` field and `SetBehavior`.** Guard both with `f.mu`. `Send` checks `behavior` first.
- [ ] **Step 4: Run the test and the full package. Expect PASS.**
- [ ] **Step 5: Commit `Add a behavior hook to the fake provider adapter`.**

### Task 2: Child worker process harness

**Files:**
- Create: `internal/dispatch/chaos_worker_test.go`
- Test: same file

**Interfaces:**
- Consumes: `testHarness`, `testAdapter.SetBehavior` (Task 1), `queue.SendTypeFor`, `queue.RetryDelayFunc`, `queue.IsFailure`.
- Produces:
  - `func requireChaos(t *testing.T)` — skips unless `SLAYPUSH_CHAOS=1`.
  - `type workerProc struct { cmd *exec.Cmd; id string }`
  - `func startWorkerProc(t *testing.T, h *testHarness, ledger, mode string, concurrency int) *workerProc` — `mode` is `record`, `block` or `delay:<ms>`. Registers `t.Cleanup` that kills and reaps the process.
  - `func (w *workerProc) Kill(t *testing.T)` — SIGKILL, then `Wait`.
  - `type ledgerEntry struct { WorkerID, Token string }`
  - `func readLedger(t *testing.T, path string) []ledgerEntry` — skips a trailing partial line.
  - `func TestMain(m *testing.M)` — when `SLAYPUSH_TEST_WORKER=1`, runs the worker and exits; otherwise `os.Exit(m.Run())`.
- Child environment: `SLAYPUSH_TEST_WORKER`, `SLAYPUSH_TEST_MASTER_KEY` (base64), `SLAYPUSH_TEST_LEDGER`, `SLAYPUSH_TEST_WORKER_ID`, `SLAYPUSH_TEST_SEND_MODE`, `SLAYPUSH_TEST_CONCURRENCY`, plus the parent's `DATABASE_URL` and `REDIS_URL`.

- [ ] **Step 1: Write the failing test `TestWorkerProc_ProcessesRecordsAndIsReaped`.** `requireChaos`. Live harness, credential, 5 seeded devices, one notification fanned out with `fanoutNotification`. Start one `record` worker. `waitForTerminalRecipients` for 5. Assert `readLedger` has exactly 5 entries, each token once, all with the worker's id. `Kill`. Assert `cmd.ProcessState != nil` and `cmd.ProcessState.Exited()` is false (signaled).
- [ ] **Step 2: Run it with `SLAYPUSH_CHAOS=1`. Expect a compile failure: harness symbols undefined.**
- [ ] **Step 3: Implement the child side.** `TestMain` builds `Handlers` from the env (`crypto.LoadMasterKey`, `platform` Postgres and Redis setup used by `requireInfra`), sets the adapter behavior (append ledger line, then `record` returns `StatusSent`, `block` blocks forever, `delay:<ms>` sleeps then returns `StatusSent`), starts an `asynq.Server` on `send:expo` with the given concurrency and the same retry and `IsFailure` settings as `startLiveWorker`, and shuts it down when stdin reaches EOF.
- [ ] **Step 4: Implement the parent side.** `startWorkerProc` re-executes `os.Args[0]` with `-test.run=^$`, passes the child environment, keeps a stdin pipe open for the child's lifetime, and registers the cleanup. The master key comes from the harness: expose it by adding a `masterKeyB64 string` field to `testHarness`, set in `buildHarness`.
- [ ] **Step 5: Run the test with `SLAYPUSH_CHAOS=1`. Expect PASS. Run the package without it. Expect the chaos test skipped.**
- [ ] **Step 6: Commit `Add a child-process worker harness for chaos tests`.**

### Task 3: Kill a worker mid-send

**Files:**
- Create: `internal/dispatch/chaos_test.go`
- Test: same file

**Interfaces:**
- Consumes: `startWorkerProc`, `workerProc.Kill`, `readLedger`, `requireChaos` (Task 2); `seedDevices`, `fanoutNotification`, `waitForTerminalRecipients`, `waitForCompletedNotifications`.

- [ ] **Step 1: Write `TestChaos_KillMidSend`.** `requireChaos`. 60 devices, one notification, `fanoutNotification`. Start worker A in `block` mode, concurrency 10. Poll the ledger until it has at least 10 entries (30 s limit); on timeout fail with "no sends in flight: ledger empty". `A.Kill`. Start worker B in `record` mode, concurrency 10. `waitForTerminalRecipients` for 60 with a 4 minute limit; the failure message states that asynq's recoverer runs every 1 minute. `waitForCompletedNotifications` (10 s). Read the ledger and count calls per token. Assert: all 60 recipients `sent`; every token has 1 or 2 calls; tokens with 2 calls number at most 10; every token appears at least once. `t.Logf` the duplicate count.
- [ ] **Step 2: Run `SLAYPUSH_CHAOS=1 go test ./internal/dispatch/ -run TestChaos_KillMidSend -count=1 -race -v -timeout 6m`. Expect PASS and a logged duplicate count of 1 to 10.**
- [ ] **Step 3: Run the same test with worker B's mode changed temporarily to `block`. Expect FAIL at the drain timeout. Revert.**
- [ ] **Step 4: Run the package without `SLAYPUSH_CHAOS`. Expect the test skipped and everything else passing.**
- [ ] **Step 5: Commit `Add a kill -9 mid-send chaos test`.**

### Task 4: Worker scaling and recorded results

**Files:**
- Modify: `internal/dispatch/chaos_test.go`
- Modify: `docs/content/docs/load-testing.md`

**Interfaces:**
- Consumes: Task 2 and Task 3 helpers.

- [ ] **Step 1: Write `TestChaos_WorkerScaling` with subtests for 1, 2 and 4 workers.** Each subtest: fresh live harness, credential, 1500 devices, one notification fanned out, `W` workers in `delay:20` mode with concurrency 10. Time the drain with a 2 minute limit. Assert: all recipients `sent`; every token called exactly once; every worker id appears in the ledger at least once. `t.Logf` workers, drain time and recipients per second.
- [ ] **Step 2: Run `SLAYPUSH_CHAOS=1 go test ./internal/dispatch/ -run TestChaos_WorkerScaling -count=1 -race -v -timeout 10m`. Expect PASS with three logged lines.**
- [ ] **Step 3: Run `SLAYPUSH_CHAOS=1 go test ./internal/dispatch/ -run TestChaos -count=1 -v -timeout 12m` without `-race` for the numbers to publish.**
- [ ] **Step 4: Add a "Worker chaos results" section to `docs/content/docs/load-testing.md`.** State the machine type, the commands from Steps 2 and 3, the duplicate count from the kill test, and a table of workers against drain time and recipients per second. Facts only.
- [ ] **Step 5: Run the full verification from Global Constraints. Commit `Add a worker scaling chaos test and record results`.**
