# AGENTS.md

Instructions for AI coding agents (Claude Code, Cursor, Aider, Codex, etc.) working in this
repository. See `README.md` for the product description and `CHANGELOG.md` for what's shipped so
far.

## What this project is

`slay-push` is a self-hostable, multi-tenant push notification dispatch microservice in Go. It
sends directly to Expo, FCM (HTTP v1), APNs (HTTP/2), and Huawei HMS — no proxying through a
third-party push backend — and exposes a device/subscriber registry and delivery-status tracking
API with OneSignal-shaped request/response fields (see `api/openapi.yaml` for the contract).

## Repo layout

```
cmd/server            main.go — subcommands: serve-all, serve-api, serve-dashboard, worker,
                       migrate, healthcheck, seed-credential, create-project, create-api-key,
                       bootstrap (rotate-key is declared but not yet implemented)
internal/
  apikey               API key generation/hashing, scoping, Redis GCRA rate limiting
  auth                 argon2id password hashing, dashboard session token generation/hashing
  config               env var config loading + validation
  crypto               envelope encryption: per-credential DEK, AES-GCM, master-key wrap/unwrap
  dashboard            operator dashboard: chi router, handlers, templ templates, static assets,
                       session-cookie auth middleware (separate from the API's key auth)
  dispatch             notification creation, fanout handler, send handler
  platform             Postgres/Redis setup, structured logging (zerolog + optional Seq sink), healthchecks
  provider             Adapter interface + self-registering factory registry
  provider/{expo,fcm,apns,hms}   one package per push provider
  queue                asynq task types/payloads/client wiring
  store/postgres       sqlc-generated queries (*.sql.go — do not hand-edit) + pg.go (hand-written bridge helpers)
  targeting            Resolver strategy interface (ByExplicitTargets, ByGroup, Registry)
  transport/http       chi router, handlers, middleware (API-key auth)
migrations             plain SQL, golang-migrate format
deploy/docker          Dockerfile, docker-compose.yml (pulls the published image), docker-compose.dev.yml (builds from source, adds Seq)
api/openapi.yaml       the public HTTP API's OpenAPI 3.0 contract
```

## Architecture (read before touching dispatch/provider/targeting code)

- **Provider adapters are Open/Closed by construction.** Each provider package
  (`internal/provider/{expo,fcm,apns,hms}`) self-registers into a factory registry via
  `provider.Register("name", New)` in an `init()` — see `internal/provider/provider.go`. Adding a
  5th provider means adding one new package; it should never require touching dispatch, queue, or
  existing provider code. `provider.CredentialTester` is the same pattern for an optional
  capability: an adapter implements `TestCredential` only if it can validate a credential locally
  (fcm/hms/apns do; expo doesn't) — callers type-assert for it rather than requiring every adapter
  to support it.
- **Targeting is a strategy interface, not a growing if/else.** `internal/targeting.Resolver` has
  exactly two implementations today: `ByExplicitTargets` (transactional — explicit device ids) and
  `ByGroup` (group push — every active device under one or more external ids). A future
  segmentation feature (`included_segments`/`filters`) is meant to land as a third strategy without
  touching the first two — those two request fields are already accepted on the wire (for
  OneSignal request-shape compatibility) and currently rejected with a 422, not removed.
- **Subscriber vs device split.** A `subscribers` row is the grouping/identity concept (one
  external id). A `devices` row is one channel-specific delivery endpoint (one push token on one
  platform) belonging to a subscriber. Don't conflate subscriber-level state (opted_out) with
  device-level state (active/stale/invalid).
- **Envelope encryption for provider credentials.** Each `provider_credentials` row has its own
  random DEK (AES-256-GCM) that encrypts the actual credential; the DEK is wrapped by
  `APP_MASTER_KEY`. See `internal/crypto`. Never log a raw credential, DEK, or `APP_MASTER_KEY`.
- **Async-first.** Notification creation (`POST /api/v1/notifications`) never resolves audience or
  sends in the HTTP request path — it inserts a `pending` row and enqueues one `fanout` job. Audience
  resolution and provider sends happen in the `worker` process via asynq.
- **Dashboard auth is a separate, session-based mechanism from API-key auth.** `internal/dashboard`
  mounts at `/` (serve-dashboard/serve-all); `internal/transport/http`'s API mounts at `/api/`
  (serve-api/serve-all) — see `runServe` in `cmd/server/main.go`. Dashboard sessions are a single
  opaque high-entropy token (`internal/auth.GenerateSessionToken`/`HashSessionToken`, same
  crypto/rand+sha256 shape as `internal/apikey`'s API keys) looked up per request in the `sessions`
  table — no JWT, deliberately, since a dashboard's traffic never approaches where a per-request DB
  lookup matters. Don't introduce a second session paradigm without a real reason.

## Commands

```bash
go build ./...
go vet ./...
gofmt -l .                 # must print nothing
go test ./...
golangci-lint run ./...    # must report 0 issues — config is .golangci.yml
govulncheck ./...          # must report no vulnerabilities called by our code
```

Local dev stack (Postgres, Redis, Seq log viewer, builds from source):

```bash
cp .env.example .env
docker compose -f deploy/docker/docker-compose.yml -f deploy/docker/docker-compose.dev.yml up
```

Regenerating sqlc code after changing `internal/store/postgres/queries/*.sql` or a migration:

```bash
sqlc generate
```

Regenerating templ code after changing a `.templ` file under `internal/dashboard/templates/`
(generated `_templ.go` files are committed, same convention as sqlc's output):

```bash
templ generate
```

## Code style

- **Comments are doc-only.** They describe behavior/contract (what a function does, an invariant,
  a unit), never rationale. Do not explain *why* something was done, reference planning
  documents, phase numbers, or issue/ticket numbers in code comments — that context belongs in the
  commit message or PR description, not in code that outlives both. Every exported identifier gets
  a standard one-line godoc comment.
- Small, focused interfaces; accept an interface, return a concrete struct (see
  `internal/targeting.Resolver`, `internal/provider.Adapter` for the pattern already in use).
- Don't add abstractions, config knobs, or error handling for scenarios the code doesn't actually
  face yet. No speculative generality — the Open/Closed extension points above exist because two
  concrete strategies/providers already justify the interface, not in anticipation of more.
- Wrap errors with `%w` and `fmt.Errorf`; check with `errors.Is`/`errors.As`, never direct
  sentinel-error comparison (`err != someErr`).
- Structured logging via `zerolog` (`internal/platform/logging.go`); never log secrets (provider
  credentials, DEKs, `APP_MASTER_KEY`, raw API keys) by field name.

## Before opening a PR / finishing a task

Run the full command list above. All of it must be clean — this is enforced in CI
(`.github/workflows/ci.yml` runs `build-test`, `lint`, and `vulncheck` as independent jobs gating
the release image build). See `CONTRIBUTING.md` for more detail and `SECURITY.md` before reporting
anything security-sensitive.
