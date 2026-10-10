# The edge

> **At a glance.**
>
> - **What:** how a client reaches a cluster by name. CoreDNS with a custom plugin serves the cluster's own zone straight from a registry table. Caddy on every node terminates TLS, with certificates that the cluster obtains once through a DNS-01 challenge it answers itself and stores sealed in the registry. An opt-in router shares port 443 so TURN relay traffic can look like HTTPS.
> - **Key numbers:** CoreDNS 1.14.7 on port 53; answer cache 10,000 entries, 30 s, served stale up to 24 h; record TTLs 300 s or 60 s; at most 13 nameserver slots. Caddy 2.11.4, HTTP/1.1 only; Let's Encrypt allows five certificates a week per name set. Stealth host `cdn-` plus 12 hex characters; router limits 10,000 connections and 32 per client IP.
> - **Code:** `core/pkg/coredns/rqlite/`, `caddy/`, `core/pkg/tlsstore/`, `core/pkg/sniproxy/`.

![DNS and nameservers: resolvers, CoreDNS, the registry and the writers of dns_records](../technical-reference/diagrams/ch24-overview.svg)

## Why the cluster is its own DNS

Every public name is a name under the base domain: the apex, `ns-<namespace>` for a tenant gateway, one name per deployment, relay hosts. The set changes whenever a namespace is created, a node is replaced or a service fails. A hosted provider would add an outside account and turn every membership change into an API call that can fail halfway. The ACME DNS-01 challenge that issues the wildcard certificate also has to write a TXT record into the zone the certificate authority queries, so the zone's authority and the certificate issuer must be one system. The cluster is therefore authoritative for its own zone, and the zone is a table.

## DNS: a zone that is a table

Nameserver nodes (installed with `--nameserver`) run CoreDNS with a `rqlite` plugin that answers from `dns_records` in the index RQLite. There are no zone files and no zone transfers.

### Resolution and the stale reserve

A query is an exact match on name and type, then a wildcard walk that drops one label at a time (`turn.ns-app` finds `*.ns-app`), then a negative answer with the zone's SOA: NODATA (NOERROR, empty) when the name owns a record of another type, has names below it, or the nearest wildcard covers it (a name that owns a record or has names below it is never covered by a wildcard), NXDOMAIN when it does not exist at all, so an AAAA query for a name that has only A records does not make a resolver forget the name. A negative answer costs three indexed database queries however deep the name is, whether a name has names below it is answered from memory, and a failed query is never answered with a guessed NXDOMAIN. Each cache miss is a leader read, so it fails when the registry has no leader.

When the backend fails, the plugin serves an expired entry for up to 24 h with a 30 s TTL, so a lost Raft leader does not take the zone offline. Negative answers are never served stale, because "this does not exist" is the answer most likely to be wrong soon, such as a namespace being provisioned. The resolver is not open: recursion is offered to the node's own processes only.

### Many writers, no leader

Any node can notice a peer is dead, so DNS edits cannot wait for a coordinator. Writers are idempotent statements safe to run on every node, and the dangerous ones carry their own guard inside the SQL. Each row carries an ownership tag (`system`, `acme`, `namespace:<name>`) and every purge selects by tag. Removing an address from a round robin uses a statement that counts the remaining active rows, so two nodes that each see two rows cannot both disable theirs.

Three mechanisms turn failure into DNS change, and they overlap on purpose. A node whose own gateway fails three probes in a row (about 90 s) withdraws its rows. The ring monitor disables a suspect node's rows. A reaper marks nodes silent for 120 s inactive and, after 15 min, purges their namespace records. Gateway-host rows keep the last record even when it points at a departed node, because an emptied name falls through to the base wildcard and sends clients to a node that does not host the namespace, which is harder to diagnose than an outage.

### Node names

With `dns.node_names_zone` set to a sub-zone (install flag `--node-names-zone`), each node copies the chain's claimed names into tagged `A` and `AAAA` rows every minute.

### Slots and delegation

A nameserver node claims the lowest free slot `ns1` to `ns13` by inserting a row whose primary key is the hostname, so Raft decides the winner. Thirteen is the number of NS records with glue that fit a classic 512-byte referral. A slot is published as an NS record only once its glue A record exists and matches. A missed heartbeat does not free a slot, since that would drop the glue and the delegation with it; `orama node remove` does. Because slots are claimed at run time, the operator cannot know which address holds `ns1` in advance. `orama node dns delegation` reads the glued slots from the registry, prints the records for the registrar and checks that the parent zone returns them. Delegation must precede the first certificate, since the certificate authority finds CoreDNS through it.

## TLS: one certificate per name per cluster

Every node terminates public TLS in its own Caddy, built with two Orama modules. The reason is the certificate authority's rate limit: when each node kept certificates on disk, a five-node cluster spent a week's allowance of five in one install, and the next join got nothing. The fix is one certificate per name per cluster, obtained by one node and loaded by the rest.

![TLS and certificates: Caddy, its two modules, the gateway and the registry](../technical-reference/diagrams/ch25-overview.svg)

### The store and the lock

`caddy.storage.orama` implements CertMagic's storage over HTTP to the local index gateway. Every value is sealed with AES-256-GCM inside the Caddy process, with the storage key as associated data, so a private key moved to another path fails to open; the gateway stores only ciphertext. Two keys are derived from the cluster secret, a MAC key and a seal key, so the key that authenticates is never the key that encrypts.

The gateway answers 404 unless the caller is loopback, the node is not a namespace gateway, and the coordination MAC verifies: method, audience, path, query, body hash, a nonce and a timestamp within 60 s. Values are capped at 256 KiB. A lock is one conditional upsert through Raft: the first node takes a 60 s lease, renews every 20 s, and an expired lease is taken over by the same statement. Locks assume clocks agree within 40 s; larger skew costs a duplicate issuance, not corruption.

Caddy waits up to 2 min for the store to open at start, because a name whose certificate cannot be read at that moment is left unmanaged until restart. Anything other than a 200 is an error, never "absent", since reading a refusal as "no certificate yet" would order one the cluster already has. A node upgraded from disk storage first imports its old certificates, each directory whole or not at all, and the store stays closed until the import succeeds.

### DNS-01 through the gateway

`dns.providers.orama` holds no DNS credentials. To solve a challenge it posts the token to the gateway, which publishes a TXT record under `_acme-challenge`. The request is signed with a separate key that authorises only that act; the gateway accepts only names inside the base domain and values matching a base64url SHA-256 digest, and deletes only the caller's own value. The apex and the wildcard share one challenge name with different values, so the table holds several TXT values at one name. There is no HTTP-01 and no self-signed fallback.

### The Caddyfile and the wildcard export

The Caddyfile is regenerated by every install and upgrade; the chosen CA persists in `node.yaml` so a staging cluster does not silently return to production. Caddy serves HTTP/1.1 only: HTTP/3 would bind UDP 443, which the relay needs, and HTTP/2 forbids the `Upgrade: websocket` header, so WebSocket authentication through the query string failed. Caddy strips the six `X-Internal-Auth-*` headers from every public request before the gateway's own MAC check.

TURN terminates TLS itself and cannot ask Caddy for a certificate, so once a minute the cluster gateway exports the newest `*.<base>` pair to disk. It writes only a pair that loads, is unexpired and covers a single label, key first, and the relay reloads it by modification time every 60 s. It never serves a self-signed certificate: a rejected certificate is indistinguishable from censorship.

## SNI routing and stealth TURN

TURN's standard ports are the ones restrictive networks block first. Stealth TURN lets a client reach the relay on port 443 with a TLS handshake that looks like any other. An opt-in router, `orama-sni-router`, takes port 443 on a node, moves Caddy to 8443, reads only the SNI from the first TLS record, and forwards the still-encrypted stream either to the shared TURN listener on 5349 or to Caddy. It terminates nothing and holds no keys. The stealth host is `cdn-` plus the first 12 hex characters of the namespace's SHA-256, one label under the base so the wildcard certificate covers it, and it names neither the application nor TURN. Any client can compute it from a namespace name, and its DNS records are public, so this is blandness, not secrecy. TURN still requires its HMAC credential; the SNI only chooses a certificate.

The router allows a 5 s ClientHello, 60 s idle, 10,000 connections and 32 per client IP, and drops a connection with no SNI. Because it opens its own connection to Caddy, the gateway sees every client as `127.0.0.1`, which collapses per-client rate-limit buckets on router-enabled nodes. The router is part of the node's public-serving check, so a crash-looping router takes its node out of DNS.

The router's TURN routes do not work today. It discovers backends by scanning per-namespace `turn-*.yaml` files that no code writes since TURN became one shared server per host, and the cluster manager deletes any that remain. The stealth host therefore falls through to Caddy, which completes a TLS handshake and then receives STUN framing. Everything around it reports success: the credentials response advertises the `turns:cdn-...:443` URI, DNS resolves it, and the end-to-end test checks only the URI and never dials the port. Plain TURN on 3478 and TURNS on 5349 are unaffected.

## How it fails

A node whose Caddy fails two consecutive 30 s checks withdraws its records, and the next healthy check restores them. A registry with no leader leaves DNS answering from cache and certificate issuance blocked, while Caddy keeps serving the certificates it already loaded. An unreachable CA leaves the plain-HTTP sites reachable. A slot holder that dies keeps its slot and glue until an operator removes it, so resolvers fall back to the other nameservers.

## Trust

CoreDNS runs as its own account in a sandbox. Caddy's admin API is a 0600 socket instead of `localhost:2019`, where any local process could load a config. Caddy can do exactly two things to the cluster, store sealed blobs and publish ACME TXT records, and the gateway refuses everything else it asks for. A compromised Caddy yields traffic, the cluster's certificates and the power to publish ACME TXT records, not general DNS control or other secrets.

The limit that matters most here is the stealth route: until the router reads its backend from the shared TURN configuration, port 443 reaches no relay.
