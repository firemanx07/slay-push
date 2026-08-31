package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type healthStatus struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Redis    string `json:"redis"`
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := NewPostgresPool(ctx, testDatabaseURL)
	if err != nil {
		t.Skipf("postgres unreachable (set DATABASE_URL to run): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	client, err := NewRedisClient(testRedisURL)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("redis unreachable (set REDIS_URL to run): %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func doHealthRequest(t *testing.T, handler http.HandlerFunc) (int, healthStatus) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	var status healthStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return w.Code, status
}

func TestHealthHandler_Healthy(t *testing.T) {
	pool := newTestPool(t)
	rdb := newTestRedisClient(t)

	code, status := doHealthRequest(t, HealthHandler(pool, rdb))
	if code != http.StatusOK {
		t.Errorf("status code = %d, want %d", code, http.StatusOK)
	}
	if status.Status != "ok" || status.Database != "ok" || status.Redis != "ok" {
		t.Errorf("got %+v, want all ok", status)
	}
}

func TestHealthHandler_DatabaseDown(t *testing.T) {
	pool := newTestPool(t)
	rdb := newTestRedisClient(t)
	pool.Close() // Ping now fails immediately — no live server needed to exercise this.

	code, status := doHealthRequest(t, HealthHandler(pool, rdb))
	if code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if status.Status != "degraded" || status.Database == "ok" {
		t.Errorf("got %+v, want degraded with a database error", status)
	}
}

func TestHealthHandler_RedisDown(t *testing.T) {
	pool := newTestPool(t)
	rdb := newTestRedisClient(t)
	_ = rdb.Close() // Ping now fails immediately.

	code, status := doHealthRequest(t, HealthHandler(pool, rdb))
	if code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if status.Status != "degraded" || status.Redis == "ok" {
		t.Errorf("got %+v, want degraded with a redis error", status)
	}
}
