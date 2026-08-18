// Package config loads process configuration from environment variables:
// how to reach Postgres/Redis, how to log, and which port to bind. Other
// operator-facing settings live in Postgres, edited from the dashboard
// after bootstrap.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr            string
	DatabaseURL         string
	RedisURL            string
	LogLevel            string
	LogFormat           string // "json" (default, prod) or "seq" (dev-only)
	SeqURL              string // only consulted when LogFormat == "seq"
	MasterKey           string // APP_MASTER_KEY, AES-256-GCM key for secrets at rest (base64, 32 bytes decoded)
	DefaultRateLimitRPS int    // per API key; per-project ceiling is 5x this
}

func Load() Config {
	return Config{
		HTTPAddr:            getEnv("APP_HTTP_ADDR", ":8080"),
		DatabaseURL:         getEnv("DATABASE_URL", "postgres://pushdispatch:pushdispatch@localhost:5432/pushdispatch?sslmode=disable"),
		RedisURL:            getEnv("REDIS_URL", "redis://localhost:6379/0"),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
		LogFormat:           getEnv("LOG_FORMAT", "json"),
		SeqURL:              getEnv("SEQ_URL", ""),
		MasterKey:           os.Getenv("APP_MASTER_KEY"),
		DefaultRateLimitRPS: getEnvInt("DEFAULT_RATE_LIMIT_RPS", 10),
	}
}

func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func (c Config) Validate() error {
	if c.LogFormat != "json" && c.LogFormat != "seq" {
		return fmt.Errorf("LOG_FORMAT must be %q or %q, got %q", "json", "seq", c.LogFormat)
	}
	if c.LogFormat == "seq" && c.SeqURL == "" {
		return fmt.Errorf("SEQ_URL is required when LOG_FORMAT=seq")
	}
	return nil
}
