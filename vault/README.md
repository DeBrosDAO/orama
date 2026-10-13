# Orama Vault

The `vault-guardian` daemon, written in Zig. Every Orama node runs one. It stores one Shamir share of each secret in the network's vault: it receives a share, checks the sender's ownership proof, and writes bytes. Splitting and combining happen in the gateway (applications) or in `sdk-vault` (tooling on the WireGuard mesh).

The vault is part of the network and has no separate documentation. Read:

- Client, gateway API and wire reference: `website/src/docs/developer/vault.mdx` (docs page `/docs/developer/vault`)
- Running, monitoring and recovering a guardian: `website/src/docs/operator/vault-guardians.mdx` (`/docs/operator/vault-guardians`)
- How it works, security model and known gaps: `docs/whitepaper/technical-reference/vol1/28-vault.md`

## Build and test

Requires Zig 0.15.2 (`minimum_zig_version` in `build.zig.zon`); a different minor release does not compile.

```bash
zig build              # debug build: zig-out/bin/vault-guardian
zig build test         # unit tests (entry point src/tests.zig)
```

Production binaries are built by `orama build`, which cross-compiles for Linux (`ORAMA_ZIG` selects the Zig binary). `src/main.zig` does not currently compile on macOS (a signal-mask initialiser valid only on Linux), so run the daemon on Linux.

## Run

```bash
./zig-out/bin/vault-guardian --data-dir /tmp/vault-dev --port 7500 --bind 127.0.0.1
```

Flags: `--config <path>` (default `/opt/orama/.orama/data/vault/vault.yaml`), `--data-dir`, `--port`, `--bind`, `--help`, `--version`. On a node the installed `vault.yaml` binds the WireGuard address on port 10106.

## Layout

| Path | Contents |
|------|----------|
| `src/main.zig` | Entry point, config, signal handling, housekeeping thread |
| `src/server/` | HTTP listener, router and handlers (V1 push/pull, V2 secrets, health) |
| `src/auth/` | Challenge and session tokens (HMAC), Ed25519 ownership proofs |
| `src/storage/` | File store (V1) and vault store (V2) |
| `src/sss/` | GF(2^8) field, split, combine, re-sharing, commitments |
| `src/membership/`, `src/peer/` | Node list, quorum formulas, peer protocol (not linked into the daemon) |
| `src/crypto/` | HMAC, AES-GCM, HKDF, secure memory, post-quantum stubs (only HMAC is linked) |

Part of the Orama Network. See the root repository for license details.
