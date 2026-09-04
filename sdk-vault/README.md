# @debros/orama-vault

Client for the Orama Network vault guardians: Shamir-split secret storage across
the guardian daemons that run on every node.

## Who this is for

**Not applications.** Guardians listen on the WireGuard overlay (`10.0.0.x`), so
only software running on the mesh can reach them: node agents, operator tooling,
and RootWallet.

An application reaches the vault through the gateway instead. `POST
/v1/vault/push` and `POST /v1/vault/pull` are served over HTTPS, do the Shamir
split and combine server-side, and authenticate each request with a per-request
Ed25519 ownership signature. That path is documented in
[docs/vault](../docs/vault) and on the website under Vault.

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
    { address: "10.0.0.1", port: 7500 },
    { address: "10.0.0.2", port: 7500 },
    { address: "10.0.0.3", port: 7500 },
  ],
  identityHex: "…64 hex characters…",
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

N is the number of configured guardians. `W > K` is the durability guarantee: a
write reported successful has persisted strictly more shares than a read
requires. The same two formulas are implemented in
`vault/src/membership/quorum.zig` and `core/pkg/shamir/shamir.go`, and the three
must agree exactly.

## Authentication, and its current limit

The guardian issues a session token after a challenge exchange in which **no
client secret takes part**: it returns a nonce and an HMAC tag computed with its
own server secret, and then verifies that same tag when the client sends it
back. Possession of an identity hash is therefore enough to obtain a session for
that identity.

The Ed25519 proof this needs is tracked as bug-51 and bug-52 and is the "Phase
3" the guardian's own source refers to. Until it lands, a guardian's
reachability on the WireGuard overlay is the real access boundary. This is why
the configuration takes no HMAC key: the field existed, was required, and was
never read by anything.

## Development

```bash
pnpm install
pnpm lint
pnpm typecheck
pnpm build
pnpm test
```
