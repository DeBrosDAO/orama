# Monitoring

Real-time cluster health monitoring via SSH. The system has two parts:

1. **`orama node report`** — Runs on each VPS node, collects all local health data, outputs JSON
2. **`orama monitor`** — Runs on your local machine, SSHes into nodes, aggregates results, displays via TUI or tables

## Architecture

```
Developer Machine                    VPS Nodes (via SSH)
┌──────────────────┐                 ┌────────────────────┐
│ orama monitor    │ ──SSH──────────>│ orama node report  │
│  (TUI / tables)  │ <──JSON─────── │  (local collector)  │
│                  │                 └────────────────────┘
│  CollectOnce()   │ ──SSH──────────>│ orama node report  │
│  DeriveAlerts()  │ <──JSON─────── │  (local collector)  │
│  Render()        │                 └────────────────────┘
└──────────────────┘
```

Each node runs `orama node report --json` locally (no SSH to other nodes), collecting data via `os/exec` and `net/http` to localhost services. The monitor SSHes into all nodes in parallel, collects reports, then runs cross-node analysis to detect cluster-wide issues.

## Quick Start

```bash
# Interactive TUI (auto-refreshes every 30s)
orama monitor --env testnet

# Cluster overview table
orama monitor cluster --env testnet

# Alerts only
orama monitor alerts --env testnet

# Full JSON report (pipe to jq or feed to LLM)
orama monitor report --env testnet
```

## `orama monitor` — Local Orchestrator

### Usage

```
orama monitor [subcommand] --env <environment> [flags]
```

Without a subcommand, launches the interactive TUI.

### Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--env` | *(required)* | Environment: `devnet`, `testnet`, `mainnet` |
| `--json` | `false` | Machine-readable JSON output (for one-shot subcommands) |
| `--node` | | Filter to a specific node host/IP |
| `--config` | *(resolver)* | Read nodes from this file instead of resolving them |

### Subcommands

| Subcommand | Description |
|------------|-------------|
| `live` | Interactive TUI monitor (default when no subcommand) |
| `cluster` | Cluster overview: all nodes, roles, RQLite state, WG peers |
| `node` | Per-node health details (system, services, WG, DNS) |
| `service` | Service status matrix across all nodes |
| `mesh` | WireGuard mesh connectivity and peer details |
| `dns` | DNS health: CoreDNS, Caddy, TLS cert expiry, resolution |
| `namespaces` | Namespace health across nodes |
| `alerts` | Active alerts and warnings sorted by severity |
| `report` | Full JSON dump optimized for LLM consumption |

### Examples

```bash
# Cluster overview
orama monitor cluster --env testnet

# Cluster overview as JSON
orama monitor cluster --env testnet --json

# Alerts for all nodes
orama monitor alerts --env testnet

# Single-node deep dive
orama monitor node --env testnet --node 51.195.109.238

# Services for one node
orama monitor service --env testnet --node 51.195.109.238

# WireGuard mesh details
orama monitor mesh --env testnet

# DNS health
orama monitor dns --env testnet

# Namespace health
orama monitor namespaces --env testnet

# Full report for LLM analysis
orama monitor report --env testnet | jq .

# Single-node report
orama monitor report --env testnet --node 51.195.109.238

# Custom config file
orama monitor cluster --config /path/to/nodes.conf --env devnet
```

### Interactive TUI

The `live` subcommand (default) launches a full-screen terminal UI:

**Tabs:** Overview | Nodes | Services | WG Mesh | DNS | Namespaces | Alerts

**Key Bindings:**

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Switch tabs |
| `j` / `k` or `↑` / `↓` | Scroll content |
| `r` | Force refresh |
| `q` / `Ctrl+C` | Quit |

The TUI auto-refreshes every 30 seconds. A spinner shows during data collection. Colors indicate health: green = healthy, red = critical, yellow = warning.

### LLM Report Format

`orama monitor report` outputs structured JSON designed for AI consumption:

```json
{
  "meta": {
    "environment": "testnet",
    "collected_at": "2026-02-16T12:00:00Z",
    "duration_seconds": 3.2,
    "node_count": 3,
    "healthy_count": 3,
    "failed_count": 0
  },
  "summary": {
    "rqlite_leader": "10.0.0.1",
    "rqlite_quorum": "ok",
    "wg_mesh_status": "ok",
    "service_health": "ok",
    "critical_alerts": 0,
    "warning_alerts": 1
  },
  "alerts": [...],
  "nodes": [
    {
      "host": "51.195.109.238",
      "role": "nameserver",
      "status": "ok",
      "report": { ... }
    }
  ]
}
```

## `orama node report` — VPS-Side Collector

Runs locally on a VPS node. Collects all system and service data in parallel and outputs a single JSON blob. Requires root privileges.

### Usage

```bash
# On a VPS node
sudo orama node report --json
```

### What It Collects

| Section | Data |
|---------|------|
| **system** | CPU count, load average, memory/disk/swap usage, OOM kills, kernel version, uptime, clock time |
| **services** | Systemd service states (active, restarts, memory, CPU, restart loop detection) for 10 core services |
| **rqlite** | Raft state, leader, term, applied/commit index, peers, strong read test, readyz, debug vars |
| **olric** | Service state, memberlist, member count, restarts, memory, log analysis |
| **ipfs** | Daemon/cluster state, swarm/cluster peers, repo size, versions, swarm key |
| **vault** | Service state, guardian health (healthy/total, read threshold, write quorum), restarts |
| **gateway** | HTTP health check, subsystem status |
| **wireguard** | Interface state, WG IP, peers, handshake ages, MTU, config permissions |
| **dns** | CoreDNS/Caddy state, port bindings, resolution tests, TLS cert expiry |
| **anyone** | Relay/client state, bootstrap progress, fingerprint |
| **network** | Internet reachability, TCP stats, retransmission rate, listening ports, UFW rules |
| **processes** | Zombie count, orphan orama processes, panic/fatal count in logs |
| **namespaces** | Per-namespace service probes (RQLite, Olric, Gateway) |
| **deployments** | Deployment counts: total, running, failed, static |
| **serverless** | Function count, engine status |

### Performance

All 15 collectors run in parallel with goroutines. Typical collection time is **< 1 second** per node. HTTP timeouts are 3 seconds, command timeouts are 4 seconds.

### Output Schema

```json
{
  "timestamp": "2026-02-16T12:00:00Z",
  "hostname": "ns1",
  "public_ip": "51.195.109.238",
  "wireguard_ip": "10.0.0.1",
  "version": "",
  "collect_ms": 526,
  "system": { "cpu_count": 4, "load_avg_1": 0.1, "mem_total_mb": 7937, ... },
  "services": { "services": [...], "failed_units": [] },
  "rqlite": { "responsive": true, "raft_state": "Leader", "term": 42, ... },
  "olric": { "service_active": true, "memberlist_up": true, ... },
  "ipfs": { "daemon_active": true, "swarm_peers": 2, ... },
  "vault": { "service_active": true, "responsive": true, "status": "healthy", ... },
  "gateway": { "responsive": true, "http_status": 200, ... },
  "wireguard": { "interface_up": true, "wg_ip": "10.0.0.1", "peers": [...], ... },
  "dns": { "coredns_active": true, "caddy_active": true, "base_tls_days_left": 88, ... },
  "anyone": { "relay_active": true, "bootstrapped": true, ... },
  "network": { "internet_reachable": true, "ufw_active": true, ... },
  "processes": { "zombie_count": 0, "orphan_count": 0, "panic_count": 0, ... },
  "namespaces": [],
  "deployments": { "total_count": 0, "running_count": 0, "failed_count": 0, "static_count": 0 },
  "serverless": { "function_count": 0, "engine_status": "ok" }
}
```

## Alert Detection

Alerts are derived from cross-node analysis of all collected reports. Each alert has a severity level and identifies the affected subsystem and node.

### Alert Severities

| Severity | Examples |
|----------|----------|
| **critical** | SSH collection failed (node unreachable), no RQLite leader, split brain, RQLite unresponsive, WireGuard interface down, WG peer never handshaked, OOM kills, service failed, UFW inactive |
| **warning** | Strong read failed, memory > 90%, disk > 85%, stale WG handshake (> 3min), Raft term inconsistency, applied index lag > 100, restart loop detected, TLS cert < 14 days, DNS down, namespace gateway down, Anyone not bootstrapped, clock skew > 5s, internet unreachable, high TCP retransmission |
| **info** | Zombie processes, orphan orama processes, swap usage > 30% |

### Cross-Node Checks

These checks compare data across all nodes:

- **RQLite Leader**: Exactly one leader exists (no split brain)
- **Leader Agreement**: All nodes agree on the same leader address
- **Raft Term Consistency**: Term values within 1 of each other
- **Applied Index Lag**: Followers within 100 entries of the leader
- **WireGuard Peer Symmetry**: Each node has N-1 peers
- **Clock Skew**: Node clocks within 5 seconds of each other
- **Binary Version**: All nodes running the same version. Currently inert: `orama node report` always emits an empty `version`, so every node reads as "unknown" and this alert never fires.

### The lifecycle harness

`e2e/lifecycle` consumes `orama monitor report --json` as its only view of the
cluster, so the report's schema is a test contract. Its predicates assert:

- **`Converged(n)`** — exactly `n` nodes, quorum `ok`, a leader, WG mesh `ok`,
  zero critical alerts, and per node: responsive rqlite in `Leader`/`Follower`,
  gateway 200, `wg0` up with N-1 peers, no crash-looping service, no failed unit.
- **`LeaderAgreement()`** — every responsive node names the same leader. Split
  brain is failed on by name, because both halves look healthy from inside.
- **`Forgotten(wgIP)`** — no surviving node lists the address, in the node list
  *or* as a WireGuard peer. A node evicted from raft but left in the mesh is the
  failure that survives a restart.
- **`Serving()`** — gateways respond and nameservers run CoreDNS, with raft
  ignored entirely. A cluster mid-election must still serve.

Adding a field is safe; renaming one that these read will fail
`go test ./e2e/lifecycle/...` in `make test`.

### The rolling-upgrade gate

`orama node upgrade --env <env>` uses the same signals, in `pkg/nodehealth`, as
its gate between nodes. A node passes when **all** of these hold:

| Signal | Why |
|--------|-----|
| Raft state is `Leader` or `Follower` | `Candidate` means an election is running; restarting the next voter during one is how a rollout loses quorum |
| A leader is known (`leader_id` non-empty) | A follower that reports no leader is in a cluster that cannot commit a write |
| Applied index within 200 of the commit index | A follower tens of thousands of entries behind is not carrying reads |
| Gateway `/health` returns 200 | The node serves no traffic until it does |

Anything short of all four stops the rollout, leaving the remaining voters
untouched. The same package backs `orama node install`'s post-install
verification, `orama node start`, and `orama node post-upgrade`, so "ready"
means one thing across the CLI.

### Per-Node Checks

- **RQLite**: Responsive, ready, strong read
- **WireGuard**: Interface up, handshake freshness
- **System**: Memory, disk, load, OOM kills, swap
- **Services**: Systemd state, restart loops
- **DNS**: CoreDNS/Caddy up, TLS cert expiry, SOA resolution
- **Anyone**: Bootstrap progress
- **Processes**: Zombies, orphans, panics in logs
- **Namespaces**: Gateway and RQLite per namespace
- **Network**: UFW, internet reachability, TCP retransmission

## Monitor vs Inspector

Both tools check cluster health, but they serve different purposes:

| | `orama monitor` | `orama inspect` |
|---|---|---|
| **Data source** | `orama node report --json` (single SSH call per node) | 15+ SSH commands per node per subsystem |
| **Speed** | ~3-5s for full cluster | ~4-10s for full cluster |
| **Output** | TUI, tables, JSON | Tables, JSON |
| **Focus** | Real-time monitoring, alert detection | Deep diagnostic checks with pass/fail/warn |
| **AI support** | `report` subcommand for LLM input | `--ai` flag for inline analysis |
| **Use case** | "Is anything wrong right now?" | "What exactly is wrong and why?" |

Use `monitor` for day-to-day health checks and the interactive TUI. Use `inspect` for deep diagnostics when something is already known to be broken.

## In-cluster failure detection (the ring monitor)

Separate from the SSH tooling above, every node runs a ring-based failure
detector inside its **index gateway** process. It is what drives automatic
recovery, so its thresholds decide how fast a dead node is noticed and how
easily a healthy one is wrongly evicted.

| Property | Value | Where |
|---|---|---|
| Runs on | index gateway only | a tenant gateway's RQLite has no `dns_nodes` rows |
| Ring | the K nodes after this one in `dns_nodes` sorted by id | K = 3 |
| Probe | `GET http://<internal_ip>:10104/v1/internal/ping` | port from `constants.GatewayAPIPort` |
| Probe interval | 10s | |
| Suspect | 3 consecutive misses (~30s) | disables that node's namespace DNS records |
| Dead | 12 consecutive misses (~2min) | needs ≥2 distinct observers to agree |
| Startup grace | 5min | no node is declared dead during it |

**The probe port is configuration, not a literal.** It is the *index gateway*
port on the peer, because the ring monitors nodes rather than namespaces and
only the index gateway serves `/v1/internal/ping`. `health.NewMonitor` returns
an error if it is unset: the port was hardcoded once, survived the move of the
index internals into the 10100–10109 block untouched, and every probe on a
healthy fleet then failed — which the ring turns into a cluster-wide false
eviction about seven minutes after the gateways start.

**A recent heartbeat outranks the probe.** Before issuing HTTP, the monitor
checks whether the peer updated `dns_nodes.last_seen` within the last 65
seconds (two heartbeat ticks). That row is a raft write, so a fresh value
proves the peer was alive *and* had quorum — evidence that does not depend on
the HTTP path. A stale heartbeat proves nothing either way and falls through to
the probe rather than counting as a miss.

Observations land in `node_health_events`; recovery is triggered only by the
lowest-id observer once quorum agrees, so a confirmed death produces one
recovery action rather than one per observer.

## Namespace DNS self-management

Separately from the ring monitor, each node probes the namespaces it hosts every
30s and keeps its own DNS records in step. rqlite and Olric are probed by
dialling their ports; the gateway is asked `GET /v1/health`, because it binds
and answers long before it has a usable schema and a TCP dial cannot tell the
difference — a gateway that could not serve a single request used to stay in the
round-robin.

The gateway probe reads **readiness only**. `starting` (waiting for its schema)
and `blocked` (schema below what the binary requires) count against the
namespace; `degraded` and `unhealthy` do not, because those are subsystem
health — rqlite and Olric have their own probes above, and withdrawing a node
because its IPFS daemon blipped would turn one unavailable subsystem into an
unavailable node.

| Namespace status | When | In DNS |
|---|---|---|
| `healthy` | every service reachable and the gateway is ready | yes |
| `starting` | the gateway is up but waiting for its schema | no |
| `unhealthy` | a service is unreachable, or the gateway is blocked | no |

| Transition | Threshold | Effect |
|---|---|---|
| healthy → not healthy | 3 consecutive non-healthy probes (~90s) | withdraws this node's `ns-<ns>` and `*.ns-<ns>` A records |
| not healthy → healthy | 3 consecutive healthy probes (~90s) | restores the records it withdrew |

A gateway that is `starting` therefore adds its convergence time to the
re-advertise lag after a restart (bug-286).

Two safety rules:

- **Never the last record.** A withdrawal is refused when this node's record is
  the only active one for that name. The count is evaluated inside the UPDATE,
  so simultaneous withdrawals on different nodes cannot empty the round-robin
  between them.
- **A peer's verdict outranks a stale one, not a live one.** Records this
  process withdrew are restored as soon as its own probe recovers. A record a
  *peer* disabled (the ring monitor's suspect path) is only reclaimed after it
  has sat untouched for 10 minutes, so a monitor that still considers this node
  suspect keeps it out.

Watch it with:

```bash
journalctl -u orama-node --no-pager | grep -E 'namespace DNS round-robin'
```

## Configuration

Resolves nodes the same way the inspector does — network API first, `nodes.conf`
as the fallback. See [INSPECTOR.md](INSPECTOR.md#configuration) for the file format.

## Prerequisites

Nodes must have the `orama` CLI installed (via `orama node install`, or updated via `orama node push` / `orama node rollout`). The monitor runs `sudo orama node report --json` over SSH, so the binary must be at `/usr/local/bin/orama` on each node.
