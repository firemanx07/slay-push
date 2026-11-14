# Slay Push platform roadmap

Status: approved 2026-11-13. Each phase gets its own design doc and plan before any code is written.

## Intent

Slay Push becomes an open, self-hosted platform for mobile app push: tested under load, secured, with an SDK, delivery webhooks and reports, standard auth, and swappable providers.

Pitch: own your push pipeline and your delivery data. One command to a running server, a first push in under 5 minutes, no per-monthly-active-user pricing.

License: Apache 2.0.

## Audience

Teams that self-host to keep data residency or audit control (fintech and other regulated sectors), teams that want device and delivery data in their own database, and teams that avoid per-user pricing.

## Goals

- Demonstrate that the server handles concurrency, queuing and orchestration, with recorded results.
- Close security gaps in secrets handling, tenant isolation, dashboard sessions and the supply chain.
- Report delivery outcomes to customers through signed webhooks and queryable reports.
- Ship an SDK that generates device IDs, registers push tokens, and reports delivered and opened events.
- Support OAuth 2.0 and OIDC next to API keys, with dashboard RBAC.
- Let providers, auth methods and delivery sinks be added as plugins.

## Non-goals

- OneSignal bulk import and migration tooling. No further investment in the OneSignal-shaped API.
- Email, SMS and chat channels.
- Journeys and event-triggered workflows.
- A hosted or cloud offering before real usage exists.
- Replacing FCM or APNs delivery. Android and iOS delivery continues to end at FCM and APNs.

## Market position

| Project | Category |
|---|---|
| ntfy, Gotify | Push to the operator's own devices. No SDK for apps, no per-end-user device registry. |
| Novu, Dittofeed | Multi-channel orchestration and journeys. Broader than push. |
| PushBase and similar | Self-hosted OneSignal-style tools, small or web-push focused. |
| FCM, Expo push | Free direct integration. Firebase offers hosted aggregate send and open reports. Delivery data stays in the provider's console, and the app backend stores its own device tokens. |

Gap addressed: an open, self-hosted platform for mobile app push with an SDK, delivery tracking and a dashboard. OneSignal limits its Free plan to 1,000 monthly active users for mobile push since 2026-09-01 (new sign-ups) and 2026-10-01 (existing accounts).

## Approaches considered

| Approach | Scope | Outcome |
|---|---|---|
| A. Best-in-class app push | SDK, delivery reports and webhooks, OAuth, plugin seams | Selected |
| B. Push plus segmentation and journeys | A plus event-triggered flows | Not selected |
| C. Multi-channel orchestrator | A plus email, SMS and chat plugins | Not selected |

## Phases

### Phase 0: Prove and secure

- Test plan for concurrency, queue and orchestration (below).
- Security review and fixes (below).
- Implement `rotate-key`, currently declared in the CLI and not implemented.
- Publish load numbers in the docs.

Done when every test case has a recorded result against a stated target and the security checklist has no open high findings.

### Phase 1: Close the loop

- Delivery webhooks: signed callbacks to a customer backend with retries.
- Delivery reports: queryable per notification.
- SDK v1: device ID generation, push token registration, delivered and opened events.
- One command to a running server, first push in under 5 minutes.

Done when a new app registers a device through the SDK, receives a push, and its backend receives a signed delivery webhook.

Open: first SDK platform (React Native/Expo, native iOS and Android, or web).

### Phase 2: Open the doors

- OAuth 2.0 for server-to-server API access, with scopes per project.
- OIDC login for the dashboard.
- Dashboard RBAC beyond the single local admin.

Done when a team signs in through its identity provider and calls the API with a scoped OAuth token. API keys keep working.

Open: identity providers to test against.

### Phase 3: Make it extensible

- Plugin contract for providers, auth methods and delivery sinks.
- CleverTap as the first third-party provider plugin, optional and off by default.
- Segmentation (`included_segments`, `filters`) as a new targeting strategy, only on demand.

Done when a provider can be added without editing server code.

Open: plugin mechanism (separate process over HTTP or gRPC, or compiled-in extensions). Decided after phases 1 and 2.

## Phase 0 test plan

Pipeline under test: API request, one `fanout` job, per-recipient send jobs on Redis (asynq), provider adapters, status in Postgres. Targets are set after a baseline run.

| Case | Action | Must hold |
|---|---|---|
| Burst enqueue | Many clients create notifications concurrently against a fake provider adapter. | Every notification reaches a final state. Nothing stays `queued` or `pending`. |
| Large fanout | One group push to a very large device set. | Fanout completes with bounded memory. Recipient count equals device count. |
| Worker killed mid-send | Kill a worker holding in-flight sends. | Jobs are retried and finish. Duplicate pushes per recipient are measured. |
| Throttle correctness | Push past the per-provider outbound limit. | Sends are delayed, not failed. Throttled tasks never consume their retry budget. |
| Provider failure storm | Fake provider returns 429 and 5xx in waves, then recovers. | Backoff applies, no retry flood, everything drains after recovery. |
| Invalid tokens at scale | Provider rejects a share of tokens as invalid or unregistered. | Those devices are marked stale or invalid once. Valid devices are unaffected. |
| Redis restart | Restart Redis during a large run. | No notification is lost. Queued work resumes. |
| Postgres pressure | Exhaust connections and restart Postgres under load. | The service recovers without manual action and without corrupted statuses. |
| Horizontal scaling | Run 1, 2 and 4 workers on the same workload. | Throughput grows with workers until a named bottleneck. No recipient is processed by two workers at once. |
| API rate limit under concurrency | Parallel requests on one key and on many keys. | The per-key limit holds. One key cannot starve another. |

Tooling: the `server loadtest` subcommand and the dev compose stack.

## Phase 0 security checklist

Secrets and keys:
- Build and test `rotate-key` for the master key and per-credential keys.
- Confirm no credential, DEK or master key reaches logs, errors or the dashboard.
- Review API key generation, hashing, scoping and revocation.

Tenant isolation:
- A key for project A can never read or send for project B.
- Same check for dashboard sessions and every dashboard route.
- Rate limits cannot be bypassed by switching project or key.

Dashboard and sessions:
- CSRF protection on every state-changing form.
- Cookie flags, session expiry and logout invalidation.
- Login throttling and password hashing parameters.

Supply chain and operations:
- `govulncheck` and lint stay clean in CI.
- Container runs as non-root on a minimal image.
- Threat model written into `SECURITY.md`.

## Decisions

| Decision | Status |
|---|---|
| Open source, Apache 2.0 | Decided |
| Scope covers security, tests, OAuth, webhooks, reports, SDK, plugins | Decided |
| OneSignal migration out of scope | Decided |
| Focus on app push, not multi-channel orchestration | Decided (approach A) |
| Order: test and secure, then SDK and webhooks, then auth, then plugins | Decided |
| Plugin mechanism | Open, decide after phases 1 and 2 |
| First SDK platform | Open, decide in phase 1 design |
| Identity providers to test against | Open, decide in phase 2 design |
