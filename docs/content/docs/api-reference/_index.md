---
title: API Reference
type: docs
weight: 7
---

The public JSON API is defined in [`api/openapi.yaml`](https://github.com/firemanx07/slay-push/blob/main/api/openapi.yaml),
the source of truth for every request/response shape below. Every request needs a bearer API
key (`Authorization: Bearer sp_live_...`) generated from a project's API Keys tab in the
dashboard, scoped to `read` or `send`.

{{< cards >}}
  {{< card link="full" title="Open the full API reference" icon="external-link" subtitle="A dedicated, full-page rendering of the OpenAPI spec." >}}
{{< /cards >}}

## Endpoints

| Method | Path | Scope | Description |
|---|---|---|---|
| `POST` | `/devices` | `send` | Register or update a device (upsert on project + token). |
| `POST` | `/notifications` | `send` | Create a notification — enqueued, never sent inline. |
| `GET` | `/notifications/{id}` | `read` | Aggregate delivery status for a notification. |
| `GET` | `/notifications/{id}/recipients` | `read` | Per-device delivery detail for a notification. |
