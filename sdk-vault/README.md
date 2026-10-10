# @debros/orama-vault

Client for the Orama Network vault guardians: Shamir-split secret storage across
the guardian daemons that run on every node.

Requires Node 20.19 or later: its @noble dependencies are ESM-only, and a CommonJS `require` of them
relies on `require(esm)`, which Node 20.19 and 22.12 enable by default.

## Who this is for

**Not applications.** Guardians listen on the WireGuard overlay (`10.0.0.x`), so
only software running on the mesh can reach them: node agents, operator tooling,
and RootWallet.

An application reaches the vault through the gateway instead. `POST
/v1/vault/push` and `POST /v1/vault/pull` are served over HTTPS, do the Shamir
split and combine server-side, and authenticate each request with a per-request
Ed25519 ownership signature. That path is documented on the
website under Vault (`website/src/docs/developer/vault.mdx`).

This package used to be a directory inside `@debros/orama`, where it added two
cryptography dependencies and twenty top-level primitives to every application's
bundle for an API none of them had a route to.

## Install

```bash
npm install @debros/orama-vault
```

## Use

```typescript
import { VaultClient, QuorumError } from "@debros/orama-vault";

const vault = new VaultClient({
  guardians: [
    { address: "10.0.0.1", port: 10106 },
    { address: "10.0.0.2", port: 10106 },
    { address: "10.0.0.3", port: 10106 },
  ],
  // 32-byte Ed25519 seed. Identity is SHA-256 of the public key.
  privateKey,
});

const secret = new TextEncoder().encode("sk-live-…");

try {
  const stored = await vault.store("api-key", secret, 1);
  console.log(`${stored.ackCount} of ${stored.totalContacted} guardians hold a share`);
} catch (error) {
  if (error instanceof QuorumError) {
    // The secret is NOT saved. error.guardianResults says which guardians failed.
    console.error(error.message, error.guardianResults);
  }
  throw error;
}

const { data, version } = await vault.retrieve("api-key");
console.log(new TextDecoder().decode(data), `(version ${version})`);
```

## What the client guarantees

**A write that returns is durable.** `store` and `delete` throw a `QuorumError`
when fewer than the write quorum acknowledged, rather than resolving with a
`quorumMet: false` field that a caller has to remember to read.

**A read never mixes versions.** Shares are grouped by the version they belong
to and only shares of one version are ever combined. A guardian that missed the
last write still answers, with a share of the previous split; combining it with
newer shares reconstructs neither and reports no error.

**The configured guardian list is the unit of redundancy.** A secret is split
into one share per guardian in the configuration, whether or not every guardian
is reachable during the write. Splitting over only the reachable ones would
reduce the cluster's redundancy to whatever happened to be up at that moment.

**A listed secret is a readable secret.** `list` reports a name when at least the
read threshold of guardians hold a share of it, which is exactly the condition
under which it can be reconstructed.

## Quorum

```
K = max(2, floor(N/3))              read threshold
W = min(N, max(K + 1, ceil(2N/3)))  write quorum
```

N is the number of configured guardians. `W > K` is the durability guarantee for
N≥3: a write reported successful has persisted strictly more shares than a read
requires. The same two formulas are implemented in
`vault/src/membership/quorum.zig` and `core/pkg/shamir/shamir.go`, and the three
must agree exactly. A one-node eval cluster does not Shamir-split: the Orama
gateway stores the envelope as a local key (`K=1`, `W=1`). Direct overlay
clients that call `split(data, 1, 2)` still fail; eval apps use the HTTPS
gateway. See `website/src/docs/operator/getting-started.mdx`.

## Authentication

Every call is checked twice by the guardian. The first check is a session token from a challenge
exchange in which **no client secret takes part**: the guardian returns a nonce and an HMAC tag computed
with its own server secret, then verifies that same tag when the client sends it back. The session
therefore proves only that the caller completed a challenge on this guardian.

The second check binds a request to an identity. Every secrets call carries `X-Vault-Pubkey` and an
Ed25519 `X-Vault-Signature` over a message that names the operation, the secret and a version or
timestamp (`vault-secret-put-v1:<identity>:<name>:<version>` and its get, delete and list siblings).
The guardian requires the SHA-256 of that public key to equal the identity in the session token. The
client signs with `privateKey`, the 32-byte Ed25519 seed in its configuration. Guardians still trust the
WireGuard overlay for the rest (plain HTTP inside the tunnel, unauthenticated status and health
endpoints); the known gaps are in `docs/whitepaper/technical-reference/vol1/28-vault.md`.

A guardian restart invalidates cached session tokens. The next call fails with an `AUTH` error and is not
retried; call `clearSessions()` and repeat.

## Development

```bash
pnpm install
pnpm lint
pnpm typecheck
pnpm build
pnpm test
```
