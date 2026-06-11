# Push Notifications — Tenant Guide

This guide explains how a tenant app (any namespace on the Orama
Network) configures push notifications end-to-end. The platform is
**bring-your-own-credentials**: you control your Apple Developer
account, your push keys, and your topic format. The platform provides
delivery infrastructure (an APNs HTTP/2 client pool, a self-hosted
ntfy server, and storage for your encrypted credentials).

Feature #72 implements this. Closes the "tenants must file an ops
ticket to get push enabled" workflow that bug #220 partially fixed for
ntfy/expo.

---

## Provider matrix

| Platform           | Provider              | Privacy            | Setup                                                |
|--------------------|-----------------------|--------------------|------------------------------------------------------|
| iOS (production)   | `apns` (direct)       | Full — no proxies  | Apple Developer account + p8 key                     |
| iOS (TestFlight)   | `apns` (sandbox env)  | Full — no proxies  | Same key, `"environment": "sandbox"`                 |
| Android (FCM)      | `expo` (legacy)       | Routes via Expo+FCM| Expo access token                                    |
| Android (no FCM)   | `ntfy`                | Full — self-hosted | ntfy topic (no Google Play Services required)        |
| Web / push API     | `ntfy`                | Full — self-hosted | Web Push protocol against `push.<dnsZone>`           |

Pick `apns` + `ntfy` for full-privacy stacks (recommended for
privacy-focused apps, GrapheneOS, etc.). Pick `expo` if you'd rather
not run your own Android push infrastructure and your users are on
Google Play Services.

---

## Step 1 — Generate Apple Push credentials (iOS only)

You need an active Apple Developer Program membership for the team
that owns your iOS app's bundle ID.

1. Go to https://developer.apple.com/account/resources/authkeys/list.
2. Click `+` to create a new key.
3. Check **"Apple Push Notifications service (APNs)"**.
4. Name it (e.g. `Orama Push - myapp prod`) and continue.
5. Download the `.p8` file IMMEDIATELY — Apple does NOT let you
   download it again later. Lose it = generate a new key.
6. Note the **Key ID** (10 chars, alphanumeric).
7. Note your **Team ID** from the top-right of the page.
8. Confirm the **Bundle ID** that matches your iOS app (Xcode →
   Project → Signing).

You should now have:
- `AuthKey_<KeyID>.p8` file
- `Key ID` (e.g. `ABC123DEFG`)
- `Team ID` (e.g. `1234567890`)
- `Bundle ID` (e.g. `com.example.myapp`)

The same key signs for **all** apps under the same Apple Developer
team — one key per team is enough.

---

## Step 2 — Choose an ntfy topic mode (Android / Web only)

When using ntfy, the gateway and your client must agree on the topic
URL each device subscribes to. Three modes:

| Mode      | Topic format                          | Privacy           | Notes                              |
|-----------|---------------------------------------|-------------------|------------------------------------|
| `opaque`  | `sha256(namespace + userId + secret)` | **Best**          | Recommended default                |
| `path`    | `ns/<namespace>/<userId>`             | Readable          | Anyone enumerating topics sees IDs |
| `user`    | `<userId>`                            | Reveals user IDs  | Minimal — rarely useful            |

For `opaque`, you generate a **topic_secret** once and bake it into
both your gateway credential record AND your client's signed app
config. Both sides hash the same triple to get the topic. Rotate the
secret by:
1. PUT new `topic_secret` (clients keep computing old topic against
   their config until the app updates).
2. Ship a new client build with the new secret.
3. After all clients update, the old topic stops receiving sends.

---

## Step 3 — Store credentials via the API

All credentials live encrypted in your namespace's row in the gateway's
RQLite cluster. Stored credentials are NEVER returned by any GET
endpoint — responses report `has_<field>: true/false` only.

Auth: every request requires a JWT issued for your wallet, scoped to
your namespace.

### APNs (iOS)

```http
PUT /v1/namespace/push-credentials/apns
Authorization: Bearer <your wallet JWT>
Content-Type: application/json

{
  "team_id":     "1234567890",
  "key_id":      "ABC123DEFG",
  "bundle_id":   "com.example.myapp",
  "p8_key":      "-----BEGIN PRIVATE KEY-----\nMIGT...\n-----END PRIVATE KEY-----",
  "environment": "production"
}
```

`environment` must be `"sandbox"` (Xcode / TestFlight builds) or
`"production"` (App Store builds). A mismatch produces `BadDeviceToken`
at send time, not at PUT time — match your build channel.

Response on success:

```json
{
  "namespace": "myapp-prod",
  "provider":  "apns",
  "configured": true,
  "updated_at": 1700000000,
  "updated_by": "0xWalletAddress…",
  "redacted": {
    "team_id":     "1234567890",
    "key_id":      "ABC123DEFG",
    "bundle_id":   "com.example.myapp",
    "environment": "production",
    "has_p8_key":  true
  }
}
```

### ntfy (Android / Web)

```http
PUT /v1/namespace/push-credentials/ntfy
Authorization: Bearer <your wallet JWT>
Content-Type: application/json

{
  "base_url":     "https://push.dbrs.space",
  "auth_token":   "tk_…",
  "topic_mode":   "opaque",
  "topic_secret": "<32-byte random secret, base64 OK>"
}
```

`base_url` and `auth_token` are both optional:
- Leave `base_url` empty to use the platform's self-hosted ntfy.
- Leave `auth_token` empty when using the platform ntfy (no auth
  needed for opaque topics) or pointing at a public ntfy server.

### Expo (legacy, optional)

Same shape via the older endpoint:

```http
PUT /v1/push/config
{ "expo_access_token": "…" }
```

This is the pre-#72 path; new code should prefer `apns` + `ntfy`.

---

## Step 4 — Verify what's configured

### Per-provider GET

```http
GET /v1/namespace/push-credentials/apns
```

Returns the redacted view (`has_p8_key: true/false` etc.) but never
the secret material. Use this to confirm what you PUT.

### Summary (what providers do I have?)

```http
GET /v1/namespace/push-credentials
```

```json
{
  "namespace":  "myapp-prod",
  "configured": ["apns", "ntfy"],
  "supported":  ["apns", "ntfy", "fcm"]
}
```

- `configured` is what your namespace has stored credentials for.
- `supported` is what this gateway knows how to deliver to (provider
  packages are compiled in and `Register()`-ed at startup).

---

## Step 5 — Register devices from your client

The client-side flow is unchanged from before #72:

```http
POST /v1/push/devices
{
  "device_id":   "<unique per-device ID>",
  "provider":    "apns",           // or "ntfy" / "expo"
  "token":       "<hex APNs token | ntfy topic | Expo token>",
  "platform":    "ios",            // or "android" / "web"
  "app_version": "1.2.3"
}
```

For `apns`, the token is the hex string Apple gives your iOS app at
launch (`UIApplication.didRegisterForRemoteNotificationsWithDeviceToken`).

For `ntfy` with `topic_mode=opaque`, the token is the sha256 hex digest
your client computes locally from `(namespace, userId, topic_secret)`.

For `ntfy` with `topic_mode=path`, the token is `ns/<namespace>/<userId>`.

### UnifiedPush (Android / GrapheneOS, no Google Play Services)

ntfy is a [UnifiedPush](https://unifiedpush.org) distributor, so Android
devices — including de-Googled **GrapheneOS** — can receive push **without
Firebase / Google Play Services**. The flow:

1. The device runs a UnifiedPush **distributor** (the ntfy Android app, or an
   embedded distributor library) pointed at your push host
   (`https://push.<your-zone>`).
2. The app registers with the distributor and is handed an **endpoint URL**,
   e.g. `https://push.<your-zone>/upXXXXXXXX`.
3. Register that endpoint as a push device:

   ```http
   POST /v1/push/devices
   {
     "device_id": "<unique per-device ID>",
     "provider":  "ntfy",
     "token":     "https://push.<your-zone>/upXXXXXXXX",   // the full endpoint
     "platform":  "android"
   }
   ```

The gateway POSTs to the endpoint **verbatim** (per the UnifiedPush spec), so
you don't have to deconstruct it. As a safety measure the endpoint's
scheme+host **must match your configured ntfy push host** — a device token can
only ever publish to your own push server, never an arbitrary host.

You may instead register just the bare **topic** (the endpoint's last path
segment) as the token — both forms work; use whichever your UnifiedPush library
makes convenient.

**GrapheneOS notes:** works under both "No Google Play" and "Sandboxed Google
Play" profiles. The distributor holds the persistent connection (not your app),
so battery impact is the distributor's; high-priority messages
(`priority: "high"`) wake the app from Doze.

---

## Step 6 — Send pushes

Two paths, depending on whether the push originates from your serverless
function or an external system:

### From a serverless function

```javascript
import { push } from "@orama/sdk";

await push.send({
  user_id: "<wallet or user ID>",
  title:   "New message",
  body:    "Hello from %1",
  channel: "messages",
  priority: "high",
});
```

The hostfunc fans out to every registered device for the user, using
each device's recorded `provider`.

### From outside (admin/internal scope)

```http
POST /v1/push/send
Authorization: Bearer <your wallet JWT>
{
  "user_id": "0xUser...",
  "title":   "New message",
  "body":    "Hello",
  "channel": "messages",
  "priority": "high"
}
```

This endpoint is JWT-gated and scoped to your namespace. **Add a finer
allow-list / admin-scope check at your gateway layer before exposing
it to untrusted callers** — see security note in `pkg/gateway/handlers/push/handlers.go`.

---

## Removing credentials

```http
DELETE /v1/namespace/push-credentials/apns
```

Idempotent — returns 200 even if nothing was stored. Subsequent push
sends for that provider become no-ops (devices registered with the
removed provider are skipped with a warning log).

---

## Platform-operator notes

These bits are for whoever runs the Orama gateway cluster, NOT tenants.

### Enabling self-hosted ntfy

The gateway installer takes a `--with-ntfy` flag (install + upgrade
commands). When set on a node, that node:

- Installs the ntfy binary at `/usr/local/bin/ntfy`.
- Runs ntfy as a `ntfy` system user with restricted privileges.
- Listens on `127.0.0.1:8090` (Caddy fronts it for public TLS).
- Persists message cache at `/var/lib/ntfy/cache.db`.
- Generates a Caddy reverse-proxy block for `push.<dnsZone>` →
  localhost:8090, with Let's Encrypt cert via the orama ACME DNS-01
  flow.

For **devnet**, enable on `ns1` (already runs Caddy):

```
orama node install --with-ntfy --nameserver  # (other flags omitted)
```

For **production**, you can either colocate with ns1 or run a
dedicated node. The installer is identical either way.

The preference persists in `/opt/orama/.orama/preferences.yaml` so
subsequent `orama node upgrade` runs keep it on without re-passing
the flag.

### How the gateway handles credentials

- `pkg/push/credentials/` — generic per-(namespace, provider) store
  with LRU+TTL cache (mirrors `pkg/ratelimit`).
- AES-256-GCM at rest via `pkg/secrets` using HKDF-derived key under
  purpose string `namespace-push-credentials`.
- Provider packages register a `Validator` at gateway startup; the
  HTTP handler dispatches to that Validator for schema validation and
  redaction. Adding a new provider (FCM, SMS, …) is one new package +
  one `pushcreds.Register(...)` call.

### Backward-compat with bug #220's `/v1/push/config`

The legacy `/v1/push/config` endpoint still works for `ntfy_base_url`
and `ntfy_auth_token` / `expo_access_token`. Field-by-field semantics:

- If a tenant has a row in `namespace_push_credentials` (the new
  #72 table) for `ntfy`, that record's `base_url` / `auth_token` /
  topic config takes precedence.
- Otherwise the gateway reads from `namespace_push_config` (the 026
  table).

This lets tenants migrate at their own pace. A future migration will
drop the legacy ntfy credential columns once all known tenants have
moved over.

---

## FAQ

**Q. Does the platform hold my Apple p8 key?**
The platform stores it encrypted in your namespace's RQLite row. The
key is derived from the cluster secret and is unique per cluster.
Operators with cluster-secret access can decrypt the key (the
encryption is to protect against database-dump exfiltration, not
against the platform operators themselves). Treat the platform
operators with the same trust level you'd treat a hosting provider.

**Q. Can two tenants share Apple credentials?**
Apple's APNs token-auth model lets one Apple Developer team sign for
all bundle IDs registered under that team. So if two of your apps
live under the same Apple Developer team, they can use the same p8
key — but you still PUT to each namespace separately (one PUT per
namespace).

**Q. What if my p8 key leaks?**
Generate a new one in the Apple Developer dashboard, PUT it to the
gateway. The old key keeps working until you revoke it on Apple's
side; the new key starts working as soon as the gateway's credential
cache TTL expires (30 s) on every gateway in the cluster.

**Q. How do I rotate the ntfy `topic_secret`?**
See "Step 2" — two-phase: ship a new client first that knows BOTH
secrets, then PUT the new secret, then ship a final client that
drops the old. Or accept a short message-loss window during cutover.

**Q. Can I use my own ntfy server instead of the platform's?**
Yes. PUT a `base_url` pointing at your ntfy server. The platform's
ntfy is just a convenience default.

**Q. Are pushes rate-limited?**
The gateway-level per-namespace rate limit (feature #69) applies to
the `POST /v1/push/send` endpoint. Per-provider send rate limits at
the dispatcher level are not yet implemented — track as a follow-up
feature.
