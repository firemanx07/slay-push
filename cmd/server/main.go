// Command server is the single slay-push binary. Which subcommand you
// pass selects its run mode; docker-compose.yml wires each container to a
// different one via `command:` overrides so the whole project ships as
// one image (see deploy/docker).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/config"
	"github.com/firemanx07/slay-push/internal/dispatch"
	"github.com/firemanx07/slay-push/internal/platform"
	_ "github.com/firemanx07/slay-push/internal/provider/apns" // self-registers "apns" into the provider registry
	_ "github.com/firemanx07/slay-push/internal/provider/expo" // self-registers "expo" into the provider registry
	_ "github.com/firemanx07/slay-push/internal/provider/fcm"  // self-registers "fcm" into the provider registry
	_ "github.com/firemanx07/slay-push/internal/provider/hms"  // self-registers "hms" into the provider registry
	"github.com/firemanx07/slay-push/internal/queue"
	"github.com/firemanx07/slay-push/internal/store/postgres"
	"github.com/firemanx07/slay-push/internal/targeting"
	apihttp "github.com/firemanx07/slay-push/internal/transport/http"
)

const shutdownTimeout = 10 * time.Second

// supportedProviders drives both the worker's per-provider queue/handler
// registration and the seed-credential CLI's flag description — one list,
// not repeated at each call site.
var supportedProviders = []string{"expo", "fcm", "apns", "hms"}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(2)
	}

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "invalid configuration:", err)
		os.Exit(1)
	}
	logger := platform.NewLogger(cfg)

	var err error
	switch os.Args[1] {
	case "serve-all", "serve-api", "serve-dashboard":
		err = runServe(cfg, logger)
	case "worker":
		err = runWorker(cfg, logger)
	case "migrate":
		err = runMigrate(cfg, logger)
	case "healthcheck":
		os.Exit(runHealthcheckProbe(cfg))
	case "seed-credential":
		err = runSeedCredential(cfg, logger, os.Args[2:])
	case "bootstrap":
		err = errors.New("bootstrap: not yet implemented (Phase 4)")
	case "rotate-key":
		err = errors.New("rotate-key: not yet implemented (post-MVP)")
	default:
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(2)
	}

	if err != nil {
		logger.Error().Err(err).Str("command", os.Args[1]).Msg("command failed")
		os.Exit(1)
	}
}

func usage() string {
	return "usage: server <serve-all|serve-api|serve-dashboard|worker|migrate|healthcheck|seed-credential|bootstrap|rotate-key>"
}

// runHealthcheckProbe lets `docker compose healthcheck:` call the binary
// itself (`CMD ["/server", "healthcheck"]`) instead of relying on curl/wget,
// which the distroless base image doesn't have. It hits the server's own
// /healthz over loopback and mirrors its HTTP status as an exit code.
func runHealthcheckProbe(cfg config.Config) int {
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		port = "8080"
	}

	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
	if err != nil {
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// runServe starts the HTTP server: the public dispatch API (register
// device, create notification, get status) plus /healthz. The htmx
// dashboard mounts onto the same mux starting Phase 4; serve-api/
// serve-dashboard remain aliases for serve-all until there's a real reason
// to split the mux by mode.
func runServe(cfg config.Config, logger zerolog.Logger) error {
	ctx := context.Background()

	db, err := platform.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	rdb, err := platform.NewRedisClient(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer rdb.Close()

	queries := postgres.New(db)

	asynqClient, err := queue.NewClient(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect asynq client: %w", err)
	}
	defer asynqClient.Close()

	targetingRegistry := targeting.NewRegistry(queries)
	dispatchHandlers := dispatch.NewHandlers(queries, asynqClient, targetingRegistry, logger)

	apiServer := &apihttp.Server{DB: queries, Dispatch: dispatchHandlers, Logger: logger}

	mux := http.NewServeMux()
	mux.Handle("/healthz", platform.HealthHandler(db, rdb))
	mux.Handle("/", apihttp.NewRouter(apiServer))

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}

	errCh := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", cfg.HTTPAddr).Msg("http server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-sigCh:
		logger.Info().Msg("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// runWorker hosts the asynq queue consumer: fanout (target resolution) plus
// one queue per provider (send:fcm/expo/apns/hms) — a slow/down provider
// never starves the others. asynq.Server.Run already handles SIGINT/SIGTERM
// with a graceful shutdown internally, so no manual signal plumbing is
// needed here.
func runWorker(cfg config.Config, logger zerolog.Logger) error {
	ctx := context.Background()

	db, err := platform.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	queries := postgres.New(db)

	asynqClient, err := queue.NewClient(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect asynq client: %w", err)
	}
	defer asynqClient.Close()

	targetingRegistry := targeting.NewRegistry(queries)
	handlers := dispatch.NewHandlers(queries, asynqClient, targetingRegistry, logger)

	redisOpt, err := queue.ParseRedisOpt(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}

	queues := map[string]int{queue.QueueFanout: 5}
	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeFanout, handlers.AsynqFanoutHandler)
	for _, providerType := range supportedProviders {
		queues[queue.SendTypeFor(providerType)] = 10
		mux.HandleFunc(queue.SendTypeFor(providerType), handlers.AsynqSendHandler)
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Queues:         queues,
		RetryDelayFunc: queue.RetryDelayFunc,
		Logger:         asynqZerologAdapter{logger},
	})

	logger.Info().Strs("providers", supportedProviders).Msg("worker: listening on fanout + per-provider send queues")
	return srv.Run(mux)
}

func runMigrate(cfg config.Config, logger zerolog.Logger) error {
	m, err := migrate.New("file://migrations", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info().Msg("migrations: no change")
			return nil
		}
		return fmt.Errorf("apply migrations: %w", err)
	}
	logger.Info().Msg("migrations applied")
	return nil
}

// runSeedCredential is a Phase 1 stand-in for the dashboard's (Phase 4)
// provider-credential form: without it there's no way to get an FCM service
// account into provider_credentials to test a real send.
func runSeedCredential(cfg config.Config, logger zerolog.Logger, args []string) error {
	fs := flag.NewFlagSet("seed-credential", flag.ExitOnError)
	providerType := fs.String("provider", "fcm", fmt.Sprintf("provider type (%s)", strings.Join(supportedProviders, ", ")))
	file := fs.String("file", "", "path to the provider credential JSON file (e.g. an FCM service account key)")
	projectSlug := fs.String("project", "default", "project slug to attach the credential to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("seed-credential: --file is required")
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read credential file: %w", err)
	}
	var js json.RawMessage
	if err := json.Unmarshal(raw, &js); err != nil {
		return fmt.Errorf("credential file is not valid JSON: %w", err)
	}

	ctx := context.Background()
	db, err := platform.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	queries := postgres.New(db)
	project, err := queries.GetProjectBySlug(ctx, *projectSlug)
	if err != nil {
		return fmt.Errorf("resolve project %q: %w", *projectSlug, err)
	}

	if _, err := queries.UpsertProviderCredential(ctx, postgres.UpsertProviderCredentialParams{
		ProjectID:    project.ID,
		ProviderType: *providerType,
		Environment:  "production",
		Credential:   raw,
	}); err != nil {
		return fmt.Errorf("upsert provider credential: %w", err)
	}

	logger.Info().Str("provider", *providerType).Str("project", *projectSlug).Msg("credential seeded")
	return nil
}
