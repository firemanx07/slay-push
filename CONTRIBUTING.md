# Contributing to slay-push

Thanks for considering a contribution. This project is early-stage (see the roadmap in
[README.md](README.md) and [CHANGELOG.md](CHANGELOG.md)) — expect the data model and API
surface to still move.

## Getting started

```bash
cp .env.example .env
docker compose -f deploy/docker/docker-compose.yml -f deploy/docker/docker-compose.dev.yml up
```

This gives you Postgres, Redis, and Seq (dev-only log viewer at `http://localhost:8081`)
alongside the app and worker with hot reload.

Outside of Docker, you'll need Go (see `go.mod` for the version) plus a reachable
Postgres/Redis for anything beyond `go build`.

## Before opening a PR

Run the same checks CI runs:

```bash
go build ./...
go vet ./...
gofmt -l .            # should print nothing
go test ./...
golangci-lint run ./...
govulncheck ./...
```

## Code style

- Comments are doc-only: they describe behavior/contract, not rationale. Don't explain *why* a
  change was made or reference planning context, phase numbers, or issue numbers in code
  comments — that belongs in the PR description and commit message, since comments outlive both
  and shouldn't need updating when the reasoning behind them is no longer relevant.
- Keep additions scoped to what's needed; avoid speculative abstractions for requirements that
  don't exist yet (see the Design Principles section of the project's extension points —
  `provider.Adapter` and `targeting.Resolver` — before adding a new one of either).
- Commit messages: short, imperative mood (`Add HMS provider adapter`, not `Added` or `Adding`).

## Adding a provider adapter

Providers self-register into `internal/provider`'s factory registry from an `init()` — see any
existing package under `internal/provider/{expo,fcm,apns,hms}` for the shape. Adding a new one
should never require touching existing provider code or the dispatch/queue layers.

## Reporting bugs / requesting features

Use the issue templates. For security issues, see [SECURITY.md](SECURITY.md) instead of opening
a public issue.
