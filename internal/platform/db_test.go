package platform

import (
	"context"
	"testing"
	"time"
)

const testDatabaseURL = "postgres://pushdispatch:pushdispatch@localhost:5432/pushdispatch?sslmode=disable" //nolint:gosec // well-known local dev DSN, not a secret
const testRedisURL = "redis://localhost:6379"

func TestNewPostgresPool_Success(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := NewPostgresPool(ctx, testDatabaseURL)
	if err != nil {
		t.Skipf("postgres unreachable (set DATABASE_URL to run): %v", err)
	}
	defer pool.Close()
}

func TestNewPostgresPool_InvalidDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := NewPostgresPool(ctx, "not-a-valid-dsn"); err == nil {
		t.Fatal("expected an error for an invalid DSN")
	}
}

func TestNewRedisClient_Success(t *testing.T) {
	client, err := NewRedisClient(testRedisURL)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	defer func() { _ = client.Close() }()
}

func TestNewRedisClient_InvalidURL(t *testing.T) {
	if _, err := NewRedisClient("not-a-valid-url"); err == nil {
		t.Fatal("expected an error for an invalid redis URL")
	}
}
