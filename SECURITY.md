# Security Policy

## Supported versions

This project is pre-1.0 and under active development. Only the latest commit on `main` is
supported — there are no maintained release branches yet.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities. Instead, use
[GitHub's private vulnerability reporting](../../security/advisories/new) for this repository
(Security tab → "Report a vulnerability"). This lets us assess and fix the issue before it's
public.

Include, if possible:
- A description of the vulnerability and its impact
- Steps to reproduce, or a proof of concept
- The affected component (e.g. a specific provider adapter, the auth/API-key middleware, the
  envelope-encryption code)

## Scope notes for reviewers

A few design points that are deliberate, not oversights — worth knowing before filing:

- **`APP_MASTER_KEY` is a single operator-held secret with no external KMS integration.**
  Provider credentials are envelope-encrypted (a per-credential AES-256-GCM data key wrapped by
  this master key), but losing the master key makes all stored credentials unrecoverable by
  design — there is intentionally no recovery path that doesn't route through it.
- **At-least-once delivery.** A crash mid-send can duplicate a push on retry. No push provider
  offers a true idempotency key, so this is a documented tradeoff, not a bug report.
- **Rate limiting fails open on Redis errors** (both inbound API-key limits and outbound
  per-provider limits) — availability is prioritized over strict enforcement for a self-hosted
  service. If you believe this crosses into a real abuse vector, please still report it.
