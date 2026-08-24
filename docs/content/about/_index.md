---
title: About
type: docs
weight: 2
sidebar:
  open: true
---

## What is slay-push?

slay-push is a self-hosted, multi-tenant push-notification dispatch service. It talks directly
to Expo, FCM (HTTP v1), APNs (HTTP/2), and Huawei HMS — no third-party push backend sits in the
middle, and no per-provider stack needs to be run and maintained separately.

One binary, backed by Postgres and Redis, gives a team a device/subscriber registry (register a
device once, target it later by device ID or by an external user ID that groups every device
under one identity), delivery-status tracking per recipient, and a small server-rendered
dashboard for managing projects, provider credentials, API keys, and devices — no config files
to hand-edit after the initial `.env`.

The request and response shapes are deliberately close to OneSignal's REST API, so a team
migrating off a hosted provider can reuse most of their existing integration code rather than
rewriting a client from scratch.

## Project Goals

- **One service, every major channel.** Expo, FCM, APNs, and HMS through one registry and one
  delivery pipeline, instead of a separate SDK and credential flow per provider.
- **A real path off hosted providers.** The OneSignal-shaped API exists so a team that's
  outgrown a hosted provider's pricing, or needs its device and delivery data to stay inside its
  own infrastructure, doesn't have to rewrite its integration to get there.
- **Multi-tenant by design.** Every project's provider credentials, API keys, and devices are
  isolated, enforced at the database level — not bolted on later.
- **No feature gating.** It's Apache 2.0, and every feature described above ships in the
  self-hosted build — there's no crippled free tier and no paid unlock inside the code.

## Author

slay-push is created and maintained by Ghassen Mellassi
([@firemanx07](https://github.com/firemanx07) on GitHub).
