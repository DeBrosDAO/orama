# The gateway

> **At a glance.**
>
> - **What:** one Go binary in two roles. The index gateway (one per node, behind Caddy) authenticates callers, enforces a per-route policy, proxies tenant traffic to the right namespace gateway and serves deployments by host name. A namespace gateway (one per tenant namespace per node) is the same code against the tenant's own RQLite and believes only requests carrying a signed hop from an index gateway.
> - **Key numbers:** port 10104 on the WireGuard address and loopback; read 60 s, write 120 s; proxy 30 s (300 s on long routes); breaker opens at 5 consecutive failures for 30 s; credential cache 60 s; hop MAC skew 60 s; general bucket 10,000 per minute, burst 5,000; credential bucket 30 per minute, burst 10.
> - **Code:** `core/pkg/gateway/`, `core/pkg/gateway/route_policy.go:buildRoutePolicies`, `core/pkg/gateway/internal_auth_hop.go`, `core/pkg/gateway/clientkey/`, `core/pkg/netguard/`, `core/cmd/gateway/`.

![Index gateway and namespace gateway on one node](../technical-reference/diagrams/ch12-overview.svg)

## The problem

A client sees one HTTPS name per cluster and one per namespace (`ns-<name>.<base domain>`). For every request the gateway decides whose credential it carries, which namespace it belongs to, which node answers, and what happens when that node is down.

Tenant isolation is physical: a namespace owns its RQLite, Olric and gateway processes. So the gateway serving a tenant must be the tenant's own process with the tenant's database. But credentials, grants and the node list live in the cluster registry, which a tenant must never write. Hence one binary in two roles, with predicates (`isNamespaceGateway`, `servesCoreRegistry`) that decide which database each query hits.

Public TLS is Caddy's job; the gateway never binds 80 or 443. Every public request therefore arrives from `127.0.0.1`, and no gate may treat the source address as evidence.

## Starting in a cluster that is still starting

The gateway listens first and reports why it cannot serve. Readiness has three states: `starting` (schema not at the required version, retried for the life of the process), `ready`, and `blocked` (a leader answered and the applied schema version is below what the binary requires; retrying cannot help). Only that last case is permanent. A failure to read the version retries with a 5 s to 60 s backoff, because treating "cannot read" as "behind" would let a 200 ms leader hiccup latch a namespace into `blocked`.

While not ready, a gate answers 503 to everything except a short read-only passthrough list (health, version, status, ping, the TLS check, ACME challenges). The list is deliberately not the set of open routes, because many of those write.

Some failures are fatal on purpose. A namespace gateway that cannot read the registry exits rather than fall back to its own `api_keys` table, which would authenticate against rows the tenant can write.

## Routes are a declared contract

More than a hundred routes each need an answer to "who may call this". The answer is a `routepolicy.Policy` per route in one table. The mux panics at registration if a pattern has no declared policy, and a request that matches nothing gets the zero policy, which requires a credential and grants nothing: unknown paths are closed.

A policy carries:

- **Access**: `Credential` (the default), `Open`, or `HandlerAuth`, where the handler authenticates the caller itself (invite tokens, the cluster secret, coordination MACs).
- **Domain and action**, such as `deploy:write`, for the scope gate.
- **Ownership**: the caller must hold a live grant in the namespace.
- **Token kind**: any credential, any JWT (so a leaked bare key is inert), a signed-in wallet, or a principal.
- **MainGateway**: serve on the index gateway even when the request arrives on an `ns-` host, for anything whose data is in the registry, such as deployments, key management and operator routes.

Tests fail the build when a route lacks a declaration or when the `Open` set differs from the reviewed one.

## One request through the stack

![The middleware pipeline](../technical-reference/diagrams/ch12-middleware.svg)

The middleware order is fixed. Outermost first:

1. **Hop verification.** Without a valid MAC, every `X-Internal-Auth-*` header is deleted, so nothing downstream asks whether they are authentic.
2. **Policy lookup, logging, security headers.** The route policy is resolved once, so later readers cannot disagree.
3. **Rate limit**, before authentication, because the credential routes are among those it protects.
4. **CORS and readiness gate.**
5. **Host routing.** `ns-<name>` hosts go to the namespace proxy; other hosts under the base domain are looked up as deployments.
6. **Authentication, authorization, scope, namespace bucket.**

Credential resolution tries, in order: a signed hop; a bearer JWT; a JWT in `?jwt=` on WebSocket upgrades only (capped at 4,096 bytes and stripped before forwarding); then an API key, checked against the replicated revocation list first (stale bound 10 s), then a 60 s credential cache, then the registry. The cache bounds how long narrowing a key takes to bite; revocation is not bounded by it. [Identity, authorization and trust](ch07-identity-authorization-and-trust.md) covers the credentials themselves.

Authorization then refuses a credential of another namespace (`NAMESPACE_MISMATCH`), looks up the live grant when the route needs ownership (`OWNERSHIP_REQUIRED`), and the scope gate checks `Domain:Action` against the caller's permission set (`INSUFFICIENT_SCOPE`). On the index gateway, `/v1/rqlite*` is refused to everyone who is not an operator, because the registry is an operator's to read.

## Proxying to a namespace gateway

The namespace gateway cannot validate API keys, which live only in the registry. So the index gateway validates the credential once, then forwards.

It finds targets by querying the registry at `level=weak` (the leader), selecting gateway members whose per-node status is `running` and whose DNS node is `active`. Selecting on node status, not the cluster rollup, lets a degraded namespace keep serving from healthy members. A registry error is a retryable 503, never a 404.

Ordering puts this node's WireGuard address first, then an FNV-32a hash of namespace and credential modulo the member count, so one caller's WebSocket subscribe and publish land on one member. The first member whose circuit breaker allows traffic is used. There is one breaker per namespace and member, so a tenant whose gateway fails never refuses another tenant on the same node. A breaker opens after 5 consecutive failures (a refused or timed-out connection, or a 502, 503 or 504 from the gateway itself, never a function's own answer), stays open 30 s and then admits one probe.

Failover is deliberately narrow: another member is tried only on a dial failure while no byte of the request body has been read. A request that reached a member is never retried on another, whatever the method, because it may have taken effect. A timeout answers 504 with a warning that a write may already have happened.

Deployments are served by host name. The index gateway resolves the label (subdomain, legacy name, or a verified custom domain), proxies to `localhost:<port>` if this node is the home node or holds a replica, and otherwise tries each replica over the overlay with 5 s per attempt and a per-node breaker. The index gateway never runs tenant functions: its database holds every tenant's registry rows side by side, so serverless traffic for any namespace but `default` is proxied to that namespace's own gateway.

## The hop MAC

The hop exists because the index gateway validated the credential and the namespace gateway cannot. It was once believed on the strength of the source address. Since every public request comes from `127.0.0.1`, an Internet client could send `X-Internal-Auth-Validated: true` with a namespace and `X-Internal-Auth-Scopes: admin` and be an admin of any namespace.

Now the header is `<unix seconds>.<hex hmac-sha256>`, keyed by an HKDF derivation of the cluster secret, over a newline-joined payload: method, path, namespace, subject, claims and scopes. Later versions add the token's expiry and id, so an open WebSocket ends with its token. A signer stamps all versions; a verifier judges by the newest it finds, with no fallback to an older one, and rejects timestamps more than 60 s from its clock in either direction. A namespace gateway also refuses an identity forwarded for a different namespace than its own.

The MAC binds method and path but not host, query or body, and carries no nonce. Inside the 60 s window a captured stamp can be replayed to a gateway of the named namespace on the same method and path. Since every node holds the cluster secret, any node can sign a hop for any identity; [identity, authorization and trust](ch07-identity-authorization-and-trust.md) covers the per-node signatures layered on it.

## Rate limits and egress controls

Buckets are token buckets held in memory per gateway process, keyed by the client network. A shared counter would put a registry round trip on every request. The cost is that a client reaching three gateways gets three buckets, and a restart gives everyone a full burst.

| Bucket | Per minute | Burst |
|---|---|---|
| general | 10,000 | 5,000 |
| credential (challenge, verify, key and token exchange) | 30 | 10 |
| chain query | 120 | 30 |
| chain simulate / broadcast, per client | 30 / 12 | 10 / 4 |
| namespace (tenant-settable up to 100,000 and 50,000) | 10,000 | 5,000 |

The first design keyed on the first `X-Forwarded-For` entry. A caller could write that header, so one value removed all limiting, including on the endpoints that mint credentials. The rewrite keys on the TCP peer. A peer inside `10.0.0.0/24` is another node and exempt. A loopback peer with a forwarded address came through Caddy, so only the last entry counts, because Caddy appends the address it saw. A loopback peer with nothing forwarded is a local process and exempt. Anything else is charged by its own address. IPv6 clients are charged as their `/64`, since a subscriber can source requests from any address in it. The sign-in challenge adds a per-wallet bucket and a ceiling of 10 unanswered nonces per wallet, counted in the registry and failing closed.

The per-namespace limiter is cached for 30 s, so a tenant's change reaches other gateways without a broadcast layer. A failed config read falls back to the defaults.

Egress is the other half. A function that fetches `http://10.0.0.5/` would reach the overlay carrying RQLite, Olric and other tenants' services, and a push URL resolving to `169.254.169.254` would reach cloud metadata. Checking the URL string cannot work: the resolver decides what a name becomes, the answer can change before the connection, and a redirect can lead anywhere. So the check sits at the socket. `netguard.GuardAddress` is a `net.Dialer.Control` hook that sees the concrete address after resolution, once per attempt and per redirect hop, and refuses loopback, private, link-local, CGNAT, multicast and 33 further reserved ranges. Function HTTP, tenant push URLs and the Tor exit policy all use the one list.

## Operators

`/v1/operator/*` serves the people who run the cluster. An operator is a wallet in the `operators` table; an unreadable table is a 503, because not knowing is not permission, and removal is one guarded statement so two concurrent removals cannot empty the list and lock the API out. The invite route mints a one-hour token that hands out every cluster secret, so it alone has an unrestricted policy.

## Failure and the main limit

A member down costs a dial failure and a failover. A hung member costs 30 s and a 504. With no registry leader, cached credentials keep working for up to 60 s, then authentication answers a retryable 503. The most important limit is that rate limits are per process, so the cluster-wide ceiling for any client scales with the number of gateways it can reach.
