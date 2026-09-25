# Orama Network — Whitepaper

Version 1.0 · September 2026

## Abstract

Most software today runs on computers owned by a handful of cloud companies. That is convenient, and it gives those companies full view of the data, full control over the service, and a single point of failure for everything built on top.

Orama Network is an open-source platform that provides the ordinary building blocks an application needs, including hosting, a SQL database, a cache, file storage, messaging, serverless functions, domains, and voice and video relays. It is built to run them on machines owned by independent operators; today its networks run on a small set of invited nodes. Every tenant gets its own small cluster instead of a slice of a shared one. All traffic between machines travels inside an encrypted mesh. Sign-in is a wallet signature, so the platform never collects an email address, a password or a phone number.

This paper describes what Orama is, how it works, what works today, what is only partly done, and what comes next. It is written against the source code. Where the code and the project's other documents disagree, we followed the code. We state limits as plainly as we state capabilities.

## 1. The problem

The commercial cloud solved a real problem: nobody wants to rack servers again. But the arrangement has a cost that is rarely stated.

**Concentration.** A small number of providers host a very large share of the internet. When one of them has a bad day, a large part of the web has one too. Your database sits on their disks, in a jurisdiction you did not choose, under terms they can change.

**Single points of failure.** An account can be suspended, a region withdrawn, a price raised. The dependency runs one way. For an application, the provider is a failure domain that no amount of engineering on your side can remove.

**Privacy.** The provider runs the hypervisor, the storage and the network, so encryption at rest is decrypted with keys the provider also controls. And because every service is reached through an account tied to a legal identity and a payment method, the provider knows who you are and, often, who your users are.

Self-hosting avoids all three and brings back the operational burden the cloud removed, at the reliability of one machine. There has been little in between: a network of machines owned by many parties that still offers a SQL database, a cache, a place for files, a queue, a function runtime, and a domain with a certificate.

## 2. What Orama is, and what it is not

**In plain terms:** Orama is a cloud platform built to run on many independently operated servers instead of one company's data centers; today it runs on a small set of invited nodes. Developers use a command-line tool and an SDK and get the familiar building blocks. Behind the interface, each application runs on its own small cluster of machines spread across the network.

**More precisely:** Orama is built from well-understood open-source components: RQLite (SQLite replicated with Raft), Olric (a distributed in-memory cache), IPFS and IPFS Cluster (content-addressed storage and pinning), libp2p GossipSub (messaging), wazero (a WebAssembly runtime), Pion (WebRTC and TURN), CoreDNS, Caddy and WireGuard. A Go control layer ties them together into a multi-tenant platform. The same software runs on every node. Nodes differ only in configuration flags and in which tenants have been placed on them.

Three design commitments explain most of the architecture:

1. **No shared data layer between tenants.** Each tenant, called a *namespace*, gets its own database cluster, cache ring and gateway processes, not a prefix on shared tables.
2. **No internal traffic on the public internet.** Machines talk to each other only through a WireGuard mesh. Services that are not meant to be public listen only on the mesh.
3. **No accounts or passwords.** Identity starts with a wallet signature. Everything else is derived from it.

**What Orama is not:**

- **Not a blockchain, and not a token project.** Wallet signatures are used only as a login method. There is no chain, no token, and no on-chain logic in the request path.
- **Not censorship-proof.** Nodes are ordinary servers at ordinary hosting providers. Orama is designed so that losing any single node does not take an application down. That is resilience, not immunity.
- **Not a defense against a hostile hypervisor.** Anyone who can read the memory of a running server can read what that server is processing. Section 5 states this precisely.
- **Not finished.** Orama is alpha software running on two small networks. It is not open for public sign-up.

## 3. How it works

### 3.1 Nodes and the mesh

A node is a Linux server that runs the Orama software. When a node joins the network, it first sets up a WireGuard tunnel to every other node and receives a private overlay address in `10.0.0.0/24`. Only after that do database, cache, storage and gateway services start, and they reach their peers on overlay addresses, never public ones. The list of mesh peers is stored in the replicated database. Every node checks its live tunnel configuration against that list on a timer, so a node that misses an update repairs itself.

A node joins with a single-use invite that expires within an hour. The invite carries the fingerprint of the issuing node's certificate, and the joining node refuses to proceed if the certificate does not match. In the current release branch (not yet on the test network), the cluster then records the new node's identity: each node generates its own Ed25519 key, and the cluster stores only the public half. That key signs the node's registration and heartbeats. The first enrollment of that key is itself signed with the node's libp2p identity key, whose public half is embedded in its peer ID, so a node cannot claim another node's identity.

The public side of a node is deliberately small: SSH on Ubuntu nodes, DNS on port 53 (nameserver nodes only), HTTP and HTTPS on ports 80 and 443, WireGuard on UDP 51820, and TURN relay ports on nodes that host voice and video. IPv6 is disabled so it cannot be used to bypass the IPv4 firewall.

### 3.2 Two planes: index and tenant

Each node runs two kinds of workloads, and both are built from the same set of systemd unit templates:

- **The index plane** runs on every node. It holds the network's own state: which nodes exist, the DNS zone, API keys and grants, sessions, the audit trail and deployment records. It includes the cluster-wide RQLite database (the *registry*), IPFS and IPFS Cluster, the messaging service, the vault guardian, Caddy for TLS, a self-hosted ntfy push server, an anonymity client, and the index gateway that serves the control API. Nameserver nodes also run CoreDNS.
- **The tenant plane** runs one set of processes per namespace (a database, a cache and a gateway), plus a media server and TURN relay if the namespace has enabled real-time features.

A supervisor process (`orama-node`) brings these units up as components with declared dependencies and keeps them converged. It retries failures with backoff instead of exiting. A node that boots while its peers are down still starts everything it can run locally, marks itself *degraded*, and returns to *active* when quorum comes back, without a restart. A reconciler runs every 60 seconds to restart missing tenant services, rewrite configurations that no longer match cluster membership, and remove members that are permanently gone.

Platform state and tenant data are kept apart. A tenant's own database no longer contains any of the platform's identity tables (keys, grants, sessions, signing keys, audit events). A single placement file decides which database each table belongs to, and a test fails if a new table is left unplaced.

### 3.3 A namespace is your own cluster

In most platforms a tenant is a column in a shared table. In Orama a tenant is a cluster. Creating a namespace:

1. picks three nodes, weighted by free capacity, and treats machines that share a public IP address as a single failure domain;
2. reserves a five-port block on each node, from a range that allows up to 20 namespaces per node;
3. starts that namespace's RQLite, Olric and gateway units in order;
4. publishes DNS records for the namespace.

The namespace's tables live in its own database file, on its own replication log, behind its own gateway. There is no cross-tenant query because there is no shared table to query across.

A wallet can own up to 10 namespaces. A network with a single eligible machine provisions one-node namespaces for evaluation. This mode is explicitly not highly available, and losing the disk loses the namespace. A network with two eligible machines refuses to provision, because a two-member Raft group cannot survive the loss of either member. Networks of three or more always provision tenants on three nodes.

### 3.4 The gateway

Every request passes through a gateway. The index gateway serves the control API: sign-in, keys, namespaces, deployments, DNS and operator actions. Each namespace gateway serves that tenant's data API. When a request for a namespace arrives at the index gateway, the index gateway authenticates it and forwards it over the mesh. The verified identity travels in internal headers protected by an HMAC over the method, path, asserted fields and a timestamp. Headers without a valid MAC are removed before any other code sees them, and Caddy strips them at the edge as well. A tenant's gateway listens only on the node's overlay address.

Each route declares its own policy: whether it needs a credential, which grant, whether the caller must be a member of the namespace, and what kind of token is accepted. A route with no declared policy cannot be registered. The middleware chain applies, in order: rate limiting, domain routing, authentication, namespace authorization, the grant check, and per-namespace rate limiting.

### 3.5 DNS and certificates

Orama is its own nameserver. CoreDNS on the nameserver nodes answers from a table in the replicated database. Creating a deployment or a namespace, or starting a certificate challenge, inserts a row, and the next DNS query sees it. There is no zone file and no external DNS provider. If the database is unreachable, CoreDNS keeps serving the last known answers for up to 24 hours with a short TTL, so a database outage does not take every name offline.

Caddy terminates public TLS and obtains certificates automatically through DNS-01 challenges written into the network's own DNS. Certificates cover the network's own domains, so every deployment gets an HTTPS address under them. A custom domain can be attached and verified by a TXT record, but certificates for custom domains are not issued yet: the TLS check accepts only subdomains of the network's base domain.

## 4. Services

Everything below is reached through the `orama` CLI, the TypeScript SDK, the Go client, or the gateway's HTTP API. There is no web dashboard, and none is planned.

| Service | What it does | Built on | Status |
|---|---|---|---|
| Static and Next.js hosting | Static sites and Next.js static exports served from content storage; Next.js SSR as a supervised process | IPFS, systemd | Live |
| Node.js and Go backends | Long-running backend processes with health checks, logs, environment variables and rollback | systemd templates | Live |
| Namespace database | A SQL database replicated across the tenant's three nodes | RQLite (SQLite + Raft) | Live |
| Per-app SQLite | Separate SQLite database files per namespace, with backups to content storage | SQLite | Live |
| Cache | Distributed key-value maps with TTLs | Olric (in memory) | Live |
| File storage | Content-addressed storage on a private swarm, pinned across the cluster | IPFS, IPFS Cluster | Live |
| Pub/sub | Topic messaging over WebSocket and REST, with presence | libp2p GossipSub | Live |
| Serverless functions | WebAssembly functions triggered by HTTP, WebSocket, pub/sub or cron | wazero | Live |
| HTTPS addresses | Every deployment gets an address under the network's domain, with certificates that renew automatically | CoreDNS, Caddy | Live |
| Custom domains | Your own domain on a deployment, verified by TXT record; certificates for custom domains are not issued yet | CoreDNS | Partial |
| Voice and video | Selective forwarding plus TURN relay, relay-only by design | Pion SFU and TURN | Live |
| Stealth TURN | TURN over TLS on port 443, routed by SNI, so relayed calls look like HTTPS | SNI router | Live, optional per node |
| Push notifications | Direct APNs, a self-hosted ntfy server for Android and web, and Expo for Android via Google | APNs, ntfy, Expo | Live |
| Anonymity proxy | Outbound HTTP and TCP tunnels through the Tor network | Tor client (client only) | Live |
| Vault | Secrets split with Shamir's scheme across guardian nodes | Zig guardian | Partial |

### Hosting

A deployment is one of five runtimes: `static`, `nextjs-static`, `nextjs` (SSR), `nodejs-backend` or `go-backend`. Static content is stored in IPFS, and any node can serve it without a running process. Dynamic deployments run as instances of hardened systemd templates. Each runs under its own dynamically allocated user, never root, with memory, CPU and task limits. It is blocked from private address ranges, so tenant code cannot reach the mesh, and its application directory is read-only. Environment variables are encrypted at rest and handed over through a file that only systemd reads.

A deployment also has its own identity. It receives a one-hour token that it renews itself and that carries only the grants its owner gave it, so no long-lived key is stored on the node. Any node can answer read requests. Changes such as updates, rollbacks and deletions are forwarded to the deployment's home node, so two nodes never modify the same application at once.

### Functions

Functions are written in Go, compiled to WebAssembly with TinyGo, and executed in process by wazero. There are no containers and no virtual-machine cold starts. Memory is configurable from 1 to 256 MB (64 MB by default), and execution time from 1 to 300 seconds (30 seconds by default). Concurrency is capped for the whole process and for each namespace, so one tenant cannot use every slot.

A function can do nothing on its own: no sockets, no files, no system calls. Every capability arrives through an explicit host function: database queries, batches and transactions; cache operations; pub/sub publishing; push sending; secrets; outbound HTTP; anonymous outbound HTTP; TURN credentials; calls to other functions; caller identity; WebSocket messaging; and ephemeral per-connection state that is cleared when a client disconnects. Auditing what tenant code can reach means reading one table.

Outbound HTTP is checked at the socket against the actual address being dialed, for every resolved address and every redirect. Loopback, private, link-local, carrier-grade NAT and multicast addresses are refused, so a function cannot reach node-local services by using a hostname that resolves to an internal address. Function SQL cannot name platform tables or use `ATTACH`, `PRAGMA` or `VACUUM`. Triggers are HTTP, persistent WebSocket, pub/sub topics and cron schedules. Chains of pub/sub-triggered functions are limited to a depth of five.

### Real-time: voice, video and data

A namespace can enable WebRTC once it has three nodes. The media server (SFU) listens only on the overlay, so clients cannot reach it directly. All media passes through a TURN relay, and the server enforces relay-only transport. As a result, two people on a call never learn each other's IP addresses. TURN credentials are HMAC-derived from a per-namespace secret that is encrypted at rest. One TURN server per host serves every namespace placed there.

**Stealth TURN** puts the relay behind port 443. A small router reads the unencrypted server name from the TLS handshake and forwards the raw bytes without decrypting them, either to TURN or to Caddy. The relay hostname is derived from a hash of the namespace name and looks like a CDN host. To a network observer, a relayed call looks like an ordinary HTTPS connection. This helps in countries that block VoIP ports.

### Push notifications

Tenants bring their own push credentials, which are stored encrypted, and no push goes through a shared platform account. iOS notifications go directly to Apple's APNs. Android devices without Google services, and web clients, use an ntfy server hosted on the nodes themselves. Expo is available for apps that prefer Google's delivery path.

### Anonymity proxy

Every node runs a Tor client (client only — it relays nothing). An application can send a single HTTP request through it (the gateway sees the request, because it performs it) or open a WebSocket tunnel that carries an end-to-end TLS stream (the gateway sees only the destination host and port). Functions have the same capability through `anon_fetch` (the older name `anyone_fetch` remains as a deprecated alias). Both paths require a signed-in wallet user, not just an app key.

### Vault (partial)

The vault is designed to store a secret without any single machine holding it. The secret is split with Shamir's scheme across guardian processes on separate nodes, and any number of shares below the threshold reveals nothing. On a network of N nodes the threshold is the larger of 2 and N divided by 3, rounded down. Reads and writes require an Ed25519 proof of ownership. What is not finished: guardians do not yet discover each other on their own, the guardian-to-guardian protocol is implemented but not started, and proactive resharing does not exist. Applications reach the vault through the gateway, which splits and recombines the secret in its own memory during the request. We therefore list the vault as partial and do not recommend it yet as the only copy of anything important.

## 5. Identity and security model

**In plain terms:** you sign in by approving a message in your wallet. The platform learns your public address and nothing else. Programs use API keys with narrow permissions that expire. Every administrative change is recorded.

### Sign-in

A user requests a challenge and signs it with an Ethereum (EIP-4361, Sign-In with Ethereum) or Solana (Sign-In with Solana) wallet. The message names the gateway's own domain, the namespace, a single-use nonce and a five-minute expiry. The server reads the wallet, nonce and namespace from the signed text itself, not from unsigned fields beside it. A signature collected by another site therefore does not verify, and a captured signature cannot be replayed. The result is a 15-minute access token and a rotating refresh token, stored only as a hash. The CLI can also sign in on a machine that has no wallet by using a device-code flow.

Every gateway signs tokens with its own Ed25519 key and publishes the public half. A namespace gateway's key is bound to its namespace, so it cannot mint tokens for another tenant. Operators can rotate signing keys without logging anyone out. Revoking a key or logging out takes effect within about ten seconds through a replicated revocation list.

### Principals, roles and grants

A *principal* is a wallet, an API key, or a deployed app. A *grant* is what a principal may do in one namespace. The roles are `owner` (exactly one per namespace, enforced by the database), `admin` (the control plane), `runtime` (the data plane) and `reader`. A grant can be limited to part of a namespace, for example specific pub/sub topics, one function, a storage prefix or a set of cache keys, and the data path enforces that limit. A limit the data path cannot enforce yet is refused when the grant is written instead of being stored and ignored.

API keys look like `orama_sk_…` or `orama_rk_…`. They include a checksum so secret scanners can recognize leaked keys, and they do not reveal which namespace they belong to. Keys expire after 90 days by default and after one year at most. Rotating a key issues a successor with the same grants and keeps the old key valid for a seven-day overlap. Keys are stored only as HMACs. A key meant to ship inside a client app carries only data-plane grants.

### Audit, rate limits and errors

Sign-ins, key and grant changes, namespace, deployment and function changes, secret changes and operator actions are written to a replicated audit trail, kept for 90 days, and readable with `orama audit`. The actor is recorded as a wallet address or a key fingerprint, never as a credential. Rate limits apply per client address before authentication. Credential endpoints have their own stricter limit, and challenge requests are also limited per wallet. Every 401 and 403 response includes a machine-readable code and a hint.

### Isolation and hardening

- **Network:** WireGuard between all nodes. RQLite listens only on the overlay and always requires authentication. Tenant gateways listen only on the overlay. Internal endpoints require both a mesh source address and a cluster credential.
- **Processes:** services run as an unprivileged `orama` user under systemd sandboxing, and tenant apps run as dynamically allocated users. Secret-bearing units cannot swap to disk, and core dumps are disabled.
- **Secrets at rest:** TURN secrets, function secrets, push credentials, deployment environment variables and agent tokens are encrypted with AES-256-GCM under a versioned key hierarchy. `orama operator rotate-secrets` re-encrypts them under a new root. Private files are encrypted before they are added to IPFS.
- **Supply chain:** release archives are signed, and the installer rejects unsigned or tampered archives.

### Current limits, stated plainly

- **Memory is not protected.** A hosting provider that can snapshot a running machine's RAM can read what that machine is processing. Only properties that come from *not collecting* data, such as having no email or phone number on file, survive that.
- **Ubuntu nodes are trusted.** Their operators have root and SSH access, and their disks are not encrypted. This is why node operation is invite-only today. A disk snapshot of an Ubuntu node exposes the cluster's shared secrets.
- **Nodes share a cluster secret,** and every node is a member of the registry database. A compromised node can still write cluster state. Rotating the cluster secret itself, and the WireGuard keys, requires a maintenance window, and no rotation tooling exists for the mesh keys yet.
- **Some internal services trust the mesh.** Media signaling and messaging gossip do not yet authenticate each peer individually.
- **TURN credentials are namespace-wide** and last 24 hours, and cannot be revoked per user.
- **Deletion is not erasure.** A deleted database row remains in the replication log until compaction. IPFS content is reclaimed by garbage collection every six hours, or immediately when requested.

## 6. Privacy by design

Orama's privacy properties come from careful arrangement of ordinary components, not from new cryptography:

- **Nothing to disclose.** There is no email, password, phone number or profile. A demand for a user's identity can return only a public key, because nothing else exists. This is the one property that no server compromise defeats.
- **No shared account layer.** Each application's data lives in its own cluster.
- **Callers hidden from each other.** Relay-only calls mean participants never learn each other's IP addresses.
- **Calls that do not look like calls.** Stealth TURN on port 443 makes a relayed call look like HTTPS.
- **Push without a middleman.** Direct APNs and self-hosted ntfy mean notification metadata does not pass through a third-party push broker unless a tenant chooses Expo.
- **Outbound anonymity.** Apps and functions can make requests through the Tor network without revealing the server's own address.
- **Operator diversity as a security property.** The more independent operators and jurisdictions a network spans, the more parties an adversary must compel. This is why the network needs to grow beyond its current operators, and why it should do so only once a hostile node cannot harm a tenant (Section 10).

The goal is modest and honest: move secrets and plaintext out of cheap, bulk, after-the-fact capture, such as disk images and backups, and into expensive, targeted, live capture. That is a real improvement. It is not invisibility.

## 7. Built on Orama

### AnChat

AnChat is an encrypted messenger and Orama's first real tenant. It is in public beta on Android (Google Play) and iOS (TestFlight), and its entire backend runs on Orama's test network:

- **About 120 WebAssembly functions** (119 app functions plus a migration runner) carry the server-side logic, written in Go and compiled with TinyGo.
- **A namespace RQLite database** holds its server state, built from 21 migrations.
- **Pub/sub** over a single WebSocket delivers real-time messages and events. Some functions are triggered by pub/sub topics and cron schedules.
- **IPFS storage** holds attachments. Each file is encrypted on the device with its own AES-256-GCM key before upload.
- **TURN and SFU** carry voice and video. One-to-one calls are relay-only, and the client falls back from UDP to TCP to TLS to stealth TLS on port 443 to get through restrictive networks. Group calls run through the SFU. Their media is encrypted in transit but is not yet end-to-end encrypted.
- **Push notifications** use APNs, including VoIP pushes for incoming calls, on iOS and Orama's self-hosted ntfy on Android. Notifications carry no message content.
- **The anonymity proxy** fetches link previews and location lookups without exposing the user's IP address.

Accounts are wallets: signing up creates a self-custodial RootWallet with a 12-word recovery phrase, and signing in is a Sign-In with Ethereum signature. No email or phone number is ever requested. Messages use a hybrid post-quantum key exchange (X25519 combined with ML-KEM-768) with authenticated symmetric encryption.

### RootWallet

RootWallet is a self-custodial wallet for crypto accounts on EVM chains, Solana and Bitcoin, plus passwords, SSH keys and two-factor (TOTP) codes. Crypto accounts are derived from a standard BIP-39 recovery phrase. Passwords, SSH keys and TOTP secrets are kept in a local encrypted vault on the device; RootWallet has no cloud backup. It is available as a desktop app and a command-line tool, and a mobile app is in development. RootWallet is integrated with Orama in three ways:

- **It signs Orama logins.** A wallet signature is the platform's native identity. The `orama` CLI gets its sign-in challenge from the gateway and has RootWallet sign it. RootWallet is also the wallet that AnChat creates for its users.
- **It holds operators' keys.** The `orama` CLI never stores an operator's SSH keys or passwords. It requests them from a local RootWallet agent over a Unix socket that only the owner can open. The agent identifies each calling program by its binary, asks the user to approve any program it has not seen before, and locks itself after a period of inactivity.
- **It signs releases.** Orama release archives are signed with a RootWallet key and verified on install.

RootWallet does not store anything on Orama today. An earlier design backed the wallet up to Orama's vault guardians as Shamir shares; that code was removed from RootWallet in September 2026, and a backup on Orama would return only once the vault is complete.

## 8. Running a node

### Today

Node operation is invite-only, and operators are vetted, because an operator of an Ubuntu node has root access to the machine (Section 5). A node needs:

| Requirement | Minimum |
|---|---|
| Operating system | Ubuntu 22.04 or 24.04, or Debian 12 (releases the Tor Project publishes packages for) |
| Architecture | amd64 or arm64 |
| CPU | 2 cores |
| Memory | 2 GB |
| Free disk | 10 GB |
| Network | A public IPv4 address |

An existing operator mints an invite with `orama node invite`, and the new operator runs `orama node install` with it. Upgrades are rolling: new binaries can be unpacked on every node in parallel, but restarts happen one node at a time, and cluster health is verified between them. The registry database needs a majority of voters, so restarting several at once would lose quorum. The CLI transfers leadership before restarting a leader. In the current release branch (not yet on the test network), dead voters are removed only when three independent signals agree, and never if doing so would lose quorum.

Each node contributes storage in proportion to its disk: IPFS's storage budget is set to half the node's disk, and garbage collection reclaims unpinned data every six hours.

### Tomorrow: OramaOS and Orama One

**OramaOS** is a locked-down operating system built for Orama nodes. It has no SSH and no shell, a read-only root filesystem, and an encrypted data partition whose key is split across peer nodes, so the operator never holds it. It updates itself through signed A/B partition images with automatic rollback after three failed boots. A single agent process manages the machine and takes commands only over the mesh with a per-node token. Enrollment is sealed under an 80-bit registration code that the operator reads from the node's console.

OramaOS is **built but has never been booted** on a live network. Known gaps include integrity verification that is compiled but not wired into boot, a syscall filter that only logs, missing escrow for the first node's key, and defects in the update path. Finishing it is step two of the roadmap.

**Orama One** is Orama's own node hardware, a device intended to run OramaOS and be distributed to the public so that anyone can run part of the network. It is on the roadmap. No hardware exists yet.

## 9. Current status and honest limits

**What exists.** Two networks, a development network and a test network, each with three nodes, all of which are nameservers. One real tenant with live users, AnChat (in public beta), uses nearly every service. The platform is open source, and everything in Sections 3 to 7 not marked partial is implemented in the code.

**What does not exist yet:**

- **Public sign-up.** Namespaces exist only on networks run by known operators.
- **A dashboard.** The CLI and SDK are the whole interface, by design.
- **Billing.** Nothing is metered or charged yet.
- **Tenant monitoring and alerting.** Tenants have health endpoints, namespace status, deployment logs and the audit trail. There is no metrics endpoint and no alerting.
- **Enforced storage quotas.** Quotas exist but are opt-in, and no namespace has one by default.
- **Scale evidence.** The design allows 20 namespaces per node and grows by adding nodes, but it has been exercised only at the scale of a few nodes and a few tenants.

**Self-audit.** We audit our own system and publish what we find. An earlier review that treated the hosting provider as the adversary produced ninety findings. Two further audits in September 2026 covered stability (35 items) and authentication and authorization (29 items). All stability items and all but three authentication items are now implemented in code and awaiting review. Several of the changes described in this paper, including authenticated RQLite, the WebAssembly egress filter, per-gateway signing keys, secret rotation and encrypt-before-add storage, landed in the current release line in September 2026 and are being rolled out to the live networks. The remaining open findings are concentrated in OramaOS and the vault and are tracked openly.

We still need an independent external security audit, and it is part of the first roadmap step.

## 10. Roadmap

The roadmap has three steps, in this order.

**1. Make the network stable.** Finish hardening: close the remaining self-audit findings, roll the current release line out to both networks, make node updates safe and automatic instead of operator-driven, and commission an external security audit. The goal is a network where losing any single node loses no data and no availability, and where a disk snapshot yields no usable credential.

**2. Finish OramaOS.** Boot it, test it, wire integrity verification and syscall filtering into the boot path, fix the update path, and move nodes onto it. A node whose operator cannot log in and whose disk key never exists on the machine is the precondition for letting people we do not know run nodes.

**3. Orama One.** Build Orama's own node hardware running OramaOS, and distribute it to the public so that anyone can run part of the network.

Each step depends on the one before it. Opening the network before a hostile node is harmless would not be decentralization. It would be an unguarded multi-tenant system with more participants.

## 11. Business model

Orama will charge for usage. Developers will pay for the resources their applications consume. Specific prices and mechanisms have not been set. There is no billing in the product today.

## 12. Open source

Orama is developed in the open.

| Component | Language | License |
|---|---|---|
| Core: node, gateway, CLI, Go client | Go | AGPL-3.0 |
| Vault guardian | Zig | AGPL-3.0 |
| OramaOS and its agent | Go, Buildroot | AGPL-3.0 |
| TypeScript SDK | TypeScript | MIT |
| Vault client SDK | TypeScript | MIT |

The core is AGPL-3.0, so anyone who runs a modified Orama as a network service must share their changes. The client SDKs are MIT-licensed, so applications that use them are not bound by the core's license.

Contributions, bug reports and security findings are welcome. If this paper says something the code does not do, that is a defect. Report it the way you would report a bug.
