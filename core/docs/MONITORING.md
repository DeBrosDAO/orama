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
| `--config` | `scripts/remote-nodes.conf` | Path to node configuration file |

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
    "healthy_count": 3
  },
  "summary": {
    "rqlite_leader": "10.0.0.1",
    "rqlite_voters": "3/3",
    "rqlite_raft_term": 42,
    "wg_mesh_status": "all connected",
    "service_health": "all nominal",
    "critical_alerts": 0,
    "warning_alerts": 1,
    "info_alerts": 0
  },
  "alerts": [...],
  "nodes": [
    {
      "host": "51.195.109.238",
      "status": "healthy",
      "collection_ms": 526,
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
| **gateway** | HTTP health check, subsystem status |
| **wireguard** | Interface state, WG IP, peers, handshake ages, MTU, config permissions |
| **dns** | CoreDNS/Caddy state, port bindings, resolution tests, TLS cert expiry |
| **anyone** | Relay/client state, bootstrap progress, fingerprint |
| **network** | Internet reachability, TCP stats, retransmission rate, listening ports, UFW rules |
| **processes** | Zombie count, orphan orama processes, panic/fatal count in logs |
| **namespaces** | Per-namespace service probes (RQLite, Olric, Gateway) |

### Performance

All 12 collectors run in parallel with goroutines. Typical collection time is **< 1 second** per node. HTTP timeouts are 3 seconds, command timeouts are 4 seconds.

### Output Schema

```json
{
  "timestamp": "2026-02-16T12:00:00Z",
  "hostname": "ns1",
  "version": "0.107.0",
  "collect_ms": 526,
  "errors": [],
  "system": { "cpu_count": 4, "load_avg_1": 0.1, "mem_total_mb": 7937, ... },
  "services": { "services": [...], "failed_units": [] },
  "rqlite": { "responsive": true, "raft_state": "Leader", "term": 42, ... },
  "olric": { "service_active": true, "memberlist_up": true, ... },
  "ipfs": { "daemon_active": true, "swarm_peers": 2, ... },
  "gateway": { "responsive": true, "http_status": 200, ... },
  "wireguard": { "interface_up": true, "wg_ip": "10.0.0.1", "peers": [...], ... },
  "dns": { "coredns_active": true, "caddy_active": true, "base_tls_days_left": 88, ... },
  "anyone": { "relay_active": true, "bootstrapped": true, ... },
  "network": { "internet_reachable": true, "ufw_active": true, ... },
  "processes": { "zombie_count": 0, "orphan_count": 0, "panic_count": 0, ... },
  "namespaces": []
}
```

## Alert Detection

Alerts are derived from cross-node analysis of all collected reports. Each alert has a severity level and identifies the affected subsystem and node.

### Alert Severities

| Severity | Examples |
|----------|----------|
| **critical** | SSH collection failed (node unreachable), no RQLite leader, split brain, RQLite unresponsive, WireGuard interface down, WG peer never handshaked, OOM kills, service failed, UFW inactive |
| **warning** | Strong read failed, memory > 90%, disk > 85%, stale WG handshake (> 3min), Raft term inconsistency, applied index lag > 100, restart loop detected, TLS cert < 14 days, DNS down, namespace gateway down, Anyone not bootstrapped, clock skew > 5s, binary version mismatch, internet unreachable, high TCP retransmission |
| **info** | Zombie processes, orphan orama processes, swap usage > 30% |

### Cross-Node Checks

These checks compare data across all nodes:

- **RQLite Leader**: Exactly one leader exists (no split brain)
- **Leader Agreement**: All nodes agree on the same leader address
- **Raft Term Consistency**: Term values within 1 of each other
- **Applied Index Lag**: Followers within 100 entries of the leader
- **WireGuard Peer Symmetry**: Each node has N-1 peers
- **Clock Skew**: Node clocks within 5 seconds of each other
- **Binary Version**: All nodes running the same version
- **WebRTC SFU Coverage**: SFU running on expected nodes (3/3) per namespace
- **WebRTC TURN Redundancy**: TURN running on expected nodes (2/3) per namespace

### Per-Node Checks

- **RQLite**: Responsive, ready, strong read
- **WireGuard**: Interface up, handshake freshness
- **System**: Memory, disk, load, OOM kills, swap
- **Services**: Systemd state, restart loops
- **DNS**: CoreDNS/Caddy up, TLS cert expiry, SOA resolution
- **Anyone**: Bootstrap progress
- **Processes**: Zombies, orphans, panics in logs
- **Namespaces**: Gateway and RQLite per namespace
- **WebRTC**: SFU and TURN service health (when provisioned)
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

## Configuration

Uses the same `scripts/remote-nodes.conf` as the inspector. See [INSPECTOR.md](INSPECTOR.md#configuration) for format details.

## Prerequisites

Nodes must have the `orama` CLI installed (via `orama node install` or `upload-source.sh`). The monitor runs `sudo orama node report --json` over SSH, so the binary must be at `/usr/local/bin/orama` on each node.
