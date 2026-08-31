---
title: Load Testing
type: docs
weight: 8
---

`server loadtest` is a black-box HTTP client bundled into the same binary as every other
subcommand — it only talks to the public API, so it works identically against a local dev run or
a real deployment, with no database or Redis access of its own.

## Running it

```bash
server loadtest \
  --base-url http://localhost:8080 \
  --api-key sp_live_... \
  --devices 200 \
  --notifications 500 \
  --concurrency 20
```

The API key needs `send` scope (it satisfies `read` too, which the completion-polling phase
needs). Flags:

| Flag | Default | Meaning |
|---|---|---|
| `--base-url` | `http://localhost:8080` | Instance to test against |
| `--api-key` | *(required)* | `sp_live_...` key with `send` scope |
| `--devices` | `100` | Devices to register in phase 1 |
| `--notifications` | `200` | Notifications to create in phase 2, each targeting a random registered device |
| `--concurrency` | `20` | Max concurrent requests per phase |

It runs three phases and reports throughput + latency percentiles for each:

1. **Register devices** — `POST /api/v1/devices`, concurrently.
2. **Create notifications** — `POST /api/v1/notifications`, concurrently, each targeting one
   random device from phase 1.
3. **Poll to completion** — a random sample (up to 20) of the notifications created in phase 2,
   polled via `GET /api/v1/notifications/{id}` until every recipient reaches a terminal state
   (`sent`/`delivered`/`failed`) or a 60s timeout. This is the phase that actually exercises
   fanout, the worker, and the outbound rate limiter — phases 1-2 only measure the HTTP/DB layer.

If you're testing against your own instance's default inbound rate limit
(`DEFAULT_RATE_LIMIT_RPS`, 10/sec per key by default), raise it first — otherwise phase 1/2 will
mostly measure that limiter tripping, not the pipeline underneath it. This tool doesn't send real
pushes unless you've seeded a real provider credential; without one, phase 3's recipients
terminate quickly with `error_code=no_credential`, which is still enough to measure the pipeline's
own throughput honestly.

## What running it found — a real bug and a real tuning gap

The first run against this project's own dev stack (200 devices, 500 notifications, default
`DEFAULT_OUTBOUND_RATE_LIMIT_RPS=20`) surfaced two genuine issues, both now fixed:

- **A silent data-loss bug.** A proactively-throttled `HandleSend` attempt (this service's own
  outbound rate limiter, not a provider's 429) was counting against asynq's `MaxRetry` the same as
  a real failure. Under a large backlog sharing one rate limit, tasks could exhaust retries purely
  from waiting their turn — never reaching the provider, and never getting marked `failed` either,
  since the throttle path never called `failRecipient`. The recipient just stayed `queued`
  forever, invisibly. Fixed by wiring `asynq.Config.IsFailure` (`internal/queue.IsFailure`) to
  exempt `*queue.ThrottledError` from the retry-count budget — a throttled task now retries
  indefinitely as capacity frees up, instead of being silently abandoned.
- **A throughput ceiling nobody configured.** Even after that fix, actual throughput sat around
  4-5/sec against a configured ceiling of 20/sec. Cause: asynq only promotes due retry/scheduled
  tasks to pending once per `DelayedTaskCheckInterval` (5s default) — far coarser than the
  limiter's own sub-second `RetryAfter`. Tuned down to 500ms in `runWorker`'s `asynq.Config`,
  which brought a 500-recipient batch's drain time from ~113s down to ~25-30s at the same RPS=20
  setting — much closer to the configured ceiling.

## Real numbers

From a clean run against this project's own dev stack (Postgres/Redis on localhost, `serve-api` +
`worker` on the same machine, `DEFAULT_OUTBOUND_RATE_LIMIT_RPS` at its default of 20, no real
provider credential seeded — 500 recipients failing fast on `no_credential` once each clears the
outbound rate limiter, which is what phase 3 is actually timing) — **these are one machine's
numbers, not a guarantee; run it against your own deployment before relying on it:**

```
Phase 1: registering 200 devices (concurrency 20)...
  device registration: 200 ok, 0 errors (of 200), 4256.4 req/s, wall clock 46.987917ms
  latency: p50=2.304917ms p95=21.138333ms p99=22.258208ms max=22.350209ms

Phase 2: creating 500 notifications (concurrency 20)...
  notification creation: 500 ok, 0 errors (of 500), 5267.2 req/s, wall clock 94.926791ms
  latency: p50=3.284917ms p95=6.967ms p99=8.179417ms max=8.444542ms

Phase 3: polling 20 notifications to completion (1m0s timeout each)...
  end-to-end completion: 20 ok, 0 errors (of 20), 0.8 req/s, wall clock 24.701155834s
  latency: p50=10.720734041s p95=24.727883209s p99=24.727883209s max=24.727883209s
```

The HTTP layer (phases 1-2) handles thousands of requests/sec with single-digit-millisecond p99
latency — Postgres writes, not the API framework, are the ceiling there. Phase 3's end-to-end
latency is dominated entirely by the outbound rate limiter pacing 500 recipients through a
20/sec ceiling (500 ÷ 20 ≈ 25s, matching the observed p99/max) — raise
`DEFAULT_OUTBOUND_RATE_LIMIT_RPS` if your provider account can sustain a higher rate and you want
faster fanout delivery.
