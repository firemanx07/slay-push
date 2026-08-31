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

// Config holds process configuration loaded from environment variables.
type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	RedisURL             string
	LogLevel             string
	LogFormat            string // "json" (default, prod) or "seq" (dev-only)
	SeqURL               string // only consulted when LogFormat == "seq"
	MasterKey            string // APP_MASTER_KEY, AES-256-GCM key for secrets at rest (base64, 32 bytes decoded)
	DefaultRateLimitRPS  int    // per API key; per-project ceiling is 5x this
	OutboundRateLimitRPS int    // per (project, provider); protects a project's own provider account from a large-fanout burst
	CookieSecure         bool   // APP_COOKIE_SECURE; false only for local plain-HTTP dev
}

// Load reads configuration from environment variables, applying defaults
// for anything unset.
func Load() Config {
	return Config{
		HTTPAddr:             getEnv("APP_HTTP_ADDR", ":8080"),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://pushdispatch:pushdispatch@localhost:5432/pushdispatch?sslmode=disable"),
		RedisURL:             getEnv("REDIS_URL", "redis://localhost:6379/0"),
		LogLevel:             getEnv("LOG_LEVEL", "info"),
		LogFormat:            getEnv("LOG_FORMAT", "json"),
		SeqURL:               getEnv("SEQ_URL", ""),
		MasterKey:            os.Getenv("APP_MASTER_KEY"),
		DefaultRateLimitRPS:  getEnvInt("DEFAULT_RATE_LIMIT_RPS", 10),
		OutboundRateLimitRPS: getEnvInt("DEFAULT_OUTBOUND_RATE_LIMIT_RPS", 20),
		CookieSecure:         getEnvBool("APP_COOKIE_SECURE", true),
	}
}

// getEnvInt reads key from the environment as an integer, returning fallback if absent or malformed.
func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// getEnvBool reads key from the environment as a boolean, returning fallback if absent or malformed.
func getEnvBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// getEnv reads key from the environment, returning fallback if absent or empty.
func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// Validate reports an error if the configuration is internally inconsistent.
func (c Config) Validate() error {
	if c.LogFormat != "json" && c.LogFormat != "seq" {
		return fmt.Errorf("LOG_FORMAT must be %q or %q, got %q", "json", "seq", c.LogFormat)
	}
	if c.LogFormat == "seq" && c.SeqURL == "" {
		return fmt.Errorf("SEQ_URL is required when LOG_FORMAT=seq")
	}
	if c.OutboundRateLimitRPS <= 0 {
		return fmt.Errorf("DEFAULT_OUTBOUND_RATE_LIMIT_RPS must be positive, got %d", c.OutboundRateLimitRPS)
	}
	return nil
}
