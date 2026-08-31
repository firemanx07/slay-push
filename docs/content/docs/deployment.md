---
title: Deployment
type: docs
weight: 4
---

## Docker Compose

The base `deploy/docker/docker-compose.yml` runs everything needed in production: `app`
(`serve-all`), `worker`, a one-shot `migrate` init container, Postgres, and Redis. Healthchecks
are wired on every service.

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
```

There's no bundled reverse proxy or TLS termination — front it with Caddy, Traefik, or nginx if
you're exposing it beyond localhost.

## Logging

Production logging is plain JSON to stdout (`LOG_FORMAT=json`), so it plugs into whatever log
stack you already run — Loki, ELK, Datadog, CloudWatch — without any extra configuration. The
`seq` log format and the bundled [Seq](https://datalust.co/seq) viewer are dev-only conveniences
from the `docker-compose.dev.yml` overlay; production never needs Seq running.

## Backing up `APP_MASTER_KEY`

Every provider credential is encrypted at rest with a key that's ultimately wrapped by
`APP_MASTER_KEY`. If you lose it, every stored provider credential becomes permanently
unrecoverable — there's no external KMS to fall back on. Treat it like a database backup:
store it somewhere durable and separate from the `.env` file itself (a secrets manager, a sealed
vault, or at minimum an encrypted backup) before you add real provider credentials.

## Scaling the worker

`worker` is a separate process specifically so provider slowness or an outage never affects
dashboard or API availability, and so it can be scaled independently of `app`:

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --scale worker=3
```

The outbound rate limiter's state lives in Redis, not in each worker process's memory, so scaling
to multiple replicas correctly shares one aggregate ceiling per `(project, provider)` pair —
replicas don't each get an independent budget that would silently multiply the configured
`DEFAULT_OUTBOUND_RATE_LIMIT_RPS`.

## Bootstrap without the setup wizard

For scripted deployments (CI, infrastructure-as-code), the `bootstrap` CLI subcommand creates the
first admin account idempotently, without needing to click through `/setup`:

```bash
docker compose -f deploy/docker/docker-compose.yml run --rm \
  -e BOOTSTRAP_ADMIN_EMAIL=admin@example.com \
  -e BOOTSTRAP_ADMIN_PASSWORD=<a-real-password> \
  app bootstrap
```

It's a no-op if an admin account already exists.
