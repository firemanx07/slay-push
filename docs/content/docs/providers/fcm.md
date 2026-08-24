---
title: FCM
type: docs
weight: 2
---

slay-push talks to Firebase Cloud Messaging's HTTP v1 API directly, authenticating as a service
account — the legacy FCM server-key API is gone and isn't supported.

## Get a service account key

1. In the [Firebase console](https://console.firebase.google.com), open your project's
   **Project settings → Service accounts**.
2. Click **Generate new private key**. This downloads a JSON file.
3. Paste the **entire contents** of that file into the dashboard's Credential JSON field, exactly
   as downloaded — don't trim or reshape it. slay-push reads `project_id` out of it to build the
   send URL, and uses the rest of the file (`private_key`, `client_email`, etc.) to sign OAuth2
   requests for the messaging scope.

The pasted JSON looks like:

```json
{
  "type": "service_account",
  "project_id": "your-firebase-project",
  "private_key_id": "...",
  "private_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n",
  "client_email": "firebase-adminsdk-...@your-firebase-project.iam.gserviceaccount.com",
  "client_id": "...",
  "token_uri": "https://oauth2.googleapis.com/token"
}
```

## Testing

**Test** on the Providers tab confirms the credential parses as a valid service account and can
mint an OAuth2 token for the messaging scope — without sending a real push.

## Registering a device

Set `"provider": "fcm"` and `"platform"` to `"android"` or `"web"` when calling
`POST /api/v1/devices`, with `token` set to the device's FCM registration token.
