# Changelog

All notable changes to this project are documented here.
Format loosely follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- Pre-Phase-4 best-practices audit: `.golangci.yml` (moderate curated set — errcheck, govet,
  staticcheck, gosec, revive, errorlint, contextcheck, and more), `govulncheck` run locally and
  wired into CI as its own job, doc comments on every exported identifier, an `api/openapi.yaml`
  contract for the public JSON API, `.github/dependabot.yml` (gomod/github-actions/docker,
  weekly), and community health files (`CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`,
  issue templates, PR template). CI's single `lint-test` job split into independent
  `build-test`/`lint`/`vulncheck` jobs, all gating the release image build.

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
