// Command server is the single slay-push binary. Which subcommand you
// pass selects its run mode; docker-compose.yml wires each container to a
// different one via `command:` overrides (see deploy/docker).
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
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/firemanx07/slay-push/internal/apikey"
	"github.com/firemanx07/slay-push/internal/config"
	"github.com/firemanx07/slay-push/internal/crypto"
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

// supportedProviders drives the worker's per-provider queue/handler
// registration and the seed-credential CLI's flag description.
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
	case "create-project":
		err = runCreateProject(cfg, logger, os.Args[2:])
	case "create-api-key":
		err = runCreateAPIKey(cfg, logger, os.Args[2:])
	case "bootstrap":
		err = errors.New("bootstrap: not yet implemented")
	case "rotate-key":
		err = errors.New("rotate-key: not yet implemented")
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
	return "usage: server <serve-all|serve-api|serve-dashboard|worker|migrate|healthcheck|seed-credential|create-project|create-api-key|bootstrap|rotate-key>"
}

// runHealthcheckProbe lets `docker compose healthcheck:` call the binary
// itself (`CMD ["/server", "healthcheck"]`). It hits /healthz over loopback
// and mirrors its HTTP status as an exit code.
func runHealthcheckProbe(cfg config.Config) int {
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		port = "8080"
	}

	client := http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%s/healthz", port), nil)
	if err != nil {
		return 1
	}
	resp, err := client.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// runServe starts the HTTP server: the public dispatch API (register
// device, create notification, get status) plus /healthz. serve-api/
// serve-dashboard are aliases for serve-all.
func runServe(cfg config.Config, logger zerolog.Logger) error {
	masterKey, err := crypto.LoadMasterKey(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}

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
	defer func() { _ = rdb.Close() }()

	queries := postgres.New(db)

	asynqClient, err := queue.NewClient(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect asynq client: %w", err)
	}
	defer func() { _ = asynqClient.Close() }()

	targetingRegistry := targeting.NewRegistry(queries)
	dispatchHandlers := dispatch.NewHandlers(queries, asynqClient, targetingRegistry, masterKey, logger)
	rateLimiter := apikey.NewRateLimiter(rdb, cfg.DefaultRateLimitRPS, logger)

	apiServer := &apihttp.Server{DB: queries, Dispatch: dispatchHandlers, RateLimiter: rateLimiter, Logger: logger}

	mux := http.NewServeMux()
	mux.Handle("/healthz", platform.HealthHandler(db, rdb))
	mux.Handle("/", apihttp.NewRouter(apiServer))

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

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

// runWorker hosts the asynq queue consumer: fanout (target resolution)
// plus one queue per provider (send:fcm/expo/apns/hms). asynq.Server.Run
// handles SIGINT/SIGTERM with a graceful shutdown internally.
func runWorker(cfg config.Config, logger zerolog.Logger) error {
	masterKey, err := crypto.LoadMasterKey(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}

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
	defer func() { _ = asynqClient.Close() }()

	targetingRegistry := targeting.NewRegistry(queries)
	handlers := dispatch.NewHandlers(queries, asynqClient, targetingRegistry, masterKey, logger)

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

// pgx5MigrateURL rewrites a postgres(ql):// DSN to the pgx5:// scheme the
// golang-migrate pgx/v5 database driver registers itself under.
func pgx5MigrateURL(databaseURL string) string {
	switch {
	case strings.HasPrefix(databaseURL, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgresql://")
	case strings.HasPrefix(databaseURL, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgres://")
	default:
		return databaseURL
	}
}

func runMigrate(cfg config.Config, logger zerolog.Logger) error {
	m, err := migrate.New("file://migrations", pgx5MigrateURL(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer func() { _, _ = m.Close() }()

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

// runSeedCredential loads a provider credential JSON file into
// provider_credentials.
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

	masterKey, err := crypto.LoadMasterKey(cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read credential file: %w", err)
	}
	var js json.RawMessage
	if err := json.Unmarshal(raw, &js); err != nil {
		return fmt.Errorf("credential file is not valid JSON: %w", err)
	}

	wrappedDEK, ciphertext, err := masterKey.Seal(raw)
	if err != nil {
		return fmt.Errorf("encrypt credential: %w", err)
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
		Credential:   ciphertext,
		WrappedDek:   wrappedDEK,
	}); err != nil {
		return fmt.Errorf("upsert provider credential: %w", err)
	}

	logger.Info().Str("provider", *providerType).Str("project", *projectSlug).Msg("credential seeded")
	return nil
}

// runCreateProject creates a new project.
func runCreateProject(cfg config.Config, logger zerolog.Logger, args []string) error {
	fs := flag.NewFlagSet("create-project", flag.ExitOnError)
	name := fs.String("name", "", "project display name")
	slug := fs.String("slug", "", "project slug (used in seed-credential/create-api-key --project)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *slug == "" {
		return errors.New("create-project: --name and --slug are required")
	}

	ctx := context.Background()
	db, err := platform.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	queries := postgres.New(db)
	project, err := queries.CreateProject(ctx, postgres.CreateProjectParams{Name: *name, Slug: *slug})
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}

	logger.Info().Str("id", postgres.UUIDTo(project.ID).String()).Str("slug", project.Slug).Msg("project created")
	return nil
}

// runCreateAPIKey creates an API key for a project. The raw key is printed
// to stdout once and is never retrievable again; it is never written to the
// structured log.
func runCreateAPIKey(cfg config.Config, logger zerolog.Logger, args []string) error {
	fs := flag.NewFlagSet("create-api-key", flag.ExitOnError)
	projectSlug := fs.String("project", "", "project slug")
	name := fs.String("name", "", "label for this key (e.g. \"backend-prod\")")
	scopeFlag := fs.String("scope", "send", "key scope: read or send")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectSlug == "" || *name == "" {
		return errors.New("create-api-key: --project and --name are required")
	}
	scope, ok := apikey.ParseScope(*scopeFlag)
	if !ok {
		return fmt.Errorf("create-api-key: --scope must be %q or %q", apikey.ScopeRead, apikey.ScopeSend)
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

	raw, prefix, err := apikey.Generate()
	if err != nil {
		return fmt.Errorf("generate api key: %w", err)
	}

	if _, err := queries.CreateAPIKey(ctx, postgres.CreateAPIKeyParams{
		ProjectID: project.ID,
		Name:      *name,
		KeyPrefix: prefix,
		KeyHash:   apikey.Hash(raw),
		Scope:     string(scope),
	}); err != nil {
		return fmt.Errorf("create api key: %w", err)
	}

	logger.Info().Str("project", *projectSlug).Str("scope", string(scope)).Msg("api key created")
	fmt.Printf("API key created for project %q (scope=%s). Save it now — it cannot be shown again:\n%s\n", *projectSlug, scope, raw)
	return nil
}
