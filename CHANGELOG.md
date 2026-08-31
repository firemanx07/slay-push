# Changelog

All notable changes to this project are documented here.
Format loosely follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- Phase 5 hardening: per-provider outbound rate limiting. The worker now caps how fast it calls
  out to each provider (`internal/dispatch.OutboundRateLimiter`, `DEFAULT_OUTBOUND_RATE_LIMIT_RPS`,
  default 20), scoped per `(project, provider)` pair rather than globally — each project has its
  own encrypted provider credentials, so one tenant's fanout burst must never throttle another's
  sends. A rate-limited attempt returns the same `queue.ThrottledError` the existing
  provider-reported-429 path already used, so it flows through the same retry/backoff machinery
  rather than needing new plumbing. Fails open on a Redis error, matching
  `internal/apikey.RateLimiter`'s existing policy for inbound limits.

### Changed
- Settled on the Apache License 2.0 for `LICENSE`, replacing the earlier Business Source
  License 1.1 draft. Source-available licenses (BSL, Elastic License 2.0, Sentry's Functional
  Source License) were considered and explicitly rejected — none of them qualify as actual open
  source (all restrict who can offer the software as a competing hosted service, which the Open
  Source Definition disallows), and the project would rather be unambiguously open than carry
  that restriction. As a standard, unmodified OSI license, it needs no custom drafting or legal
  review the way the BSL draft did.

### Fixed
- `devices.subscriber_id` only referenced `subscribers(id)`, so nothing at the database level
  stopped a device from being linked to a subscriber in a *different* project — the tenant
  boundary this codebase enforces everywhere else (API keys, provider credentials, rate limits).
  `subscribers` now carries `unique (project_id, id)`, and `devices` foreign-keys on
  `(project_id, subscriber_id)` against it instead, so a cross-project link is now rejected by
  Postgres itself rather than relying on every call site getting it right. The nullable
  `subscriber_id` (a device with no linked subscriber yet) still works — Postgres's default
  `MATCH SIMPLE` behavior skips the check when any column of a composite foreign key is null.

### Added
- Dashboard styling pass: vendored Pico.css (classless, no build step — same "single vendored
  static asset" convention already used for `htmx.min.js`), a consistent header/nav bar (branding,
  logged-in user, logout) on every authenticated page, and a real tab bar with an active-tab
  indicator on project detail pages.
- Phase 4b: dashboard content pages. Project list/detail (create + overview), a Providers tab
  (add/update encrypted credentials, "test credentials" via a new `provider.CredentialTester`
  optional interface implemented by fcm/hms/apns — expo falls back to a JSON-shape check), an
  API Keys tab (create with one-time reveal, revoke), a Devices tab (search by
  `external_user_id`/status), and a Notifications tab plus per-notification detail with
  htmx-polled delivery status that stops polling once every recipient reaches a terminal state.
  Every dashboard user still sees every project (operator-control-panel model, confirmed with the
  user) — project visibility is centralized in one helper so a future per-customer scoping layer
  is a filter added there, not a rewrite.
- Phase 4a: dashboard local-auth backend. `users`/`sessions` tables, argon2id password hashing
  (`internal/auth`), and a single opaque session token (crypto/rand + sha256, same shape as
  `internal/apikey`'s API keys) looked up per request — no JWT, since a dashboard's traffic never
  approaches the volume where a per-request DB lookup matters. `internal/dashboard` (chi router +
  `a-h/templ` templates) serves `/setup` (first-run admin creation, unreachable once an admin
  exists), `/login`, `/logout`, and an authenticated `/`. `serve-api`/`serve-dashboard`/`serve-all`
  now actually mount different routers instead of being aliases for the same one. `bootstrap` CLI
  subcommand implemented (`BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD`, idempotent). Dashboard
  page work (project/provider/device/notification browsing) is a separate follow-up PR.
- Pre-Phase-4 best-practices audit: `.golangci.yml` (moderate curated set — errcheck, govet,
  staticcheck, gosec, revive, errorlint, contextcheck, and more), `govulncheck` run locally and
  wired into CI as its own job, doc comments on every exported identifier, an `api/openapi.yaml`
  contract for the public JSON API, `.github/dependabot.yml` (gomod/github-actions/docker,
  weekly), and community health files (`CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`,
  issue templates, PR template). CI's single `lint-test` job split into independent
  `build-test`/`lint`/`vulncheck` jobs, all gating the release image build.

### Fixed
- README's architecture diagram and Roadmap were stale relative to the shipped code: the
  diagram showed `serve-all` as the only mode and listed a `delivery receipts`/`auth_config`
  schema that was never built; now reflects the real `serve-api`/`serve-dashboard` mode split
  and the actual table list (adds `subscribers`, drops the never-built `auth_config`). Roadmap
  now marks Phases 0–4 complete and Phase 5 (hardening) as current, and the Configuration
  section no longer claims auth mode/rate limits are dashboard-configurable (they aren't yet).

### Changed
- `runMigrate` now uses golang-migrate's `pgx/v5` database driver instead of `database/postgres`
  (`lib/pq`), removing `lib/pq` from the dependency tree entirely — `govulncheck` flagged several
  unfixed `lib/pq` advisories that this sidesteps rather than waiting on upstream.

- Phase 3: multi-tenancy and machine auth. Envelope-encrypted
  `provider_credentials` (per-credential AES-256-GCM DEK wrapped by
  `APP_MASTER_KEY`), API keys (`sp_live_` prefix, `read`/`send` scopes,
  revocation, Redis GCRA rate limiting), and auth middleware that resolves
  the project from the API key — replacing the hardcoded "default" project
  lookup. `create-project` and `create-api-key` CLI subcommands. `app`,
  `worker`, and `seed-credential` now fail fast if `APP_MASTER_KEY` is
  missing or malformed.

### Fixed
- `provider.Status`'s zero value was `StatusSent`, so an adapter's early
  `return provider.SendResult{}, err` (e.g. on a credential/setup failure)
  was silently recorded as a successful send. `StatusUnknown` is now the
  zero value.
- `ListNotificationRecipients` didn't scope by project, so a valid API key
  for one project could list another project's notification recipients if
  it knew the notification id.
- `provider_credentials.credential` was `jsonb`, which can't hold the
  arbitrary binary ciphertext envelope encryption produces; changed to
  `bytea`.
- Rate-limit `Retry-After` truncated sub-second delays to `0`, which reads
  as "retry immediately" right after being rejected; now rounds up to at
  least 1 second.

- Phase 2: hand-rolled Expo and Huawei HMS adapters and a `sideshow/apns2`-based
  APNs adapter, all self-registering into the provider registry alongside FCM.
  The subscriber/device split (`subscribers` table, `devices.subscriber_id`)
  and `targeting.ByGroup` add group push — send to every active device under
  an external id. The notification API now speaks OneSignal-style field
  names (`include_player_ids`, `include_external_user_ids`), with
  `included_segments`/`filters` accepted on the wire shape but rejected for
  now (segmentation is deliberately out of scope). Device registration
  accepts an optional `external_user_id` to link a device to a subscriber.
- Phase 1: core schema (`projects`, `devices`, `notifications`,
  `notification_recipients`, `provider_credentials`, seeded default
  project), the `provider.Adapter` self-registering factory interface, a
  hand-rolled FCM HTTP v1 adapter (OAuth2 service-account auth, per-credential
  token caching, invalid-token/throttled/transient error classification),
  the `targeting.Resolver` strategy interface (`ByExplicitTargets`
  implemented; `ByGroup`/`ByFilter` reserved for later), asynq-backed
  fanout/send queues with retry backoff, `Retry-After` honoring, and the
  `(notification_id, device_id)` idempotency anchor, and a bare JSON API
  (register device, create notification, get status/recipients).
- `seed-credential` CLI subcommand — a Phase 1 stand-in for the dashboard's
  (Phase 4) provider-credential form.
- Phase 0 scaffolding: repo layout, Go module, `cmd/server` binary with
  `serve-all`/`serve-api`/`serve-dashboard`/`worker`/`migrate`/`healthcheck`
  subcommands, `/healthz` (Postgres + Redis connectivity), structured
  logging (zerolog, JSON to stdout in prod, optional Seq sink in dev),
  base + dev Docker Compose stack, CI skeleton.
