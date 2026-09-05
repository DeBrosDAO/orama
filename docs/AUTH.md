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
a thirty-day one. `DELETE /v1/auth/sessions/{id}` ends one.

Ending a session stops it minting new access tokens. An access token already
minted from it keeps working until it expires, at most fifteen minutes; the
response says so. `POST /v1/auth/logout` with `all` is the immediate one, and it
is all-or-nothing by nature.

---

## What a credential may do

Three things narrow a request, in this order.

**1. The route's policy.** Every route declares what it needs — a credential at
all, which grant, whether the caller must hold a live grant in the namespace,
and what kind of token. The middleware reads the policy of the route the request
matched; it never looks at the path. A route with no declared policy cannot be
registered. See `pkg/gateway/route_policy.go`.

**2. The grant set.** A key carries grants; a wallet gets the grants of its role
in the namespace. The gateway refuses an operation whose grant the credential
does not hold, and the refusal names the grant that was missing.

| Grant | What it reaches |
|-------|-----------------|
| `admin` | the control plane: deployments, functions, secrets, keys, members, the raw database |
| `invoke` | invoking a private function |
| `storage` | upload, pin, get, unpin |
| `pubsub` | publish, subscribe, presence |
| `push` | registering a device for push |
| `webrtc` | TURN credentials, signalling, rooms |
| `proxy` | the anonymising proxy and the tunnel |
| `cache` | the cache |

**3. The kind of token.** `storage`, `webrtc` and `proxy` additionally require a
genuine logged-in user — a wallet JWT, not a key and not a JWT exchanged from
one. That is what makes a key extracted from an app bundle worthless on those
paths: it reaches nothing without a user behind it. An admin credential is
exempt, and so is the one server-side reclaim (`DELETE /v1/storage/unpin/:cid`),
which a userless job may reach by exchanging its key for a token.

---

## Roles

A namespace has exactly one owner and any number of members. A member holds a
role, and a role is a grant set.

| Role | Holds |
|------|-------|
| `owner` | everything, and only one wallet at a time |
| `admin` | the control plane |
| `runtime` | the data plane |
| `reader` | nothing beyond the routes that ask for no grant |

```bash
orama members list
orama members add 0xabc… --role admin
orama members remove 0xabc…
orama members transfer 0xabc…      # the owner, and only the owner
```

Ownership is transferred rather than granted, and it is one step: the outgoing
owner keeps an admin grant, and there is no moment where the namespace has no
owner.

### Narrowing a grant

A grant may be narrowed to a resource, and four domains apply it:

| Selector | What it matches |
|----------|-----------------|
| `pubsub:topic=chat.*` | publish, publish-batch, and the subscribe WebSocket |
| `fn:name=checkout` | function invocation |
| `storage:avatars/*` | upload, get, pin and unpin, against the name the object was uploaded with |
| `cache:key=sessions/*` | get, mget, put, delete and scan, against `<map>/<key>` |

Two steps, not one. A grant with a selector holds exactly the scope that
selector narrows — `storage:avatars/*` holds `storage` and nothing else — and
the data path then narrows that scope to what the selector matches. The scope
gate decides whether a caller may touch this class of thing at all and cannot
see which object is being touched.

`*` stands for any run of characters and crosses `/` deliberately:
`avatars/*` is meant to cover `avatars/2026/03/me.png`, and stopping at the
separator would grant less than it appears to.

A storage name is normalised before it is compared, so `/avatars/me.png` and
`avatars//me.png` are the same object. `..` in a name is **refused**, not
resolved: a storage name is a label rather than a filesystem path, and resolving
one would let `avatars/../keys/x` match `avatars/*`. A cache key is not a path
and is not normalised — `sessions/../tokens/x` is a key called `../tokens/x` in
the `sessions` map, and the map is what the grant names.

An object a selector cannot be compared against — a CID this namespace recorded
no name for — is refused for a narrowed grant and reached as before by an
unnarrowed one. "I could not work out what you are touching" is not a reason to
allow it.

A selector in a domain the data path cannot yet enforce is refused when the
grant is written, rather than stored and silently ignored. `db` and deployments
are the two: both narrow `admin`, and `admin` is the whole control plane, so a
grant narrowed to `db:table=posts:read` would hold admin everywhere except the
database routes that narrowed it — a wider grant wearing a narrower name. They
wait on the control-plane vocabulary being split. `push` waits on something
smaller: its API has no topic.

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
`0600` in its own secrets directory, and publishes the public half **to the
cluster registry** — not to the tenant database it may also be holding — so the
rest of the cluster can verify what it mints. A token's `kid` names the key.

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
the audit trail, pending device logins and signing keys followed. A namespace
gateway learns where its registry is after it starts, so each of these resolves
the database per call rather than capturing the handle it was built with — which
is exactly how they ended up in the wrong one.

A namespace id is resolved there too, and resolving a name does not create it.

---

## Between nodes

The main gateway validates a request and forwards the result to a namespace
gateway in headers: the namespace it resolved, the JWT subject it verified, and
the grants of the key it looked up. Whether to believe them is answered by an
HMAC over the request's method, path, every asserted field and a timestamp,
keyed from the cluster secret. The first middleware in the chain deletes every
`X-Internal-Auth-*` header that did not arrive with a valid MAC.

The source IP is not consulted, and must not be: every public request arrives
from `127.0.0.1`, because Caddy terminates TLS and proxies to localhost.

---

## What is not done yet

- A **function** has no identity of its own yet. Host calls still run on the
  gateway's handles, so what a function does is not attributable to the function
  (the remaining half of feat-372). Deployed apps do have one — see below.
- Resource selectors are enforced on pubsub, function invocation, storage and
  the cache. `db` and deployments both narrow `admin`, which is the whole
  control plane, so they cannot be narrowed until that vocabulary is split; a
  push selector has no topic in the push API to name (feat-394).
- A namespace's RQLite binds every interface; the firewall, not the bind
  address, is what keeps it off the internet. The namespace gateway in front of
  it now binds the overlay (chg-387).
- There is no web page to approve a device login at, so the flow above is
  approved from a second machine's CLI rather than from a browser.
