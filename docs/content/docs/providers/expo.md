---
title: Expo
type: docs
weight: 1
---

Expo push tokens (`ExponentPushToken[...]`) are sent straight to Expo's push API — no separate
Expo project setup is strictly required.

## Credential JSON

```json
{
  "access_token": ""
}
```

`access_token` is optional. Leave it empty (or omit the field entirely — `{}` is a valid
credential) unless you've enabled
[enhanced security for push notifications](https://docs.expo.dev/push-notifications/sending-notifications/#additional-security)
on your Expo account, in which case paste the access token you generated there.

## Testing

Expo has no local credential test — there's no key material to validate offline, since an empty
credential is itself valid. The **Test** button on the Providers tab isn't available for Expo;
the first real send is what surfaces a misconfigured or revoked access token.

## Registering a device

Set `"provider": "expo"` and `"platform"` to `"ios"`, `"android"`, or `"web"` when calling
`POST /api/v1/devices`, with `token` set to the device's Expo push token.
