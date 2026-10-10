# Anonymity and Tor

> **At a glance.**
>
> - **What:** two uses of Tor. Every cluster node runs a client on the public Tor network for three gateway routes (`/v1/proxy/anon`, `/tunnel`, `/relay`) and `anon_fetch`. Separately, the global layer runs the Orama Tor network, with its own authorities, relays, opt-in exits and per-validator onion services, so a wallet can submit a transaction without its address reaching the validator.
> - **Key numbers:** node SOCKS `127.0.0.1:9050`; ORPort 31020, DirPort 31021; at least 3 authorities with 12-month signing certificates; tx gate on loopback 31022, 3 calls, 512 KiB body, 20 requests per second with burst 40, no logging; tunnel 24 per user and 512 per node, 256 MiB each way, 30 min; relay 128 streams, 64 MiB, 5 min.
> - **Code:** `core/pkg/anonproxy/`, `core/pkg/tornet/`, `core/pkg/chainonion/`, `core/pkg/txgate/`, `chain/x/vpnlaunch/`.

![Two Tor networks: the node's public-Tor client and the Orama Tor network](../technical-reference/diagrams/ch38-overview.svg)

## Two problems with opposite requirements

The first is egress on behalf of users. An application sometimes needs the destination not to learn who asked. A few Orama-owned exits would be a tiny anonymity set under one operator, so the node's client joins the public Tor network and its many independent exits. The price is that the gateway sits between user and Tor.

The second is submitting chain transactions. A wallet posting to a validator tells it the wallet's IP address, a public-Tor exit would see the transaction in the clear, and a public-Tor client cannot reach a service that exists only inside Orama. An onion service solves both ends: the validator never sees the sender and the traffic never leaves Tor. Orama runs its own network for this. Its anonymity set is small today, and the code says so: a network file that claims to be public is refused.

Three rules hold everywhere. Every client fails closed: anonymised egress is never downgraded to the node's address, and an onion submission is never sent in the clear. Tor is unmodified upstream, installed from the Tor Project's repository under a key pinned by fingerprint. Authority identity keys stay offline.

## The node's client and the gateway routes

The node's Tor is client-only, with `IsolateSOCKSAuth` and an `IPAddressDeny` for every private range, WireGuard included, so it reaches only public relays whatever a caller asks. `core/pkg/anonproxy/` is the only dialer: host names go to the proxy unresolved, and if the proxy is down the dial fails. No code path dials directly.

**`/v1/proxy/anon`** performs one HTTP request for the caller. The gateway sees the URL, headers and body, so this is anonymity toward the destination only.

**`/v1/proxy/tunnel`** carries an opaque TCP stream over a WebSocket, so the client runs TLS end to end and the gateway relays ciphertext. It admits wallet-JWT callers only, public hosts at ports 80 and 443, and caps concurrency per user and per node. A failed dial returns a fixed sentence, because a tunnel that reports why it failed is a probe for whatever the exit can reach. Circuit isolation uses a SOCKS credential that is an HMAC of the wallet subject under a node-local secret: stable across restarts, different on every node, never the wallet address. The gateway learns destination and volume, not content.

**`/v1/proxy/relay`** is the tunnel with the identity removed. A client downloads an object without the node that holds it learning the client's address, by opening this socket on a different node and running TLS to the serving node through it. It takes no credential, pins the destination to the cluster's own hosts on port 443, uses a random circuit per stream, and leaves no log line, row or metric. The host check is exact: ASCII only and whole-label suffix matching, because Unicode case folding turns the Kelvin sign into a `k` and check and resolver would disagree.

`anon_fetch` gives WASM functions the same guarantee: if Tor is down it returns an error, never a direct request.

## The Orama Tor network

One file, `tor-network.json`, names the network's directory authorities, voting schedule and validator onion addresses. One function parses it, strictly: unknown fields are errors, `private` must be true, at least three authorities with public IPv4 addresses and distinct identities, and a voting schedule that satisfies Tor's own startup checks, so a bad file fails at install and not on a restart. The file is public and unsigned; its integrity is the release's. Every torrc starts with `UseDefaultFallbackDirs 0` and the file's authorities, so a node never asks the public network's directories for anything.

The roles are `dirauth`, `relay`, an opt-in `exit` and `onion`. Each runs under its own account, because an onion service anyone can reach must not share a uid with an authority's signing key. Authorities list at most one relay per IP address, the Sybil control. An exit is refused unless the network file allows exits, and its policy rejects every private range and the mail and file-sharing ports.

### The key ceremony

`orama maint global tor ceremony` runs on an air-gapped machine. For each authority it makes the relay identities and an authority identity key with a 12-month signing certificate, then cross-checks Tor's output rather than trusting it: the fingerprint must equal the SHA-1 the ceremony computes itself, and the key file must carry the encrypted marker, proving the passphrase arrived (on stdin, never a command line). The identity key signs certificates and nothing else, and goes to offline media in two places; only the signing key, certificate and relay identity are deployed. At install, the host checks that the bundle belongs to this authority and is unexpired, and never overwrites an installed identity key with different bytes.

Authorities then vote on the file's schedule and sign the consensus. A timer archives each voting period on the authority host: consensus, the votes that made it and a manifest of hashes. Nothing publishes the archive.

## Submitting a transaction over onion

![Transaction submission over the Orama Tor network](../technical-reference/diagrams/ch38-tx-submission.svg)

A validator with the `onion` role runs a v3 hidden service forwarding port 80 to the **tx gate** on loopback. The gate has no key and no access to the chain home, and forwards exactly three calls to the chain's REST API: an account read, a broadcast (JSON, at most 512 KiB), and a lookup by hash. Everything else is a 404 that never reaches the chain. It forwards no caller header and logs nothing. Every request arrives from the local Tor process, so it cannot tell callers apart, and its limits are whole-gate: 20 requests per second with a burst of 40, 16 in flight. Past either it answers 429, "try another validator". The chain, not the gate, validates the transaction.

On the wallet side, `core/pkg/chainonion/` is the contract: no environment proxy, no direct dialer, no redirects, a dial only through a loopback SOCKS proxy. One client per transaction means one circuit per transaction. If the onion service cannot be reached, the error says the transaction was not sent and nothing was tried outside Tor. With `--onion-network`, the CLI starts a Tor client for that one command and picks a validator onion uniformly at random, so no transaction is steered to a fixed validator. `orama maint vpn up` runs the same client as a loopback SOCKS5 proxy.

## Trust and limits

Orama runs every authority. While it holds a majority, it could sign a consensus that lists only its own relays and so deanonymise a user of the network. `chain/x/vpnlaunch` encodes when that stops being true: for 30 consecutive days at least 5 independent authorities, 100 relays from 40 operators, 15 exits in 5 countries and no operator above 10% of consensus weight. It is not linked into any node, nothing builds the data it judges, and its launch switch is a constant `false`, so the public VPN beta stays closed by design.

One limit is worth stating plainly: `/v1/proxy/anon` and `anon_fetch` pass no isolation credential, so an exit can link requests from different users of one node. Only the tunnel and relay separate circuits. All routes also share one Tor process per node, so that process is the first bottleneck.
