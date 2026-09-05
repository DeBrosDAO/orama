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
leaving it.

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

Ownership is transferred rather than granted: a namespace with no owner is
claimable by whoever signs in to it next, so handing it over is one step. The
outgoing owner keeps an admin grant.

A grant may be narrowed to a resource — `pubsub:topic=chat.*`,
`fn:name=checkout` — and publish, subscribe and invoke apply it. A selector in a
domain the data path cannot yet enforce is refused when the grant is written,
rather than stored and silently ignored.

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
clients still has to move.

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
`0600` in its own secrets directory, and publishes the public half so the rest
of the cluster can verify what it mints. A token's `kid` names the key.

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

- A workload — a deployed app or a function — has no identity of its own. Apps
  receive `ORAMA_NAMESPACE` and `ORAMA_GATEWAY_URL` but no credential, so one
  that talks to the platform still carries a key somebody put there (feat-372).
- Resource selectors are enforced on pubsub and function invocation. Storage and
  the database resolve no grant on the request, so a selector naming them
  authorises nothing yet (chg-392).
- Namespace gateways and namespace RQLite bind every interface; the firewall,
  not the bind address, is what keeps them off the internet (chg-387).
