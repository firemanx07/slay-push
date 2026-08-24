---
title: Configuration
type: docs
weight: 2
---

Only what's needed before the database is reachable lives in environment variables. Everything
else — provider credentials, API keys — is configured from the dashboard after first-run setup
and stored in Postgres.

| Variable | Default | Notes |
|---|---|---|
| `POSTGRES_PASSWORD` | `pushdispatch` (dev) | Set a real password before exposing this beyond localhost. |
| `APP_PORT` | `8080` | Host-side port for the dashboard/API. |
| `APP_MASTER_KEY` | *(none)* | AES-256-GCM key encrypting provider credentials at rest. **Losing it makes stored credentials unrecoverable.** Generate with `openssl rand -base64 32`. |
| `APP_COOKIE_SECURE` | `true` | Marks the dashboard session cookie HTTPS-only. Set to `false` only for local plain-HTTP development. |
| `LOG_FORMAT` | `json` | `seq` is only meaningful with the dev compose overlay. |
| `LOG_LEVEL` | `info` | |

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
