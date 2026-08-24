---
title: HMS
type: docs
weight: 4
---

slay-push talks to Huawei Push Kit's REST API, authenticating with an OAuth2 client-credentials
exchange against your app's ID and secret.

## Get an app ID and secret

1. In [AppGallery Connect](https://developer.huawei.com/consumer/en/service/josp/agc/index.html),
   open your project, then your app, then **Push Kit**.
2. Under **Project settings → App information**, note the **App ID**.
3. Under the same project's **Project settings → General information**, note (or generate) the
   **App secret**.

## Credential JSON

```json
{
  "app_id": "1234567890123456789",
  "app_secret": "abcdef0123456789abcdef0123456789"
}
```

## Testing

**Test** on the Providers tab performs the OAuth2 client-credentials exchange against Huawei's
token endpoint and confirms it succeeds — without sending a real push.

## Registering a device

Set `"provider": "hms"` and `"platform": "android"` when calling `POST /api/v1/devices`, with
`token` set to the device's HMS push token.
