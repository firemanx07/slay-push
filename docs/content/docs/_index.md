---
title: Documentation
type: docs
weight: 1
sidebar:
  open: true
---

Everything needed to self-host slay-push: getting a stack running, configuring it, wiring up
each push provider, and understanding how the pieces fit together.

{{< cards >}}
  {{< card link="getting-started" title="Getting Started" icon="play" subtitle="Clone, configure, and run the stack for the first time." >}}
  {{< card link="configuration" title="Configuration" icon="adjustments" subtitle="Every environment variable, and what's dashboard-managed instead." >}}
  {{< card link="architecture" title="Architecture" icon="view-boards" subtitle="Run modes, the request/queue/worker flow, and the two auth mechanisms." >}}
  {{< card link="deployment" title="Deployment" icon="cloud-upload" subtitle="Compose in production, logging, scaling the worker, backing up the master key." >}}
  {{< card link="dashboard" title="Dashboard" icon="desktop-computer" subtitle="Projects, providers, API keys, devices, and notifications." >}}
  {{< card link="providers" title="Providers" icon="paper-airplane" subtitle="Credential setup for Expo, FCM, APNs, and HMS." >}}
  {{< card link="api-reference" title="API Reference" icon="code" subtitle="The public JSON API, rendered from the OpenAPI spec." >}}
{{< /cards >}}
