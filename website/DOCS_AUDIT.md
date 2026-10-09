# Website docs audit

Branch `nightly` at `6b08177c`, network version 0.3.0, SDK `@debros/orama` 0.3.0.
Audited: 31 MDX pages under `website/src/docs/` (8,426 lines), the navigation in
`website/src/data/docs-navigation.ts`, the renderer (`src/pages/docs.tsx`, the sidebar,
the search index in `vite.config.ts`, `scripts/build-llms.mjs`), against the code
(`core/`, `chain/`, `os/`, `sdk/`), the 33 engineering docs in `docs/`, and the RootWallet
repo (read only).

Rule used throughout: the code is the truth, then `docs/*.md`, then plans. Where a
plan describes something that is not in the tree it is listed under "Not built" and the
pages say so plainly.

## 1. The headline findings

1. **The "install from scratch" journey does not exist as one path.** The operator pages
   describe `orama node setup` but leave out the steps that make a cluster actually come
   up: choosing and delegating the base domain (NS records and glue, the
   `orama node dns delegation` command), the fact that a joiner pins the genesis node's
   certificate and so the delegation must resolve first, the ACME CA choice
   (`--acme-ca letsencrypt-staging`) and the shared certificate store, the firewall
   ports to open in front of the VPS, the signed-archive requirement (the genesis
   wallet must sign the first archive), the host-key pin (`--host-key`), and a verify
   step after each stage with recovery for the failures people hit. `RUN_YOUR_OWN_CLUSTER.md`
   and `DEVNET_INSTALL.md` have the sequence; the website has fragments of it.
2. **Whole product areas have no page at all.** Namespaces and their lifecycle; sign-in
   (wallet, device-bound sessions, the lobby, sign-in policy, open sign-in, signing in from
   a machine with no wallet); roles, grants, members and scoped API keys; the audit
   trail; push notifications; relayed fetch and fetch capabilities; stealth TURN; the
   Tor client and the anonymity proxy; node replacement; global nodes; the
   security model; the Go client SDK; the Gateway API surface; the one-VPS eval; backups
   and restore; the whole RootWallet product.
3. **The blockchain section is badly stale.** `blockchain/what-it-is.mdx` says there is
   no governance module, no NFT module, no CosmWasm and that shielded transfers are not
   implemented. The tree has `x/houses` (two-house governance), `x/token`, `x/nodes`,
   `x/storage`, `x/relay`, `x/archive`, `x/inclusion`, `x/cnft`, `x/market`,
   `x/confidential`, `x/shielded` (real verifiers), `x/wasm` with five standard
   contracts, and the `orama-global` services. Five pages cover about a tenth of it.
4. **The CLI reference is a hand-written fork of a generated file.**
   `developer/cli-reference.mdx` (524 lines) shows `orama 0.122.108` and covers a
   fraction of the command tree. `docs/CLI_REFERENCE.md` is generated from cobra and a
   test fails if it drifts. The website should be generated from it, not maintained
   next to it.
5. **The SDK reference documents the 2025 SDK.** `sdk-reference.mdx` (911 lines) covers
   `auth` (challenge/verify only), `db`, `pubsub`, `network`. It has no `cache`,
   `storage`, `functions`, `chain` or `relay` sections, no devices, no scopes, no
   workload identity. SDK 0.3.0 exports all of them (`sdk/src/`).
6. **The navigation model cannot carry the content.** Four flat persona lists, no
   grouping, no place for security/architecture, privacy, or RootWallet. The sidebar,
   search dialog, search-index plugin (`vite.config.ts`) and `build-llms.mjs` all
   hard-code the persona set and the five chain page file names.
7. **Diagrams are supported and under-used.** The code-block renderer turns a
   ` ```mermaid ` fence into a diagram (`src/components/ui/mermaid.tsx`); code in other
   languages is highlighted by shiki (typescript, javascript, bash, json, go, yaml, html,
   css, sql, toml, ini). Only a handful of pages use diagrams. The rewrite uses mermaid for
   flows and sequences, and plain text blocks for layouts.

## 2. Page-by-page verdicts (existing pages)

Verdicts: KEEP (accurate, light edits), UPDATE (accurate core, stale or thin parts),
REWRITE, REPLACE (generate from source), MOVE (re-home in the new tree).

### Developer

| Page | Verdict | What is wrong or missing |
|---|---|---|
| getting-started | REWRITE | Auth section teaches the old challenge/verify handshake only; no namespaces, no device-bound sessions, no open sign-in, no scoped keys. Uses `orama env use devnet` as if environments ship by default: a fresh CLI has none (`core/cmd/orama/internal/environment.go`, `noEnvironmentHelp`). Lists "Proxy" as a core service without the privacy limits. |
| deployments | UPDATE | Missing deployment env management (`orama app env set/unset/list`), `orama app grants` (what a deployed app may do as itself), `app stats`, rollbacks detail, workload identity. Check URL examples. |
| databases | UPDATE | SQLite and RQLite ORM sections are fine in shape; missing namespace RQLite export/import and sealed backups (`orama namespace backup/restore`), the guard rails on tenant SQL (`sqlguard`), per-namespace quotas. |
| vault | UPDATE | Needs to be checked against `vault/` (Zig guardian) and `docs/vault/*`; hard-codes devnet host names. |
| pubsub | UPDATE | Missing reserved topics, capability WebSockets and the access rules in `AUTH.md`; scoped keys. |
| cache | UPDATE | Missing namespace quotas and scopes; accurate otherwise. |
| storage | REWRITE | No relayed fetch, no fetch capabilities (`/v1/storage/fetch-caps`, `X-Orama-Fetch-Cap`), no storage deals on-chain, no quota/GC behaviour, `/v1/storage/get` now always sends `Content-Length`. |
| functions | UPDATE | Needs `function triggers`, `versions`, `enable/disable`, `logs`, host-function table from `SERVERLESS.md` (`anon_fetch`, `storage_fetch_cap_mint`, push), secrets, limits. |
| domains | UPDATE | Hard-codes `orama-devnet.network` / `orama-testnet.network` as "the" environments. Needs custom-domain TXT flow checked against `orama domain`. |
| webrtc | REWRITE | Uses `?api_key=` on the signalling URL; current model is identity, admission, join tickets, kicks with a generation (`WEBRTC.md`, "Identity, admission and moderation"; migration 075). No stealth TURN, no room placement, no TURN multi-tenancy. |
| sdk-reference | REWRITE | See headline 5. |
| cli-reference | REPLACE | See headline 4. |
| video-tutorials | KEEP | Not touched. |

### Operator

| Page | Verdict | What is wrong or missing |
|---|---|---|
| getting-started | REWRITE | Becomes the start of the from-scratch journey. Hardware table (4 vCPU / 8 GB) is a recommendation; the enforced floors (2 CPU, 2 GB, 10 GB free; Debian 12/13, Ubuntu 22.04/24.04/26.04) must be stated exactly. No DNS step. |
| node-setup | REWRITE | Two overlapping install flows, no delegation, no ACME CA, no shared store, no host-key pin, no signed archive explanation, no verification. |
| node-management | UPDATE | Accurate for list/ssh/status; add `orama node remove`, replacement runbook, wallet ownership explained once. |
| monitoring | UPDATE | Subcommand table lacks `chain` and `traffic`; no telemetry API, no public `/status`, no ring monitor, no namespace DNS self-management (`MONITORING.md`). |
| upgrades | UPDATE | Needs the leader-last ordering, `--acme-ca` on upgrade, schema migrations (`orama node schema`), the autoupdate command, and signed archive rotation. |
| wireguard | UPDATE | Mostly sound; add the persisted peer table behaviour and the `monitor mesh` output. |
| security | REWRITE | States "RQLite HTTP auth is not currently enforced". False on this branch: `pkg/rqlite/authfile.go` installs the auth file and rqlited is started with `-auth`; `DEVNET_INSTALL.md` says rqlited always requires basic auth. Checklist must be rebuilt. |
| orama-os | UPDATE | Compare with `ORAMAOS_DEPLOYMENT.md`; image source, enrollment code and unlock flow need exact commands. |
| troubleshooting | UPDATE | Covers 8 generic problems; `COMMON_PROBLEMS.md` has 22 specific, named failures with root causes and fixes (Olric unavailable, missing cluster-state.json, rqlite refuses to start, IPFS GC timeouts, WAL replay crash-loop, snapshot never catches up, 504 on slow uploads, RootWallet agent locked). |
| nameserver | UPDATE | Registrar steps good; needs choosing the base domain, parent-zone glue under a zone you already host, `--cloudflare-token-file`, and `orama node dns delegation`. |
| video-tutorials | KEEP | |

### Contributor

| Page | Verdict | What is wrong or missing |
|---|---|---|
| architecture | MOVE + REWRITE | Contributor-only and short. The architecture material becomes a top-level section (see 4). The "OramaOS is in development" line is stale in tone: the image builder and agent are in `os/`. |
| dev-setup, code-style, testing, deployment | UPDATE | `testing` and `deployment` do not mention the fleet e2e framework (`e2e/`), coverage gate (`make test`), signed archives. |
| video-tutorials | KEEP | |

### Blockchain

| Page | Verdict | What is wrong or missing |
|---|---|---|
| what-it-is | REWRITE | Governance, NFT, CosmWasm, shielded all stated as absent; they are present. |
| supply | UPDATE | Check emission numbers against `x/emission` defaults (halving-with-tail). |
| fees | UPDATE | Add the fee ante decorator order, fee grants, state deposits as implemented. |
| validators | UPDATE | Add staking, delegation, unbonding, `orama global validator` key migration. |
| running | REWRITE | Predates `orama global install`; describes the stagenet script only. |

`build-llms.mjs` copies these five files by name into `dist/llms/`. The names are kept
(or the script updated in the same commit).

## 3. Topics the docs must cover that have no page

Each row names the page that will carry it and the source it is written from.

| Topic | New page | Source of truth |
|---|---|---|
| What Orama is, who it is for, what is and is not built | `start/what-is-orama` | `README.md`, `docs/whitepaper/`, `docs/CLIENT_SURFACE.md` |
| The three journeys and where to start | `start/journeys` | this audit |
| Glossary | `start/glossary` | all |
| Architecture overview, planes, binaries, ports | `architecture/overview` | `docs/ARCHITECTURE.md`, `core/cmd/*` |
| Node process model and boot | `architecture/node-process-model` | `ARCHITECTURE.md` "Node process model" |
| Request lifecycle and middleware stack | `architecture/request-lifecycle` | `ARCHITECTURE.md` "Data Flow", `core/pkg/gateway` |
| Namespaces: what one is, its cluster, lifecycle | `developer/namespaces` | `DEPLOYMENT_GUIDE.md`, `core/pkg/namespace`, `orama namespace` |
| Sign-in, wallets, lobby, sign-in policy, open sign-in | `developer/sign-in` | `docs/AUTH.md` |
| Devices and device-bound sessions | `developer/devices` | `AUTH.md` "Devices", `sdk/src/auth/device.ts` |
| Roles, members, grants, scoped API keys | `developer/access-control` | `AUTH.md` "Roles", "Keys", `orama members`, `orama namespace keys` |
| Audit trail | in `access-control` | `orama audit`, `AUTH.md` "The record" |
| Push notifications | `developer/push` | `docs/PUSH_NOTIFICATIONS.md` |
| Relayed fetch | `privacy/relayed-fetch` and in `storage` | `SECURITY.md` "Relayed fetch", `sdk/src/storage/relay-*.ts`, `AUTH.md` "Fetch capabilities" |
| Backups and restore of a namespace | `developer/backups` | `RUN_YOUR_OWN_CLUSTER.md` "A sealed backup" |
| Go client SDK | `developer/go-sdk` | `docs/GO_CLIENT_SDK.md` |
| Gateway HTTP API surface | `developer/api-surface` | `docs/API_SURFACE.md` |
| Security model, threat model, what is not defended | `architecture/security-model` | `docs/SECURITY.md`, `docs/AUTH.md` |
| Trust boundaries and secrets | `architecture/secrets-and-keys` | `SECURITY.md`, `AUTH.md` "Which key signed a token", "Between nodes" |
| TLS and the shared certificate store | `operator/tls-certificates` | `DEVNET_INSTALL.md`, `ARCHITECTURE.md#tlshttps`, `core/pkg/tlsstore` |
| Prerequisites, from-scratch install, verify, recover | `operator/install-from-scratch` | `RUN_YOUR_OWN_CLUSTER.md`, `DEVNET_INSTALL.md`, `NAMESERVER_SETUP.md`, `core/pkg/install` |
| One-VPS eval | `operator/one-vps` | `docs/EVAL.md` |
| Building and signed archives | `operator/build-and-sign` | `DEV_DEPLOY.md` |
| Joining nodes, invites | `operator/joining-nodes` | `DEVNET_INSTALL.md` appendix, `core/pkg/invite` |
| Node replacement | `operator/node-replacement` | `docs/NODE_REPLACEMENT.md` |
| Wiping a node | `operator/clean-node` | `docs/CLEAN_NODE.md` |
| Cluster settings and operators | `operator/cluster-admin` | `orama cluster`, `orama operator` |
| Inspector | `operator/inspector` | `docs/INSPECTOR.md` |
| Global nodes | `operator/global-nodes` | `docs/RUN_A_GLOBAL_NODE.md` |
| Sandbox clusters | `contributor/sandbox` | `docs/SANDBOX.md` |
| Stealth TURN | `privacy/stealth-turn` | `docs/STEALTH_TURN.md` |
| Tor client and anonymity proxy | `privacy/tor` | `ARCHITECTURE.md` section 6, `core/pkg/anonproxy`, `core/pkg/tornet` |
| VPN | `privacy/vpn` | `chain/x/vpnlaunch`; status stated plainly (see 5) |
| Chain: modules, tokenomics, governance, storage deals, relay rewards, token factory, CNFT/market, shielded, contracts, explorer, running oramad | `blockchain/*` (14 pages) | `docs/CHAIN.md`, `chain/x/*` |
| Vulnerability disclosure | `architecture/disclosure` | `docs/BOUNTY.md` |
| RootWallet: what it is, install, wallets, vault, agent, sign-in with Orama, security, CLI, mobile vs desktop | `rootwallet/*` (9 pages) | `/Users/pen/dev/debros/rootwallet` (read only) |
| Full CLI reference | `developer/cli/*` generated | `docs/CLI_REFERENCE.md` |

## 4. Proposed information architecture

The sidebar's persona switch becomes a section switch with eight sections. Pages within
a section are grouped under small headings (a `group` field on each link).

1. **Start here** (`start/`): what is Orama, the three journeys, glossary, what is built and
   what is planned.
2. **Developers** (`developer/`):
   - Get started: quick start, namespaces, sign-in, devices, access control and API keys
   - Build: deployments, domains, databases, cache, storage, pubsub, functions, vault,
     push, WebRTC
   - Operate your app: backups and restore, audit trail
   - Reference: SDK (TypeScript), Go SDK, gateway API surface, CLI (generated, split by command family)
3. **Operators** (`operator/`), written as the "install everything from nothing" journey:
   - Plan: what you need, one-VPS eval or a real cluster, DNS and domain
   - Install: build and sign, genesis node, delegation, TLS, join nodes, verify, first app
   - Run: node management, monitoring, upgrades, backups, cluster admin
   - Fix: troubleshooting, node replacement, clean node, inspector
   - Platforms: OramaOS, global nodes, WireGuard, security hardening
4. **Architecture and security** (`architecture/`): overview, process model, request
   lifecycle, security model, secrets and keys, disclosure.
5. **Blockchain** (`blockchain/`): what the chain is, modules, ORAMA supply and emission,
   fees, validators and staking, governance houses, storage deals, relay rewards, token
   factory, CNFTs and market, shielded, contracts, explorer, running a chain node.
6. **Privacy network** (`privacy/`): overview of what is hidden from whom, Tor client and
   anonymity proxy, relayed fetch, stealth TURN, VPN (status).
7. **RootWallet** (`rootwallet/`): overview, install (desktop, mobile, CLI), wallets and
   chains, vault, the agent and how Orama uses it, signing in to Orama, security, CLI.
8. **Contributors** (`contributor/`): dev setup, code style, testing, deployment, sandbox.

Site plumbing changed with it: `Persona` type, `docs-navigation.ts`, sidebar,
search dialog labels, the search-index persona detection in `vite.config.ts`, and
`build-llms.mjs`.

## 5. What is not built (and the pages must say so)

Confirmed against the tree on this branch:

- A hosted dashboard and an Orama MCP do not exist (`docs/CLIENT_SURFACE.md`).
- The chain has no `x/gov` (changes go through `x/houses` or a hard fork), no IBC, no EVM.
- OramaOS disk encryption for Ubuntu nodes is a design only (`docs/DISK_ENCRYPTION.md`).
- Windows desktop RootWallet does not exist (README of the RootWallet repo).
- Items under "Not built here" in `CHAIN.md` and "Not built yet" in `RUN_A_GLOBAL_NODE.md`.
- The `plans/open-network/` tree on this branch holds only `decisions/C0-spikes.md`; the
  rest of the Open Network plan lives in other worktrees. Nothing from it is documented as shipped
  unless it is in the code on this branch.

## 6. Code and engineering-doc discrepancies noticed (not fixed in code)

- `orama env` help text says "Available default environments: production, devnet,
  testnet", but a fresh install has none (`noEnvironmentHelp` in
  `core/cmd/orama/internal/environment.go`). `docs/CLI_REFERENCE.md` copies the stale
  sentence because it is generated from the help text. The website pages say what the
  code does.

## 7. Method for Phase 2

- The CLI reference is generated by `website/scripts/build-cli-docs.mjs` from
  `docs/CLI_REFERENCE.md` into `website/src/docs/developer/cli/*.mdx`.
- Every prose page is written from the code and engineering doc named above. Commands
  and flags are checked against `docs/CLI_REFERENCE.md`.
- Mermaid for flows and sequences (the renderer supports it); text blocks for layouts.
- Hosts in examples use `example.com`, `203.0.113.x` (documentation range) and
  `<node-ip>` placeholders. No production IPs, secrets or credentials.
