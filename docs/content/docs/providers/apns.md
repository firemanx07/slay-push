---
title: APNs
type: docs
weight: 3
---

slay-push talks to APNs over HTTP/2 and supports both of Apple's auth methods: token-based
(`.p8`, no expiry) and certificate-based (annual expiry). Token-based is preferred if you're
setting this up fresh.

`bundle_id` is required for both auth methods.

## Token-based (recommended)

1. In [Apple Developer → Certificates, Identifiers & Profiles → Keys](https://developer.apple.com/account/resources/authkeys/list),
   create a key with the **Apple Push Notifications service (APNs)** capability enabled and
   download the `.p8` file. Apple only lets you download it once.
2. Note the **Key ID** (shown on the key's page) and your **Team ID** (top-right of the Apple
   Developer account page).

```json
{
  "auth_type": "token",
  "key_id": "ABCD123456",
  "team_id": "TEAM1234XX",
  "private_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n",
  "bundle_id": "com.yourcompany.yourapp",
  "environment": "production"
}
```

`auth_type` defaults to `"token"` if omitted. `environment` defaults to `"production"` — set it
to `"sandbox"` for a development-signed build talking to APNs' sandbox endpoint.

## Certificate-based (legacy)

Only use this if you don't have a `.p8` key to migrate with — it expires annually and has to be
renewed by hand.

```json
{
  "auth_type": "cert",
  "cert_pem": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
  "key_pem": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n",
  "bundle_id": "com.yourcompany.yourapp",
  "environment": "production"
}
```

## Testing

**Test** on the Providers tab parses the key/certificate material and builds a real APNs client
from it — without sending a push — so a malformed `.p8`/PEM or a missing `bundle_id` surfaces
immediately instead of on the first real send.

## Registering a device

Set `"provider": "apns"` and `"platform": "ios"` when calling `POST /api/v1/devices`, with
`token` set to the device's APNs device token.
