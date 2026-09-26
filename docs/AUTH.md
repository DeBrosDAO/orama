# Authentication and authorization

Who someone is, what they may do, and how the gateway decides. This is the one
page for the model; `docs/SECURITY.md` is the record of what each piece
replaced and why.

---

## The two identities

Everything that reaches the gateway is one of two things.

**A wallet** is a person. It proves itself by signing a message: EIP-4361
(Sign-In with Ethereum) for an EVM address, SIWS for a Solana one. The gateway
issues the message, the wallet signs it, and the gateway hands back a JWT.

**A key** is a program. `orama_<type>_<payload>_<checksum>`, all base62, minted
by an owner of a namespace and carrying a fixed set of grants. It proves itself
by being presented.

Nothing else authenticates. There is no password, no session cookie, and no
form of sign-up: a namespace is created by a wallet, and every credential in it
descends from that wallet.

---

## Signing in

```
client                          gateway
  |  POST /v1/auth/challenge       |
  |------------------------------->|  mints a nonce, stores it single-use
  |  <-- the message to sign ------|  EIP-4361 / SIWS text, with the nonce,
  |                                |  the domain, and issued-at + expiry
  |  sign it in the wallet         |
  |                                |
  |  POST /v1/auth/verify          |
  |  { message, signature }        |
  |------------------------------->|  recovers the address from the signature,
  |                                |  checks the message it was given back is
  |                                |  the one it issued, consumes the nonce
  |  <-- access + refresh token ---|
```

The message goes back **verbatim**. It carries the nonce the gateway will
consume, the domain the gateway will compare against its own, and the times it
will check — a signature over a message the gateway did not issue proves
nothing about which site asked for it.

The access token lasts 15 minutes. The refresh token lasts 30 days, is stored
hashed, and rotates on every use: presenting one twice is a replay, and the
second attempt fails and is recorded.

`orama auth login` does this with RootWallet, which signs without the key
leaving it. What it keeps is the session — the access and refresh tokens above.
It used to read them out of the response, drop them, and store the API key that
came alongside, which then went in front of every gateway the CLI was pointed at
for the next ninety days. The key is now presented once, to exchange it, and
only when there is no session to renew.

### The lobby

A challenge with no namespace signs you in to `default`. That is the **lobby**:
it belongs to nobody, needs no grant, and writes none. What you get there is a
session and no key, and the one thing that session reaches is
`POST /v1/namespaces` — which creates a namespace and makes you its owner.

Signing in used to claim: the first wallet to reach a namespace with no owner
became its owner. `default` is created by migration 001 with no owner, so on
each cluster it belonged to whichever wallet signed in first, and everyone after
that got a 403 on the namespace that is supposed to be where you stand before
you own anything. Creating a namespace is now the only thing that writes an
owner grant.

A namespace with no owner is one nobody may sign in to (`NAMESPACE_UNOWNED`),
not one the next caller takes.

### Signing in from a machine with no wallet on it

The handshake above needs a wallet on the same machine. On a server reached over
SSH, in a container, or in CI there is none, and the answer used to be a
permanent API key in an environment variable.

The device authorization grant (RFC 8628) splits the two halves:

```
waiting machine                 gateway                 a machine with a wallet
  |  POST /v1/auth/device          |                             |
  |------------------------------->|  records a pending login    |
  |  <-- device code + user code --|                             |
  |                                |                             |
  |  prints the user code          |                             |
  |                                |   POST /v1/auth/device/approve
  |                                |   { user_code, message, signature }
  |                                |<----------------------------|
  |                                |  the same signature check   |
  |                                |  /v1/auth/verify makes      |
  |  POST /v1/auth/device/token    |                             |
  |  { device_code }               |                             |
  |------------------------------->|                             |
  |  <-- access + refresh token ---|                             |
```

Nothing secret crosses between the two machines. The user code is short so it
can be read aloud; it is worthless on its own, because approving it still costs
a wallet signature. The device code is the waiting machine's own credential: it
is 256 bits, stored only as a SHA-256 hash, and collects a session exactly once.

A pending login lasts ten minutes. Polling faster than the interval the gateway
handed back answers `slow_down`; before approval, `authorization_pending`; after
a refusal, `access_denied`. A login nobody came back for is swept away by the
next one.

`orama auth login` prints the user code and the command to run; `orama auth
approve <code>` on a machine that has a wallet is what approves it, and
`--deny` refuses.

There is no `verification_uri` in the response. The RFC's field names a page a
human opens, and there is no such page yet — `orama auth approve <code>` is the
client for the approval endpoint today, and it is what the waiting machine tells
you to run. When a web approval page exists it adds the field and nothing else
changes.

### Which machines are signed in as you

`GET /v1/auth/sessions` lists the live refresh tokens of the calling wallet —
never the tokens themselves, which would turn a fifteen-minute access token into
a thirty-day one — each with the device it is bound to, if any.
`DELETE /v1/auth/sessions/{id}` ends one.

Every session has an id of its own, carried in its access tokens as `sid` and
kept across refresh-token rotations. Ending a session revokes its refresh token
and puts its id on the revocation list, so the access tokens it already minted,
and the sockets they hold open, stop within the list's staleness. A session
issued before sessions carried an id has nothing to name its access tokens by:
ending one stops it minting new ones, the access tokens it already minted work
until they expire, at most fifteen minutes, and the response says so.
`POST /v1/auth/logout` with `all` ends every session of the wallet at once.

---

## Devices

A session belonged to an account and nothing else. Every installation of an
application signed in as the same wallet held interchangeable sessions, so losing
one phone meant ending every session the account had, and nothing a function ran
could tell which installation was calling.

A session may now be bound to a **device**: a key pair one installation holds.
The gateway accepts ECDSA P-256 (ES256 — what iOS's Secure Enclave, Android's
StrongBox and WebCrypto keep non-extractable) and Ed25519. The device's id is the
RFC 7638 thumbprint of its public key: it is computed, never stated, so a client
can name only a device whose key it presents. A signature is base64url, and a
P-256 one may be the 64-byte `r‖s` JOSE uses or ASN.1 DER.

A session bound to a device:

- is issued only against the device's signature over the same sign-in message
  the wallet signed;
- is refreshed only with a fresh proof from the device;
- carries the device in its access tokens as `did`, which a function reads with
  `get_caller_device_id` ([SERVERLESS.md](SERVERLESS.md#context)) the way it
  reads the account with `get_caller_jwt_subject`;
- ends — refresh tokens, access tokens and every socket they hold open — when the
  device is revoked, while the account's other devices stay signed in.

Binding a device is the client's choice in every namespace. A namespace's
**session policy** decides what a sign-in that binds none may still get
(below). Sessions without a device keep working exactly as before.

### Signing in with a device

```
client                                          gateway
  |  POST /v1/auth/challenge                       |
  |  { wallet, namespace, device_id }  ----------->|  the message's Resources carry
  |  <-- message naming urn:orama:device:<id> -----|  urn:orama:device:<device_id>
  |                                                |
  |  the wallet signs the message                  |
  |  the device key signs the same message         |
  |                                                |
  |  POST /v1/auth/verify                          |
  |  { message, signature,                         |
  |    device_key, device_signature,               |
  |    device_label } ---------------------------->|  both signatures, one spent nonce
  |  <-- access + refresh token, device_id --------|
```

`device_key` is the public JWK; `device_id` is its RFC 7638 thumbprint, which
the client computes before asking for the challenge (the SDK's `deviceIdOf`),
and which the response repeats. The wallet's signature says
which device it lets in; the device's says the key is really there; neither alone
binds anything. A device-bound sign-in is not handed an API key: the device is
the credential, and a key for the whole account beside it would outlive revoking
the device.

A key belongs to one account in one namespace, and the lobby binds none. A
revoked device is a tombstone: its key can never sign in again, by anyone. An
Ed25519 key of small order, which "verifies" signatures nobody made, is refused.

The access token of a device-bound session is still a bearer token for its
fifteen minutes; the device's proof guards what outlives it — the refresh, and
the acts listed below. A device-bound session's refresh token is marked (`dv1_`)
and stored under a hash of its own, so a gateway that predates devices cannot
find it, and so cannot refresh it without the device.

### Proving the device on later requests

Refreshing a device-bound session, approving a link from a device, collecting a
linked session, and — from a device-bound session — revoking a device or ending
a session each carry a **device proof**:

```json
"device_proof": {"iat": 1790000000, "id": "<16-128 chars of [A-Za-z0-9_-], random>", "sig": "<base64url>"}
```

`sig` is the device's signature over this text, lines joined by `\n`:

```
orama-device-proof-v1
<action>          refresh | approve | claim | revoke | end-session
<namespace>
<binding>         the refresh token | the user code | the device code
                  | the device id | the session id
<iat>
<id>
```

`iat` must be within 60 seconds of the gateway's clock, and `id` is spent in the
nonce table, so a proof is good once, for one action on one credential. A refresh
without the proof is refused before anything is spent — the rotation, or the
reuse grace a just-rotated token still has — so a thief holding only the refresh
token cannot use either up.

### Revoking a device

```
GET    /v1/auth/devices          the account's devices, revoked ones included
DELETE /v1/auth/devices/{id}     revoke one
```

Revoking tombstones the device, revokes its refresh tokens (including the
reuse-grace slot a just-rotated one would still have), and puts `device:<id>` on
the revocation list — so its access tokens and the sockets they hold open stop
within ten seconds on every gateway. It cannot mint a new session afterwards: its
key is refused at sign-in, refresh and approval. The account's other devices are
untouched. Push registrations made from the device end with it
([PUSH_NOTIFICATIONS.md](PUSH_NOTIFICATIONS.md)).

Any of the account's sessions may revoke any of its devices: one bound to an
active device, with that device's proof (`revoke`, over the id being revoked),
or one bound to no device, which only the wallet can have made. The proof is
what stops an access token lifted off one phone signing the account out of all
the others. A session bound to a pending or revoked device can do nothing here.
Ending a session from a device-bound session takes a proof the same way
(`end-session`, over the session id).

**When the user cannot help themselves.** Under the `approval` policy an account
that lost its only device — or whose stolen device revoked the others — has
nothing left that can approve a new one, and a wallet signature alone cannot, by
design. A namespace operator (the members-write permission) can:

```
GET    /v1/namespace/devices?subject=<wallet>   an account's devices
DELETE /v1/namespace/devices/{id}               revoke one of them
```

With no active device left, the user's next sign-in enrols its device as the
account's first again.

### Adding a device: approval and linking

`PUT /v1/namespace/session-policy {"device_policy": ...}` (the namespace-write
permission) sets what a sign-in must prove:

| Policy | A sign-in that binds no device | A new device's first sign-in |
|--------|-------------------------------|------------------------------|
| `optional` (default) | a session bound to the account | active |
| `required` | refused, `DEVICE_REQUIRED` | active |
| `approval` | refused, `DEVICE_REQUIRED` | active if it is the account's first; otherwise **pending** |

Under `required` and `approval` nothing else hands an end user a credential bound
to no device either: `POST /v1/auth/api-key` and approving a plain
`orama auth login` device code with a wallet answer `DEVICE_REQUIRED`, and so
does refreshing an end user's session that is bound to no device — so turning the
policy on ends those sessions at their next refresh, within fifteen minutes.
Setting `required` or `approval` also revokes every key an end user's sign-in
minted in the namespace — the current one and the ones it rotated from — and the
response says how many (`revoked_sign_in_keys`). A wallet counts as an operator
here exactly as it does at sign-in: its owner, admin or developer grant must be
live — not revoked, not expired, its principal not disabled — and only such
wallets' keys are untouched. A key that expired more than an hour ago (the
longest a token exchanged from a key lives) authenticates nothing and is left as
it is. If revoking them stops partway, the policy is still set (and audited) and
the answer is a `503` with `POLICY_SWEEP_INCOMPLETE` and how many were revoked;
repeating the `PUT` revokes the keys that are left. A sign-in that read the
policy just before it was set can mint its key just after the sweep read the
keys; repeating the `PUT` a moment later revokes that one too. A plain device
login a wallet approved before the policy changed cannot be collected after it.

A wallet holding a control-plane role in the namespace — owner, admin, developer —
is held to `optional` whatever the policy says. The policy protects the
application's users; the people operating the namespace sign in from the CLI,
which holds no device key, and whoever holds such a wallet can change the policy
anyway.

A **pending** device holds no session. The sign-in that enrolled it answers `202`
with `status: pending_approval`, a `user_code` and a `device_code`: the new
device shows the user code, one of the account's active devices approves it, and
the new device collects its session.

```
new device                         gateway                   an active device
  |  (sign-in → 202, or:)             |                              |
  |  POST /v1/auth/device             |                              |
  |  { namespace, device_key }        |                              |
  |---------------------------------->|  a pending login holding     |
  |  <-- device_code + user_code -----|  the new device's key        |
  |                                   |   POST /v1/auth/devices/approve
  |                                   |   { user_code, device_proof }|
  |                                   |<-----------------------------|
  |  POST /v1/auth/device/token       |                              |
  |  { device_code, device_proof }    |                              |
  |---------------------------------->|                              |
  |  <-- access + refresh, device_id -|                              |
```

Starting the link from the new device (`POST /v1/auth/device` with `device_key`)
is **seedless linking**: no wallet is involved at all, and the new device takes
its account from the device that approves it. Starting it from an
approval-policy sign-in is **new-device approval**: the wallet already named the
account, and only one of that account's devices may approve. Either way the
approver proves it holds its own key over the user code, and collecting the
session takes the new device's proof over the device code — the codes alone
collect nothing, and the approving device must still be active when the new one
collects — revoking a stolen approver reaches the device it let in. A device
link cannot be approved with a wallet signature (`orama auth approve`); a plain
login cannot be approved by a device. A wallet can refuse a link only if the
link is its account's.

**Rolling it out.** A gateway that predates devices knows nothing of the policy,
of device-bound sign-in or of `device:` revocations: it signs in without a
device, mints keys, and approves device links with a wallet signature. Set a
session policy, and ship clients that bind devices, only once every gateway in
the cluster runs this version. (What it cannot do is refresh a device-bound
session: it cannot find one.)

---

## What a credential may do

One sentence, three parts: **in this domain, this action, on this resource.**

```
storage:read:avatars/*      read anything under avatars/
db:write:posts              write one table
fn:invoke:checkout          run one function
deploy:*:*                  everything about deployments
*:*:*                       everything
```

A domain is a part of the platform: `storage`, `pubsub`, `cache`, `push`,
`webrtc`, `proxy`, `fn` on the data plane; `db`, `deploy`, `secrets`, `members`,
`namespace`, `audit`, `operator` on the control plane. An action is `read`,
`write`, `invoke` or `manage`. A resource is a glob, and `*` in it matches any
run of characters — including `/`, deliberately, so `avatars/*` covers
`avatars/2026/03/me.png`.

There used to be two models. A **scope** was one of eight words on a key, seven
data-plane ones and `admin`, which was the entire control plane — 58 routes
required it. A **selector** was a `domain:pattern` string on a grant. Neither
could express the other, so a translation sat between them, and it cost more
than complexity: there could be no `developer` role, because every
control-plane route needed the one word; and a grant could not be narrowed to a
table or a deployment, because doing so would have left it holding `admin`
everywhere else.

### The check happens twice, and they are different questions

The **gate**, before the handler runs, asks whether the credential reaches this
domain and action at all. It cannot ask about the object, because nothing has
parsed the request yet.

The **handler** asks again with the object: this CID, this topic, this key. A
credential that reaches storage and is narrowed to `avatars/*` passes the first
and is refused at the second for anything else.

An object nothing could name — a CID this namespace recorded no name for — is
refused by a narrowed permission and reached by an unrestricted one. "I could
not work out what you are touching" is not a reason to allow it.

### What is on disk is unchanged

A key still carries a comma-separated scope string; a grant still carries a role
and an optional selector. One place turns them into permissions, and nothing
about a credential's authority changes in the translation: `admin` is every
permission there is, a data-plane word is its whole domain, `storage:avatars/*`
is `storage:*:avatars/*`, and `db:table=posts:read` is `db:read:posts`.

---

## Roles

A namespace has exactly one owner and any number of members. A member holds a
role, and a role is a set of permissions.

| Role | Holds |
|------|-------|
| `owner` | everything, and only one wallet at a time |
| `admin` | everything the owner does, except being the owner |
| `developer` | the data plane, plus `db`, `deploy`, `secrets` and `fn:manage` — and **not** `members`, `namespace` or `operator` |
| `runtime` | the data plane: storage, pubsub, cache, push, webrtc, proxy, and invoking functions |
| `reader` | nothing beyond the routes that ask for no permission |

`developer` is new, and it could not exist before: every control-plane route
required the single `admin` word, so the role would have resolved to exactly the
same authority and been a label claiming a boundary that was not there. It is
the role for somebody who builds and runs the application but does not decide
who else may.

```bash
orama members list
orama members add 0xabc… --role developer
orama members remove 0xabc…
orama members transfer 0xabc…      # the owner, and only the owner
```

Ownership is transferred rather than granted, and it is one step: the outgoing
owner keeps an admin grant, and there is no moment where the namespace has no
owner.

### Narrowing a grant

A grant may be narrowed to a resource, and four domains apply it today:

| Selector | What it matches |
|----------|-----------------|
| `pubsub:topic=chat.*` | publish, publish-batch, and the subscribe WebSocket |
| `fn:name=checkout` | function invocation |
| `storage:avatars/*` | upload, get, pin and unpin, against the name the object was uploaded with |
| `cache:key=sessions/*` | get, mget, put, delete and scan, against `<map>/<key>` |

A storage name is normalised before it is compared, so `/avatars/me.png` and
`avatars//me.png` are the same object. `..` in a name is **refused**, not
resolved: a storage name is a label rather than a filesystem path, and resolving
one would let `avatars/../keys/x` match `avatars/*`. A cache key is not a path
and is not normalised — `sessions/../tokens/x` is a key called `../tokens/x` in
the `sessions` map, and the map is what the grant names.

A selector can only narrow. `storage:avatars/*` on a `reader`, who holds
nothing, grants nothing: a narrowing that widens is not a narrowing.

A selector in a domain whose **resource** no data path checks is refused when
the grant is written, rather than stored and silently ignored. `db` and
deployments are the two left: the permission is now expressible and the domain
is now narrow — `db:read:posts` no longer implies the rest of the control plane
— but nothing yet parses a statement for the tables it touches, so the table
half would not be applied.

---

## Keys

```bash
orama namespace keys create --scope app-runtime --label web   # data plane only
orama namespace keys create --scope admin --label ci          # everything
orama namespace keys list
orama namespace keys rotate --id <id>
orama namespace keys revoke --id <id>
```

- Every key expires: 90 days by default, a year at most. There is no way to ask
  for one that does not.
- A key does **not** name its namespace. It used to be `ak_<random>:<namespace>`,
  so a key pasted into an issue published which tenant it belonged to.
- The checksum means a leaked key is recognisable offline, by a secret scanner
  or by this code, and a mistyped one is refused without a database lookup.
- Stored as an HMAC. The gateway never holds the key it issued.
- `sk` labels a key holding the control plane and `rk` one holding only the data
  plane. It is a label for whoever finds the string, not what decides authority
  — the row does that.
- Rotating mints a successor with the same grants and shortens the original's
  life to an overlap (7 days by default) rather than revoking it, so there is a
  window in which to deploy the new one.

**Where a key belongs.** A key in a browser bundle is public. Give it the data
plane and nothing else, and let the user's own login carry the rest — `storage`,
`webrtc` and `proxy` will refuse the key on its own anyway. A key that touches
the control plane belongs on a server.

---

## Sending a credential

```
Authorization: Bearer <token>
```

That is the form to use, for a key and for a JWT alike. A token exchanged from
a key carries the key's **stored** form as its subject, not the key: a JWT
payload is base64, not encryption, and a 15-minute token goes to more places
than a 90-day credential should. On a WebSocket upgrade,
where a browser cannot set a header, `?api_key=` or `?token=` is read instead
— and only there: a credential in a query string ends up in the access log, in
the Referer of the next request the page makes, and in history.

Three other spellings are still accepted and are going away: `X-API-Key`,
`Authorization: ApiKey <token>`, and `Authorization: <token>` with no scheme. A
request that uses one comes back with `Deprecation: true` and an
`X-Orama-Deprecation` header saying what to send instead, and the first use by
each namespace is recorded in the audit trail so an owner can see which of their
clients still has to move. Neither the CLI nor the SDK sends one any more.

`ORAMA_TOKEN` is the CI credential and takes either shape. A token is sent as it
is; a key is exchanged for a session once per run, rather than being sent on
every request that run makes.

---

## How long a change takes to land

| Change | When it takes effect |
|--------|----------------------|
| Revoking a key | at once, everywhere — the revocation list is replicated and consulted before any cache |
| Revoking a token | at once, by its `jti` |
| Narrowing a **wallet's** grant | on the next request; the grant is resolved per request |
| Narrowing a **key** — editing its scopes, or revoking a grant it holds | within one minute, on every gateway that had seen it |
| Revoking the token an open WebSocket was opened with | the socket is closed within 10 seconds (`4403`) |
| Ending a session (`DELETE /v1/auth/sessions/{id}`) | its access tokens are refused, and its sockets closed, within 10 seconds |
| Revoking a device | its sessions, access tokens and sockets end within 10 seconds; the account's other devices are untouched |
| The token an open WebSocket was opened with expiring | the socket is closed within 10 seconds of two minutes past its `exp` (`4401`), unless it was refreshed on the socket |
| Revoking a capability, or the device that issued it | the upgrades it would open are refused, and the sockets it opened closed, within 10 seconds (`4403`); a device's revocation is kept seven days, as long as a capability can live |
| Setting a session policy | at once for everything that issues a credential (sign-in, API keys, approvals, device-link claims), which reads the policy itself; a refresh may be judged by a policy its gateway read up to 10 seconds earlier |

That minute is `CredentialStaleness`, and it is a promise rather than a tuning
knob: it is the middleware cache's TTL, and it is named so an operator who
narrows a key knows when it lands.

What a credential may do is read from the grant on every request, not from the
token it is carrying. A token that carried its own answer was a token whose
answer could not be changed until it expired.

---

## Revoking

Revoking a key stops the key **and** the tokens exchanged from it. A JWT
verifies on its signature alone, so there is a revocation list: one token by its
`jti`, or every token issued to a subject before a moment. Revoking a key writes
the second kind, which covers every outstanding token from it. A token minted
*after* the revocation is a new grant and is deliberately not covered.

The list is held in memory and reloaded every 10 seconds. That interval is the
staleness: a revocation takes effect within it.

Logging out revokes the refresh token **and** the access token, so "log me out"
does not mean "stop me getting a new one".

### Open WebSockets

A WebSocket is authorized once, at the upgrade, and then stays open. It used to
stay open on trust: a socket opened with a fifteen-minute token kept serving for
as long as the client kept it, and no revocation reached it.

Every socket opened with a token — a function socket, stateless or persistent,
and a pub/sub subscription — is now registered with that token's claims. Every
10 seconds, the revocation list's own staleness, each gateway reloads the list
and re-checks all of its open sockets against it, so a revocation recorded on any
gateway closes the sockets it covers on every gateway within that interval:

| Close code | Means |
|------------|-------|
| `4401` | the token expired more than two minutes ago and was not refreshed on the socket — reconnect with a fresh token |
| `4403` | the token, its session or its subject was revoked — sign in again |

The two minutes cover clock skew and a client refreshing its token on the
socket: a persistent function socket takes `{"__orama":"auth.refresh","jwt":…}`
and is held to the new token from then on (see
[SERVERLESS.md](SERVERLESS.md#websockets)). A refresh must be for the **same
subject** the socket was opened with, and a socket opened with an API key or no
credential cannot take one on — a refresh keeps a socket open, it does not hand
it to somebody else.

A socket opened with an API key has no token, and is not re-checked: revoking the
key stops it opening new sockets, not the ones it has open.

### Capability WebSockets

A messaging application wants a sender to reach a recipient's mailbox without
the node that terminates the socket being told which account is sending. A
function that declares `ws_auth: capability` in function.yaml accepts a
**capability** in place of a credential:

```
GET /v1/functions/rpc-router/ws?namespace=anchat&cap=<token>
```

The recipient's device mints the capability from inside the function
(`capability_mint`, [SERVERLESS.md](SERVERLESS.md#capabilities)) and hands it to
its correspondents however the application likes. A sender presents it and
nothing else: a request carrying both a capability and a credential of its own
is refused (`400`), because the point is that no credential names the sender.

A capability names one namespace, one function — the one that minted it — a
resource the application chose (a mailbox id), the device that issued it, a
random id and an expiry of at most seven days. It is a payload and an HMAC-SHA256
over it, keyed per namespace by HKDF from the cluster secret, so any gateway of
the cluster can check one and nothing outside it can mint or alter one. Minting
takes a device-bound session: a capability is revoked with the device that
issued it, so it needs one. Everything is checked **before** the upgrade and
before a persistent instance is taken from the pool. The token comes first, from
the URL alone — the MAC, the expiry, that the namespace and function are the ones
asked for, and that neither the capability nor its issuing device has been
revoked — so a stranger with a made-up token costs one HMAC, never a registry
read or a WASM instance. Only then is the function looked up: it must exist, be
enabled, accept capabilities, and not be internal. A capability names a function,
not a version; it opens the live one, and a request that pins a version
(`name@3`) is refused. A forged, expired or misdirected token, a pinned version,
and a function that is gone, disabled or does not accept capabilities all get the
same `403`, so a probe learns nothing about which check failed or which
functions exist; a revoked capability is told it was revoked. Only a `GET`
upgrade of exactly `/v1/functions/{fn}/ws` is looked at this way; nothing else
under `/v1/functions/` becomes reachable by carrying a capability.

Revoking works through the revocation list: `capability_revoke` refuses one
capability, and revoking the device that issued it refuses every capability it
issued. Either way the sweeper closes the sockets they opened within 10 seconds
(`4403`); one whose capability expired is closed two minutes later (`4401`).
`capability_revoke` takes the capability's token rather than its id, so only a
capability the namespace was really issued can be put on the list, and its entry
lives as long as the capability and the two minutes a socket may outlast it; revoking one twice, or one that has
expired, writes nothing. A device's revocation is kept for seven days — as long
as any capability can live — where a revocation that covers only tokens is kept
an hour. Signing an account out everywhere, or ending a session, does not reach
its devices' capabilities: revoke the device, or the capability. A redeploy that
drops `ws_auth: capability` refuses new capability sockets and every frame on an
open stateless one; an open persistent socket keeps its instance until it closes
or its capability is revoked or expires. A capability-opened socket cannot take
`auth.refresh` — it is refused before the offered token is even read: the socket
has no token, and a token would name the account the capability exists not to
name.

The upgrade is rate limited per client address in a bucket of its own — 60 a
minute, bursting to 20 — on the gateway that faces the internet; namespace
gateways see only the overlay, which is exempt. Behind a shared NAT that is one
bucket for everybody behind it, and a sender with many addresses gets many
buckets: it bounds a flood from one place, not a distributed one. One capability
holds at most 16 sockets open on a gateway at once (`429` beyond that), so a
leaked capability spread over many addresses cannot take a gateway's whole
persistent-socket pool.

Every gateway and namespace gateway must run this version before a function
relies on it. Until then it fails closed: a gateway that predates it asks the
upgrade for a credential and answers `401`.

**What this is, and what it is not.** No credential on the socket names the
sender. That is all. The gateway still learns:

- the client's IP address, which the request log records, as it does for every
  request;
- the timing and size of every connection and frame;
- which recipient's mailbox is being written to — it is in the capability, which
  sits in the upgrade URL the way `?jwt=` does;
- that the caller holds a capability that recipient issued, and which one: its
  id, and the device that minted it — and so the recipient's account, which the
  device belongs to;
- that two connections made with the same capability, from whatever addresses,
  are the same holder's;
- that a sender with a signed-in socket open from the same address at the same
  time is probably the same person.

A recipient who issues one capability per correspondent has, by that choice,
told the gateway which correspondent is sending. Unlinkable capabilities (blind
signatures) are not implemented.

---

## When a request is refused

Every 401 and 403 carries `{error, code, hint}` — what happened, and what to do
about it — plus the fields that make it actionable.

| Code | Means |
|------|-------|
| `AUTH_MISSING` | no credential was presented |
| `AUTH_INVALID_KEY` | the key is not one this cluster knows |
| `AUTH_REVOKED` | the credential was revoked — sign in again |
| `AUTH_EXPIRED` | the token expired — refresh |
| `USER_JWT_REQUIRED` | this operation needs a logged-in user; a key alone is not enough |
| `INSUFFICIENT_SCOPE` | the credential lacks a grant; `required_scope` names it |
| `NAMESPACE_MISMATCH` | the credential belongs to another namespace |
| `OWNERSHIP_REQUIRED` | the credential holds no grant in this namespace |
| `NOT_AN_OPERATOR` | the wallet is not on the cluster's operator list |
| `DESTINATION_NOT_ALLOWED` | the proxy refused the destination |

Signing in has its own, because "your signature did not verify" and "you signed
the wrong message" are different problems:

| Code | Means |
|------|-------|
| `AUTH_MESSAGE_MALFORMED` | the message is not a Sign-In-With message this gateway can read |
| `AUTH_DOMAIN_MISMATCH` | the message names a domain this gateway does not serve |
| `AUTH_MESSAGE_EXPIRED` | the message is outside its own issued-at/expiry window |
| `AUTH_SIGNATURE_INVALID` | the signature does not recover the address in the message |
| `AUTH_CHALLENGE_INVALID` | the nonce is unknown, already used, or expired |
| `NAMESPACE_UNKNOWN` | no such namespace — `orama namespace create` makes one |
| `NAMESPACE_NOT_OWNED` | the namespace belongs to another wallet |
| `NAMESPACE_UNOWNED` | the namespace has no owner, so nobody may sign in to it |
| `NAMESPACE_HAS_NO_KEYS` | the lobby namespace has no keys; create a namespace first |
| `TOO_MANY_CHALLENGES` | too many challenges asked for; slow down |

A device-bound session has its own, because the next move differs — sign a
proof, ask another device, or accept that this key is done:

| Code | Means |
|------|-------|
| `DEVICE_REQUIRED` | the namespace requires sessions bound to a device (403) |
| `DEVICE_KEY_INVALID` | the key is not a P-256 or Ed25519 public JWK, or is not the device the message names (400) |
| `DEVICE_SIGNATURE_INVALID` | the device's signature over the sign-in message does not verify (401) |
| `DEVICE_PROOF_REQUIRED` | a device-bound credential was presented without the device's proof (401) |
| `DEVICE_PROOF_INVALID` | the proof is stale, reused, or not the device's (401) |
| `DEVICE_REVOKED` | the device was revoked; its key can never hold a session again (403) |
| `DEVICE_PENDING` | the device waits for another of the account's devices to approve it (403, or 202 on sign-in) |
| `DEVICE_NOT_FOUND` | the account has no such device (404) |
| `DEVICE_KEY_TAKEN` | the key is enrolled for another account (403) |
| `POLICY_SWEEP_INCOMPLETE` | the session policy is set, but revoking the end users' existing sign-in keys stopped partway; repeat the `PUT` (503) |

The TypeScript SDK mirrors these as a typed error hierarchy; see
[TS_SDK.md](TS_SDK.md).

---

## The record

`audit_events` holds what changed and who changed it: sign-ins and their
failures, the refresh-replay tripwire, keys minted, rotated and revoked, grants
given and taken away, ownership transferred, namespaces created and deleted,
functions and deployments deployed and deleted, secrets set and deleted, and
operator actions.

```bash
orama audit                      # oldest first
orama audit --follow             # and keep printing
orama audit --action key.issue --principal 0xabc… --since 2026-09-01T00:00:00Z
```

A refused request is deliberately **not** recorded: one row per 401 would let
anyone with a network connection fill a table replicated to every node. Events
are kept 90 days.

The actor is never a credential. A wallet is recorded as itself; anything else
is recorded as a fingerprint. The trail is readable by every owner of the
namespace, so a subject goes through that redaction whatever it turns out to
be — which is what stopped the exchanged token's subject reaching the table
while that subject was still the raw key.

---

## Operating the cluster

`/v1/operator/*` — minting a cluster invite, listing nodes, claiming one —
requires the `admin` grant **and** a wallet on the cluster's operator list. An
invite is handed every secret the cluster holds, including the one the JWT
signing key is derived from.

A cluster with an empty operator list refuses every operator endpoint. An
unreadable list refuses too: not knowing whether someone is an operator is not
permission to treat them as one.

---

## Which key signed a token

Every gateway generates its own Ed25519 signing key at first boot, keeps it
`0600` in its own state directory (`data/namespaces/<ns>/gateway`, `0700`; the
index gateway's is `data/namespaces/index/gateway`), and publishes the public
half **to the cluster registry** — not to the tenant database it may also be
holding — so the rest of the cluster can verify what it mints. It publishes
once its schema is up, and stays not ready (refusing everything, so minting
nothing) until the key is published. A token's `kid`
names the key. A key file that holds the old cluster-derived key (what a
0.122.x node wrote, carried into the index gateway's state directory by the
upgrade) is replaced with a key of the gateway's own on load, never signed with.

**A namespace gateway's key is bound to its namespace.** A token signed with it
is refused — everywhere, including on the gateway that signed it — unless its
`namespace` claim matches. That is what stops one tenant's gateway minting a
token for another. The index gateway's key is bound to nothing: it is the
control plane, and it is what `orama auth login --namespace X` signs in with.

The key used to be HKDF-derived from the cluster secret with a fixed label. Every
node holds that secret, so every node held the private key that signs for every
namespace — and there was nothing to rotate to, because one derivation has one
output. Tokens minted before the change keep verifying for one access-token
lifetime after each gateway restarts, and then that key is refused: a key every
node can derive must not outlive the upgrade.

```bash
orama operator rotate-signing-key
```

Publishes a new key, starts signing with it, and leaves the outgoing one
verifying the tokens it already signed until they expire. Two `kid`s are in
flight for that window. Nobody is signed out and nothing restarts. It needs the
admin grant **and** a wallet on the operator list.

Stored secrets (function secrets, push tokens, TURN, deployment environments,
agent tokens) are sealed under an encryption root that starts as a copy of the
cluster secret. `orama operator rotate-secrets` rewrites the envelope;
`--rotate` generates a new root so a captured previous IKM cannot open new
rows. The cluster secret (IPFS-Cluster PSK / mesh bearer) is not touched.

`GET /v1/auth/jwks` serves every live key, each carrying the namespace it is
bound to alongside the standard members.

---

## A workload's identity

A deployed app is a principal — `app:<namespace>/<name>` — with grants its owner
chooses, and it holds a token rather than a key.

The token is minted at start, staged by systemd from a file only the gateway can
read, and exposed to the app at `$ORAMA_TOKEN_FILE` owned by the app's own user.
It lasts an hour and the app renews it at `POST /v1/auth/renew` with the token it
is holding — so nothing long-lived is on the node, and nothing privileged has to
rewrite anything while the app runs.

Grants are resolved when the token is minted, not baked in at deploy: taking one
away reaches a running app on its next renewal. An app nobody has granted
anything to holds a token that reaches nothing, which is the only safe default —
the alternative is every app starting with the namespace's whole data plane,
which is the permanent key this replaces wearing a different hat.

A deployment cannot be granted the control plane. Only a workload token may be
renewed; a user session is renewed by its refresh token, which rotates and can be
revoked, and letting any access token mint its own successor would make a stolen
one good for ever.

```bash
orama app grants set my-api runtime
orama app grants list
```

---

## Where this is all kept

Everything above — who somebody is, what they may do, which sessions are live,
which tokens are refused, and the record of it — lives in the **cluster
registry**, the RQLite the index gateway owns.

That matters because a namespace gateway holds a second database: the tenant's
own. The tenant reads and writes it, and a namespace admin can export it whole
and import a replacement. Anything of the platform's kept there is state its
subject can rewrite, and state the rest of the cluster never sees.

Keys and grants moved to the registry first. Sessions, challenges, revocations,
the audit trail, pending device logins and signing keys followed; session
devices and namespaces' session policies were placed there from the start. A namespace
gateway learns where its registry is after it starts, so each of these resolves
the database per call rather than capturing the handle it was built with — which
is exactly how they ended up in the wrong one.

A namespace id is resolved there too, and resolving a name does not create it.

The namespace's own RQLite no longer has those tables at all. Which database
each of the platform's tables belongs in is recorded in one place, the list of
what a namespace database is not given is derived from it, and a test fails when
a migration creates a table nobody has placed.

---

## Between nodes

The main gateway validates a request and forwards the result to a namespace
gateway in headers: the namespace it resolved, the JWT subject, custom claims,
`exp`, `iat`, `jti` and the bound device and session (`did`, `sid`) it verified,
and the grants of the key it looked up.
Whether to believe them is answered by an HMAC over the request's method, path,
every asserted field and a timestamp, keyed from the cluster secret. The first
middleware in the chain deletes every `X-Internal-Auth-*` header that did not
arrive with a valid MAC.

The token's times and id are what let a namespace gateway hold a socket to the
token that opened it. Before they crossed the hop, a socket proxied from the main
gateway had no expiry and no revocation could name it.

The MAC comes in versions, each adding fields to the one before:
`X-Internal-Auth-MAC` covers the original four, `-MAC-V2` adds `exp`, `iat` and
`jti`, and `-MAC-V3` adds `did` and `sid`. A main gateway stamps every version,
so a namespace gateway that predates the newest keeps accepting it during a
rolling upgrade (it ignores the headers it does not know, and passes them on as
it passes on everything else it does not know). A request is judged by the
newest MAC it carries, alone, and the fields that version does not cover are
deleted — a hop from an older main gateway is believed exactly as far as it was
signed. One carrying only the first has no token times; its token is given the most time any token can have
left (`MaxTokenLifetime`, an hour), so a socket it opens still ends within the
hour, and an issue time one revocation-list staleness back, so a revocation the
main gateway's list might not yet have held still reaches it. Refusing it instead
would have treated every signed-in request an older main gateway proxied to an
upgraded namespace gateway as anonymous, for as long as the rollout took.

Accepting an older MAC gives nobody anything they did not have: removing the
newer one from a genuine hop takes a position inside the WireGuard mesh, which
is a node, and every node holds the cluster secret every version is keyed from.

The source IP is not consulted, and must not be: every public request arrives
from `127.0.0.1`, because Caddy terminates TLS and proxies to localhost.

---

## What is not done yet

- A **function** has no identity of its own yet. Host calls still run on the
  gateway's handles, so what a function does is not attributable to the function
  (the remaining half of feat-372). Deployed apps do have one — see below. The
  object those host calls hang off now holds no per-invocation state, which is
  what makes giving them one a matter of passing an identity rather than of
  finding somewhere to put it.
- Resource selectors are enforced on pubsub, function invocation, storage and
  the cache. `db` and deployments both narrow `admin`, which is the whole
  control plane, so they cannot be narrowed until that vocabulary is split; a
  push selector has nothing in the push API to name (feat-394) — the rotating
  push topics of FEAT-265 are random ids a device picks, not something a grant
  could be written against.
- A namespace's RQLite binds only the node's WireGuard address and always
  requires basic auth (feat-269); the namespace gateway in front of it binds the
  overlay too (chg-387).
- There is no web page to approve a device login at, so the flow above is
  approved from a second machine's CLI rather than from a browser.
