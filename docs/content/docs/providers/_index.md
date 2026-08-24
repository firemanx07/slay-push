---
title: Providers
type: docs
weight: 6
sidebar:
  open: true
---

Each provider is configured the same way, from a project's **Providers** tab in the dashboard:
pick the provider, paste a credential as JSON into the **Credential JSON** field, and save.

{{< cards >}}
  {{< card link="expo" title="Expo" icon="bell" subtitle="An Expo access token — optional, only needed for enhanced security." >}}
  {{< card link="fcm" title="FCM" icon="bell" subtitle="A full Firebase service-account JSON file." >}}
  {{< card link="apns" title="APNs" icon="bell" subtitle="A .p8 token key, or a legacy certificate pair." >}}
  {{< card link="hms" title="HMS" icon="bell" subtitle="A Huawei app ID and app secret pair." >}}
{{< /cards >}}

After saving, use **Test** on the credential's row to validate it locally — parses/builds a real
client from it — without sending an actual push. Expo doesn't support this check; see
[Expo](expo) for why.
