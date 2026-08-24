---
title: Configuration
type: docs
weight: 2
---

Only what's needed before the database is reachable lives in environment variables. Everything
else — provider credentials, API keys — is configured from the dashboard after first-run setup
and stored in Postgres.

Two of these (`POSTGRES_PASSWORD`, `APP_PORT`) are read by `docker-compose.yml` itself, not by
the application — they template the Postgres container's password and the host-side port
mapping. Everything else is read directly by the Go binary (`internal/config`).

| Variable | Default | Read by | Notes |
|---|---|---|---|
| `POSTGRES_PASSWORD` | `pushdispatch` (dev) | Compose | Set a real password before exposing this beyond localhost — interpolated into `DATABASE_URL` for the `app`/`worker` containers. |
| `APP_PORT` | `8080` | Compose | Host-side port the dashboard/API is exposed on. |
| `APP_HTTP_ADDR` | `:8080` | App | The address the binary itself binds to inside its container. |
| `DATABASE_URL` | *(localhost default, overridden by Compose)* | App | Full Postgres connection string. |
| `REDIS_URL` | *(localhost default, overridden by Compose)* | App | Full Redis connection string. |
| `APP_MASTER_KEY` | *(none)* | App | AES-256-GCM key encrypting provider credentials at rest. **Losing it makes stored credentials unrecoverable.** Generate with `openssl rand -base64 32`. |
| `APP_COOKIE_SECURE` | `true` | App | Marks the dashboard session cookie HTTPS-only. Set to `false` only for local plain-HTTP development. |
| `DEFAULT_RATE_LIMIT_RPS` | `10` | App | Per-API-key rate limit; the per-project ceiling is 5x this. |
| `LOG_FORMAT` | `json` | App | `seq` is only meaningful with the dev compose overlay. |
| `LOG_LEVEL` | `info` | App | |
| `SEQ_URL` | *(none)* | App | Only read when `LOG_FORMAT=seq`. |

## Why no external KMS

Provider credentials use envelope encryption: each credential gets its own randomly generated
key (AES-256-GCM), and that key is itself wrapped by `APP_MASTER_KEY`. This keeps key rotation
cheap — rotating the master key only means re-wrapping small per-credential keys, not
re-encrypting every credential — without requiring an external KMS/Vault dependency, which would
break the "one `docker compose up`" self-hosting story. `APP_MASTER_KEY` is the one secret you're
responsible for protecting and backing up.

## Not yet configurable from the dashboard

- **Authentication mode.** Local email/password login only — no OIDC yet.
- **Rate limits.** A single global setting, not per-project.

Both are tracked on the [project roadmap](https://github.com/firemanx07/slay-push#roadmap).
