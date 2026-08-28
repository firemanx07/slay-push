package config

import "testing"

func TestLoad_Defaults(t *testing.T) {
	for _, key := range []string{
		"APP_HTTP_ADDR", "DATABASE_URL", "REDIS_URL", "LOG_LEVEL", "LOG_FORMAT",
		"SEQ_URL", "APP_MASTER_KEY", "DEFAULT_RATE_LIMIT_RPS", "APP_COOKIE_SECURE",
	} {
		t.Setenv(key, "")
	}

	cfg := Load()

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.DatabaseURL != "postgres://pushdispatch:pushdispatch@localhost:5432/pushdispatch?sslmode=disable" { //nolint:gosec // the well-known, documented default local-dev DSN (see .env.example), not a real credential
		t.Errorf("DatabaseURL = %q, want the default local dev DSN", cfg.DatabaseURL)
	}
	if cfg.RedisURL != "redis://localhost:6379/0" {
		t.Errorf("RedisURL = %q, want %q", cfg.RedisURL, "redis://localhost:6379/0")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want %q", cfg.LogFormat, "json")
	}
	if cfg.SeqURL != "" {
		t.Errorf("SeqURL = %q, want empty", cfg.SeqURL)
	}
	if cfg.MasterKey != "" {
		t.Errorf("MasterKey = %q, want empty", cfg.MasterKey)
	}
	if cfg.DefaultRateLimitRPS != 10 {
		t.Errorf("DefaultRateLimitRPS = %d, want 10", cfg.DefaultRateLimitRPS)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false, want true (default)")
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("APP_HTTP_ADDR", ":9090")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://y")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "seq")
	t.Setenv("SEQ_URL", "http://seq:5341")
	t.Setenv("APP_MASTER_KEY", "some-key")
	t.Setenv("DEFAULT_RATE_LIMIT_RPS", "42")
	t.Setenv("APP_COOKIE_SECURE", "false")

	cfg := Load()

	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9090")
	}
	if cfg.DatabaseURL != "postgres://x" {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://x")
	}
	if cfg.RedisURL != "redis://y" {
		t.Errorf("RedisURL = %q, want %q", cfg.RedisURL, "redis://y")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.LogFormat != "seq" {
		t.Errorf("LogFormat = %q, want %q", cfg.LogFormat, "seq")
	}
	if cfg.SeqURL != "http://seq:5341" {
		t.Errorf("SeqURL = %q, want %q", cfg.SeqURL, "http://seq:5341")
	}
	if cfg.MasterKey != "some-key" {
		t.Errorf("MasterKey = %q, want %q", cfg.MasterKey, "some-key")
	}
	if cfg.DefaultRateLimitRPS != 42 {
		t.Errorf("DefaultRateLimitRPS = %d, want 42", cfg.DefaultRateLimitRPS)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure = true, want false")
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"json format, no seq url needed", Config{LogFormat: "json"}, false},
		{"seq format with seq url", Config{LogFormat: "seq", SeqURL: "http://seq:5341"}, false},
		{"seq format without seq url", Config{LogFormat: "seq"}, true},
		{"invalid format", Config{LogFormat: "xml"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetEnvHelpers(t *testing.T) {
	t.Run("getEnv falls back on empty string, not just unset", func(t *testing.T) {
		t.Setenv("SLAY_TEST_STR", "")
		if got := getEnv("SLAY_TEST_STR", "fallback"); got != "fallback" {
			t.Errorf("getEnv = %q, want %q", got, "fallback")
		}
	})

	t.Run("getEnvInt falls back on malformed value", func(t *testing.T) {
		t.Setenv("SLAY_TEST_INT", "not-a-number")
		if got := getEnvInt("SLAY_TEST_INT", 7); got != 7 {
			t.Errorf("getEnvInt = %d, want 7", got)
		}
	})

	t.Run("getEnvInt reads a valid value", func(t *testing.T) {
		t.Setenv("SLAY_TEST_INT", "99")
		if got := getEnvInt("SLAY_TEST_INT", 7); got != 99 {
			t.Errorf("getEnvInt = %d, want 99", got)
		}
	})

	t.Run("getEnvBool falls back on malformed value", func(t *testing.T) {
		t.Setenv("SLAY_TEST_BOOL", "not-a-bool")
		if got := getEnvBool("SLAY_TEST_BOOL", true); !got {
			t.Error("getEnvBool = false, want true (fallback)")
		}
	})

	t.Run("getEnvBool reads a valid value", func(t *testing.T) {
		t.Setenv("SLAY_TEST_BOOL", "false")
		if got := getEnvBool("SLAY_TEST_BOOL", true); got {
			t.Error("getEnvBool = true, want false")
		}
	})
}
