package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/crypto"
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
	"github.com/firemanx07/slay-push/internal/targeting"
)

const (
	envDatabaseURL = "DATABASE_URL"
	envRedisURL    = "REDIS_URL"
)

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error

	clientOnce  sync.Once
	asynqClient *asynq.Client
	redisOpt    asynq.RedisConnOpt
	clientErr   error
)

// requireInfra skips t if Postgres or Redis aren't reachable via
// DATABASE_URL/REDIS_URL — the same env vars internal/config reads, so
// these tests run against the standing local dev stack with no extra
// setup, and against CI's service containers there.
func requireInfra(t *testing.T) (*pgxpool.Pool, *asynq.Client, asynq.RedisConnOpt) {
	t.Helper()
	poolOnce.Do(func() {
		dsn := os.Getenv(envDatabaseURL)
		if dsn == "" {
			poolErr = errors.New("DATABASE_URL not set")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pool, poolErr = pgxpool.New(ctx, dsn)
		if poolErr == nil {
			poolErr = pool.Ping(ctx)
		}
	})
	clientOnce.Do(func() {
		redisURL := os.Getenv(envRedisURL)
		if redisURL == "" {
			clientErr = errors.New("REDIS_URL not set")
			return
		}
		redisOpt, clientErr = queue.ParseRedisOpt(redisURL)
		if clientErr == nil {
			asynqClient = asynq.NewClient(redisOpt)
			clientErr = asynqClient.Ping()
		}
	})
	if poolErr != nil {
		t.Skipf("postgres unreachable (set %s to run): %v", envDatabaseURL, poolErr)
	}
	if clientErr != nil {
		t.Skipf("redis unreachable (set %s to run): %v", envRedisURL, clientErr)
	}
	return pool, asynqClient, redisOpt
}

// testHarness bundles everything a test needs: a ready Handlers, the
// queries handle it shares (tx-scoped in Mode A, pool-scoped in Mode B),
// and the fresh test project everything is scoped under.
type testHarness struct {
	handlers *Handlers
	queries  *postgres.Queries
	pool     *pgxpool.Pool
	client   *asynq.Client
	redisOpt asynq.RedisConnOpt
	project  postgres.Project
}

// newTestHarness (Mode A): single goroutine, no live asynq.Server — wraps
// Postgres access in one pgx.Tx rolled back at test end. Do not use this
// for anything involving a second goroutine or a live worker (see
// newLiveHarness).
func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	p, client, ropt := requireInfra(t)
	ctx := context.Background()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	q := postgres.New(p).WithTx(tx)
	return buildHarness(t, ctx, p, client, ropt, q)
}

// newLiveHarness (Mode B): committed fixtures, explicit cleanup. Required
// whenever a live asynq.Server or a second goroutine must see the rows —
// a tx on one connection is invisible to a worker on another connection,
// and a single pgx.Tx can't be used concurrently from two goroutines.
func newLiveHarness(t *testing.T) *testHarness {
	t.Helper()
	p, client, ropt := requireInfra(t)
	ctx := context.Background()
	q := postgres.New(p)
	h := buildHarness(t, ctx, p, client, ropt, q)
	t.Cleanup(func() { cleanupProjectRows(ctx, p, postgres.UUIDTo(h.project.ID)) })
	return h
}

func buildHarness(t *testing.T, ctx context.Context, p *pgxpool.Pool, client *asynq.Client, ropt asynq.RedisConnOpt, q *postgres.Queries) *testHarness {
	t.Helper()
	project, err := q.CreateProject(ctx, postgres.CreateProjectParams{
		Name: "integration-test",
		Slug: "itest-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create test project: %v", err)
	}
	h := &Handlers{
		DB:        q,
		Queue:     client,
		Targeting: targeting.NewRegistry(q),
		Crypto:    testMasterKey(t),
		Logger:    zerolog.Nop(),
	}
	return &testHarness{handlers: h, queries: q, pool: p, client: client, redisOpt: ropt, project: project}
}

// cleanupProjectRows deletes everything under projectID, in FK order.
// Best-effort — a leftover row in a shared dev DB is harmless, and
// t.Cleanup can't fail the test anyway.
func cleanupProjectRows(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) {
	_, _ = pool.Exec(ctx, `delete from notification_recipients where notification_id in (select id from notifications where project_id = $1)`, projectID)
	_, _ = pool.Exec(ctx, `delete from notifications where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from devices where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from provider_credentials where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from subscribers where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from projects where id = $1`, projectID)
}

// mustCreateDevice registers a device with an explicit token, no
// subscriber — enough for the explicit-device-ID targeting these tests use.
func mustCreateDevice(t *testing.T, ctx context.Context, q *postgres.Queries, projectID uuid.UUID, token string) postgres.Device {
	t.Helper()
	device, err := q.UpsertDevice(ctx, postgres.UpsertDeviceParams{
		ProjectID:    postgres.UUIDFrom(projectID),
		Token:        token,
		Platform:     "android",
		ProviderType: "expo",
		Metadata:     []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create test device: %v", err)
	}
	return device
}

// mustCreateCredential inserts an active "expo"/"production" credential
// row, encrypted with mk. The fake adapter never parses the credential
// payload, so its shape is irrelevant.
func mustCreateCredential(t *testing.T, ctx context.Context, q *postgres.Queries, mk crypto.MasterKey, projectID uuid.UUID) {
	t.Helper()
	wrappedDEK, ciphertext, err := mk.Seal([]byte(`{"access_token":"fake"}`))
	if err != nil {
		t.Fatalf("seal test credential: %v", err)
	}
	if _, err := q.UpsertProviderCredential(ctx, postgres.UpsertProviderCredentialParams{
		ProjectID:    postgres.UUIDFrom(projectID),
		ProviderType: "expo",
		Environment:  "production",
		Credential:   ciphertext,
		WrappedDek:   wrappedDEK,
	}); err != nil {
		t.Fatalf("create test credential: %v", err)
	}
}

// testMasterKey generates a fresh random master key for one test. Each
// harness gets its own — never share one across harnesses/tests, since a
// credential sealed with one key can't be opened with another.
func testMasterKey(t *testing.T) crypto.MasterKey {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate master key: %v", err)
	}
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}
	return mk
}

// cleanupSendTask registers a t.Cleanup that deletes the send:expo task
// for recipientID (its asynq.TaskID) and closes the Inspector used to do
// it — asynq.NewInspector owns its own Redis connection pool, so it must
// be closed, not just discarded, once we're done with it.
func cleanupSendTask(t *testing.T, redisOpt asynq.RedisConnOpt, recipientID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		inspector := asynq.NewInspector(redisOpt)
		defer func() { _ = inspector.Close() }()
		_ = inspector.DeleteTask(queue.SendTypeFor("expo"), recipientID.String())
	})
}
