# Changelog

All notable changes to this project are documented here.
Format loosely follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
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
