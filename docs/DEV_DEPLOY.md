# Development Guide

## Prerequisites

- Go 1.26.7+ (see `go.mod`)
- Node.js 18+ (for anyone-client in dev mode)
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

# 1. Build binary archive (cross-compiles all binaries for linux/amd64)
orama build
# Creates: /tmp/orama-<version>-linux-amd64.tar.gz

# 2. Push archive to all nodes (fanout via hub node)
orama node push --env testnet

# 3. Rolling upgrade (one node at a time, in node-list order)
orama node upgrade --env testnet
```

### Fresh Node Install

```bash
# Build the archive first (if not already built)
orama build

# Install on a new VPS (auto-uploads binary archive, zero compilation)
orama node install --vps-ip <ip> --nameserver --domain <domain> --base-domain <domain>
```

The installer auto-detects the binary archive at `/opt/orama/manifest.json` and copies pre-built binaries instead of compiling from source.

**Install verifies the node before it reports success.** The final phase waits for, in dependency order:

1. `orama-node.service` active **and not crash-looping** (`NRestarts` is 0 — an active unit systemd is about to restart for the fifth time is the failure this catches)
2. rqlite in raft state `Leader` or `Follower` — not merely answering on `:10100`, which it does while still Candidate or still replaying its log
3. `wg0` up with an interface
4. the gateway's `/health` returning 200

The first component that does not come up is named, install exits **non-zero**, and no `✅` is printed. Before change-287 install printed `✅ Production installation complete!` unconditionally — after a partial template install, after a failed DNS seed, after a supervisor that started and exited — and the operator, the CLI and the next node's join all proceeded on the assumption that the node was up.

Two related orderings changed in the same commit:

- **Namespace systemd templates install before the services that use them.** Phase 5 starts `orama-node`, whose first act is to start `orama-namespace-wireguard@index`; with no template installed systemd answers `Unit ... not found` and the supervisor exits. Install used to depend on systemd's restart loop to converge past that. Any missing or unwritable template is now fatal and the error names it.
- **DNS seeding runs after verification, and is fatal on a `--nameserver` genesis node** (advisory elsewhere). That node serves the zone and nothing else creates its records; the heartbeat "self-heal" the old warning promised re-advertises a node's own A record, it does not seed a zone's NS or SOA. Seeding now runs after the gateway answers `/health` — which is the signal that migrations completed — instead of behind six escalating sleeps totalling 105 seconds that could not tell "still migrating" from "broken".

### What runs on a node

The installer enables **only** `orama-node`. That unit is the supervisor: it starts `orama-namespace-*@index` (WireGuard, IPFS, rqlite, olric, pubsub, gateway, vault, Caddy, …) and, on `--nameserver` nodes, `orama-namespace-coredns@nameserver`. Tenant clusters are `orama-namespace-{rqlite,olric,gateway}@<name>`.

Use `orama node …` (start/stop/restart/upgrade). Do not enable leftover `orama-ipfs.service`, `orama-olric.service`, `caddy.service`, or `coredns.service` — the installer writes their unit files for rollback and disables them on purpose. Upgrade and restart used to start them again (they were listed in `GetProductionServices` and the restart priority order), so they raced `@index` for 10102, 10107, `:53` and `:443` until `IndexSupervisor` stopped them on its next start. `systemd.IsLeftoverHostUnit` now keeps them out of both lists. Index RQLite data stays at `~/.orama/data/rqlite`. Internals are `10100–10109`; do not mix a voter still on 5001 with one on 10100.

### Upgrading a Multi-Node Cluster (CRITICAL)

**NEVER restart all nodes simultaneously.** Index RQLite uses Raft consensus and requires a majority (quorum) to function. Never restart multiple RQLite voters in the same step.

#### Safe Upgrade Procedure

```bash
# Full rollout (build + push + rolling upgrade, one command)
orama node rollout --env testnet

# Or with more control:
orama node push --env testnet                     # Push archive to all nodes
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
5. **Hands leadership to another voter** before restarting a node that is
   leading, and refuses to restart it if no other voter takes it. The plan said
   "leader — last, after leadership transfer" while nothing performed the
   transfer, so restarting the leader forced an election that failed every
   in-flight write.
6. **Gates on each node actually rejoining** before touching the next: raft state
   `Leader` or `Follower`, a leader known to exist, an applied index caught up to
   the leader's commit index, and a gateway serving `/health`.
7. **Stops the rollout** the moment a node fails that gate, leaving the remaining
   voters untouched and the cluster serving.

`--delay` is now the per-node budget for step 6 (how long a node has to rejoin
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

`orama node pre-upgrade` hands index RQLite leadership to another voter before
the node restarts, **aborts** if it cannot, and then confirms another node has
actually taken leadership before allowing the stop — a node that stepped down
into a cluster where nobody was elected must not be removed from it.

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

**Pattern B — pre-apply migrations explicitly via the CLI.**
On any node:
```bash
sudo orama node schema status      # show binary required vs applied
sudo orama node schema apply --yes # apply pending migrations
```
Then start the new gateway. Useful when you want explicit control during a high-risk upgrade or when the auto-apply path is failing for reasons you want to debug separately.

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
journalctl -u orama-node -n 100 | grep "Boot component"
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
2. Reset the kept node to a single-member cluster, preserving its data
3. Start it and confirm it comes back as Leader with its data intact — before
   touching any other node, so a failed recovery leaves every copy intact
4. Delete `raft.db`, `raft/`, `db.sqlite` (+`-shm`/`-wal`) and `rsnapshots` on
   every other node
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
orama push --host 1.2.3.4                   # A node not in the inventory yet
```

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
| `--anyone-client` | Install Anyone as a SOCKS5 client on `:9050` (this is already the default) |

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
| `--delay <seconds>` | Delay between nodes during rolling upgrade (default: 30) |

#### `orama build`

| Flag | Description |
|------|-------------|
| `--arch <arch>` | Target architecture (default: amd64) |
| `--output <path>` | Output archive path |
| `--verbose` | Verbose build output |

#### `orama push` / `orama node push`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (default: the active one) |
| `--node <ip>` | Push to a single node IP from the inventory |
| `--host <ip>` | Push to a node that is not in the inventory yet |
| `--user <user>` | SSH user for `--host` (default: root) |
| `--direct` | Sequential upload (no hub fanout) |

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

The guard **fails closed**. If the local RQLite is running but its status cannot
be read, the command refuses rather than guessing: "I could not look" is not
"go ahead". The one case it allows without a reading is `orama-namespace-rqlite@index`
being stopped, since a node whose RQLite is already down contributes nothing to
quorum.

`--force` skips the check. Use it when you have confirmed the remaining voters
can still form quorum — for example when deliberately taking down a cluster, or
when the node is a non-voter the guard could not classify.

#### Leadership handover

`orama node pre-upgrade` hands index RQLite leadership to another voter before
the node restarts, and **aborts** if this node is still the leader afterwards.
Restarting a leader that never stepped down forces an election and fails
in-flight writes, so a failed handover stops the upgrade rather than warning
about it.

The handover is confirmed against `/status` — the POST only *starts* it, and
raft still has to elect the target. A build whose rqlite has no
`transfer-leadership` API is tolerated: the node falls back to SIGTERM
step-down.

Tenant namespace handovers stay advisory. Losing a namespace leader degrades
that namespace, not the node's ability to restart safely.

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

# 3. Enroll the OramaOS node
orama node enroll --node-ip <vps-public-ip> --token <invite-token> --gateway <gateway-url>

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
