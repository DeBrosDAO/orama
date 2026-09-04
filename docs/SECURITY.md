# Security Hardening

This document describes all security measures applied to the Orama Network, covering both Phase 1 (service hardening on existing Ubuntu nodes) and Phase 2 (OramaOS locked-down image).

## Phase 1: Service Hardening

These measures apply to all nodes (Ubuntu and OramaOS).

### Network Isolation

**CIDR Validation (Step 1.1)**
- WireGuard subnet restricted to `10.0.0.0/24` across all components: firewall rules, rate limiter, auth module, and WireGuard PostUp/PostDown iptables rules
- Prevents other tenants on shared VPS providers from bypassing the firewall via overlapping `10.x.x.x` ranges

**IPv6 Disabled (Step 1.2)**
- IPv6 disabled system-wide via sysctl: `net.ipv6.conf.all.disable_ipv6=1`
- Prevents services bound to `0.0.0.0` from being reachable via IPv6 (which had no firewall rules)

### Authentication

**Internal Endpoint Auth (Step 1.3)**
- Every `/v1/internal/wg/*` endpoint requires both an overlay source address and the cluster secret
- Peer *registration* (`POST /v1/internal/wg/peer`) enforces the same pair. It previously checked neither: there was no overlay check at all, and the secret check was written `if configured != "" && supplied != configured`, so a gateway with no cluster secret accepted a peer insertion from anyone who could reach it
- A gateway with no cluster secret configured now refuses these endpoints (`503`) instead of allowing them, since there is no way to authenticate the caller
- `node_id` and `public_key` on peer registration are parsed (libp2p peer id; base64 32-byte Curve25519, control characters rejected) before they are stored, because both are rendered into `wg0.conf` on every node

**Rate limiting**
- The client is the peer address. It used to be the first `X-Forwarded-For` entry, and any address in the WireGuard subnet was exempt from every limit — so one header removed all rate limiting, including from the endpoints that mint credentials
- `X-Forwarded-For` is honoured only when the peer is the local reverse proxy, and only its last entry: Caddy appends the address it is talking to, so the last entry is real and the ones before it are the caller's. Loopback with a forwarding header is **not** exempt, because every public request arrives from `127.0.0.1`
- Credential endpoints get a separate bucket, 30 a minute per address against a general 10,000. `/v1/auth/challenge` is limited per wallet too, since it writes a row for a wallet the caller does not have to own

**Node-to-node coordination (`/v1/internal/namespace/spawn`, `/v1/internal/namespace/repair`)**
- One node asking another to spawn a namespace's services, or to repair an under-provisioned cluster, was authenticated by `X-Orama-Internal-Auth: namespace-coordination` — a constant in this repository — plus a check that the source address is on the WireGuard overlay
- Neither is a credential. The string is public, and being on the overlay is not a privilege: every namespace's services are on that mesh, so any tenant workload that could reach a node's gateway port could spawn or stop services for any namespace on it
- The request carries a MAC over the method, path and query, keyed by `HKDF(cluster secret, "internal-coordination")`, with a one-minute window in both directions. The query is covered because the namespace travels there — without it a stamp for one namespace would be replayable onto another. The overlay check stays as defence in depth
- This is not node *identity*: every node holds the cluster secret, so any node can sign for any other. It closes the gap between "anything on the mesh" and "anything in the cluster"; per-node identity is the node-principal work
- The three `/v1/internal/namespace/webrtc/*` endpoints were **removed** rather than authenticated. Nothing in the repository called them — `orama namespace enable webrtc` goes to the public route, which does the work itself — so they were three paths exempt from the API-key middleware, guarded by that same constant, and reachable by anything on the mesh

**Inter-gateway trust (`X-Internal-Auth-*`)**
- The main gateway validates a request and forwards the result to a namespace gateway in these headers: the namespace it resolved, the JWT subject it verified, and the grant set of the API key it looked up. The namespace gateway believes all three without re-checking anything, and skips its ownership gate on the strength of them
- Whether to believe them is answered by an `X-Internal-Auth-MAC` header: HMAC-SHA256 over the request's method and path plus every field the headers assert plus a timestamp, keyed by `HKDF(cluster secret, "internal-auth-hop")`. Every node in a cluster derives the same key and nobody outside it can. The first middleware in the chain deletes every `X-Internal-Auth-*` header that did not arrive with a valid MAC, so nothing below it has to ask whether what it sees is authentic
- Covering the method and path means a MAC observed on a harmless read cannot be replayed onto a write, and the ±60s window bounds replay of the request it was minted for. The MAC is consumed at the hop it authenticates and never forwarded
- The source IP is not consulted. It used to be the only check — loopback or `10.0.0.0/24` — and **the source IP of every public request is 127.0.0.1**, because Caddy terminates TLS and reverse-proxies to `localhost`. Caddy forwards client headers by default, so `X-Internal-Auth-Validated: true` with a namespace and `admin` in the scopes header was an unauthenticated admin bypass on every gateway, from the internet
- Caddy strips all six headers on the way up (`header_up -X-Internal-Auth-*` in every `reverse_proxy` block) as defence in depth: two independent places have to fail before a forged header is believed
- A gateway with no cluster secret derives no key. It trusts no internal-auth header, and it refuses to proxy a request it cannot sign rather than forwarding an assertion it cannot back

**The raw-database routes (`/v1/rqlite*`)**
- They serve whatever database the gateway they reach is configured against. On a namespace gateway that is the tenant's own; on the gateway that fronts the cluster it is the registry — `api_keys`, `namespace_ownership`, `refresh_tokens`, `wireguard_peers`, `deployment_env_vars`, `invite_tokens`
- On the cluster gateway they now require an operator. They needed the `admin` grant and ownership of *some* namespace, and the cross-namespace check that would have caught the mismatch runs only when the gateway serves a named namespace — which the cluster gateway does not. So any tenant's admin key could export the registry, or import over it
- This covers the whole surface, not just export and import: the ORM HTTP gateway mounts `query`, `exec`, `select`, `find` and `transaction` under the same prefix and against the same database
- A tenant's own database is reached through their namespace gateway (`ns-<namespace>.<base domain>`), which the refusal names

**Operating the cluster (`/v1/operator/*`)**
- Minting a cluster invite, listing the cluster's nodes and claiming a node require the `admin` grant **and** a wallet on the cluster's operator list (`operators` table). The endpoints had no scope entry and no ownership entry, so they fell through to "any valid credential is enough" — and an invite token is handed every secret the cluster holds, including the cluster secret the JWT signing key is derived from. A key extracted from a public app bundle reached it
- The list is seeded at migration 044 from `dns_nodes.operator_wallet`, which is what `orama node install --operator-wallet` writes. That flag is validated as a `0x` + 40-hex address and normalised, because it used to be a free-form string a typo could silently ruin
- A cluster with an empty operator list refuses every operator endpoint. An unreadable list refuses too: not knowing whether someone is an operator is not permission to treat them as one
- **Residual, closed by Phase 1/3:** a wallet is still resolved from a bare API key (`namespace_ownership` → the namespace's owner), because the CLI authenticates with an API key rather than a wallet session. So an *admin* key belonging to a namespace whose owner is an operator still reaches these endpoints. The scope requirement closes the app-bundle path; separating an operator credential from a namespace-admin credential is the device-flow login and key-profile work

**Invite tokens**
- Stored as `sha256:<hex>` of the token, never the token. The column was the raw token as a primary key, so a disk snapshot, a raw rqlite query or the export endpoint yielded a credential for the whole cluster. Migration 044 deletes every plaintext row rather than converting it — SQLite cannot hash — so an unconsumed invite has to be re-minted once
- Maximum lifetime is one hour, down from seven days. An invite is a credential for every secret the cluster has, and a week outlived the reason it was minted

**Node join (`/v1/internal/join`)**
- An invite token is checked for liveness before any work is done on its behalf, and consumed atomically only once the request is known to be serviceable
- A join is refused if the public IP, WireGuard key or peer id is already registered to a node that is up — liveness being `confirmed_at` **or** a `dns_nodes` row at the same overlay address, since a node on an older binary clears its own `confirmed_at`
- The pre-join cleanup deletes only rows the refusal check exempted: residue of the caller's own unfinished joins, never a live node's row. `public_ip` is caller-supplied and unverified against the source address, so an unscoped delete here is a node-eviction primitive
- A uniqueness conflict returns 409 and keeps the token spent; only a cluster fault releases it. Releasing on a caller-triggerable failure makes a single-use token replayable

**`/v1/auth/simple-key` was removed**
- It required that *some* API key was present, then took the wallet and the namespace from the request body with no cross-check against the authenticated key, and minted a key for that namespace. A runtime key scraped from a browser bundle minted an admin key for anyone's namespace. It had no scope entry and no ownership entry either
- Its only first-party caller was `orama auth login --simple`, a convenience for re-authenticating when credentials already existed. That flag is gone; `orama auth switch` picks a stored credential without a server call, which is what the convenience was for

**Namespace creation**
- Creating a namespace is `POST /v1/namespaces` (`orama namespace create`), authenticated by a wallet JWT. It writes the namespace and its single owner grant together, applies a per-wallet quota of 10, and is the only thing that starts provisioning
- It used to be a side effect of asking for a login challenge: `/v1/auth/challenge` ran `INSERT OR IGNORE INTO namespaces`, unauthenticated, for whatever name the body carried. Squatting a name was free, a typo made a namespace, and verifying the signature afterwards spun up a real cluster — so an anonymous caller could create infrastructure
- A challenge for a namespace that does not exist is a 404 with the code `NAMESPACE_UNKNOWN`. A challenge with no namespace uses the gateway's default, which is how a wallet signs in before it owns anything
- A key-authenticated caller cannot create a namespace: a namespace's owner is a wallet, and one with no wallet owner is claimable by whoever signs in to it next
- The name is validated as what it becomes — a DNS label, a systemd instance name and a directory — and platform names are reserved

**What a wallet signs (`/v1/auth/challenge`)**
- The challenge is a Sign-In with Ethereum message (EIP-4361), or the Solana equivalent — the same grammar with one word changed in the header line. `/v1/auth/verify` and `/v1/auth/api-key` take the signed message back and read the wallet, the nonce and the namespace out of it; nothing beside it in the request body is read, because nothing beside it was signed
- The message names this gateway's own host, taken from the request's `Host`, so a signature collected by any other site does not verify here. It used to be a bare 32-byte nonce: a signature over that proves possession of the key and nothing else, so any signature that wallet had ever made anywhere was in principle an Orama login — and the wallet dialog showed the user a base64 blob they had no way to judge
- The namespace is in the message twice: in the statement the user reads, and as a `urn:orama:namespace:<name>` resource the gateway acts on. Both are inside the signature, so the namespace a caller is signed in to is the one the user approved
- The message states its own expiry, five minutes, checked independently of the nonce row's. A refusal carries a code: `AUTH_DOMAIN_MISMATCH`, `AUTH_MESSAGE_EXPIRED`, `AUTH_MESSAGE_MALFORMED`, `AUTH_SIGNATURE_INVALID`, or `AUTH_CHALLENGE_INVALID`
- `AUTH_CHALLENGE_INVALID` is one code for three causes — never issued, already used, expired. Telling them apart would make the endpoint an oracle for which wallets hold outstanding challenges, and the caller's next move is the same in all three

**Challenge nonces**
- One wallet may hold 10 unanswered, unexpired challenges in a namespace. The row is written for whatever wallet the body names and nothing proves the caller owns it, so without a ceiling a grind fills the table for a victim's wallet
- Spent and expired challenges are removed on a ticker. Nothing removed them before: every challenge ever issued stayed in a Raft-replicated table

**Who may do what in a namespace (`principals` and `grants`)**
- Authorization was one row in `namespace_ownership`: a row meant owner, no row meant refused, and there was nothing in between. A second person on a team was given the owner's credentials or nothing, a service account could only be modelled as another owner, and there was no way to record who granted what or to make a grant expire
- A **principal** is who — a wallet, or a service account (an API key). A **grant** is what they may do in one namespace, as a named role, optionally narrowed to a resource and optionally expiring. Migration 050 moves every existing ownership row across and drops the old table
- The roles are `owner`, `admin` (the control plane), `runtime` (the data plane: invoke, storage, push, webrtc, proxy, pubsub, cache) and `reader` (a member with no grant, reaching only the routes that require none). A role this gateway does not recognise — written by a newer one — grants nothing rather than defaulting to something
- `developer` is deliberately **not** a role yet. Every control-plane route requires the single `admin` grant, so a `developer` role would resolve to exactly the same grant set as `admin`: a label claiming a boundary that is not there. It arrives when the control-plane vocabulary is split
- Resource selectors narrow a grant to part of a namespace. `pubsub:topic=chat.*` and `fn:name=checkout` are **applied by the data path**: publish, publish-batch, the subscribe WebSocket and function invoke all check the caller's grant against the topic or function name they are about. A `pubsub` grant used to be every topic in the namespace, so one leaked runtime key read every conversation in an application
- A selector in a domain the data path cannot yet name — storage, db, push, cache — is **refused at the point of writing** rather than stored. A stored-but-unapplied selector reads as a working restriction in `orama members list` and authorises nothing; refusing it is the honest version, so a selector you can create is one that is applied
- `*` stands for any run of characters and crosses `/` deliberately: a storage selector of `avatars/*` is meant to cover `avatars/2026/03/me.png`, and stopping at the separator would grant less than it appears to
- A grant with no selector is the whole role. Narrowing only ever takes access away — the scope gate has already decided whether the caller may reach that class of thing at all — and a selector this binary cannot parse permits nothing
- `/v1/namespace/members` (`orama members`) lists, adds and removes them. It is admin-scoped and namespace-owned. Transferring the namespace needs the **owner**: an admin who could transfer could take it, which would make admin and owner the same thing again
- The owner cannot be removed, only transferred, and the transfer is one statement: a namespace with no owner is claimable by whoever signs in to it next. The previous owner keeps an admin grant, because handing a project over should not lock you out of it in the same instant
- Signing in to a namespace that has **no** owner still claims it. `default` is created by migration 001 with no owner and is where a wallet signs in before it owns anything, so refusing an unowned namespace would mean nobody could sign in to a fresh cluster. Filed separately

**Namespace ownership (`/v1/auth/verify`, `/v1/auth/api-key`)**
- A namespace has at most one owner, enforced by a partial unique index rather than by a check the code remembers to make. The first wallet to sign in to a namespace nobody owns becomes its owner; every later wallet is refused with `403` and the code `NAMESPACE_NOT_OWNED`, before a JWT, a refresh-token row, an API key or cluster provisioning exists
- Ownership used to be written as a side effect of minting a key, unconditionally. Any wallet that signed a fresh nonce and named an existing namespace in the request body became an admin co-owner of it: the row satisfied the namespace gate, the gate marked the caller a confirmed owner, and a confirmed owner's wallet JWT carries admin
- Every key is minted with its grant written on the row. An empty `scopes` column grants nothing; it used to mean "predates scoping" and was read as admin, and `GetOrCreateAPIKey` wrote no scopes column at all — so every key minted by a wallet login was an admin key, and the legacy-key cutover was undone on each login
- `/v1/auth/register` was removed. Nothing called it, the `apps` row it wrote was never read, it stored a literal placeholder as the app's public key, and it took namespace ownership the same way

**OramaOS agent (`:9998`) and enrollment (`:9999`)**
- The agent's command receiver binds the node's overlay address and requires a per-node bearer token on every route. It bound every interface — under a comment claiming WireGuard only — and checked nothing, so restarting any service on any node took one POST from anywhere that could route to it. Being on the mesh is not the credential: every namespace's services are on that mesh
- The token is minted by the node at enrollment, written `0600` on its encrypted data partition, and stored on the gateway encrypted with a key derived from the cluster secret (`HKDF(cluster secret, "node-agent-token")`), because the gateway has to present it and so cannot hash it
- A node with no address or no token starts **no** receiver. Falling back to listening on everything without a credential is the state this exists to end
- Enrollment is sealed under the registration code the operator carries from the node's console: AES-256-GCM with `HKDF(code, "orama-enrollment-seal-v1")`, in both directions. The cluster secret, the swarm key and the WireGuard configuration used to cross the network as plaintext JSON over HTTP on the node's public IP, to an endpoint that accepted any POST — so anyone who reached a booting node first could enrol it into their own cluster
- The code is never served. A `GET` on `:9999` returned it to whoever asked, which published the one secret the operator carried and let anyone race them for it. The gateway proves it holds the code instead of fetching it, and a wrong one fails to decrypt at the node
- The code is 80 bits, up from 32. It keys the payload that carries the cluster secret, so it is a key rather than an identifier
- The seal has a copy in each of two Go modules, which cannot import each other. `contracts/enrollment/seal.json` pins the derivation, and each side's tests check against it

**Node enrolment (`/v1/node/enroll`)**
- `node_ip` is parsed as IPv4 and stored canonicalised. It is rendered into `Endpoint =` in the `wg0.conf` of every other node, so an unvalidated value is a WireGuard config injection

**RQLite Authentication (Step 1.7)**
- Credentials are generated at genesis and written to `rqlite-auth.json` / `rqlite-password`
- `rqlited` is **not** started with `-auth` today. What keeps the RQLite API off the public internet is the firewall / WireGuard overlay, not RQLite HTTP auth
- Enabling it is a **two-pass rollout**, and the config has two settings so the passes can be separated:
  - `database.rqlite_auth_file` — the credentials clients send. `GenerateNodeConfig` now writes this (plus `rqlite_username` / `rqlite_password`) into every generated `node.yaml`. Setting it is always safe: rqlite ignores credentials it does not require
  - `database.rqlite_enforce_auth` — starts `rqlited` with `-auth`, making it reject unauthenticated callers. Default off
- These were **one** setting until change-287. That made the rollout impossible: the only way to give a node credentials was to simultaneously start refusing every peer that had none — including every node still on the previous release, whose `/join`, `/status` and `/remove` calls would 401 mid-upgrade and look exactly like Raft breaking
- Setting `rqlite_enforce_auth` with no `rqlite_auth_file` is a config **error**, not a silent no-op (`pkg/config/validate/database.go`)

**RQLite admin client**
- Every call to rqlite's admin API (`/status`, `/nodes`, `/join`, `/remove`, `/db/backup`, transfer-leadership) goes through one client, `pkg/rqlite/adminclient.go`, which attaches credentials from the auth file
- Before change-287 these were fourteen bare `http.Client` calls, none sending credentials. Enabling `-auth` would have 401'd all of them at once: reconciliation, backups and leadership transfer would stop, and nothing in the logs would say "credentials". `AdminClient` names a 401 explicitly for that reason
- The one remaining direct client in `pkg/rqlite` is `client.freshHTTP` — the SQL read path, which authenticates from its DSN
- **Still unauthenticated:** the gateway and namespace SQL DSNs. `gateway.Config.RQLiteUsername` / `RQLitePassword` are read but never assigned outside tests, so those DSNs carry no credentials. `rqlite_enforce_auth` cannot be switched on fleet-wide until that is fixed

**Olric Gossip Encryption (Step 1.8)**
- Olric v0.7.0's YAML loader has **no** `encryptionKey` field; a generated key was shipped and silently dropped
- That plumbing is removed. Memberlist confidentiality is the WireGuard overlay (`10.0.0.x`), not Olric AES-GCM
- Wiring `MemberlistConfig.SecretKey` would require embedding Olric, not a YAML field

**IPFS Cluster TrustedPeers (Step 1.9)**
- IPFS Cluster `TrustedPeers` populated with actual cluster peer IDs (was `["*"]`)
- New peers added to TrustedPeers on all existing nodes during join
- Prevents unauthorized peers from controlling IPFS pinning

**Vault V1 Auth Enforcement (Step 1.14)**
- V1 push/pull endpoints require a valid session token when vault-guardian is configured
- Previously, auth was optional for backward compatibility — any WG peer could read/overwrite Shamir shares

### Tenant isolation

- **SQLite ATTACH:** tenant query connections register `sqlite3_tenant_noattach` with `SQLITE_LIMIT_ATTACHED=0`. `ATTACH`/`DETACH` and extra statements in one query are rejected before exec
- **WASM `http_fetch` / `anyone_fetch`:** the destination is checked on the socket, in `net.Dialer.Control`, with the address the connection is about to be made to — once per attempt, for every address the resolver returned and for every hop of a redirect. Loopback, RFC 1918, link-local, unspecified, multicast, carrier-grade NAT, and the IPv6 forms that wrap an IPv4 address (`::ffff:`, NAT64, 6to4) are refused, so tenant code cannot reach rqlite, Olric, the node agent or another namespace's services
- The check used to read the URL string, and it returned "allowed" for any host that was not an IP literal. `http://rqlite.internal/`, or any name the tenant controlled pointed at `10.0.0.5`, went straight through. A name is not an address: the resolver decides what it becomes, the answer can change between the check and the connection, and a redirect goes somewhere the first URL never named
- What is still checked on the URL is what can be settled from the text: the scheme, the names that mean the machine itself (`localhost`, `*.localhost`, `metadata.google.internal`), and an IP literal that is already refusable — answered as a clear message rather than as a connection failure
- **WASM memory:** wazero runtime `WithMemoryLimitPages` from `MaxMemoryLimitMB` (default 256 MB)
- **WASM concurrency:** global semaphore plus a per-namespace slot so one tenant cannot fill the process
- **Private function invoke:** HTTP `/v1/invoke` is unauthenticated at the middleware; `canInvokeFn` requires the `invoke` grant (or a SIWE wallet) for private functions. Storage-only API keys cannot invoke
- **Node/network control:** `/v1/node/{status,command,logs,leave}` and `/v1/network/{connect,disconnect}` require admin. `/v1/node/enroll` authenticates via invite token in the handler
- **IPFS Cluster:** generated unit and `ipfs-cluster-service init` refuse an empty `CLUSTER_SECRET`
- **Agent logs:** `/v1/agent/logs?service=` is an allowlist (`rqlite`, `olric`, `ipfs`, `ipfs-cluster`, `gateway`, `coredns`, `agent`); path traversal is rejected

### Token & Key Storage

**Refresh Token Hashing (Step 1.5)**
- Refresh tokens stored as SHA-256 hashes in RQLite (never plaintext)
- On lookup: hash the incoming token, query by hash
- On revocation: hash before revoking (both single-token and by-subject)
- Existing tokens invalidated on upgrade (users re-authenticate)

**API Key Hashing (Step 1.6)**
**What an API key is**
- `orama_<type>_<payload>_<checksum>`, base62. The type (`sk` control plane, `rk` data plane) is a label for whoever finds the string, and follows the key's grants rather than being chosen; the scopes column is what decides what it may reach
- The checksum is not a security property — anybody can compute it — but it means a leaked key is recognisable offline, which is what secret-scanning partnerships work on, and a mistyped one is refused before a database is touched
- The key no longer carries its namespace. `ak_<random>:<namespace>` published which tenant a key belonged to in every issue, log line and support ticket it was ever pasted into
- Every key has an expiry: 90 days by default, a year at most, and no option for a key that never expires. Migration 051 rebuilds `api_keys` to make `expires_at` and `scopes` NOT NULL, and gives existing keys 90 days from the migration rather than 90 days from when they were minted — dating it from creation would expire every key older than three months the moment it ran
- Rotation mints a successor with the same grants and shortens the original to an overlap (7 days by default) instead of revoking it. Revoking in the same breath as minting is an outage: whatever is deployed with the old key stops the moment the new one exists
- A revoked key is refused within ten seconds rather than sixty. The lookup caches a key's namespace and grants for a minute, so a revocation used to take that long to bite on every gateway that had seen the key; the revocation list is replicated and reloaded every ten seconds and is consulted first
- Signing in mints a **new** key rather than returning the wallet's existing one. It used to `SELECT api_keys.key` and hand that back — which is the HMAC, since production always configures the secret — so a returning owner's second login answered with a string that authenticates nothing. The raw key is shown once and is not recoverable

- API keys stored as HMAC-SHA256 hashes using a server-side secret
- HMAC secret generated at cluster genesis, stored in `~/.orama/secrets/api-key-hmac-secret`
- Namespace and index gateway spawn refuse to start if that file is missing or empty
- On lookup: compute HMAC, query by hash against the **core/index** RQLite registry (never a tenant RQLite)
- In-memory cache uses raw key as cache key (never persisted)
- Startup migrates leftover plaintext `ak_…` rows to HMAC in place; lookup is hashed-only after that

**TURN Secret Encryption (Step 1.15)**
- TURN shared secrets encrypted at rest in RQLite using AES-256-GCM
- Encryption key derived via HKDF from the cluster secret with purpose string `"turn-encryption"`

### TLS & Transport

**InsecureSkipVerify Fix (Step 1.10)**
- During node join, TLS verification uses TOFU (Trust On First Use)
- Invite token output includes the CA certificate fingerprint (SHA-256)
- Joining node verifies the server cert fingerprint matches before proceeding
- After join: CA cert stored locally for future connections
- Production CLI and `tlsutil.NewHTTPClientForDomain` do **not** set `InsecureSkipVerify` — public Caddy certs verify against system CAs
- `TCPSNIGateway` sets `MinVersion: TLS 1.2`
- Certificate serials are 128-bit `crypto/rand` values
- wg0.conf is chmod 0600 after write (WriteFile and tee); umask is not trusted

**WebSocket Origin Validation (Step 1.4)**
- All WebSocket upgraders validate the `Origin` header against the node's configured domain
- Non-browser clients (no Origin header) are still allowed
- Prevents cross-site WebSocket hijacking attacks

### Process Isolation

**Dedicated User (Step 1.11)**
- Host and namespace daemons (gateway, rqlite, olric, sfu, turn, pubsub, ipfs, caddy, coredns) run as `User=orama`
- Caddy and CoreDNS get `AmbientCapabilities=CAP_NET_BIND_SERVICE` for ports 80/443 and 53
- WireGuard stays as root (kernel netlink requires it)
- Anyone client/relay stay `debian-anon`
- vault-guardian already had proper hardening
- `/opt/orama/bin` is `root:orama` mode `0750` so the orama user can execute binaries but cannot replace them

**systemd Hardening (Step 1.12)**
- All service units include:
  ```ini
  ProtectSystem=strict
  ProtectHome=yes
  NoNewPrivileges=yes
  PrivateDevices=yes
  ProtectKernelTunables=yes
  ProtectKernelModules=yes
  RestrictNamespaces=yes
  ProtectProc=invisible
  ```
- `ReadWritePaths` is per-service (data dir + logs), not the whole `.orama` tree
- Units that do not need cluster secrets set `InaccessiblePaths=…/secrets`; the gateway and node keep `ReadOnlyPaths` on `secrets/`
- Applied to both template files (`pkg/environments/templates/`) and hardcoded unit generators (`pkg/environments/production/services.go`) plus `core/systemd/orama-namespace-*@.service`

**Tenant deployments**
- A deployment is a tenant's own code, uploaded through the API and run on a node that also runs the cluster's control plane. Its unit had none of the hardening above: no `User=`, so it ran as root, and only `PrivateTmp`
- Each deployment now runs under `DynamicUser=yes` — systemd allocates a user for the unit and reclaims it when the unit stops, so no two deployments share an identity and none of them is root — plus the block above, `RestrictSUIDSGID`, `RestrictRealtime`, `LockPersonality`, `RemoveIPC` and `ProtectControlGroups`
- `IPAddressDeny` keeps tenant code off the private ranges. The WireGuard overlay on `10.0.0.0/8` carries rqlite, Olric and every other namespace's services, and the deployment sits on the same host; loopback stays allowed because that is how the node's own reverse proxy reaches the app
- The app's own directory is read-only. `StateDirectory` and `CacheDirectory` give it somewhere to write, exported as `ORAMA_STATE_DIR` and `ORAMA_CACHE_DIR`
- `MemoryMax`, `CPUQuota` and `TasksMax` come from the deployment's recorded limits, with the platform defaults when it has none. `MemoryMax=0M` is never written for a deployment that simply has no limit recorded

**Deployment environment variables**
- The values are the tenant's and were interpolated into the unit as `Environment="{{.}}"`, unescaped. A value carrying a double quote and a newline closed the assignment and wrote whatever unit directives it liked, into a unit that ran as root
- They are written to an `EnvironmentFile` instead, mode `0600` in a `0700` directory, outside the deployment's own world-readable directory. systemd reads it as PID 1 before dropping privileges, so the deployment's own user never sees the file. It is deleted when the deployment stops
- The encoding is read off systemd's parser (`src/basic/env-file.c`): the value is double-quoted and exactly the four characters systemd unescapes inside double quotes — `"`, `\`, `` ` ``, `$` — are escaped. Every other byte, newlines and spaces included, is literal. The encoder is tested against a transcription of that parser, not against its own rules
- A value must be valid UTF-8 and free of NUL, because systemd discards an assignment that is not and the variable would simply be missing at runtime. One value is capped at 64 KiB: every value is replicated to every node
- `deployments.environment` held plaintext JSON, and it is where the platform's own guide tells people to put their secrets, so every tenant's keys and passwords sat in a Raft-replicated table and in every backup of it. It is encrypted with AES-256-GCM under a key derived from the cluster secret. Rows written before this are read as plaintext and rewritten encrypted on the next change
- `PORT`, `ENTRY_POINT` and the `ORAMA_*` names are set by the platform and refused from a tenant: a deployment that could set `ORAMA_GATEWAY_URL` could point itself at another namespace's gateway
- `deployment_env_vars`, created with the comment "separate for security" and never written to, is dropped. It claimed environment variables were held somewhere deliberate while they were stored in the clear elsewhere

**Function SQL (`db_query`, `db_execute`, and the batch forms)**
- A function's SQL runs on the gateway's own database handle. On a namespace gateway that is the namespace's rqlite — which is both the tenant's application database and the database that authenticates the namespace, because the core migrations run there. `api_keys`, `namespace_ownership`, `refresh_tokens`, `nonces`, `operators`, `wireguard_peers` and `function_secrets` sit in the same schema as the tenant's own tables
- So a function could `UPDATE api_keys SET scopes = 'admin'`, make any wallet the namespace's owner, or read every other function's secrets — from guest code, with no further credential
- A statement that names one of those tables is refused, however the name is written: bare, `"quoted"`, `[bracketed]`, `` `backticked` ``, `'as a string literal'` (SQLite accepts one where a table name goes), schema-qualified, or hidden behind a comment. `ATTACH`, `DETACH`, `PRAGMA` and `VACUUM` are refused outright, and one host call runs one statement
- The refused list is what grants authority, holds a credential, or configures the platform — not every table the core migrations create. Several of those have generic names a tenant may already be using as their own; a namespace database has exactly one table called `apps`, and it belongs to whoever wrote to it first. A test fails when a migration adds a table that nobody has put on one side or the other
- **This is a filter, and it is not the fix.** A view or trigger created before the filter existed can still reach a protected table when queried by its own name, because the statement doing the querying never names it. The fix is that platform state should not live in a database a tenant's SQL can name at all; the filter closes the direct path while that is built

**Function invocation**
- A cron row firing, a pubsub trigger matching or the JWT claims provider running has no per-invocation caller, so it skips the caller check. What says so is an explicit flag the gateway's own dispatchers set. It used to be inferred from the trigger type, and a nested `function_invoke` from a system-triggered parent was given a trigger type that counted as system — so a value meaning "skip authorization" travelled with the work as an ordinary field
- A persistent-WebSocket upgrade makes the same authorization decision as every other path. It used to check only that an internal function had an admin caller, so a merely private function was reachable over a persistent socket by a caller with no wallet and no invoke grant, while the identical function over HTTP refused them
- A nested call carries the caller's invoke grant. It did not, so a caller who could run a function directly was refused by that same function's own nested call
- `Invoker.InvokeByID` and the exported `Invoker.CanInvoke` are gone. The first ran a function with no authorization at all; the second re-read the function from the registry and then passed the invoke grant as a hardcoded `true`. Neither had a caller outside its own tests

**Failing closed**
- A gateway that validates API keys against the cluster's registry — every namespace gateway does; its own rqlite is the tenant's — does not start if that registry does not answer. It used to log a warning and carry on, and the key lookup then fell back to the local database: the tenant's own rqlite, which holds an `api_keys` table the core migrations created there. A gateway that could not reach the registry did not stop authenticating, it started authenticating against a table the tenant can write
- `Connect()` on the registry client brings up its own side and reports success without having spoken to the database, so it is not evidence the registry is there. A single read against `api_keys` is, and that is what the boot path does
- **The availability consequence is deliberate.** While the cluster's registry is unreachable, a namespace gateway does not start, and a running one cannot validate keys. Serving with the wrong idea of who holds which key is worse than not serving. Giving namespace gateways a signed key snapshot they can validate against locally is the way out, and belongs with feat-212
- A credential in a query string is read only on a WebSocket upgrade, where a browser cannot set a header. There were two copies of the extraction and they disagreed: the middleware's was upgrade-only, the auth handler's took `?api_key=` on any request — so a POST to `/v1/auth/token` could carry a key in its URL, into the access log, into the Referer of whatever the page loaded next, and into history. One copy now, with the decision passed in by the caller, and a test fails if a second appears
- A key whose stored scope column is empty grants nothing. It used to grant `admin`
- The WireGuard peer endpoints refuse when the gateway has no cluster secret, rather than treating "nothing to check against" as "nothing to check", and compare in constant time

**Which routes need what**
- Who may call what is decided by three hand-maintained lists of path prefixes — `isPublicPath`, `requiredScope` and `requiresNamespaceOwnership` — and nothing connects them to the routes they describe. A route can match none of them, or match two that contradict each other, and the only symptom is an endpoint answering the wrong thing to the wrong caller
- That had already happened. `/v1/node/enroll` was exempted from the scope check because its handler validates and consumes a single-use invite token, and was never added to `isPublicPath`. The CLI sends that token as `Authorization: Bearer <token>`; the API-key middleware takes any non-JWT Bearer token as an API key, found nothing, and answered 401 — which the CLI reported as "invalid or expired invite token". Enrolling a node could not work, and the error blamed the token
- Every registered route now carries an explicit classification — open to anyone, authenticated by its own handler, or needs a credential — and a test walks the route registrations and fails on a route nobody has classified, on a classification for a route that no longer exists, and on the contradictions: a handler-authenticated route that the middleware would refuse first, a credential route marked public, a public route carrying a scope or ownership requirement that can never run because both middlewares return early for a public path
- This is not the fix. The fix is a policy declared where the route is registered, which is Phase 2; the classification is what keeps the lists honest until then
- The middleware chain itself now has tests: anonymous, a runtime key, an admin key, a key from another namespace, a key the registry does not know, a signed-in wallet, and a wallet signed in to another namespace. Cross-namespace isolation had been covered only in a `//go:build e2e` suite that `make test` does not run, and `scopeMiddleware` and `authorizationMiddleware` were never invoked by a unit test at all

**Secrets on disk, and on the way in**
- A namespace's SFU config carries the namespace's TURN shared secret and its rqlite DSN, which has the database password in it, and was written 0644. Any local account on the node could mint TURN credentials for the namespace and read its database. It is 0600, written atomically so a file an older release left world-readable is replaced rather than adjusted
- Joining a cluster sends the invite token, which is a credential for every secret the cluster holds. Without a fingerprint to pin, the client set `InsecureSkipVerify` and checked nothing at all, so the token went to whoever answered the address. It refuses to join now. Every invite carries the fingerprint — `orama node invite` reads this node's certificate and will not mint an invite without it, and `orama node install` decodes it from the token — so the refusal only affects a bare token from somewhere else, and the error says so
- A namespace's own rqlite has an `api_keys` table, because the core migrations run there. Nothing validates against it, but rows written before keys were hashed hold the raw `ak_…` value — working credentials for the platform, in the clear, in a database the tenant can read. `MigratePlaintextAPIKeys` hashes such rows but runs against the registry and never sees these. A namespace gateway removes them at boot, and only the plaintext ones: a hashed row is inert too, but it is not a credential
- Still open, and split out as its own ticket: a TURN credential is `<expiry>:<namespace>` with nothing per-user in it, so one credential relays for every user of the namespace and nothing can be revoked before it expires. Changing that is a protocol change on both ends and interacts with a TTL that was set deliberately for a live tenant

**Revoking a credential**
- Revoking an API key stopped the key and did nothing to the JWTs already exchanged from it. They verify on the signature alone, so an operator was told the credential was gone while it still had up to fifteen minutes of full access. Logging out was the same shape: it dropped the refresh token and left the access token valid, so "log me out" meant "stop me getting a new one"
- Tokens carry a `jti` now, and there is a list of revocations checked on every request: one token by its id, or every token issued to a subject before a moment. Revoking a key writes the second kind — one row covers every outstanding token from that key. A token minted *after* the revocation is a new grant and is deliberately not covered
- A token exchanged from a key carries the raw key as its subject while the revoking code only ever has the hash, so the check looks under both
- The list is held in memory and reloaded every 10 seconds, because a database round trip per request costs a cross-region hop. That interval is the staleness: a revocation takes effect within it. Fifteen minutes became ten seconds. A failed reload keeps the previous list rather than clearing it — forgetting the revocations because one query failed would turn a database blip into every revoked token working again
- A token that names no key is refused. There used to be a branch accepting a token with no `kid` at all and verifying it against the RSA key, so a token that named no key selected one by omission
- An RSA signing key under 2048 bits is refused at boot. The size was never checked

**The record**
- `audit_events` has existed since the first migration and had never been written to. Nothing recorded who minted a key, who was granted what, who revoked it, or who signed in — so the first question anyone asks about a credential, when did this appear and who made it, had no answer anywhere
- Recorded now: a challenge issued, a sign-in succeeding or failing, a refresh, the refresh-replay tripwire, a logout, a key minted or revoked, the legacy-key sweep, a namespace created, an operator minting an invite. Each with the actor, the namespace, the client's address, the user agent and whether it succeeded
- A refused request is deliberately **not** recorded. One row per 401 would let anyone with a network connection fill a Raft-replicated table
- The table's old shape could not hold these: `namespace_id` was `NOT NULL` with a foreign key, so an attempt against a namespace that does not exist — exactly the interesting case — could not be written. It is a name now, nullable, with `result` and `user_agent` as columns
- Readable at `GET /v1/audit`, admin grant, and the namespace comes from the caller's own credential rather than the query string: reading another namespace's trail would say who its owners are and when they sign in
- A failed write is logged, not returned. The record is evidence, not a control; refusing a login because the audit row could not be written would turn a database blip into an outage

**Saying why a request was refused**
- A 401 had at least six distinct causes and told them apart only by an English string, so nothing could distinguish "you sent nothing" from "your key was revoked" from "your token expired" without matching on prose. That is what cost days on bug-160 and bug-164
- Every 401 and 403 now carries `{error, code, hint}` — what happened, and what to do about it — plus the fields that make it actionable: `required_scope` on a missing grant, both namespaces on a mismatch. The codes are `AUTH_MISSING`, `AUTH_INVALID_KEY`, `AUTH_REVOKED`, `AUTH_EXPIRED`, `USER_JWT_REQUIRED`, `INSUFFICIENT_SCOPE`, `NAMESPACE_MISMATCH`, `OWNERSHIP_REQUIRED`, `NOT_AN_OPERATOR`, `DESTINATION_NOT_ALLOWED`
- `INSUFFICIENT_SCOPE`, `USER_JWT_REQUIRED` and `NOT_AN_OPERATOR` keep the spellings already on the wire. The audit proposed different ones; renaming a code the SDK already switches on would break every client that does
- A test walks the source and fails on a 401 or 403 written without a code
- The SDK mirrors the list as `AuthCode`, and a revoked credential gets its own error class because it is the one case where the answer is "sign in again" rather than "check what you sent"

### Supply Chain

**Binary Signing (Step 1.13)**
- Build archives include `manifest.sig` — a rootwallet EVM signature of the manifest hash
- During install, the signature is verified against the embedded Orama public key
- Unsigned or tampered archives are rejected

## Phase 2: OramaOS

These measures apply only to OramaOS nodes (mainnet, devnet, testnet).

### Immutable OS

- **Read-only rootfs** — SquashFS. dm-verity hashes can be built into the image; they are **not** wired into the boot path, so integrity is not enforced at boot today
- **No shell** — `/bin/sh` symlinked to `/bin/false`, no bash/ash/ssh
- **No SSH** — OpenSSH not included in the image
- **Minimal packages** — only what's needed for systemd, cryptsetup, and the agent

### Full-Disk Encryption

- **LUKS2** with AES-XTS-Plain64 on the data partition
- **Shamir's Secret Sharing** over GF(256) — LUKS key split across peer vault-guardians
- **Adaptive threshold** — K = max(2, floor(N/3)) where N is the number of peers; writes require W = min(N, max(K+1, ceil(2N/3))) shares so a successful write is always recoverable
- **Key zeroing** — LUKS key wiped from memory immediately after use
- **Malicious share detection** — fetch K+1 shares when possible, verify consistency

### Service Sandboxing

Each service runs in isolated Linux namespaces:
- **CLONE_NEWNS** — mount namespace (filesystem isolation)
- **CLONE_NEWUTS** — hostname namespace
- **Dedicated UID/GID** — each service has its own user
- **Seccomp filtering** — per-service syscall allowlist

Note: CLONE_NEWPID is intentionally omitted — it makes services PID 1 in their namespace, which changes signal semantics (SIGTERM ignored by default for PID 1).

### Signed Updates

- A/B partition scheme with systemd-boot and boot counting (`tries_left=3`)
- All updates signed with rootwallet EVM signature (secp256k1 + keccak256)
- Signer address: `0xb5d8a496c8b2412990d7D467E17727fdF5954afC`
- P2P distribution over WireGuard between nodes
- Automatic rollback on 3 consecutive boot failures

### Zero Operator Access

- Operators cannot read data on the machine (LUKS encrypted, no shell)
- Management only through Gateway API → agent over WireGuard
- All commands are logged and auditable
- No root access, no console access, no file system access

## RAM-to-disk (swap and cores)

Guest `mlock`/`mlockall` is **not** used in the Go services. `mlock(2)` does not survive `execve`, so a launcher that locks then execs `rqlited`/`caddy`/`olric-server` gives those binaries zero locked pages. Go `string` values are also unzeroable. Against a hosting-provider RAM snapshot, mlock buys nothing.

The control that keeps secrets off the **block device** is cgroup `MemorySwapMax=0` on secret-bearing units, plus install-time `swapoff` / `fs.suid_dumpable=0` / systemd-coredump `Storage=none`. That does **not** stop a RAM snapshot or provider VM-suspend.

## What this does not defend against today

Stated so the gaps above are known positions, not implied protections:

- **RAM snapshot** of a running node (secrets in process memory, including gateway vault combine)
- **Hosting-provider / hypervisor access** to the guest
- **Ubuntu fleet SSH** — Zero Operator Access and LUKS FDE apply to OramaOS only
- **dm-verity at boot** — hashes may exist in the OramaOS image; they are not wired into the boot path
- **RQLite HTTP auth** — `rqlited -auth` is not enabled; overlay + firewall are the control. The clients are ready (see above); the gateway/namespace DSNs are not
- **ntfy** — no auth-file in v1; listen-localhost is the control
- **Namespace gateways bind every interface** (`:PORT`, not the overlay address). Reaching one directly still requires a MAC to assert anything, so this is exposure of a listener rather than of an authorization decision. Moving it needs the two local health checks in `pkg/namespace/cluster_manager.go` moved with it — see the bugboard issue
- **A captured disk snapshot of RQLite** — plaintext application data, including `deployment_env_vars`
- **Immediate erase of deleted rows** — a SQL `DELETE` is a Raft log entry; the original INSERT remains in `raft.db` until size-driven compaction, not a privacy TTL
- **Olric memberlist AES-GCM** — v0.7.0 YAML has no `encryptionKey`; WireGuard is the control
- **WireGuard key rotation** — none; `wg0.conf` is 0600. Rotation would be a rolling mesh with a health gate

## Rollout Strategy

### Phase 1 Batches

```
Batch 1 (zero-risk, no restart):
  - CIDR fix
  - IPv6 disable
  - Internal endpoint auth
  - WebSocket origin check

Batch 2 (medium-risk, restart needed):
  - Hash refresh tokens
  - Hash API keys
  - Binary signing
  - Vault V1 auth enforcement
  - TURN secret encryption

Batch 3 (high-risk, coordinated rollout):
  - RQLite auth — two passes, in this order:
    1. Roll out binaries + configs carrying `rqlite_auth_file` (enforcement off) to **every** node. Nothing changes behaviourally; clients simply start sending credentials
    2. Only once the whole fleet is on pass 1, set `rqlite_enforce_auth: true` and restart followers first, leader last
    Doing these in one pass 401s every peer still on the old binary
  - Olric encryption (simultaneous restart)
  - IPFS Cluster TrustedPeers

Batch 4 (infrastructure changes):
  - InsecureSkipVerify fix
  - Dedicated user
  - systemd hardening
```

### Phase 2

1. Build and test OramaOS image in QEMU
2. Deploy to sandbox cluster alongside Ubuntu nodes
3. Verify interop and stability
4. Gradual migration: testnet → devnet → mainnet (one node at a time, maintaining Raft quorum)

## Verification

All changes verified on sandbox cluster before production deployment:

- `make test` — all unit tests pass
- `orama monitor report --env sandbox` — full cluster health
- Manual endpoint testing (e.g., curl without auth → 401)
- Security-specific checks (IPv6 listeners, RQLite auth, binary signatures)
