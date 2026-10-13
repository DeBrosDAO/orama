# Identity, authorization and trust

> **At a glance.**
>
> - **What:** how a caller becomes a known subject and what it may then do. A wallet signs a message the gateway issued, spends a single-use nonce and receives a 15-minute EdDSA token plus a rotating refresh token. Authority is read from a grant in the cluster registry on every request, never from the token. Between nodes, MACs keyed from the cluster secret prove membership, and a per-node Ed25519 key proves identity for the one class of call where that matters.
> - **Key numbers:** challenge good for 5 min, at most 10 unanswered per wallet; access token 15 min; refresh token 30 days, 60 s reuse grace; revocation list reloaded every 5 s with a 10 s staleness bound; data-plane grant cache 10 s, key cache 60 s; API keys live 90 days (maximum 365); workload tokens 1 h; every inter-node stamp is valid for 60 s either side.
> - **Code:** `core/pkg/gateway/auth/`, `core/pkg/gateway/auth/siw/`, `core/pkg/gateway/route_policy.go:buildRoutePolicies`, `core/pkg/auth/coordination_v2.go:CheckCoordination`, `core/pkg/auth/nodeapi.go:VerifyNodeAPI`, `core/pkg/gateway/handlers/nodeapi/credentials.go:VerifierFor`.

![Authorization: credentials, the gates and the registry they read](../technical-reference/diagrams/ch14-overview.svg)

## The problem

The people who matter hold keys, not passwords. A wallet signature is a permanent credential unless something makes it single-use, so the system must turn "this key signed these bytes" into "this key logged in once, to this host, for this namespace, before this deadline".

Two further facts shape everything else. A token that verifies on its signature alone cannot be taken back, yet gateways answer from memory on many nodes and a revoked session must stop everywhere within seconds. And the signing key is itself a target: the first design derived one Ed25519 key from the cluster secret, so any node or compromised namespace gateway could mint a token for any tenant, with nothing to rotate to.

The resulting rule is that a token says who you are, and what you may do is read from the registry on every request through short caches with named lifetimes.

## Signing in with a wallet

The gateway never asks a wallet to sign a bare nonce, because then any signature the wallet ever produced would be a login. The message is EIP-4361 (or its Solana variant), strictly parsed. It contains the host the client connected to, so a signature collected by another site fails; a namespace URN, read from the signed bytes and never from the request body beside them; an optional device URN; a 256-bit nonce; and a five-minute expiry. Ethereum signatures are EIP-191 recovery; Solana signatures are raw ed25519.

The nonce is spent by one conditional statement:

```sql
UPDATE nonces SET used_at = datetime('now')
 WHERE namespace_id = ? AND wallet = ? AND nonce = ?
   AND used_at IS NULL AND expires_at > datetime('now')
```

The affected-row count is the lock: two concurrent requests race on the update and exactly one sees 1. Unknown, spent and expired nonces share one error code, so the endpoint is not an oracle for which wallets hold challenges. A challenge writes a replicated row for whatever wallet the body names, so it is limited three ways: the credential bucket per client network, a per-wallet bucket, and a ceiling of 10 unanswered nonces per wallet counted in the registry, which fails closed.

Signing in used to claim ownership: the first wallet to reach an ownerless namespace became its owner, so the lobby namespace `default` belonged to whoever came first. Now only creating a namespace writes an owner grant. The sign-in gate admits a wallet that holds a live grant, or, if the owner opened sign-in, admits a grantless end user who gets a session and nothing else.

## Tokens, sessions and revocation

Each gateway signs with its own Ed25519 key, published to the registry before the gateway serves. A namespace gateway's key is bound to its namespace, and the verifier on every gateway enforces the binding, so a compromised tenant gateway cannot mint for another tenant even if it lies. The index gateway's keys sit in a root-only directory its tenants cannot read. Rotation orders its steps for safe failure: store the key, publish it, switch signing, then retire the old key 15 minutes later, so nobody is signed out.

The refresh token is 32 random bytes stored as a SHA-256 hash and replaced on every use. A token revoked in the last 60 s is accepted once more, recovering a client whose rotation response was lost. A spent token presented again is a replay, answered 401 and audited. A registry error during refresh is a 503, never a 401, because during a rolling restart a 401 would force a full wallet sign-in on a client that cannot perform one.

A session may also be bound to a device key, with a proof of possession for each sensitive action, and machines with no wallet use an RFC 8628 device login. A namespace sets a device policy and a sign-in policy to say how much of this it demands.

Revocation closes the gap a signature-only token leaves. `revoked_tokens` holds entries for one token id, for every token issued to a subject before an instant, or for a device or session. Each gateway reloads it every 5 s and refuses to answer from a copy older than 10 s. If the registry cannot be read, authentication answers a retryable 503: unknown is never "not revoked". A failed reload keeps the old list rather than clearing it, because forgetting revocations on one failed query would revive every revoked token. WebSockets are authorized once at upgrade, so a sweeper re-checks every token-opened socket each 5 s and closes it when the token expires, is revoked, or cannot be vouched for for two minutes.

## What a caller may do

Every route declares its requirement in one table ([the gateway](ch06-the-gateway.md)). The check runs twice: a gate by domain and action before the handler, and a check by object inside it. A permission has the form `domain:action:resource`, over 14 domains split into a data plane (storage, pub/sub, cache, push, WebRTC, proxy, functions) and a control plane (databases, deployments, secrets, members, namespace settings, audit, operator).

| Role | Reaches |
|---|---|
| `reader` | only routes that ask for no permission |
| `runtime` | the data plane, and function invocation |
| `developer` | the data plane plus databases, deployments, secrets, function management |
| `admin` | everything in the namespace |
| `owner` | the same, one wallet per namespace; the only role that can transfer it |

Grants live in the registry, never in a tenant database, because a tenant's owner can export that database whole and replace it by import. A grant may carry a selector such as `storage:avatars/*` or `pubsub:topic=chat.*`, and an expiry. A selector only narrows: a narrowed grant holds nothing outside its domain, and a selector the binary cannot parse authorizes nothing, since ignoring it would silently widen the grant to the whole role. Writing a grant replaces the principal's previous one, and revoking sets `revoked_at` without deleting the row. Ownership moves in one statement, never as a revoke followed by a grant, since an ownerless namespace is claimable by the next sign-in.

How long a change takes to land is stated, not hoped for: key revocation within 10 s, narrowing a key within 60 s, a wallet's data-plane grant within 10 s, a control-plane change on the next request. Grants are read at RQLite `level=weak`, which routes to the leader, so a follower cannot answer 403 to a grant the leader just acknowledged. The cost is that a cluster with no leader fails reads rather than serving stale ones.

API keys have the form `orama_<type>_<payload>_<checksum>` with 192 random bits, stored as an HMAC-SHA256 digest and shown once. The `sk` or `rk` prefix tells a finder how bad the leak is, but the scopes column decides authority. Every key expires, rotation overlaps the old and new for seven days by default, and revoking a key also revokes JWTs already exchanged from it. A runtime key ships inside client bundles, so it cannot reach the control plane, and storage and WebRTC additionally demand a principal token, which makes an extracted key inert there.

A deployed application is a principal too. It holds a one-hour workload token staged by systemd, restaged every 20 minutes, with only the grants its owner chose, restricted to `runtime` or `reader`. An app that was never granted anything reaches nothing.

## Trust between nodes

All node-to-node traffic crosses WireGuard, but the overlay is not a credential: every namespace's services sit on the same mesh, so a tenant workload has an overlay source address. Two questions hide inside "is this call legitimate", and they use different mechanisms that never substitute for each other.

![Inter-node trust: what each mechanism proves and what it is keyed from](../technical-reference/diagrams/ch15-overview.svg)

**Does the caller belong to this cluster?** A MAC keyed by HKDF from the cluster secret answers it, with a separate purpose label for each consumer. Caddy faces the internet, so its key must not be the coordination key. The coordination stamp is HMAC-SHA256 over method, the receiving node's peer id, path, query, body hash, a 16-byte nonce and the time. A verifier judges a v2 stamp as v2 only, with no retry as v1, refuses stamps made before its own process started, checks the MAC before consuming the nonce so nothing without the key can fill the cache, and remembers 65,536 nonces for 120 s. State-changing routes require v2, so a captured stop stamp cannot be replayed with a different body for another namespace. The gateway-to-gateway hop MAC, described in the gateway chapter, is the same family.

**Which node is the caller?** Every node holds the cluster secret, so it cannot say. For most calls that is acceptable, since a node already in the Raft cluster can do worse directly. It is not acceptable when a node writes itself into the registry that DNS and placement route on. So each node generates a second Ed25519 key, enrols only the public half, and signs every registration and heartbeat. Enrolment avoids trust on first use: it is signed with the libp2p identity and verified against the public key embedded in the claimed peer id, so only the machine holding node X's identity can enrol a key for X. A node with no live credential is verified against nothing, with no fallback to the secret. Departure revokes the row rather than deleting it, because deletion would let the machine's own identity register again. Re-admission needs an operator-minted invite.

## How it fails

A clock skew over 60 s makes every stamp between two nodes fail in both directions. A registry outage turns authentication into a retryable 503 once the revocation list is stale, and credential-cache hits keep working for up to 60 s. A node whose libp2p identity is silently regenerated gets a new peer id and is refused admission.

The most important limit: the cluster secret is fleet-wide and effectively never rotated, and namespace gateways must read it to verify hops. Code execution in one tenant's gateway can therefore sign a hop for any identity and spawn or tear down namespaces on any node. The node stamp narrows this for registration only.
