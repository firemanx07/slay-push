package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/config"
	"github.com/firemanx07/slay-push/internal/crypto"
	"github.com/firemanx07/slay-push/internal/platform"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// testDatabaseURL is this project's standing local dev Postgres — the same
// DSN config.Load() defaults to, reused across every package's tests.
const testDatabaseURL = "postgres://pushdispatch:pushdispatch@localhost:5432/pushdispatch?sslmode=disable" //nolint:gosec // well-known local dev DSN, not a secret

var (
	dbCheckOnce sync.Once
	dbCheckErr  error
)

// requireDB skips t if the standing dev Postgres isn't reachable.
func requireDB(t *testing.T) {
	t.Helper()
	dbCheckOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pool, err := platform.NewPostgresPool(ctx, testDatabaseURL)
		if err != nil {
			dbCheckErr = err
			return
		}
		pool.Close()
	})
	if dbCheckErr != nil {
		t.Skipf("postgres unreachable (set DATABASE_URL to run): %v", dbCheckErr)
	}
}

// openPool opens a fresh pool for verification/cleanup, independent of the
// pool each run* function opens and closes internally.
func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := platform.NewPostgresPool(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testMasterKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate master key: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	if _, err := crypto.LoadMasterKey(b64); err != nil {
		t.Fatalf("load master key: %v", err)
	}
	return b64
}

func TestUsage(t *testing.T) {
	got := usage()
	for _, want := range []string{"serve-all", "worker", "migrate", "healthcheck"} {
		if !strings.Contains(got, want) {
			t.Errorf("usage() = %q, want it to mention %q", got, want)
		}
	}
}

func TestPgx5MigrateURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"postgres scheme", "postgres://u:p@host/db", "pgx5://u:p@host/db"},
		{"postgresql scheme", "postgresql://u:p@host/db", "pgx5://u:p@host/db"},
		{"other scheme passthrough", "pgx5://already/converted", "pgx5://already/converted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pgx5MigrateURL(tt.in); got != tt.want {
				t.Errorf("pgx5MigrateURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// newHealthzListener binds an ephemeral loopback port serving /healthz with
// the given status, returning the listener (caller must Close) and the
// "host:port" address to point cfg.HTTPAddr at.
func newHealthzListener(t *testing.T, status int) (net.Listener, string) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	})
	go func() { _ = http.Serve(ln, mux) }() //nolint:gosec // test-only loopback listener, no real deployment
	return ln, ln.Addr().String()
}

func TestRunHealthcheckProbe_Success(t *testing.T) {
	ln, addr := newHealthzListener(t, http.StatusOK)
	defer func() { _ = ln.Close() }()

	if got := runHealthcheckProbe(config.Config{HTTPAddr: addr}); got != 0 {
		t.Errorf("runHealthcheckProbe = %d, want 0", got)
	}
}

func TestRunHealthcheckProbe_NonOKStatus(t *testing.T) {
	ln, addr := newHealthzListener(t, http.StatusServiceUnavailable)
	defer func() { _ = ln.Close() }()

	if got := runHealthcheckProbe(config.Config{HTTPAddr: addr}); got != 1 {
		t.Errorf("runHealthcheckProbe = %d, want 1", got)
	}
}

func TestRunHealthcheckProbe_ConnectionRefused(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens here now — connection refused

	if got := runHealthcheckProbe(config.Config{HTTPAddr: addr}); got != 1 {
		t.Errorf("runHealthcheckProbe = %d, want 1", got)
	}
}

func TestRunHealthcheckProbe_MalformedAddrFallsBackToDefaultPort(t *testing.T) {
	t.Helper()
	// No colon, so net.SplitHostPort fails and the function falls back to
	// port 8080. We don't assert the resulting code — whatever's listening
	// on 8080 on the machine running this test is out of our control — only
	// that the fallback branch runs without panicking.
	_ = runHealthcheckProbe(config.Config{HTTPAddr: "not-a-valid-addr"})
}

func TestRunCreateProject_MissingFlags(t *testing.T) {
	cfg := config.Config{DatabaseURL: testDatabaseURL}
	if err := runCreateProject(cfg, zerolog.Nop(), []string{}); err == nil {
		t.Fatal("expected error for missing --name/--slug")
	}
}

func TestRunCreateProject_Success(t *testing.T) {
	requireDB(t)
	pool := openPool(t)
	slug := "cmdtest-" + uuid.NewString()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `delete from projects where slug = $1`, slug) })

	cfg := config.Config{DatabaseURL: testDatabaseURL}
	if err := runCreateProject(cfg, zerolog.Nop(), []string{"--name", "CMD Test", "--slug", slug}); err != nil {
		t.Fatalf("runCreateProject: %v", err)
	}

	q := postgres.New(pool)
	project, err := q.GetProjectBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	if project.Name != "CMD Test" {
		t.Errorf("Name = %q, want %q", project.Name, "CMD Test")
	}
}

func TestRunCreateAPIKey_UnknownProject(t *testing.T) {
	requireDB(t)
	cfg := config.Config{DatabaseURL: testDatabaseURL}
	err := runCreateAPIKey(cfg, zerolog.Nop(), []string{"--project", "no-such-project-" + uuid.NewString(), "--name", "k"})
	if err == nil {
		t.Fatal("expected error for an unknown project slug")
	}
}

func TestRunCreateAPIKey_InvalidScope(t *testing.T) {
	cfg := config.Config{DatabaseURL: testDatabaseURL}
	err := runCreateAPIKey(cfg, zerolog.Nop(), []string{"--project", "x", "--name", "k", "--scope", "not-a-scope"})
	if err == nil {
		t.Fatal("expected error for an invalid --scope")
	}
}

func TestRunCreateAPIKey_Success(t *testing.T) {
	requireDB(t)
	pool := openPool(t)
	slug := "cmdtest-" + uuid.NewString()
	cfg := config.Config{DatabaseURL: testDatabaseURL}
	if err := runCreateProject(cfg, zerolog.Nop(), []string{"--name", "CMD Test", "--slug", slug}); err != nil {
		t.Fatalf("runCreateProject: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `delete from api_keys where project_id = (select id from projects where slug = $1)`, slug)
		_, _ = pool.Exec(ctx, `delete from projects where slug = $1`, slug)
	})

	if err := runCreateAPIKey(cfg, zerolog.Nop(), []string{"--project", slug, "--name", "cmd test key", "--scope", "read"}); err != nil {
		t.Fatalf("runCreateAPIKey: %v", err)
	}

	q := postgres.New(pool)
	project, err := q.GetProjectBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	keys, err := q.ListAPIKeysByProject(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("ListAPIKeysByProject: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "cmd test key" || keys[0].Scope != "read" {
		t.Errorf("got keys %+v, want exactly one named %q scoped %q", keys, "cmd test key", "read")
	}
}

func TestRunSeedCredential_MissingFile(t *testing.T) {
	cfg := config.Config{DatabaseURL: testDatabaseURL, MasterKey: testMasterKey(t)}
	if err := runSeedCredential(cfg, zerolog.Nop(), []string{}); err == nil {
		t.Fatal("expected error for missing --file")
	}
}

func TestRunSeedCredential_UnknownProject(t *testing.T) {
	requireDB(t)
	credFile := filepath.Join(t.TempDir(), "cred.json")
	if err := os.WriteFile(credFile, []byte(`{"project_id":"x"}`), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	cfg := config.Config{DatabaseURL: testDatabaseURL, MasterKey: testMasterKey(t)}
	err := runSeedCredential(cfg, zerolog.Nop(), []string{"--file", credFile, "--project", "no-such-project-" + uuid.NewString()})
	if err == nil {
		t.Fatal("expected error for an unknown project slug")
	}
}

func TestRunSeedCredential_MalformedJSON(t *testing.T) {
	credFile := filepath.Join(t.TempDir(), "cred.json")
	if err := os.WriteFile(credFile, []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	cfg := config.Config{DatabaseURL: testDatabaseURL, MasterKey: testMasterKey(t)}
	err := runSeedCredential(cfg, zerolog.Nop(), []string{"--file", credFile, "--project", "irrelevant"})
	if err == nil {
		t.Fatal("expected error for a malformed credential file")
	}
}

func TestRunSeedCredential_Success(t *testing.T) {
	requireDB(t)
	pool := openPool(t)
	slug := "cmdtest-" + uuid.NewString()
	masterKeyB64 := testMasterKey(t)
	cfg := config.Config{DatabaseURL: testDatabaseURL, MasterKey: masterKeyB64}
	if err := runCreateProject(cfg, zerolog.Nop(), []string{"--name", "CMD Test", "--slug", slug}); err != nil {
		t.Fatalf("runCreateProject: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `delete from provider_credentials where project_id = (select id from projects where slug = $1)`, slug)
		_, _ = pool.Exec(ctx, `delete from projects where slug = $1`, slug)
	})

	credFile := filepath.Join(t.TempDir(), "cred.json")
	rawCred := []byte(`{"project_id":"test-project","private_key":"fake"}`)
	if err := os.WriteFile(credFile, rawCred, 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}

	if err := runSeedCredential(cfg, zerolog.Nop(), []string{"--provider", "fcm", "--file", credFile, "--project", slug}); err != nil {
		t.Fatalf("runSeedCredential: %v", err)
	}

	q := postgres.New(pool)
	project, err := q.GetProjectBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	creds, err := q.ListProviderCredentialsByProject(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("ListProviderCredentialsByProject: %v", err)
	}
	if len(creds) != 1 || creds[0].ProviderType != "fcm" {
		t.Fatalf("got credentials %+v, want exactly one for provider fcm", creds)
	}

	// Round-trip: the stored ciphertext must decrypt back to the original
	// credential bytes under the same master key runSeedCredential used.
	masterKey, err := crypto.LoadMasterKey(masterKeyB64)
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}
	fullCred, err := q.GetActiveProviderCredential(context.Background(), postgres.GetActiveProviderCredentialParams{
		ProjectID: project.ID, ProviderType: "fcm", Environment: "production",
	})
	if err != nil {
		t.Fatalf("GetActiveProviderCredential: %v", err)
	}
	decrypted, err := masterKey.Open(fullCred.WrappedDek, fullCred.Credential)
	if err != nil {
		t.Fatalf("decrypt seeded credential: %v", err)
	}
	if string(decrypted) != string(rawCred) {
		t.Errorf("decrypted credential = %q, want %q", decrypted, rawCred)
	}
}

func TestRunBootstrap_MissingEnv(t *testing.T) {
	cfg := config.Config{DatabaseURL: testDatabaseURL}
	if err := runBootstrap(cfg, zerolog.Nop()); err == nil {
		t.Fatal("expected error when BOOTSTRAP_ADMIN_EMAIL/PASSWORD are unset")
	}
}

func TestRunBootstrap_IdempotentAcrossRepeatCalls(t *testing.T) {
	requireDB(t)
	pool := openPool(t)
	email := "cmdtest-" + uuid.NewString() + "@example.com"
	t.Setenv("BOOTSTRAP_ADMIN_EMAIL", email)
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "test-password-12345")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `delete from users where email = $1`, email) })

	cfg := config.Config{DatabaseURL: testDatabaseURL}
	// Whichever branch fires first (create, or skip because the shared dev
	// DB already has an admin) is fine — both must return nil, and a second
	// call must also return nil without erroring on a duplicate.
	if err := runBootstrap(cfg, zerolog.Nop()); err != nil {
		t.Fatalf("first runBootstrap: %v", err)
	}
	if err := runBootstrap(cfg, zerolog.Nop()); err != nil {
		t.Fatalf("second runBootstrap (should be idempotent): %v", err)
	}
}

func TestRunMigrate_NoChange(t *testing.T) {
	requireDB(t)

	// runMigrate hardcodes "file://migrations", relative to the repo root —
	// go test's working directory is this package's directory, so point it
	// there for the duration of this test.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	cfg := config.Config{DatabaseURL: testDatabaseURL}
	if err := runMigrate(cfg, zerolog.Nop()); err != nil {
		t.Fatalf("runMigrate: %v", err)
	}
}
