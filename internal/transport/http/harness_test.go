package http

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
	goredis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/apikey"
	"github.com/firemanx07/slay-push/internal/crypto"
	"github.com/firemanx07/slay-push/internal/dispatch"
	_ "github.com/firemanx07/slay-push/internal/provider/expo" // registers "expo" for provider.Known/Get; these tests never call Send
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

	redisOnce   sync.Once
	rawRedisURL string
	redisErr    error
)

// requireInfra skips t if Postgres or Redis aren't reachable via
// DATABASE_URL/REDIS_URL, mirroring internal/dispatch's harness — these
// tests run against the standing local dev stack with no extra setup, and
// against CI's service containers there.
func requireInfra(t *testing.T) (*pgxpool.Pool, string) {
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
	redisOnce.Do(func() {
		rawRedisURL = os.Getenv(envRedisURL)
		if rawRedisURL == "" {
			redisErr = errors.New("REDIS_URL not set")
			return
		}
		client := asynq.NewClient(mustParseRedisOpt(rawRedisURL))
		defer func() { _ = client.Close() }()
		if err := client.Ping(); err != nil {
			redisErr = err
		}
	})
	if poolErr != nil {
		t.Skipf("postgres unreachable (set %s to run): %v", envDatabaseURL, poolErr)
	}
	if redisErr != nil {
		t.Skipf("redis unreachable (set %s to run): %v", envRedisURL, redisErr)
	}
	return pool, rawRedisURL
}

func mustParseRedisOpt(redisURL string) asynq.RedisConnOpt {
	opt, err := queue.ParseRedisOpt(redisURL)
	if err != nil {
		panic(err)
	}
	return opt
}

// testHarness bundles a Server and the fresh test project everything is
// scoped under.
type testHarness struct {
	server  *Server
	queries *postgres.Queries
	pool    *pgxpool.Pool
	project postgres.Project
}

// newTestHarness (Mode A): single goroutine, no auth middleware involved
// (tests call handler methods directly, injecting the project id via
// contextWithProjectID) — wraps Postgres access in one pgx.Tx rolled back
// at test end. Server.RateLimiter is deliberately nil: these tests never
// go through requireScope, so it's never dereferenced. Server.Pool still
// points at the real pool (handleRegisterDevice's device_uuid-rotation
// path begins its own transaction via it, independent of s.DB) — tests
// that use this harness must avoid exercising that path, since its own
// commit wouldn't be rolled back by this harness's cleanup. Simple
// registration (no device_uuid, or no external_user_id) never touches it.
func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	p, redisURL := requireInfra(t)
	ctx := context.Background()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	q := postgres.New(p).WithTx(tx)
	return buildHarness(t, ctx, p, q, redisURL)
}

// newLiveHarness (Mode B): committed fixtures, explicit cleanup. Required
// for anything that goes through the real router/middleware — a
// tx-scoped DB is invisible to requireScope's detached background
// last-used-at write, which runs on its own connection via s.DB after the
// request handler has already returned.
func newLiveHarness(t *testing.T) *testHarness {
	t.Helper()
	p, redisURL := requireInfra(t)
	ctx := context.Background()
	q := postgres.New(p)
	h := buildHarness(t, ctx, p, q, redisURL)
	t.Cleanup(func() { cleanupProjectRows(ctx, p, postgres.UUIDTo(h.project.ID)) })
	return h
}

func buildHarness(t *testing.T, ctx context.Context, p *pgxpool.Pool, q *postgres.Queries, redisURL string) *testHarness {
	t.Helper()
	project, err := q.CreateProject(ctx, postgres.CreateProjectParams{
		Name: "transport-http-test",
		Slug: "itest-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create test project: %v", err)
	}

	asynqClient := asynq.NewClient(mustParseRedisOpt(redisURL))
	t.Cleanup(func() { _ = asynqClient.Close() })

	dispatchHandlers := dispatch.NewHandlers(q, asynqClient, targeting.NewRegistry(q), testMasterKey(t), zerolog.Nop())

	server := &Server{
		DB:       q,
		Pool:     p,
		Dispatch: dispatchHandlers,
		Logger:   zerolog.Nop(),
	}
	return &testHarness{server: server, queries: q, pool: p, project: project}
}

// cleanupProjectRows deletes everything under projectID, in FK order.
// Best-effort — a leftover row in a shared dev DB is harmless.
func cleanupProjectRows(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) {
	_, _ = pool.Exec(ctx, `delete from notification_recipients where notification_id in (select id from notifications where project_id = $1)`, projectID)
	_, _ = pool.Exec(ctx, `delete from notifications where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from api_keys where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from devices where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from provider_credentials where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from subscribers where project_id = $1`, projectID)
	_, _ = pool.Exec(ctx, `delete from projects where id = $1`, projectID)
}

// mustCreateAPIKey creates an active API key for projectID and returns the
// raw bearer token (the only place the plaintext ever exists).
func mustCreateAPIKey(t *testing.T, ctx context.Context, q *postgres.Queries, projectID uuid.UUID, scope apikey.Scope) string {
	t.Helper()
	raw, prefix, err := apikey.Generate()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	if _, err := q.CreateAPIKey(ctx, postgres.CreateAPIKeyParams{
		ProjectID: postgres.UUIDFrom(projectID),
		Name:      "test key",
		KeyPrefix: prefix,
		KeyHash:   apikey.Hash(raw),
		Scope:     string(scope),
	}); err != nil {
		t.Fatalf("create test api key: %v", err)
	}
	return raw
}

// newRateLimiter builds a real apikey.RateLimiter against the standing
// Redis instance — it strictly requires a *redis.Client, no interface
// seam and no miniredis in this repo today.
func newRateLimiter(t *testing.T, redisURL string, rps int) *apikey.RateLimiter {
	t.Helper()
	opt, err := goredis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := goredis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })
	return apikey.NewRateLimiter(client, rps, zerolog.Nop())
}

// testMasterKey generates a fresh random master key for one test.
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
