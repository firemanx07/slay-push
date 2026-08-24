---
title: Dashboard
type: docs
weight: 5
---

The dashboard is a server-rendered operator control panel — every logged-in user sees every
project in the deployment. There's no per-user project ownership or self-service signup; it's
built for whoever operates the deployment, not for end customers.

## First run

The first visit to the dashboard redirects to `/setup`, which creates the first admin account.
Once an admin exists, `/setup` is permanently unreachable — there's no way to re-run it against
an already-initialized instance. From then on, `/login` and `/logout` handle sessions as usual.

## Projects

A project is the unit of tenancy: it owns its own provider credentials, API keys, and devices.
Create one from the dashboard's project list, then open it to reach its tabs:

- **Providers** — add or update a credential for expo/fcm/apns/hms, and run a local
  "test credentials" check without sending a real push. See the
  [provider setup guides](../providers) for what to paste into the credential field for each one.
- **API Keys** — create a key (shown once at creation — copy it immediately, it can't be
  retrieved again) or revoke one. Keys are scoped to `read` or `send`.
- **Devices** — search registered devices by external user ID or status
  (`active`/`stale`/`invalid`).
- **Notifications** — browse send history; opening a notification shows per-recipient delivery
  status that live-updates until every recipient reaches a terminal state.

## Credential testing

"Test credentials" validates a stored credential locally — parsing/building a client from it —
without sending an actual push. Expo doesn't support this check (there's nothing to validate
locally beyond JSON shape); expo credentials rely on a real send to surface a bad token.
