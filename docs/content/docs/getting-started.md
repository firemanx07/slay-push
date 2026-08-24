---
title: Getting Started
type: docs
weight: 1
---

## Requirements

- Docker and Docker Compose
- A domain or reverse proxy in front of the service if you're exposing it beyond localhost —
  slay-push doesn't bundle TLS termination

## Run it

```bash
git clone https://github.com/firemanx07/slay-push.git
cd slay-push
cp .env.example .env
```

Edit `.env`:

- Set `POSTGRES_PASSWORD` to something real.
- Generate `APP_MASTER_KEY`:

  ```bash
  openssl rand -base64 32
  ```

  This key encrypts every provider credential at rest. **There is no recovery path if it's
  lost** — no external KMS is involved by design (see [Configuration](../configuration) for why).
  Store it somewhere durable before you add real provider credentials.

Start the stack:

```bash
docker compose -f deploy/docker/docker-compose.yml up
```

Open `http://localhost:8080`. The first visit routes to a setup wizard that creates your admin
account. Once that's done, `/setup` becomes permanently unreachable — there's no way to re-run it
against an already-initialized instance.

## Local development

The dev overlay adds hot reload and a [Seq](https://datalust.co/seq) log viewer:

```bash
docker compose -f deploy/docker/docker-compose.yml -f deploy/docker/docker-compose.dev.yml up
```

Seq's UI is at `http://localhost:8081`. This overlay is dev-only by design — production logging
is plain JSON to stdout, so it plugs into whatever log stack you already run (Loki, ELK, Datadog,
CloudWatch) without needing Seq at all.

## Next steps

1. Log in and create a project.
2. Add a provider credential — see the [provider setup guides](../providers).
3. Generate an API key from the dashboard and start registering devices — see the
   [API Reference](../api-reference).
