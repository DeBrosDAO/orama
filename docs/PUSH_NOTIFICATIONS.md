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
| `path`    | `ns-<namespace>-<userId>`             | Readable          | Anyone enumerating topics sees IDs |
| `user`    | `<userId>`                            | Reveals user IDs  | Minimal — rarely useful            |

> **An ntfy topic is a single path segment.** ntfy serves topics at
> `https://<host>/<topic>`, so a topic containing `/` is not a nested topic —
> it is a different URL that ntfy answers with `404 page not found`, and every
> push to it fails. Use a separator that is not `/` (this is why `path` mode
> uses `-`). `topic_mode` is a convention between your client and your own
> topic-derivation code: the gateway publishes to whatever token the device
> registered and never re-derives it from the mode.

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

`auth_token` is optional — leave it empty when using the platform ntfy (no
auth needed for opaque topics) or pointing at a public ntfy server.

`base_url` falls back to the namespace gateway's own `ntfy_base_url`, which
the host sets to `https://push.<dnsZone>` and forwards to every namespace
gateway it spawns. Set it explicitly when you run your own ntfy server. If
neither is set the ntfy provider is never registered and every ntfy push fails
*before* any HTTP request — `http=0`, with `reason` naming the missing base URL.

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
  "supported":  ["apns", "ntfy"]
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

For `ntfy` with `topic_mode=path`, the token is `ns-<namespace>-<userId>` — a
single path segment, per the topic rule in Step 2.

### Registrations from a device-bound session

When the registering session is bound to a device ([AUTH.md](AUTH.md#devices)),
the registration is recorded against that device — the `did` the gateway
verified, not the `device_id` in the body, which stays the app's own label. From
then on revoking the device and ending its push registrations are one fact:
before listing or sending, the gateway asks the cluster registry which of the
user's registrations belong to revoked devices, skips them, and deletes them.
No push sent through a registration made at `/v1/push/devices` reaches a revoked
device, whichever gateway revoked it, and nothing has to clean up after it. A
registration from a session bound to no device behaves as it always has.

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

## Step 5b — Register by rotating push topic (no account binding)

`POST /v1/push/devices` keys a device on the caller's account (the JWT subject,
or the `account_id` claim), so the gateway holds a lasting account → device
mapping. A **push topic** registration (FEAT-265) keys the device on a value
the device chooses instead, and stores nothing about the account. Both modes
are available per registration; the account-bound path above is unchanged.

A push topic here is unrelated to an ntfy topic (Step 2): it is an address the
gateway resolves to a device, whatever the device's provider.

### How it works

1. The device generates a random **topic secret** — 16 to 64 bytes (at least
   128 bits) from a cryptographic RNG, sent **hex-encoded** — and keeps it. Use
   a fresh secret per namespace: the same secret gives the same topic id
   everywhere.
2. The **topic id** is the lowercase hex SHA-256 of the decoded secret bytes.
   The device computes it and gives it to whoever should be able to push to it
   (its contacts, through your app's own channel). The registration response
   also returns it.
3. Senders address the topic id. Registering, refreshing and removing the topic
   require the secret, which the gateway never stores — it stores only the
   topic id. A contact who knows a topic id can push to it, but cannot re-point
   it or delete it with that id: the id presented as a secret hashes to a
   different topic. (Two exceptions are listed under "What this does not
   protect".)
4. To rotate, the device generates a new secret and registers its provider
   token under it. A provider token belongs to **one** topic per namespace, so
   the new registration removes the previous topic in the same atomic write;
   senders holding the old id get `TopicNotFound` from then on.

### Register or refresh

```http
POST /v1/push/topics
Authorization: Bearer <credential with the namespace's push grant>
{
  "topic_secret": "<32–128 hex characters>",
  "provider":     "apns",          // "ntfy" | "expo" | "apns" | "apns_voip"
  "token":        "<the provider token, as for /v1/push/devices>"
}
```

```json
{ "status": "ok", "topic_id": "<64 hex characters>", "expires_at": 1700611200 }
```

A registration lives **7 to 8 days**: `expires_at` is now + 7 days
(`push.TopicTTL`) rounded **up** to a whole UTC day
(`push.TopicExpiryGranularity`), so the stored time does not record when the
device registered. Re-registering with the same secret moves `expires_at`
forward and may replace the token (a refreshed APNs token, say). A device that
stops refreshing stops receiving once it expires.

### Remove

```http
DELETE /v1/push/topics
{ "topic_secret": "<the same secret>" }
```

`200` when removed; `404` when there is no such topic — including when the
secret is wrong, because a wrong secret names a different topic.

### Errors

| Status | Meaning |
|---|---|
| `400` | Body not JSON; secret not hex or outside 16–64 bytes; unknown `provider`; `token` missing or over 512 bytes. |
| `403` | The namespace could not be resolved. |
| `404` | `DELETE` of a topic that is not registered (or a wrong secret). |
| `405` | A method other than `POST` or `DELETE`. |
| `500` | The namespace database refused the write; nothing was changed. |
| `503` | Push is not configured on this gateway. |

Authorization is the route policy's, identical to `/v1/push/devices`: the
namespace's `push:write` grant with ownership, from any credential that holds
it. Unlike device registration, the handler does not require a logged-in user
and never reads the caller's identity, so the app's runtime key can register
topics on its own. Registration is rate-limited by the same per-client and
per-namespace limiters as every other gateway route; there is no per-namespace
cap on the number of topic rows.

### What this protects

- **The table holds no account ↔ topic mapping.** `push_topics` has no user,
  subject, wallet or registration-time column, the handler never reads the
  caller's subject or claims, and the push code logs neither the subject, the
  topic id nor the secret. The provider-token fingerprint is keyed separately
  from `push_devices.token_fp` and bound to the namespace, so neither the two
  tables nor two namespaces' tables can be joined on it. Rows are stored in
  topic-id order (`WITHOUT ROWID`), not the order they were registered in.
- **Senders cannot link rotations or take over a topic by its id.** A contact
  knows only topic ids; each rotation is a fresh, unrelated id.
- **Provider tokens are kept out of what senders see.** Providers drop the
  request URL from transport errors (it holds the token, or the ntfy topic a
  UnifiedPush endpoint resolves to), and a failed delivery's reason and message
  have any remaining copy of the token replaced with `[device-token]` and any
  request URL with `[request-url]`. A function that passes its
  `push_send_topic` envelope on does not hand the token to the sender.
- **Serverless functions cannot touch the table.** `push_topics` is on the SQL
  guard's reserved list in `SERVERLESS.md`; a function that could write it
  could re-point a topic without the secret.

### What this does not protect

- **The provider token is a stable identifier, and the platform sees it.** An
  APNs/FCM token or ntfy topic does not change when the device rotates its
  push topic, so anyone with the namespace database and the cluster's
  encryption root can link successive topics of one device through it. If the
  same token is also registered on the account path (`/v1/push/devices`), the
  two registrations can be linked by decrypting both. Rotation hides the
  device from senders, not from the platform.
- **Whoever knows a device's provider token can take its topic.** Registering
  that token under their own secret removes the device's topic (one token, one
  topic) and routes their topic to the device, until the device next
  re-registers. `push_devices` has the same last-writer-wins rule. APNs and
  Expo tokens are not normally exposed; ntfy topics must be unguessable.
- **Namespace members with `db:write` can read and rewrite the rows.** The
  table lives in the namespace's own RQLite, so `/v1/rqlite/*`, the database
  export and the import reach it. The secret protects a topic from senders and
  runtime callers, not from the namespace's own owners and developers.
- **Transport metadata still exists.** The namespace-grant check on this route,
  as on every push route, logs the caller's identity, and the request log
  records method, path, client IP and API key id for 7 days. That shows *that*
  a caller registered a topic and when; the topic id and secret are in the
  body, which is not logged. The stored expiry is only a day, so in a
  namespace with more than one registration a day it does not single out the
  request that made the row — but a `db:write` member who watches the table
  live can still match a new row to the request that just arrived.
- **Revoking a device cannot reach its topic registrations.** Nothing ties a
  topic to an account, so revoking an account's device (the future FEAT-422)
  has no row to find. What bounds a topic is its expiry and the device's own
  rotation; a lost device's topics lapse within 8 days. A device that wants to
  stop receiving at once calls `DELETE /v1/push/topics`.
- **A dead token keeps its row until expiry.** When the provider reports the
  token unregistered, a function sender sees `unregistered: true` in the
  `push_send_topic` envelope (an HTTP sender gets `502`) but holds no secret to
  remove the topic; the row lapses with its expiry.

### Rolling out

Use `/v1/push/topics` and deploy functions that import `push_send_topic` only
once every node runs a release that has them: an older gateway rejects the
route, and an older engine cannot instantiate a module that imports the new
host call.

---

## Step 6 — Send pushes

Two paths, depending on whether the push originates from your serverless
function or an external system:

### From a serverless function

Functions are Go compiled with TinyGo (see `SERVERLESS.md`). Push is
exposed as the `push_send` / `push_send_v2` host functions
(pointer/length ABI, same as the other host calls):

```go
//go:wasmimport env push_send
func pushSend(userIDPtr *byte, userIDLen uint32, msgPtr *byte, msgLen uint32) uint32

msg, _ := json.Marshal(map[string]any{
    "title":    "New message",
    "body":     "Hello from %1",
    "channel":  "messages",
    "priority": "high",
})
// pass the user ID and msg bytes via the ptr/len ABI
```

The `msg` JSON matches `pkg/serverless/hostfunctions.PushSendArgs`:
`title`, `body`, `channel`, `priority` (`"high"`/`"normal"`), `badge`,
`sound`, `data`, `target_provider`, `exclude_provider`, `message_id`.
The user ID is passed separately (the namespace comes from the
server-trusted invocation context — a function can only push to users
in its own namespace).

`push_send` returns 1/0. `push_send_v2` instead returns a packed
`ptr<<32|len` JSON envelope with per-device results (HTTP status,
reason, unregistered flag) — parse it; a non-zero return does NOT mean
every device succeeded.

The hostfunc fans out to every registered device for the user, using
each device's recorded `provider`.

To push to a **push topic** (Step 5b) instead of a user, use
`push_send_topic`. It takes the topic id where `push_send_v2` takes the user
ID, accepts the same `msg` JSON, and returns the same envelope:

```go
//go:wasmimport env push_send_topic
func pushSendTopic(topicIDPtr *byte, topicIDLen uint32, msgPtr *byte, msgLen uint32) uint64
```

The topic id must be 64 lowercase hex characters, and only topics registered in
the invocation's own namespace are reachable. A topic that is not registered,
or has expired, is not a failed call: the envelope is `ok: false`,
`devices_attempted: 0`, with one result carrying `reason: "TopicNotFound"` and
`unregistered: true` — stop addressing that id. A return of `0` means the call
failed: it was invalid (malformed topic id or JSON, message over 16 KiB, no
namespace) or the gateway could not look the topic up (database unavailable,
token undecryptable). When push is not configured, it returns the same
`ok: true`, nothing-attempted envelope as `push_send_v2`.

`target_provider` and `exclude_provider` apply as they do for a user: if they
exclude the topic's one device, the envelope is `ok: true` with
`devices_attempted: 0` — nothing was sent.

### From outside (a backend holding the push grant)

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

This endpoint is scoped to your namespace and needs its `push:write` grant,
from a wallet JWT or an API key; the `runtime` role holds that grant, so it is
not admin-only. **Add a finer allow-list / admin-scope check at your gateway
layer before exposing it to untrusted callers** — see security note in
`pkg/gateway/handlers/push/handlers.go`.

The push-topic equivalent takes `topic_id` in place of `user_id`, with the same
message fields. It carries exactly the route policy of `/v1/push/send`: the
namespace's `push:write` grant with ownership. That grant is part of the
data-plane set the `runtime` role holds, so it is not an admin-only route —
the same is true of `/v1/push/send` today:

```http
POST /v1/push/topics/send
{ "topic_id": "<64 hex characters>", "title": "New message", "body": "Hello" }
```

`200` delivered; `400` malformed `topic_id`; `404` topic not registered or
expired; `502` the provider refused delivery; `503` push not configured.

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

### Self-hosted ntfy (installed on every node)

ntfy is installed unconditionally on every node by `orama node install`
and `orama node upgrade` — there is no flag to enable or disable it,
and nothing is persisted to `preferences.yaml`. Each node:

- Installs the ntfy binary at `/usr/local/bin/ntfy`.
- Runs ntfy as a `ntfy` system user with restricted privileges.
- Listens on `127.0.0.1:10109` (Caddy fronts it for public TLS).
- Persists message cache at `/var/lib/ntfy/` (owned by the ntfy user).
- Generates a Caddy reverse-proxy block for `push.<dnsZone>` →
  localhost:10109, with Let's Encrypt cert via the orama ACME DNS-01
  flow.

ntfy only binds to localhost, so nodes that don't host a public
`push.*` DNS entry simply run an idle ntfy with no inbound traffic —
uniform install means no per-node toggling and no surprises when the
DNS topology changes.

### How the gateway handles credentials

- `pkg/push/credentials/` — generic per-(namespace, provider) store
  with LRU+TTL cache (mirrors `pkg/ratelimit`).
- AES-256-GCM at rest via `pkg/secrets` using HKDF-derived key under
  purpose string `namespace-push-credentials`.
- Provider packages register a `Validator` at gateway startup; the
  HTTP handler dispatches to that Validator for schema validation and
  redaction. Adding a new provider (FCM, SMS, …) is one new package +
  one `pushcreds.Register(...)` call.
- Push-topic registrations (Step 5b) live in the namespace table
  `push_topics` (migration 059), keyed on `(namespace, topic_id)`. The
  provider token is sealed under the encryption root with purpose
  `push-topic-tokens`, and `orama operator rotate-secrets` re-encrypts it with
  the other stored secrets. Its fingerprint (`token_fp`, purpose
  `push-topic-token-fp`) is keyed from the **cluster secret** instead, which a
  secrets rotate leaves alone — the walk cannot recompute fingerprints, so a
  rotating key would stop a rotated topic from replacing the old one. Both
  purposes differ from the `push_devices` ones. A registration's eviction of
  superseded and expired rows and its upsert run as one atomic RQLite batch;
  there is no background sweeper.

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
The gateway-level per-client and per-namespace rate limits (feature #69)
apply to every push route, `POST /v1/push/send` and device and topic
registration included. Per-provider send rate limits at
the dispatcher level are not yet implemented — track as a follow-up
feature.
