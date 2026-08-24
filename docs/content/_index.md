---
title: slay-push
layout: hextra-home
---

<div class="hx:max-w-2xl">
{{< hextra/hero-badge >}}
  <div class="hx:w-2 hx:h-2 hx:rounded-full hx:bg-primary-400"></div>
  <span>Apache 2.0 · self-hosted</span>
{{< /hextra/hero-badge >}}
<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  Push notifications, self-hosted
{{< /hextra/hero-headline >}}
</div>
<div class="hx:mb-10">
{{< hextra/hero-subtitle >}}
  A self-hosted, multi-tenant dispatch service that sends directly to Expo, FCM, APNs, and
  Huawei HMS — no third-party push backend in between.
{{< /hextra/hero-subtitle >}}
</div>
<div class="hx:mb-10 hx:flex hx:flex-wrap hx:gap-4">
{{< hextra/hero-button text="Get Started" link="docs/getting-started" >}}
{{< hextra/hero-button text="View on GitHub" link="https://github.com/firemanx07/slay-push" >}}
</div>
{{< callout type="warning" >}}
**Not yet recommended for production use.** slay-push is MVP-complete — self-hostable,
multi-tenant, all four providers, dashboard-managed credentials and API keys — but the
hardening pass (integration tests, load testing, per-provider outbound rate limiting) is still
in progress. See the [Roadmap](https://github.com/firemanx07/slay-push#roadmap) for status.
{{< /callout >}}
</div>

<div class="hx:mt-16"></div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="One service, four channels"
    subtitle="Sends directly to Expo, FCM (HTTP v1), APNs (HTTP/2), and Huawei HMS — no proxying through a third-party push backend."
    icon="paper-airplane"
  >}}
  {{< hextra/feature-card
    title="OneSignal-shaped API"
    subtitle="Request/response shapes close enough to OneSignal's REST API that a team migrating off it can reuse most of their existing integration code."
    icon="code"
  >}}
  {{< hextra/feature-card
    title="Multi-tenant by design"
    subtitle="Every project gets isolated provider credentials, API keys, and devices — enforced at the database level, not just in application code."
    icon="server"
  >}}
  {{< hextra/feature-card
    title="Dashboard-managed, no config files"
    subtitle="Provider credentials, API keys, and devices are all managed from a server-rendered dashboard after the first-run setup wizard."
    icon="desktop-computer"
  >}}
  {{< hextra/feature-card
    title="Encrypted at rest"
    subtitle="Provider credentials use envelope encryption — a per-credential key wraps the secret, wrapped in turn by your own master key."
    icon="lock-closed"
  >}}
  {{< hextra/feature-card
    title="Async by default"
    subtitle="Notification creation returns immediately; a Redis-backed queue resolves targeting and dispatches in the background, with retries and idempotency built in."
    icon="lightning-bolt"
  >}}
{{< /hextra/feature-grid >}}

<div class="hx:mt-8"></div>
