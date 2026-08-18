# slay-push

[![Sponsor](https://img.shields.io/badge/sponsor-GitHub%20Sponsors-ea4aaa)](https://github.com/sponsors/firemanx07)

Self-hosted, multi-tenant push notification dispatch microservice. Sends directly to Expo,
FCM (HTTP v1), APNs (HTTP/2), and Huawei HMS Push Kit — no proxying through a third-party
push backend. Includes a device/subscriber registry, delivery-status tracking, and a small
server-rendered dashboard. Ships as a Docker image; configured entirely through the dashboard
after `docker compose up`.

## Why

Most self-hosted alternatives either only support one push channel or require running a
separate stack per provider. slay-push is one binary, one Postgres/Redis-backed service,
that talks to all four channels directly, with request/response shapes close enough to
OneSignal's REST API that a team migrating off OneSignal can reuse most of their existing
integration code.

## Status

Early scaffolding — see [CHANGELOG.md](CHANGELOG.md) and the phased roadmap below. Not yet
ready for production use.

## Architecture

```
client backend --(API key)--> app:serve-all --> enqueue --> Redis --> worker --> FCM/APNs/HMS/Expo
                                  |                                        |
                                  +--------------- Postgres <--------------+
                                  (projects, devices, notifications, delivery receipts,
                                   users, sessions, api_keys, auth_config, provider_credentials)
operator browser --(session)--> app:serve-all (htmx dashboard)
```

One Go codebase/image, three run modes selected by the container's `command:`:
`serve-all` (public API + dashboard), `worker` (asynq queue consumer, calls provider adapters),
`migrate` (one-shot schema migration).

## Quickstart

```bash
cp .env.example .env
# edit .env: set POSTGRES_PASSWORD, generate APP_MASTER_KEY with `openssl rand -base64 32`

docker compose -f deploy/docker/docker-compose.yml up
```

Then open `http://localhost:8080` — the first visit routes to the setup wizard.

For local development with hot reload and a Seq log viewer:

```bash
docker compose -f deploy/docker/docker-compose.yml -f deploy/docker/docker-compose.dev.yml up
```

Seq UI: `http://localhost:8081`. Seq is dev-only by design — production logging is plain JSON
to stdout, so it plugs into whatever log stack you already run (Loki, ELK, Datadog, CloudWatch).

## Configuration

Only what's needed before the database is reachable lives in env vars (see `.env.example`).
Everything else — auth mode (local/OIDC), provider credentials, API keys, rate-limit overrides —
is configured from the dashboard after first-run setup and stored in Postgres.

| Variable | Default | Notes |
|---|---|---|
| `POSTGRES_PASSWORD` | `pushdispatch` (dev) | set a real password before exposing this beyond localhost |
| `APP_PORT` | `8080` | host-side port for the dashboard/API |
| `APP_MASTER_KEY` | *(none)* | AES-256-GCM key encrypting provider credentials at rest; **losing it makes stored credentials unrecoverable** — there's no external KMS in this design |
| `LOG_FORMAT` | `json` | `seq` only meaningful with the dev compose overlay |
| `LOG_LEVEL` | `info` | |

## Roadmap

Phased MVP plan (each phase independently runnable/testable via `docker compose up`):

0. Repo & project scaffolding *(current)*
1. Core data model + single-provider (FCM) dispatch
2. Multi-provider (Expo/APNs/HMS) + subscriber/device registry + group push
3. Multi-tenancy + API keys
4. Dashboard + local auth (MVP-complete point)
5. Hardening pass (integration tests, load test, docs)

Post-MVP: OIDC login, bulk device import (OneSignal migration), multi-role dashboard RBAC,
encryption key rotation tooling, and — only once the self-hosted product has real usage — an
optional hosted/cloud offering.

## License

Source-available (Business Source License-style — see [LICENSE](LICENSE), currently a draft
pending legal review). Self-hosting, modifying, and internal use are free; the only restriction
is reselling this exact code as a competing hosted service without a commercial agreement.
Converts to Apache-2.0 after the license's change date.

## Contributing

Issues and PRs welcome once the MVP phases land. If this project is useful to you, a
[GitHub Sponsors](https://github.com/sponsors/firemanx07) contribution helps keep it maintained.
