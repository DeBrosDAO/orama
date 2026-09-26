# Development Guide

## Prerequisites

- Go 1.26.7+ (see `go.mod`)
- [Zig](https://ziglang.org/download/) — `orama build` cross-compiles the vault
  with it, and the gateway with cgo through `zig cc` (static musl): the gateway
  links `mattn/go-sqlite3` for namespace SQLite databases, which does not work
  in a `CGO_ENABLED=0` build
- Node.js 18+ (for the TypeScript SDK in `sdk/`)
- macOS or Linux
- **The RootWallet desktop app, open and unlocked** — every command in the
  "Deploying to VPS" section below needs it

### RootWallet

There are no SSH keys on disk. Every command that reaches a node — `orama push`,
`orama rollout`, `orama node setup`, `orama monitor report`, `orama ssh` — asks
the RootWallet desktop app's agent for a wallet-derived key over a Unix socket
at `~/.rootwallet/agent.sock`, writes it to a `0600` temp file for the length of
the command, and wipes it afterwards. `RW_AGENT_SOCK` overrides the path.

**Before a rollout: open the app and unlock it.** Then expect this:

- **First run after a rebuild, one approval prompt.** Approval is keyed on the
  hash of the calling binary, so every `make build` produces a new `orama` that
  RootWallet has not seen before and asks about once.
- **One unlock for the whole run.** The agent locks itself after 30 minutes of
  no traffic, and a six-node rolling upgrade spends far longer than that in SSH
  sessions the agent never sees. The CLI touches the agent every five minutes
  for as long as it holds keys, so the window stays open until the command
  finishes and closes immediately after.
- **A command that seems to hang is usually a prompt.** Look at the desktop app.
  A first run against a locked wallet waits up to two minutes for approval and
  two more for the unlock before it gives up.

If a command fails with a RootWallet error, the message says what to do; the
codes are listed in
[Troubleshooting](COMMON_PROBLEMS.md#13-rootwallet-agent-locked-waiting-or-unreachable).

Every command and flag the CLI defines is in the
[CLI reference](CLI_REFERENCE.md), which is generated from the command tree and
checked by a test, so it cannot drift from the code. This page covers the
workflows.

## Building

```bash
# Build all binaries
make build

# Outputs:
#   bin/orama-node        — the node binary
#   bin/orama             — the CLI
#   bin/gateway           — standalone gateway (optional)
#   bin/identity          — identity tool
#   bin/sfu               — WebRTC SFU
#   bin/turn              — TURN server
#   bin/orama-sni-router  — SNI router
```

## Running Tests

```bash
make test
```

### Lifecycle harness

`make test` and `make test-e2e` never reboot a node, kill a voter, join one, or
upgrade one — `e2e/cluster` tests read-consistency levels and `e2e/production`
stops a deployment process. That is why the change-287 stability audit's
findings all had to be established by reading code: nothing could observe them.

`e2e/lifecycle` closes that gap. It drives a real 3-node cluster **only through
the `orama` CLI** and observes it **only through `orama monitor report --json`
and `dig`** — a harness that reaches around the CLI would test a path no
operator runs, and could pass while the CLI reported something different.

```bash
ORAMA_LIFECYCLE_ENV=<disposable-env> make test-lifecycle
```

**Never point it at testnet or mainnet.** These scenarios reboot nodes and
destroy VMs; the harness refuses those two names outright.

| Scenario | What it proves |
|----------|----------------|
| Reboot one node | Quorum holds; the node comes back with a complete mesh and no crash-loop |
| Reboot all three | DNS and the gateway answer within 90s **before** raft has a leader, then the cluster converges |
| Kill a voter (VM destroyed) | Survivors keep committing; every membership view — raft *and* WireGuard — forgets it |
| Join a fourth node | It becomes a full member of every store, not just raft |
| Decommission a node | Same end state as an abrupt death, reached cleanly |
| Rolling upgrade, one node broken | The rollout **stops**, names the node, and leaves the leader untouched |
| Rolling upgrade, healthy | The leader is the last step in the plan |
| Index rqlite down everywhere | DNS still answers, from the stale cache |

Four things have no CLI equivalent, because they are not things the CLI should
be able to do: destroying a VM abruptly, breaking a node so an upgrade fails on
it, creating a new VM, and (for DNS) naming the zone. Each is a command you
supply — Multipass, Lima, a cloud CLI:

| Variable | Purpose |
|----------|---------|
| `ORAMA_LIFECYCLE_ENV` | The disposable environment (required) |
| `ORAMA_LIFECYCLE_DESTROY` | Destroy a node abruptly; receives the host as `$1` |
| `ORAMA_LIFECYCLE_BREAK` | Make an upgrade fail on a node; receives the host as `$1` |
| `ORAMA_LIFECYCLE_PROVISION` | Create a node; prints its IP on stdout |
| `ORAMA_LIFECYCLE_BASE_DOMAIN` | The zone the DNS scenarios query |
| `ORAMA_LIFECYCLE_RESOLVER` | Resolver for `dig` (defaults to the system resolver) |
| `ORAMA_BIN` | The binary under test (defaults to `./bin/orama`) |

A scenario whose hook is not set **skips** rather than passing — a green run
that silently omitted the kill-a-voter scenario would be worse than no harness.

The convergence predicates (`Converged`, `LeaderAgreement`, `Forgotten`,
`Serving`) are plain Go with no build tag, so `make test` exercises them against
recorded report shapes. That is what stops the harness from going green by
asserting nothing.

## Deploying to VPS

All binaries are pre-compiled locally and shipped as a binary archive. Zero compilation on the VPS.

### Deploy Workflow

```bash
# One-command: build + push + rolling upgrade
orama node rollout --env testnet

# Or step by step:

# 1. Build binary archive (cross-compiles all binaries for linux/amd64) and
#    sign its manifest with your RootWallet (unlock the desktop app first)
orama build
# Creates: /tmp/orama-<version>-linux-amd64.tar.gz

# 2. Push archive to all nodes (fanout via hub node). --archive is required:
#    the newest archive in /tmp may be another checkout's build.
orama node push --env testnet --archive /tmp/orama-<version>-linux-amd64.tar.gz

# 3. Rolling upgrade (one node at a time: followers first, the leader last).
#    Without --yes it prints the plan; each node runs the staged build's CLI.
orama node upgrade --env testnet --yes
```

Upgrading from 0.122.x the first time: see [First upgrade from 0.122.x](#first-upgrade-from-0122x).

### Signed archives

Nodes install only build archives signed by an address they trust. The list of
trusted addresses — the **trust anchor** — is `/etc/orama/archive-signers` on
every node: root:root 0644 in a root-only directory, one lowercase `0x` address
per line. There is no unsigned mode, no source-build mode and no built-in
signer: a cluster trusts its operator's wallet, not a DeBros key.

**Build.** `orama build` signs by default. It asks the RootWallet agent for its
active account before compiling (a locked or absent wallet fails in seconds),
then signs through the agent's `wallet:sign:orama-archive` capability (the
request carries `purpose: "orama-archive"`) — the first time, RootWallet asks
you to approve `orama` for archive signing, separately from `wallet:sign`, and
shows the build's version, commit, date and signer rotation. The message signed (EIP-191
`personal_sign`) is:

```
Orama build archive v1
version: <version>
commit: <commit>
arch: <arch>
date: <build date, RFC 3339>
signers: <the --signers rotation, or none>
manifest sha256: <hex SHA-256 of manifest.json exactly as archived>
```

The fixed first line means a signature your wallet gave any other application
can never pass as a build signature, and the dialog shows a change of who is
trusted before you approve it. A field with a character that is not printable
(controls, line separators, bidirectional overrides) is refused. The build checks the signature with the
same code every node runs before it writes the archive. The manifest lists the
SHA-256 of every file the archive carries (binaries by name, templates as
`systemd/<name>`, packages as `packages/<name>`), so nothing in the archive is
outside the signature. `--unsigned` builds an archive for local inspection
only; no node installs it. `orama node rollout` builds a signed archive the
same way.

**Push.** `orama push` uploads into a fresh `mktemp -d` directory on each node
and runs the node's **installed** CLI, `/usr/local/bin/orama node
stage-archive`. Under a lock on `/opt/orama` that install and upgrade share, it
removes staging (and setup CLI) directories an interrupted run left, extracts the upload into a
private directory (regular files and directories under the archive's own paths
only; at most 1024 entries, 512 MiB per file, 4 GiB in all; modes set
explicitly), verifies the signature against the node's anchor, every file
against the signed manifest and the architecture against the node's, makes
`bin/` and its binaries root:orama 0750, and only then swaps `manifest.json`,
`manifest.sig`, `bin/`, `systemd/` and `packages/` into place — the old
manifest out first and the new one in last, and everything put back if any
step fails. A refused archive leaves `/opt/orama` exactly as it was; this
matters because systemd runs `orama-node`, the gateway, SFU, TURN and vault
straight from `/opt/orama/bin`. Because the verifier is the node's installed
CLI, push reaches only installed nodes; a fresh machine gets its first archive
from `orama node setup` (or `orama node install --remote`).

**First install.** A new machine has no verified binary of its own: the one
that runs the install comes out of the archive. So `orama node setup` and
`orama node install --remote --archive <path>` verify the archive **on your
machine** — against your RootWallet account (setup) or `--operator-wallet`
(`--remote`) — check that it is built for the node's architecture (`uname -m`),
and upload not the file you named but a canonical archive written from the
verified files, into a `mktemp -d` directory. On the node, `/opt/orama` must be root's alone; only the
CLI is extracted first, into a root-only directory under it; it runs only if
its SHA-256 is the one in the verified manifest, and it puts the archive in
place with `node stage-archive` — the verification, lock and crash-safe swap a
push uses (creating the anchor from your wallet on a machine that has none).
Setup is skipped only when the node already has exactly this manifest **and** a
CLI with its checksum.

**Install and upgrade.** Phase 2b holds the `/opt/orama` lock, verifies the
extracted archive against the anchor (architecture before any rotation) and only
then copies anything to `/usr/local/bin` and installs the privileged helper;
namespace templates are installed under the same lock. No archive, an unreadable manifest, a missing `manifest.sig`,
an untrusted signer, a file that does not match or is not listed, another
architecture, or a missing or empty anchor fails the install or upgrade with
the reason. An upgrade runs the same check (without changing anything) before
it stops any service, so an archive Phase 2b would refuse never takes the node
down. A genesis install requires `--operator-wallet` (EIP-55 checked when given in
mixed case) and creates the anchor from it **before** Phase 2b — only after the
archive has verified against it, so a mistyped wallet writes nothing — and drops
any rotation mark an earlier cluster left; an existing anchor must trust exactly
that wallet. The first archive must therefore be signed by that wallet (see
[DEVNET_INSTALL.md](DEVNET_INSTALL.md)). A joining node installs Tor and checks
the archive — present, built for this node, every file matching its signed
manifest — **before** it requests the join, so an archive that cannot be
installed never spends the invite; then it writes the anchor (and rotation mark)
the minting node returns in the join response. That list is served by the
minting node's gateway, which runs as the orama user, so setup passes
`--expect-archive-signers` — the `--join-via` node's anchor read over your SSH,
or else your wallet. The joiner sends that expectation in the join request, and
the minting node refuses a mismatch (HTTP 409, naming the list it trusts)
before it spends the invite or writes a peer row; the joiner also refuses a
response naming anything else. A cluster that trusts more than your wallet is
joined with `--join-via`. A
node without an anchor refuses to admit anyone, before the invite is spent.

**Rotating signers.** `orama build --signers 0xA,0xB` writes that list into the
signed manifest. A node that verifies the archive against its current anchor
rewrites the anchor to exactly that list in Phase 2b (atomically, root-owned).
The list must include the account signing the build — so the archive still
verifies after the rotation, and re-running an interrupted upgrade, or a join
through a node that already rotated, installs it again. Retiring a key
therefore takes two builds: the old key signs one that adds the new key
(`--signers 0xOld,0xNew`), then the new key signs one that drops the old
(`--signers 0xNew`). Each node records the build date of the last rotation it
took (`/etc/orama/archive-signers.rotated`, written before the anchor and copied
to joining nodes) and accepts a rotation only from a build at least that new —
the same date resumes an interrupted rotation of that build — and never moves
the mark to a date more than an hour ahead of its clock (nor takes such a mark
from a join), so an older signed build cannot be
replayed to bring a retired key back. A list of at most 32 signers. Nodes that miss a rotating build still
trust the old list, so roll it out everywhere before the next one. Signature
checks prove who built an archive, not that it is the newest: rolling a node
back to an older build signed by a trusted key is allowed.

**Nodes installed before archive signing** have no anchor, and a plain push
refuses them until they get one. Create it with the push that carries the first
signed build:

```bash
orama push --env testnet --archive <path> --trust-signers 0xYourWallet
```

A push with `--trust-signers` does not use the node's installed CLI at all, so
it also reaches 0.122.x nodes, whose CLI has no `node stage-archive`. It stages
the way `orama node install --remote` does: the archive is verified **on your
machine** against `--trust-signers`, and what is uploaded is a canonical archive
written from the verified files; on each node only `bin/orama` is extracted —
into a root-only `mktemp -d` directory under `/opt/orama`, which must be root's
and writable only by root — and it runs only if its SHA-256 is the one the
verified manifest lists. That CLI then runs `node stage-archive --trust-signers
…`, which verifies the archive again: against the node's anchor when it has one
(which must then be exactly `--trust-signers`), and otherwise against
`--trust-signers`, writing the anchor only after the archive passed. A mistyped
address fails that push and leaves nothing behind. A new anchor never inherits
a rotation mark left beside a removed one. `--trust-signers` never changes an
existing anchor (that is what `--signers` is for). The script reaches the node
base64-encoded on a pipe into `bash -s`, so it survives the fanout's `ssh '…'`.

#### First upgrade from 0.122.x

The whole hop runs this release's code, from this checkout:

```bash
orama build                                                   # signed with your RootWallet
orama push --env testnet --archive <path> --trust-signers 0xYourWallet
orama node upgrade --env testnet                              # read the plan
orama node upgrade --env testnet --yes
```

1. `orama push --trust-signers` puts the verified archive in `/opt/orama` on
   every node, and creates each node's anchor, as above. The nodes keep running
   0.122.x; nothing is stopped.
2. `orama node upgrade --env … --yes` reads every node's raft state — a 0.122.x
   `node.yaml` has no rqlite credentials and its index rqlite runs without
   `-auth`, which the probe reads from `node.yaml` (no auth file and no
   credentials at all) — and runs `/opt/orama/bin/orama node upgrade --restart`
   on each node in turn: the CLI of the staged build, not the node's
   `/usr/local/bin/orama`. Before running it the node refuses — naming `orama
   push … --trust-signers` — unless it has a trust anchor (a node that was
   never pushed a signed build has none, and its `/opt/orama/bin/orama` is its
   old release's) and `/opt/orama`, `/opt/orama/bin` and the CLI are root's,
   writable by nobody else and not symlinks; the check fails closed when `find`
   cannot inspect a path. So every step on the node is this release's code —
   the checks, the raft identity capture, the leadership hand-over and the stop
   included — and the post-swap re-exec has nothing to hand over to (the
   running binary already is the installed one).
3. The same happens on every later upgrade: `orama push` stages the build,
   the rolling upgrade runs its CLI.

What the 0.122.x CLI cannot do is why it is not used for this hop: its build
path (`./cmd/cli`) no longer exists, its `orama node upgrade --env` has no plan,
no `--yes`, no leadership hand-over and a fixed sleep between nodes, its archive
check refuses a manifest signed under this scheme, and its stop step wrote a
recovery `peers.json` that this release's index rqlite would act on.

On that first hop, before anything is stopped, each node also records its raft
identity (see [Upgrading a Multi-Node Cluster](#upgrading-a-multi-node-cluster-critical)),
and the tenant namespaces' leadership is not handed over: their 0.122.x
rqlites run with the credentials in `secrets/rqlite-auth.json`, which a 0.122.x
`node.yaml` does not name, so each is reported as not addressable and keeps its
leader until the node restarts (an election per namespace, not a lost quorum).

New joins need the minting node to have an anchor, so give the cluster its
anchors before adding nodes.

### Fresh Node Install

```bash
# Build the archive first (if not already built)
orama build

# Install on a new VPS: verifies the archive here, uploads it, installs it
orama node install --remote --vps-ip <ip> --archive /tmp/orama-<version>-linux-amd64.tar.gz \
  --operator-wallet 0xYourWallet --nameserver --domain <domain> --base-domain <domain>
```

The installer installs from the build archive extracted at `/opt/orama` (`manifest.json` beside it) and nothing else; there is no compile-on-the-node mode. The archive must verify first — see [Signed archives](#signed-archives).

**Install verifies the node before it reports success.** The final phase waits for, in dependency order:

1. `orama-node.service` active **and not crash-looping** (`NRestarts` is 0 — an active unit systemd is about to restart for the fifth time is the failure this catches)
2. rqlite in raft state `Leader` or `Follower` — not merely answering on `:10100`, which it does while still Candidate or still replaying its log
3. `wg0` up with an interface
4. the gateway's `/health` returning 200

The first component that does not come up is named, install exits **non-zero**, and no `✅` is printed. Before change-287 install printed `✅ Production installation complete!` unconditionally — after a partial template install, after a failed DNS seed, after a supervisor that started and exited — and the operator, the CLI and the next node's join all proceeded on the assumption that the node was up.

Two related orderings changed in the same commit:

- **Namespace systemd templates install before the services that use them.** Phase 5 starts `orama-node`, whose first act is to start `orama-namespace-wireguard@index`; with no template installed systemd answers `Unit ... not found` and the supervisor exits. Install used to depend on systemd's restart loop to converge past that. Any missing or unwritable template is now fatal and the error names it.
- **Install and upgrade seed no DNS records.** They used to write `ns1`..`ns3` NS records, an `ns1` SOA and apex/wildcard A records on every run, whatever slots the cluster had. `orama-node`'s DNS component owns the zone: each `--nameserver` node claims an `nsN` slot and writes its glue and its apex/wildcard A records, and the NS set and SOA follow the glued slots, on the sweep 30 seconds after it starts (see [NAMESERVER_SETUP.md](NAMESERVER_SETUP.md)).

### What runs on a node

The installer enables **only** `orama-node`. That unit is the supervisor: it starts `orama-namespace-*@index` (WireGuard, IPFS, rqlite, olric, pubsub, gateway, vault, Caddy, …) and, on `--nameserver` nodes, `orama-namespace-coredns@nameserver`. Tenant clusters are `orama-namespace-{rqlite,olric,gateway}@<name>`.

Use `orama node …` (start/stop/restart/upgrade). Install writes one host unit, `orama-node.service`, which logs to the journal (`orama node logs node`). The per-daemon host units older installs wrote — `orama-ipfs`, `orama-ipfs-gc`, `orama-ipfs-cluster`, `orama-olric`, `orama-vault`, `caddy`, `coredns`, `ntfy` and `orama-sni-router` — are stopped, disabled and deleted by Phase 5 of every install and upgrade; a node that has not been upgraded yet may still have them, disabled. Upgrade and restart used to start them again (they were listed in `GetProductionServices` and the restart priority order), so they raced `@index` for 10102, 10107, `:53` and `:443` until `IndexSupervisor` stopped them on its next start. `systemd.IsLeftoverHostUnit` keeps them out of both lists. Index RQLite data stays at `~/.orama/data/rqlite`. Internals are `10100–10109`, plus the IPFS Cluster swarm on `10114` bound to the node's WireGuard address; do not mix a voter still on 5001 with one on 10100.

The firewall reconcile (Phase 6b) adds and removes only rules tagged `comment orama`, plus one exception: it deletes the exact untagged rules that releases before the tag added and no longer want (`core/pkg/install/firewall_legacy.go`: 9001/tcp, 443/udp, `from 10.0.0.0/8`, the per-namespace TURN relay blocks, and the 2025 setup's Olric/IPFS/Cluster rules with their original comments). A rule on the SSH port is never removed. To keep one of those ports open on purpose, add the rule with a comment of your own.

Every node also runs a client-only **Tor** daemon, `orama-namespace-tor@index`, whose SOCKS port `127.0.0.1:9050` serves `/v1/proxy/anon`, `/v1/proxy/tunnel` and the `anon_fetch` host function. Install and upgrade set it up in **Phase 2d**: remove the Anyone network if present, mask the package's own `tor.service`/`tor@default.service`, add the Tor Project's apt repository if it is not already in place for this OS release (`deb.torproject.org`, key pinned by fingerprint), `apt-get update` and `apt-get install tor deb.torproject.org-keyring` — which installs Tor or upgrades it to the repository's current release — and write `/etc/orama/tor/torrc`. apt is run with `DPkg::Lock::Timeout=300`, so installs and purges wait up to 300 s for a dpkg lock held by unattended-upgrades. Any error fails the install/upgrade.

**Tor is upgraded on every Orama upgrade, before the node's services stop.** `orama node upgrade` runs Phase 2d right after Phase 2, while the node still serves; it needs outbound HTTPS to `deb.torproject.org`, and a failure aborts the upgrade with nothing stopped. The Orama unit keeps running the old Tor binary until the upgrade's stop step; the new one starts when `orama-node` brings `@index` back up. After the post-swap re-exec, Phase 2d runs again under the new binary in "ensure" form: with Tor installed and its repository current it only re-masks the distro units and rewrites the torrc, touching no network. Between Orama upgrades nothing updates Tor — default unattended-upgrades takes only the distribution's own origins. The OS must be one the Tor Project publishes packages for (`jammy`, `noble`, `resolute`, `bookworm`, `trixie`); on any other codename Phase 2d stops with an error naming them, before fetching anything. `IsSupportedOS` lists exactly those releases — Ubuntu 22.04/24.04/26.04 and Debian 12/13 — and Phase 1 of `orama node install` refuses any other release before touching the machine. Interim Ubuntu releases (24.10, 25.04, 25.10) are refused: they are past end of life and have no Tor Project suite; move such a VPS to 26.04 with `do-release-upgrade`, one release at a time.

#### Tor replaces Anyone (first upgrade to this release)

The first upgrade of a node that ran the Anyone network removes it for good: `orama-namespace-anyone-client@index`, `orama-anyone-client`, `orama-anyone-relay` and `anon.service` are stopped and disabled (unmasked first if `orama node stop` masked them), the `anon` package and `nyx` (installed only for the anon control port) are purged, and its apt source and key, `/etc/anon`, `/var/lib/anon` (relay keys included), `/var/log/anon` and the Orama-written unit/env/log files are deleted. The rolling upgrade runs the staged build's CLI (see [First upgrade from 0.122.x](#first-upgrade-from-0122x)), so this is the pre-stop Phase 2d: a failure to reach `deb.torproject.org` aborts the upgrade with the node still serving. Only an upgrade started by hand with a 0.122.x CLI runs Phase 2b under the old binary, which knows nothing of Tor and installs Anyone again; the post-swap Phase 2d under the new binary then removes it with the node's services stopped. Such an old binary may re-exec the new one with `--anyone-client` on its command line; the new `orama node upgrade` accepts that flag only together with the hidden re-exec marker and refuses it from an operator. Every step checks before it acts, so re-running the upgrade, or upgrading a node that never had Anyone, does nothing extra. Nothing opens a firewall port for Tor: a client needs no inbound port. Afterwards `orama monitor report` and `orama inspect --subsystem tor` flag any node where Anyone leftovers remain.

#### Privileged helper replaces the sudoers rules (first upgrade to this release)

`orama-node` runs as the `orama` user; its root actions (Orama's own systemd units, the TURN firewall rules, WireGuard peer persistence) go through the privileged helper — see [SECURITY.md](SECURITY.md). There is no sudo or sudoers involved: `orama-privhelper.socket` listens on `/run/orama-privhelper.sock` (root:orama, 0660, `Accept=yes`), each connection runs one `orama-privhelper@.service` instance as root, and callers use `orama-privhelper call …`. sudo could not work anyway: systemd implies `no_new_privs` for `orama-node`'s sandboxing.

Phase 2b installs the helper under the archive lock, and the post-swap step (`EnsurePrivHelper`, before Phase 4b and before `orama-node` restarts) ensures it again — which is what installs it when Phase 2b was a 0.122.x binary's, on an upgrade started by hand with the old CLI. It copies `bin/orama-privhelper` to `/usr/local/bin/orama-privhelper` (root, 0755, verified byte-identical), writes both units to `/etc/systemd/system`, enables and restarts the socket, and deletes the old wildcard rules in `/etc/sudoers.d/orama-namespaces`. Every step is fatal: a release archive without `bin/orama-privhelper` fails the upgrade at that step with nothing restarted.

#### `orama-node` moves the old layout (first start on this release)

Every gateway runs as `orama-namespace-gateway@<ns>` (`User=orama`, `ProtectSystem=strict`), to which `secrets/` and `configs/` are read-only, so it writes only under `data/`: its own keys and encryption-root cache in `data/namespaces/<ns>/gateway/`, tenant SQLite in `data/sqlite/`, deployments in `data/deployments/`, and the host TURN config in `data/turn/turn.yaml`. Namespace units read their env files from the root-owned `/var/lib/orama-unit-env/<ns>/<svc>.env`, and deployments read their environment and workload token from the root-only `/var/lib/orama-deploy/` — both written only through `orama-privhelper` (see [SECURITY.md](SECURITY.md)). A 0.122.x node holds all of this where the old code wrote it.

`orama-node` moves it itself, as the `orama` user, when it first starts on this release: the `legacy-layout` boot component (`pkg/legacylayout`) runs right after `data-dir`, and every other component — WireGuard, libp2p, storage, rqlite, the gateway, every namespace service — waits for it. Root takes no part: every path is in the tree the `orama` user owns, and a root process walking it would follow whatever symlinks that user planted.

| Old path (under `/opt/orama/.orama`) | Now | How |
|---|---|---|
| `secrets/jwt-signing-key.pem`, `secrets/jwt-eddsa-key.pem` | `data/namespaces/index/gateway/` (same names) | renamed; copied if `secrets/` is not writable by `orama-node` (below) |
| `sqlite/` | `data/sqlite/` | renamed |
| `deployments/<ns>/<name>/` | `data/deployments/<ns>-<name>/` (dots in either become `-`, as `process.InstanceName`) | the owner marker `.orama-owner` (`{"namespace":…,"name":…}`, the host-level claim every gateway checks) is written into it, replacing one an archive carried, then renamed; the empty old tree is removed |
| inline `Environment="…"` lines of `/etc/systemd/system/orama-deploy-<ns>-<name>.service` | `/var/lib/orama-deploy/orama-deploy-<ns>-<name>.env` | read (the unit is world-readable, opened with `O_NOFOLLOW`), stripped of the platform's variables (`PORT`, `ORAMA_*`, the entry point) and of `ENTRY_POINT` — 0.122.x wrote them from a tenant-writable row, and the gateway sets them when it starts the deployment — rendered as an env file and handed to `orama-privhelper deploy set-env`, before that deployment's directory moves |
| `configs/turn.yaml` | `data/turn/turn.yaml` | renamed |
| `data/namespaces/<ns>/<svc>.env` | `/var/lib/orama-unit-env/<ns>/<svc>.env` | read with `O_NOFOLLOW`, handed to `orama-privhelper unitenv set`, then deleted |
| `deployment-env/orama-deploy-<instance>.env`, `.token` | `/var/lib/orama-deploy/orama-deploy-<instance>.env`, `.token` | read with `O_NOFOLLOW`, handed to `orama-privhelper deploy set-env` / `set-token`, then deleted; the empty directory is removed |

For each path: only the old one exists → moved or staged; neither, or only the new one → nothing to do; **both** → the component fails naming both paths and the node starts nothing else, so it stays down and the rollout's readiness gate stops at it. Keep the one the node should use and move the other out of both places; the boot supervisor retries the component, so nothing needs restarting. Every path is checked before anything changes, so a refusal leaves the node as it was, and a node already moved does nothing. An env file already in `/var/lib/orama-unit-env` with **identical** contents is what an interrupted run leaves, so the old copy is just deleted; different contents are a refusal. Names are checked as `orama-privhelper` checks them (namespace and service for env files, `orama-deploy-<instance>.env|.token` for deployment files), and a symlink, or a file the helper would refuse, fails the component. Only the env files of units that read one today (their template names `/var/lib/orama-unit-env/%i/<svc>.env`) are staged; those of units that must not get one — `wireguard`, `tor` and `ntfy`, which run as root or as their own users and for which the helper refuses an env file; `anyone-client`, replaced by Tor; and `turn`, the per-namespace TURN server the shared `orama-turn.service` replaced, whose env file in the new tree would make the node start and enable `orama-namespace-turn@<ns>` against the shared server's 3478 — are deleted, and one for any other service fails the component. `deployment-env/` exists only on nodes that ran a pre-release build of this line — 0.122.x wrote each deployment's environment inline into its unit in `/etc/systemd/system` and has no such directory. A 0.122.x deployment's directory must be `deployments/<ns>/<name>/`, two directories whose names map to a valid unit instance; a file or symlink in that tree, or two deployments mapping to the same instance, fails the component before anything moves. Its environment is staged in the same pass that moves its directory, and never again — staged later it would overwrite the one the gateway writes when it starts the deployment. 0.122.x had no workload tokens, so none is staged.

The legacy per-deployment units themselves are root's: the upgrade stops them with everything else before the swap, and once `orama-node` is back and healthy its last step stops, disables and deletes every `/etc/systemd/system/orama-deploy-<instance>.service` (the legacy name: no `@`, an instance of the characters `orama-privhelper` accepts) and reloads systemd. A regular file or a mask (a symlink to `/dev/null`) is removed; any other symlink under such a name fails the step. Left in place, such a unit keeps the tenant's secrets in a world-readable file, holds the deployment's port, and fails on its next start because its working directory moved. A 0.122.x deployment is therefore **stopped by the upgrade** and stays stopped until it is deployed again: the template unit that replaces it needs a workload token, which only the gateway mints when it starts a deployment (see [COMMON_PROBLEMS.md](COMMON_PROBLEMS.md) §17). `/var/lib/orama-deploy` is root-only, so the node cannot compare what is already there: every file in `deployment-env/` is staged. Nothing writes there before the move — gateways start after it — so what is staged is what the deployment last ran with. A helper that refuses leaves the old file in place for the next attempt.

The signing keys are the one thing the node may be unable to rename: install writes `secrets/` as root. If `orama-node` cannot write that directory (it is root's, or mounted read-only), it copies each key, recording its SHA-256 in `data/namespaces/index/gateway/legacy-signing-keys.copied` before making the copy. The upgrade step `RemoveCopiedSigningKeys` — root, after the privileged helper and before the restart — then deletes an original in `secrets/` only when its contents match that record **and** the copy exists; it opens `secrets/` and each key without following symlinks and touches those two names only. On the upgrade that crosses the layouts the node has not run yet, so the originals are removed by the next upgrade; the node clears the record once they are gone. **Rolling back** to 0.122.x after a node has moved is not a supported path: the old binary looks for everything at the old paths, so its gateway starts with no signing keys (it generates new ones, which invalidates every token issued before) and no tenant databases, and upgrading again then stops on every path both layouts hold until an operator decides which copy to keep. The copied EdDSA key is the cluster-derived key 0.122.x signed with; the index gateway replaces it with a key of its own on first boot. Tenant gateways generate their own keys in their own state directories; a tenant gateway still on a YAML without `state_dir` refuses to start until the index gateway's restore rewrites its config.

Until the node has restarted on this release, the upgrade reads the old layout where it needs node state:

- **Phase 6b (firewall)** keeps the TURN relay range open when the node relays TURN, judged from files because the TURN units are stopped by then: `data/turn/turn.yaml`, and on a node not yet moved `configs/turn.yaml` or `data/namespaces/<ns>/turn.env`. A location it cannot read fails the upgrade rather than closing the range.
- **pre-/post-upgrade** read the namespace rqlite env files from `/var/lib/orama-unit-env` once it exists, and from `data/namespaces/<ns>/rqlite.env` before it does. A tenant namespace the node serves (`data/namespaces/<ns>/cluster-state.json`) with rqlite data on it (`data/namespaces/<ns>/rqlite/`) but no `rqlite.env` in the tree read fails the step: its leadership cannot be handed over. A directory without `cluster-state.json` is a leftover that runs nothing and is ignored.

#### `node.public_ip` is recorded on upgrade

`orama node invite` builds the join URL from `node.public_ip` in `node.yaml`, which only `orama node install` (`--vps-ip`) used to write. Phase 4 of every upgrade records it: `--public-ip` if given, else the address `node.yaml` already records, else the source address of the default route — the address `orama-node` registers for itself in `dns_nodes`. Whichever it is must be a public IPv4 address — not private, carrier-grade NAT (`100.64.0.0/10`), loopback or link-local; the value read back from `node.yaml` is checked again, and so is the one `orama node invite` reads. It is resolved **before anything is stopped**, so a node that has none fails the upgrade still serving; a node behind NAT has a private route source address and must be told, with `/opt/orama/bin/orama node upgrade --restart --public-ip <ip>` on it. The value resolved there is the one Phase 4 records (after a re-exec it is handed to the new process as `--public-ip`). The rolling upgrade does not pass `--public-ip`: it is per node.

#### Local control planes move off loopback TCP (first upgrade to this release)

Each node's upgrade changes these together, so nothing on the node talks across a version boundary:

- **Caddy** gets `admin unix//run/orama-caddy/admin.sock|0600` and its DNS provider a `key_file` (`/etc/caddy/orama-acme.key`, written by Phase 4). The Caddy binary and the Caddyfile come from the same archive; an old Caddy cannot parse `key_file`, and a new gateway refuses an unsigned DNS-01 call.
- **IPFS Cluster** requires basic auth on its REST API from the `service.json` Phase 2c rewrites; the gateway, `orama-node` and `orama node report` derive the same password from the cluster secret.
- **pubsub** serves on `/run/orama-pubsub/pubsub.sock`; the gateways reach it there. `PUBSUB_LISTEN` in its env file is no longer read.
- **The index gateway** binds the node's WireGuard address and `127.0.0.1`, not every interface. A gateway YAML from before (`listen_addr: ":10104"`) is refused, so the gateway restarts until `orama-node` rewrites the YAML on its first reconcile — a few seconds of 502 from Caddy on that node.
- **orama-privhelper** serves only `orama-node` and the cluster gateway, by unit. A tenant gateway's request is refused.
- **Mixed window:** a node on this release refuses an older node's unsigned `/v1/network/status` (IPFS Cluster peer discovery on the old node then finds nothing until it is upgraded; nothing is removed), and answers an upgraded node's signed one. `/v1/health` and `/v1/status` no longer carry namespaces or peers; an operator reads `/v1/operator/health`.

### Upgrading a Multi-Node Cluster (CRITICAL)

**NEVER restart all nodes simultaneously.** Index RQLite uses Raft consensus and requires a majority (quorum) to function. Never restart multiple RQLite voters in the same step.

#### Safe Upgrade Procedure

```bash
# Full rollout (build + push + rolling upgrade, one command; pushes exactly
# the archive it built)
orama node rollout --env testnet
orama node rollout --env testnet --no-build --archive <path>   # an existing build

# Or with more control:
orama node push --env testnet --archive <path>    # Push archive to all nodes
orama node upgrade --env testnet                  # Print the rolling upgrade plan
orama node upgrade --env testnet --node 1.2.3.4   # Single node only
orama node upgrade --env testnet --yes            # Execute the plan
orama node upgrade --env testnet --delay 600      # Allow 10 min per node to rejoin
```

What the rolling upgrade does:

1. **Reads every node's raft state** over SSH before touching anything.
2. **Builds and prints a plan**: followers first, the leader last, nameservers
   spaced apart so the zone always has one answering. The order is deterministic,
   so the plan you approve is the plan that runs.
3. **Stops before starting** if the state does not permit a rollout — no node
   reports itself leader (no quorum), two do (mid-election), or any node's state
   could not be read. An unreachable node is never assumed to be a healthy
   follower.
4. **Requires `--yes`.** Without it the plan is printed and nothing is restarted.
5. **Runs the staged build's CLI on each node**: `/opt/orama/bin/orama node
   upgrade --restart`, which `orama push` verified and put in place — never the
   node's `/usr/local/bin/orama`, the release being replaced — so every step
   below is the new release's code.
6. **Hands over before it stops anything.** On each node, while it still
   serves: record the raft identity — the id the index rqlited runs under, the
   address its configuration holds it at and the members' addresses, read from
   its `/status` into `raft-node-id`, `raft-adv-addr` and
   `data/cluster-membership.json` (a node not in its own configuration, or whose
   recorded id is not the one rqlited runs under, is not upgraded); then the
   quorum check, the maintenance flag, leadership transfer on the index and each
   namespace rqlite, and confirmation that another voter leads. Only then are the
   services stopped. This used to run in the restart step, after the stop: the
   leader was stopped without stepping down, and the quorum check, reading a
   half-upgraded node, refused. A refusal leaves the node serving. If the index
   rqlite is not running at all (an upgrade re-run after the first attempt
   stopped the node), there is no leadership to hand over and only the quorum
   check and the flag remain.
7. **Stops** `orama-node` first (the supervisor, so it cannot start again what is
   being stopped), then every namespace unit systemd has loaded — in any state,
   so a unit between two crash-loop restarts is not missed — then 0.122.x's
   per-deployment units (`orama-deploy-<instance>.service`), then the host
   daemons older installs wrote (`installers.LegacyHostUnits`). Every stop is
   fatal. Nothing is written into the raft directory: the stop used to write a
   recovery `peers.json` from `/nodes`, taking each member's raft id for its
   address and leaving the non-voters out, and every upgraded node restarted
   into a configuration of addresses that do not exist.
8. **Gates on each node actually rejoining** before touching the next: raft state
   `Leader` or `Follower`, a leader known to exist, an applied index caught up to
   the leader's commit index, and a gateway serving `/health`.
9. **Stops the rollout** the moment a node fails that gate, leaving the remaining
   voters untouched and the cluster serving. The maintenance flag is cleared only
   once the node serves again, so a node that did not come back stays out of
   rotation.

**The index raft port moves (7001 → 10101) on the upgrade from 0.122.x.** Raft
identity does not follow it: each node restarts under the id recorded in step 6
(`<wg-ip>:7001` on a 0.122.x node) at its new address, and because the
configuration still holds it at `:7001`, its rqlited is started with `-join` to
the other recorded members. The leader removes the member and adds it back at
`:10101` (rqlite: "Modifying a node's Raft network addresses"); once the
configuration holds the node at its new address, `raft-adv-addr` is updated and
later restarts pass no `-join`. For that moment the configuration is one voter
short, so **only one node's address may change at a time, with every other voter
up** — a majority changing address at once leaves no leader to re-register any of
them, which is a `recover-raft`. The rolling upgrade's one-node-at-a-time gate
is what guarantees it; do not upgrade 0.122.x voters in parallel by hand. A
cluster of one cannot re-register itself: reform it at the new address with
`orama node recover-raft --env <env> --leader-raft-addr <wg-ip>:10101`.

`--delay` is now the per-node budget for step 8 (how long a node has to rejoin
before the rollout stops), not an unconditional sleep between nodes. A sleep
cannot tell a node that rejoined in 20 seconds from one that never came back, so
the old rollout restarted the next voter either way — which is how a rolling
upgrade takes out a quorum.

Sample output:

```
Reading cluster state from 3 nodes...

Rolling upgrade plan (3 nodes, 3 nameservers):

  1. 10.0.0.1         nameserver-ns1         follower (nameserver — spaced so the zone keeps answering)
  2. 10.0.0.3         nameserver-ns3         follower (nameserver — spaced so the zone keeps answering)
  3. 10.0.0.2         nameserver-ns2         leader — last, after leadership transfer

Each node is upgraded only after the previous one reports Leader or Follower,
an applied index caught up to the leader, and a gateway serving /health.
```

`orama node pre-upgrade` (and the upgrade itself, before its stop) hands index
RQLite leadership to another voter, **aborts** if it cannot, and then confirms
another node has actually taken leadership before allowing the stop — a node
that stepped down into a cluster where nobody was elected must not be removed
from it.

#### What NOT to Do

- **DON'T** stop all nodes, replace binaries, then start all nodes
- **DON'T** run `orama node upgrade --restart` on multiple nodes in parallel
- **DON'T** clear RQLite data directories unless doing a full cluster rebuild
- **DON'T** use `systemctl stop orama-node` on multiple nodes simultaneously (that also stops `@index` via `PartOf`)

#### Schema-Migration Ordering Invariant

The gateway binary embeds a set of SQL migrations. The highest-numbered migration is the schema version that binary REQUIRES — **the gateway will refuse to start if its required schema isn't applied** (the schema-version contract added after the 2026-05-06 incident).

**Migrations take a cluster-wide lock.** Every runner — the node's rqlite, the
index gateway, each namespace gateway, `orama node schema apply` — acquires
`cluster_locks('schema-migrations')` before it reads which versions are applied,
and holds it until it is done. rqlite serialises writes through raft, so a
conditional UPDATE is a linearizable compare-and-swap and therefore a correct
mutex across nodes.

Without it, N gateways starting together each snapshotted the applied set
*before* doing anything and each ran the whole pending list. DDL is guarded by
`IF NOT EXISTS` and survives that; DML is not. Migration 019 was
`UPDATE refresh_tokens SET revoked_at = ... WHERE revoked_at IS NULL`, so a
second node reaching it a minute after the first revoked every token issued in
between — a silent fleet-wide logout.

The lock is TTL-bounded (10 minutes), so a node that dies mid-apply does not
block the fleet. That also means **every migration's DML must be re-runnable**:
an apply that dies before recording its version re-runs the whole file next
start. `migrations/idempotence_test.go` applies every migration, snapshots the
database, applies them all again and asserts nothing moved — a new migration
whose DML is not guarded fails at `go test`, not in production.

This means rolling upgrades have ONE invariant you must respect:

> The new gateway binary's required migrations must be applied to RQLite **before or as part of** starting the new binary on a node.

There are two acceptable patterns:

**Pattern A — let the gateway apply migrations on startup (default).**
The gateway calls `ApplyEmbeddedMigrations` during `NewDependencies` and asserts the schema is at the required version before serving traffic. If the apply succeeds, you're done. If a transient error blocks the apply, gateway startup aborts with a clear `schema mismatch: binary requires version N, database has M` error.

This is the default for both the genesis startup flow and rolling upgrades. No operator action required when it works.

#### Registry backups and restore

The index RQLite is backed up hourly by the leader. Each snapshot is written to
the leader's local `backups/rqlite`, **and** encrypted and pinned into IPFS,
with its CID, SHA-256 and size recorded in `rqlite_backups`.

The off-box copy is the one that matters. A snapshot on the leader's own disk
protects against nothing that actually happens: the disk fails, the VPS is
deleted, `orama node wipe` removes it, or leadership moves and the series
fragments across nodes so no node holds a usable history. And an unrecorded CID
is unfindable, which is the same as no backup.

Retention is 24 hourly plus one a day for 7 days, locally and pinned. It was
three files — three hours of history, and only the hours that node was leader.

To see what exists:

```bash
sudo orama node schema status --env <env>
```

...and query the index for the newest:

```sql
SELECT taken_at, taken_by, cid, size_bytes FROM rqlite_backups ORDER BY taken_at DESC LIMIT 5;
```

To restore, fetch the CID, decrypt it with the node's
`secrets/secrets-encryption-key`, verify the SHA-256 against the recorded one,
and hand the resulting SQLite file to `rqlited`'s restore path. **A backup is
only as good as the last time someone restored it** — exercise this on a
scratch cluster, not for the first time during an incident.

#### Mixed-version window: WireGuard peer rows (migration 038)

Migration 038 adds `confirmed_at` to `wireguard_peers` and is safe to apply while
old-binary nodes are still writing: the column is nullable, the backfills are
one-shot, and old binaries name their columns explicitly so they never trip on
it. Two things about the window itself are worth knowing:

- **Upgrade the node serving `/v1/internal/join` first.** An old binary handling
  a join still allocates `max+1` and writes `INSERT OR REPLACE`, so it can
  overwrite a row a new binary just inserted and take its overlay address. It
  also ignores the `peer_id` a new installer sends and writes the old synthetic
  id instead.
- **Don't issue invite tokens during the roll.** Same reason: which behaviour a
  join gets depends on which node answers it.

An old-binary node also keeps self-registering without `confirmed_at`, so its row
reads as unconfirmed on a new-binary leader. It is not at risk — the same
statement refreshes `created_at`, keeping the row inside the 30-minute join
grace, and a row is only ever dropped when it is *also* unmatched in `dns_nodes`.
Once every node is on the new binary this resolves on the next 60s sync tick.

#### Mixed-version window: unscoped API keys (migration 043)

Migration 043 writes `scopes = 'admin'` onto every live key whose scopes column
was empty, because an empty column used to be *read* as admin and the new
binary reads it as no access at all. Access does not change: the grant that was
being inferred is written down.

The window is one-directional. An old binary still mints a key with no scopes on
every wallet login, and a new binary denies that key everywhere. So during the
roll a wallet that logs in against a not-yet-upgraded node can come away with a
key that an upgraded node refuses. Logging in again once the node is upgraded
mints a correct one, and `orama namespace keys revoke-legacy` sweeps any that
were left behind.

The same migration makes one wallet owner per namespace a database invariant. If
a namespace has several wallet owners today — which the takeover bug allowed —
only the earliest survives the migration, and the others lose access to it. That
is the fix, not a side effect: they should never have had it.

#### Cutover: invite tokens and the operator list (migration 044)

Migration 044 deletes every invite token in the registry. They were stored in
plaintext, and SQLite has no hash function, so there is no way to convert them
from inside a migration. **Any invite minted before the upgrade stops working**;
re-mint with `orama node invite`. The maximum lifetime is also now one hour,
down from seven days.

It also creates the `operators` table and seeds it from
`dns_nodes.operator_wallet` — what `orama node install --operator-wallet` wrote.
`/v1/operator/*` refuses a wallet that is not on that list, so **a cluster
installed without `--operator-wallet` seeds nothing and no one can mint an
invite or list nodes** until a row is inserted:

```sql
INSERT INTO operators (wallet, added_by) VALUES (LOWER('0x…'), 'manual');
```

Check what was seeded before upgrading the first node:

```bash
sudo orama node logs node --since -5min | grep -i migration
```

```sql
SELECT wallet, added_by FROM operators;
```

**Pattern B — pre-apply migrations explicitly via the CLI.**
On any node:
```bash
sudo orama node schema status      # show binary required vs applied
sudo orama node schema apply --yes # apply pending migrations
```
Then start the new gateway. Useful when you want explicit control during a high-risk upgrade or when the auto-apply path is failing for reasons you want to debug separately.

#### Mixed-version window: ownership and API key expiry (migrations 050, 051)

Both are **expand-only** in this release; the next one contracts them.

- **050** creates `principals` and `grants` and backfills them from
  `namespace_ownership`, and leaves `namespace_ownership` in place. 0.122.x
  gateways read and write it on every ownership check, namespace claim and key
  mint; dropping it failed all of them for the whole rolling window. This
  release does not read it. The two are not kept in step during the window: an
  owner a 0.122.x gateway records exists only in `namespace_ownership`, and one
  this release records only in `grants`. The contract migration re-runs 050's
  backfill (idempotent — it adds only owners with no live owner grant) and then
  drops the table.
- **051** adds `expires_at`, `rotated_from` and `principal_id` to `api_keys` in
  place, gives every existing key 90 days from the migration, and constrains
  nothing. The table rebuild that made `expires_at` and `scopes` NOT NULL failed
  every key 0.122.x gateways minted during the window (they name neither). A key
  a 0.122.x gateway mints in the window has no expiry, which this release's
  lookup (`expires_at > now`) does not match: it works on 0.122.x gateways only
  and stops at the end of the rollout — mint keys after the rollout. Only keys
  that existed when 051 first ran are given the 90-day window: it records the
  highest key id in `api_keys_expiry_cutoff` before anything else and bounds
  every backfill by it, so a replay of 051 after the window opened does not turn
  window keys into live ones. The contract migration **revokes** every key above
  the cutoff that still has no expiry — it never backfills one — and then
  rebuilds the table with `expires_at` and `scopes` NOT NULL.

#### Access tokens across the upgrade

0.122.x gateways signed access tokens with the EdDSA key every node derived from
the cluster secret and with the node's RS256 key in `secrets/`. This release's
gateways each sign with a key of their own and publish its public half in
`signing_keys` (migration 052); tenant gateways generate theirs in their own
state directories. An access token issued before a node's upgrade stops
verifying on that node's gateways, and during the window a token issued by an
upgraded gateway does not verify on a 0.122.x one (it does not know the `kid`).
Clients get a 401 and **refresh**: refresh tokens are registry rows (a hash of
the token), not signatures, so they survive the upgrade and mint a token the
gateway verifies. A client that cannot refresh logs in again. The
cluster-derived EdDSA key verifies — never signs — for one access-token
lifetime after a gateway's **first** boot on this release, and not after: the
deadline is written to the gateway's state directory
(`legacy-signing-key-retired-at`) on that boot, so a restart does not re-arm a
key every node and every namespace gateway holds.

#### Verifying schema state remotely

Tenants can self-check schema drift without SSH access via:
```
GET /v1/schema-status
```
Returns `{ok, required_version, applied_version, in_sync, pending: [...]}`. The same data is available via `orama node schema status` for operators with shell access.

#### Build-time guard (CI)

`go test ./migrations/` runs a roundtrip test that opens an in-memory SQLite, applies every embedded migration, and exercises representative SQL operations from the platform's Go code. If a Go handler is added that references a column no migration creates, the test fails — drift is caught at PR review time, not at production deploy.

When adding a new platform table or column:
1. Write the migration in `core/migrations/NNN_description.sql`
2. Update the relevant Go code that reads/writes the new column
3. Add an exemplar to `migrations/roundtrip_test.go` mirroring the new SQL — this enforces the contract permanently

#### A node that boots without a quorum

`orama-node` no longer exits when it cannot reach a raft leader. It brings up
everything that needs only the local machine — WireGuard, IPFS, the local rqlite
replica, CoreDNS, the index gateway, Caddy, ntfy, tenants — reports its
lifecycle state as `degraded`, and keeps retrying the cluster half in the
background. When quorum returns it goes to `active` with no restart.

So a node in `degraded` is serving. Check which components have not converged
before reaching for a recovery command:

```bash
sudo orama node logs node -n 100 | grep "Boot component"
```

Every failed attempt logs the component name, the attempt count, the retry delay
and the underlying error. `Node lifecycle state changed` lines carry the list of
components that are not converged. Only escalate to `recover-raft` when the
whole cluster is leaderless, not when one node reports `degraded`.

#### Recovery from Cluster Split

If nodes get stuck in "Candidate" state or show "leader not found" errors:

```bash
# Reads every node's applied index, keeps the furthest ahead, and prints what
# each one reported before asking you to confirm.
orama node recover-raft --env testnet

# Or name the node whose data to keep yourself.
orama node recover-raft --env testnet --leader 1.2.3.4

# When rqlite is not answering anywhere, so the leader's raft address cannot be
# read from the cluster.
orama node recover-raft --env testnet --leader-raft-addr 10.0.0.1:10101
```

**One node's data is kept. Every other node's raft log and database are
DELETED.** Nothing is backed up: there is no copy to restore from afterwards.
Take a backup yourself first if the surviving node might not be the right one.
This document used to say "backup + delete"; there was never a backup.

What happens:
1. Stop orama-node on every node
2. Reset the kept node to a single-member cluster, preserving its data. The
   recovery `peers.json` names it under the raft id its rqlited runs with and at
   its raft address, which are different values: the id is what the node
   records in `data/rqlite/raft-node-id` (its address only when there is no such
   file, i.e. it was started without `-node-id`), the address comes from
   `--leader-raft-addr` or from the leader's entry in `/nodes` (its `addr`
   field; `/nodes` is keyed by id). On the live path the id `/nodes` reports for
   the leader must be the one the node records, or nothing is reset. A marker
   that exists but cannot be read fails the recovery rather than being taken
   for "no marker". The leader's `raft-adv-addr` and membership record are
   rewritten to name only its address, the configuration the recovery installs,
   so a restart before the membership recorder runs does not read a stale
   address as an address change. This used
   to take the `/nodes` key for the address — refusing every cluster on
   peer-id raft ids — and wrote `id=<address>` on the `--leader-raft-addr` path,
   leaving the leader outside its own configuration.
3. Start it and confirm it comes back as Leader with its data intact — before
   touching any other node, so a failed recovery leaves every copy intact
4. Delete `raft.db`, `raft/`, `db.sqlite` (+`-shm`/`-wal`) and `rsnapshots` on
   every other node, and write its `data/cluster-membership.json` naming the kept
   node — so a wiped node with no join address of its own (the genesis node)
   joins it rather than bootstrapping or refusing to start (see
   [ARCHITECTURE.md](ARCHITECTURE.md), "Only a node that has never been a member
   bootstraps a cluster")
5. Start them one at a time; each pulls a full snapshot from the kept node
6. Verify cluster health

### Replacing a nameserver VPS (keep cluster alive)

See **[NODE_REPLACEMENT.md](NODE_REPLACEMENT.md)** — join new node first, sync, DNS,
namespace safety, then Raft-remove and clean. Written from the 2026-08-03 devnet
cutover; use the same process for testnet.

### Removing a node

There are two operations, and picking the wrong one is how a deleted VPS ends up
still counted toward raft quorum.

**`remove`** retires a node from the cluster and then erases it. Run this for a
node that is or was a member. It works from a *survivor*: prints what the
removal costs every raft cluster the node is a voter in — the platform cluster
and each namespace it serves — and refuses if any would lose quorum; takes the
node out of the platform raft configuration; writes an eviction tombstone so
nothing re-adds it automatically; releases its mesh address, nameserver slot,
namespace memberships, namespace port blocks and its TURN and SFU allocations;
and marks it retired so the cluster purges its DNS records. Then it wipes the
target. `decommission` is accepted as an alias.

```bash
# Show the quorum impact and the statements, change nothing.
orama node remove --env testnet --node 1.2.3.4 --dry-run

orama node remove --env testnet --node 1.2.3.4 --force

# The machine is already gone: do the cluster-side removal only.
orama node remove --env testnet --node 1.2.3.4 --offline --force
```

Every step is keyed on the node and safe to repeat, so a removal that failed
part way through is finished by running it again.

**`wipe`** erases a node and says nothing to the cluster. Use it for a node that
is already retired, that never joined, or to finish a removal whose wipe
failed.

```bash
orama node wipe --env testnet --force                       # every node
orama node wipe --env testnet --node 1.2.3.4 --force        # one node
orama node wipe --env testnet --nuclear --force             # also shared binaries
```

`orama node clean` is deprecated and now runs `wipe`. It only ever erased the
target, so a cleaned node stayed a configured raft voter, kept its
`wireguard_peers` row re-applied to every survivor's interface, and kept its
`dns_nodes` row. It also stopped only the legacy host unit names, leaving tenant
`orama-namespace-*@*` units running under a data directory that had just been
deleted — both fixed in `wipe`.

### Push Options

`orama push` and `orama node push` are the same command; so are `orama rollout`
and `orama node rollout`, and `orama nodes` and `orama node list`.

```bash
orama push --env devnet                     # Fanout via hub (default, fastest)
orama push --env testnet --node 1.2.3.4     # A single node from the inventory
orama push --env testnet --direct           # Sequential, no fanout
orama push --host 1.2.3.4                   # An installed node not in the inventory yet
orama push --env testnet --trust-signers 0xYourWallet  # Nodes installed before archive signing
```

Every node verifies the archive before anything under `/opt/orama` changes
(see [Signed archives](#signed-archives)).

With no `--env`, push targets the active environment (`orama env current`).

### CLI Flags Reference

#### `orama node install`

| Flag | Description |
|------|-------------|
| `--vps-ip <ip>` | VPS public IP address (required) |
| `--domain <domain>` | Domain for HTTPS certificates. Required for nameserver nodes (use the base domain, e.g., `example.com`). Auto-generated for non-nameserver nodes if omitted (e.g., `node-a3f8k2.example.com`) |
| `--base-domain <domain>` | Base domain for deployment routing (e.g., example.com) |
| `--nameserver` | Configure this node as a nameserver (CoreDNS + Caddy) |
| `--join <url>` | Join existing cluster via HTTPS URL (e.g., `https://node1.example.com`) |
| `--token <token>` | Invite token for joining (from `orama node invite` on existing node) |
| `--force` | Force reconfiguration even if already installed |
| `--skip-firewall` | Skip UFW firewall setup |
| `--skip-checks` | Skip minimum resource checks (RAM/CPU) |
| `--operator-wallet <addr>` | Operator wallet; required for a genesis install, where it becomes the archive trust anchor |
| `--remote` | Install the machine at `--vps-ip` over SSH |
| `--archive <path>` | With `--remote`: the build to upload, verified locally against `--operator-wallet` first |
| `--expect-archive-signers <addr,...>` | When joining: the archive signers the join response must name exactly; the archive is verified against them before the join (setup passes it) |

#### `orama node invite`

| Flag | Description |
|------|-------------|
| `--expiry <duration>` | Token expiry duration (default: 1h, e.g. `--expiry 24h`) |

**Important notes about invite tokens:**

- **Tokens are single-use.** Once a node consumes a token during the join handshake, it cannot be reused. Generate a separate token for each node you want to join.
- **Expiry is checked in UTC.** RQLite uses `datetime('now')` which is always UTC. If your local timezone differs, account for the offset when choosing expiry durations.
- **Use longer expiry for multi-node deployments.** When deploying multiple nodes, use `--expiry 24h` to avoid tokens expiring mid-deployment.

#### `orama node upgrade`

| Flag | Description |
|------|-------------|
| `--restart` | Restart all services after upgrade (local mode) |
| `--env <env>` | Target environment for remote rolling upgrade |
| `--node <ip>` | Upgrade a single node only |
| `--public-ip <ip>` | This node's public IP, recorded as `node.public_ip` (default: the recorded one, else the default route's source address) |
| `--delay <seconds>` | Seconds a node has to rejoin after its upgrade before the rollout stops (a readiness budget, not a sleep) |
| `--yes` | Execute the printed rolling plan; without it nothing is restarted |

With `--env`, each node runs the staged build's CLI (`/opt/orama/bin/orama node upgrade --restart`), not its installed one.

#### `orama build`

| Flag | Description |
|------|-------------|
| `--arch <arch>` | Target architecture (default: amd64) |
| `--output <path>` | Output archive path |
| `--verbose` | Verbose build output |
| `--unsigned` | Do not sign the manifest: a local-only archive no node installs |
| `--signers <addr,...>` | Rotate the trusted archive signers to these addresses (signed builds only) |

Signing is the default; `--sign` is accepted and deprecated.

#### `orama push` / `orama node push`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (default: the active one) |
| `--node <ip>` | Push to a single node IP from the inventory |
| `--host <ip>` | Push to a node that is not in the inventory yet |
| `--user <user>` | SSH user for `--host` (default: root) |
| `--direct` | Sequential upload (no hub fanout) |
| `--trust-signers <addr,...>` | Verify the archive here against these addresses and stage it with its own verified CLI (reaches 0.122.x nodes); creates the trust anchor on nodes that have none, and requires an existing one to be exactly this list |

`--ip` and `--fanout` are deprecated. `--ip` is now `--host`; fanning out is the
default, so `--fanout` is accepted and ignored, and `--direct` opts out.

#### `orama rollout` / `orama node rollout`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--no-build` | Skip the build step |
| `--yes` | Skip confirmation |
| `--delay <seconds>` | Delay between nodes (default: 30) |

#### `orama node remove` (alias: `decommission`)

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--node <ip>` | Node to remove (required) |
| `--dry-run` | Print the quorum impact and the statements, change nothing |
| `--offline` | The node is already gone: cluster-side removal only |
| `--nuclear` | When wiping, also remove shared binaries |
| `--force` | Skip confirmation (DESTRUCTIVE) |

#### `orama node wipe`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--node <ip>` | Wipe a single node only; omit for every node |
| `--nuclear` | Also remove shared binaries |
| `--force` | Skip confirmation (DESTRUCTIVE) |

#### `orama node clean`

Deprecated; runs `wipe`. See "Removing a node".

#### `orama node recover-raft`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--leader <ip>` | IP of the node whose data to keep. Default: the node with the highest applied index, which the command reads and prints |
| `--leader-raft-addr <host:port>` | The kept node's raft address, e.g. `10.0.0.1:10101`. Use when rqlite is not answering anywhere, so it cannot be read from the cluster |
| `--force` | Skip confirmation (DESTRUCTIVE) |

#### `orama node` (Service Management)

Use these commands to manage services on production nodes:

```bash
# Stop all services (orama-node, coredns, caddy)
sudo orama node stop

# Start all services
sudo orama node start

# Restart all services
sudo orama node restart

# Check service status
sudo orama node status

# Diagnose common issues
sudo orama node doctor
```

**Note:** Always use `orama node stop` instead of manually running `systemctl stop`. The CLI ensures all related services (including CoreDNS and Caddy on nameserver nodes) are handled correctly.

#### Quorum guard (`--force`)

`stop`, `restart` and the pre-upgrade step refuse to run when stopping this node
would cost the index RQLite its quorum, and print the arithmetic:

```
Quorum check: 3/3 voters reachable, 2 would remain (need 2).
```

Quorum is a majority of the **configured** voters. Stopping a node does not
remove it from the raft configuration — it only makes it unreachable — so on
three voters you may stop one, and on two voters you may stop neither.
Membership shrinks only through an explicit remove.

The guard **fails closed**. If the local RQLite may be running but its status
cannot be read, the command refuses rather than guessing: "I could not look" is
not "go ahead". The one case it allows without a reading is neither
`orama-namespace-rqlite@index` nor (on a 0.122.x node) `orama-node` being active —
0.122.x ran the index rqlited inside `orama-node`, and the upgrade from it runs
this check on such a node — since a node whose RQLite is already down contributes nothing to
quorum. `orama-node` counts only on a node without the
`orama-namespace-rqlite@.service` template (the 0.122.x layout): on this release
it is always active and never runs rqlited, so a node whose rqlite@index is down
stays stoppable without `--force`. The pre-upgrade step then writes the maintenance flag (a failure to
write it is fatal) and, with no rqlited running, has no leadership to hand over.

`--force` skips the check. Use it when you have confirmed the remaining voters
can still form quorum — for example when deliberately taking down a cluster, or
when the node is a non-voter the guard could not classify.

#### Leadership handover

`orama node pre-upgrade` — which the upgrade runs before it stops anything —
hands index RQLite leadership to another voter before the node stops, and
**aborts** if this node is still the leader afterwards.
Restarting a leader that never stepped down forces an election and fails
in-flight writes, so a failed handover stops the upgrade rather than warning
about it.

The handover is confirmed against `/status` — the POST only *starts* it, and
raft still has to elect the target. A build whose rqlite has no
`transfer-leadership` API is tolerated: the node falls back to SIGTERM
step-down.

Tenant namespace handovers stay advisory. Losing a namespace leader degrades
that namespace, not the node's ability to restart safely. Finding them is not:
a tenant namespace with rqlite data on the node but no `rqlite.env` (in
`/var/lib/orama-unit-env`, or `data/namespaces/<ns>/` on a node still on the old
layout) fails the step instead of being skipped.

#### `orama node report`

Outputs comprehensive health data as JSON. Used by `orama monitor` over SSH:

```bash
sudo orama node report --json
```

See [MONITORING.md](MONITORING.md) for full details.

#### `orama monitor`

Real-time cluster monitoring from your local machine:

```bash
# Interactive TUI
orama monitor --env testnet

# Cluster overview
orama monitor cluster --env testnet

# Alerts only
orama monitor alerts --env testnet

# Full JSON for LLM analysis
orama monitor report --env testnet
```

See [MONITORING.md](MONITORING.md) for all subcommands and flags.

### Node Join Flow

```bash
# 1. Genesis node (first node, creates cluster)
# Nameserver nodes use the base domain as --domain
sudo orama node install --vps-ip 1.2.3.4 --domain example.com \
    --base-domain example.com --nameserver

# 2. On genesis node, generate an invite
orama node invite --expiry 24h
# Prints: sudo orama node install --join https://example.com --token <TOKEN> \
#           [--ca-fingerprint <FP>] --vps-ip <NEW_NODE_IP> --nameserver
# Drop --nameserver when joining as a regular node.

# 3a. Join as nameserver (requires --domain set to base domain)
sudo orama node install --join http://1.2.3.4 --token abc123... \
    --vps-ip 5.6.7.8 --domain example.com --base-domain example.com --nameserver

# 3b. Join as regular node (domain auto-generated, no --domain needed)
sudo orama node install --join http://1.2.3.4 --token abc123... \
    --vps-ip 5.6.7.8 --base-domain example.com
```

The join flow establishes a WireGuard VPN tunnel before starting cluster services.
All inter-node communication (RQLite, IPFS, Olric) uses WireGuard IPs (10.0.0.x).
No cluster ports are ever exposed publicly.

#### DNS Prerequisite

The `--join` URL should use the HTTPS domain of the genesis node (e.g., `https://node1.example.com`).
For this to work, the domain registrar for `example.com` must have NS records pointing to the genesis
node's IP so that `node1.example.com` resolves publicly.

**If DNS is not yet configured**, you can use the genesis node's public IP with HTTP as a fallback:

```bash
sudo orama node install --join http://1.2.3.4 --vps-ip 5.6.7.8 --token abc123... --nameserver
```

This works because Caddy's `:80` block proxies all HTTP traffic to the gateway. However, once DNS
is properly configured, always use the HTTPS domain URL.

**Important:** Never use `http://<ip>:10104` — that is the internal index gateway and is blocked by
UFW from external access. The join request goes through Caddy on port 80 (HTTP) or 443 (HTTPS),
which proxies to the gateway internally.

## OramaOS Enrollment

For OramaOS nodes (mainnet, devnet, testnet), use the enrollment flow instead of `orama node install`:

```bash
# 1. Flash OramaOS image to VPS (via provider dashboard)
# 2. Generate invite token on existing cluster node
orama node invite --expiry 24h

# 3. Enroll the OramaOS node — --code is printed on the node's console
orama node enroll --node-ip <vps-public-ip> --code <registration-code> --token <invite-token> --gateway <gateway-url>

# 4. For genesis node reboots (before 5+ peers exist)
orama node unlock --genesis --node-ip <wg-ip>
```

OramaOS nodes have no SSH access. All management happens through the Gateway API:

```bash
# Status, logs, commands — admin credential required
curl "https://gateway.example.com/v1/node/status?node_id=<id>" \
  -H "Authorization: Bearer <admin-api-key>"
curl "https://gateway.example.com/v1/node/logs?node_id=<id>&service=gateway" \
  -H "Authorization: Bearer <admin-api-key>"
```

See [ORAMAOS_DEPLOYMENT.md](ORAMAOS_DEPLOYMENT.md) for the full guide.

**Note:** `orama node wipe` (and the deprecated `clean`) does not work on OramaOS nodes (no SSH). For graceful departure use the Gateway API (`POST /v1/node/leave`), or reflash the image for a factory reset. There is no `orama node leave` CLI command.

## Pre-Install Checklist (Ubuntu Only)

Before running `orama node install` on a VPS, ensure:

1. **Stop Docker if running.** Docker commonly binds ports 4001 and 8080 which conflict with IPFS. The installer checks for port conflicts and shows which process is using each port, but it's easier to stop Docker first:
   ```bash
   sudo systemctl stop docker docker.socket
   sudo systemctl disable docker docker.socket
   ```

2. **Stop any existing IPFS instance.**
   ```bash
   sudo systemctl stop ipfs
   ```

3. **Stop any service on port 53** (for nameserver nodes). The installer handles `systemd-resolved` automatically, but other DNS services (like `bind9` or `dnsmasq`) must be stopped manually.

## Recovering from Failed Joins

If a node partially joins the cluster (registers in RQLite's Raft but then fails or gets cleaned), the remaining cluster can lose quorum permanently. This happens because RQLite thinks there are N voters but only N-1 are reachable.

**Symptoms:** RQLite stuck in "Candidate" state, no leader elected, all writes fail.

**Solution:** Do a full clean reinstall of all affected nodes. Use [CLEAN_NODE.md](CLEAN_NODE.md) to reset each node, then reinstall starting from the genesis node.

**Prevention:** Always ensure a joining node can complete the full installation before it joins. The installer validates port availability upfront to catch conflicts early.

## Debugging Production Issues

Always follow the local-first approach:

1. **Reproduce locally** — set up the same conditions on your machine
2. **Find the root cause** — understand why it's happening
3. **Fix in the codebase** — make changes to the source code
4. **Test locally** — run `make test` and verify
5. **Deploy** — only then deploy the fix to production

Never fix issues directly on the server — those fixes are lost on next deployment.

## Trusting the Self-Signed TLS Certificate

When Let's Encrypt is rate-limited, Caddy falls back to its internal CA (self-signed certificates). Browsers will show security warnings unless you install the root CA certificate.

### Downloading the Root CA Certificate

From VPS 1 (or any node), copy the certificate:

```bash
# Copy the cert to an accessible location on the VPS
ssh ubuntu@<VPS_IP> "sudo cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt /tmp/caddy-root-ca.crt && sudo chmod 644 /tmp/caddy-root-ca.crt"

# Download to your local machine
scp ubuntu@<VPS_IP>:/tmp/caddy-root-ca.crt ~/Downloads/caddy-root-ca.crt
```

### macOS

```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ~/Downloads/caddy-root-ca.crt
```

This adds the cert system-wide. All browsers (Safari, Chrome, Arc, etc.) will trust it immediately. Firefox uses its own certificate store — go to **Settings > Privacy & Security > Certificates > View Certificates > Import** and import the `.crt` file there.

To remove it later:
```bash
sudo security remove-trusted-cert -d ~/Downloads/caddy-root-ca.crt
```

### iOS (iPhone/iPad)

1. Transfer `caddy-root-ca.crt` to your device (AirDrop, email attachment, or host it on a URL)
2. Open the file — iOS will show "Profile Downloaded"
3. Go to **Settings > General > VPN & Device Management** (or "Profiles" on older iOS)
4. Tap the "Caddy Local Authority" profile and tap **Install**
5. Go to **Settings > General > About > Certificate Trust Settings**
6. Enable **full trust** for "Caddy Local Authority - 2026 ECC Root"

### Android

1. Transfer `caddy-root-ca.crt` to your device
2. Go to **Settings > Security > Encryption & Credentials > Install a certificate > CA certificate**
3. Select the `caddy-root-ca.crt` file
4. Confirm the installation

Note: On Android 7+, user-installed CA certificates are only trusted by apps that explicitly opt in. Chrome will trust it, but some apps may not.

### Windows

```powershell
certutil -addstore -f "ROOT" caddy-root-ca.crt
```

Or double-click the `.crt` file > **Install Certificate** > **Local Machine** > **Place in "Trusted Root Certification Authorities"**.

### Linux

```bash
sudo cp caddy-root-ca.crt /usr/local/share/ca-certificates/caddy-root-ca.crt
sudo update-ca-certificates
```

## Push notifications

Push provider configuration is **tenant-self-service** as of bug #220
follow-up. Tenants set their own ntfy / Expo credentials via authenticated
HTTP — operators no longer need to edit YAML and restart for every namespace
that wants push.

### Tenant flow (no operator involvement)

```bash
# Set per-namespace config
curl -X PUT https://ns-anchat-test.orama-devnet.network/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>' \
  -H 'Content-Type: application/json' \
  -d '{"ntfy_base_url": "https://ntfy.sh"}'

# Read current config (secrets redacted to booleans)
curl https://ns-anchat-test.orama-devnet.network/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>'

# Clear (push reverts to gateway YAML defaults, or 503 if no defaults)
curl -X DELETE https://ns-anchat-test.orama-devnet.network/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>'
```

Per-namespace config takes effect on the NEXT push send (the cached
dispatcher is invalidated on PUT/DELETE). No restart needed.

### Operator flow (cluster-wide defaults — optional)

Operators can seed a cluster-wide ntfy default in node.yaml. Per-namespace
config OVERRIDES the default; namespaces with no row inherit it.

```yaml
# node.yaml — the only push-related YAML key, nested under http_gateway.
# node.yaml is strictly decoded: unknown keys (e.g. a top-level push:
# block) make config parsing fail and orama-node refuse to start.
http_gateway:
  ntfy_base_url: "https://ntfy.sh"   # default for namespaces with no override
```

There is no YAML key for a default Expo access token — Expo tokens can
only be set per-namespace via `PUT /v1/push/config`.

### Encryption

Sensitive credentials (`ntfy_auth_token`, `expo_access_token`) are
AES-256-GCM-encrypted at rest in the `namespace_push_config` table using
a key derived from the cluster secret. The GET endpoint returns boolean
`has_X` flags only — credentials are NEVER echoed back over HTTP.

### Disabling push entirely

If `cluster_secret` isn't configured on the gateway, the push subsystem
is disabled and `/v1/push/*` returns 503. To enable: set the cluster secret
and restart. (This is the only operator-side restart still required, and
it's a one-time action at gateway provisioning.)

## Project Structure

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full architecture overview.

Key directories:

```
cmd/
  cli/          — CLI entry point (orama command)
  node/         — Node entry point (orama-node)
  gateway/      — Standalone gateway entry point
pkg/
  cli/          — CLI command implementations
  gateway/      — HTTP gateway, routes, middleware
  deployments/  — Deployment types, service, storage
  environments/ — Production (systemd) and development (direct) modes
  rqlite/       — Distributed SQLite via RQLite
```
