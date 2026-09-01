# Inspector

The inspector is a cluster health check tool that SSHs into every node, collects subsystem data in parallel, runs deterministic checks, and optionally sends failures to an AI model for root-cause analysis.

## Pipeline

```
Collect (parallel SSH) → Check (deterministic Go) → Report (table/JSON) → Analyze (optional AI)
```

1. **Collect** — SSH into every node in parallel, run diagnostic commands, parse results into structured data.
2. **Check** — Run pure Go check functions against the collected data. Each check produces a pass/fail/warn/skip result with a severity level.
3. **Report** — Print results as a table (default) or JSON. Failures sort first, grouped by subsystem.
4. **Analyze** — If `--ai` is enabled and there are failures or warnings, send them to an LLM via OpenRouter for root-cause analysis.

## Quick Start

```bash
# Inspect all subsystems on devnet
orama inspect --env devnet

# Inspect only RQLite
orama inspect --env devnet --subsystem rqlite

# JSON output
orama inspect --env devnet --format json

# With AI analysis
orama inspect --env devnet --ai
```

## Usage

```
orama inspect [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `scripts/nodes.conf` | Path to node configuration file |
| `--env` | *(required)* | Environment to inspect (`devnet`, `testnet`) |
| `--subsystem` | `all` | Comma-separated subsystems to inspect |
| `--format` | `table` | Output format: `table` or `json` |
| `--timeout` | `30s` | SSH command timeout per node |
| `--verbose` | `false` | Print collection progress |
| `--output` | *(none)* | Save results to a directory as markdown (e.g., `./results`) |
| `--ai` | `false` | Enable AI analysis of failures |
| `--model` | `moonshotai/kimi-k2.5` | OpenRouter model for AI analysis |
| `--api-key` | `$OPENROUTER_API_KEY` | OpenRouter API key |

### Subsystem Names

`rqlite`, `olric`, `ipfs`, `dns`, `wireguard` (alias: `wg`), `system`, `network`, `namespace`, `anyone`, `webrtc`

Multiple subsystems can be combined: `--subsystem rqlite,olric,dns`

## Subsystems

| Subsystem | What It Checks |
|-----------|---------------|
| **rqlite** | Raft state, leader election, readyz, commit/applied gap, FSM pending, strong reads, debug vars (query errors, leader_not_found, snapshots), cross-node leader agreement, term consistency, applied index convergence, quorum, version match |
| **olric** | Service active, memberlist up, restart count, memory usage, log analysis (suspects, flapping, errors), cross-node memberlist consistency |
| **ipfs** | Daemon active, cluster active, swarm peer count, cluster peer count, cluster errors, repo usage %, swarm key present, bootstrap list empty, cross-node version consistency |
| **dns** | CoreDNS active, Caddy active, ports (53/80/443), memory, restart count, log errors, Corefile exists, SOA/NS/wildcard/base-A resolution, TLS cert expiry, cross-node nameserver availability |
| **wireguard** | Interface up, service active, correct 10.0.0.x IP, listen port 51820, peer count vs expected, MTU 1420, config exists + permissions 600, peer handshakes (fresh/stale/never), peer traffic, catch-all route detection, cross-node peer count + MTU consistency |
| **system** | Core services (`orama-node`, `orama-namespace-{olric,ipfs,ipfs-cluster,caddy,wireguard}@index`; leftover unit names still accepted during rolling upgrade), nameserver CoreDNS, failed systemd units, memory/disk/inode usage, load average, OOM kills, swap, UFW active, process user (orama), panic count, expected ports |
| **network** | Internet reachability, default route, WireGuard route, TCP connection count, TIME_WAIT count, TCP retransmission rate, WireGuard mesh ping (all peers) |
| **namespace** | Per-namespace: RQLite up + raft state + readyz, Olric memberlist, Gateway HTTP health. Cross-namespace: all-healthy check, RQLite quorum per namespace |
| **anyone** | Anyone client: SOCKS5 `:9050` + control `:9051`, bootstrap %. Leftover `orama-anyone-relay` is a warning. Skipped on nodes with neither unit |
| **webrtc** | Per-namespace SFU/TURN services active, cross-node SFU coverage (3 nodes) and TURN redundancy (2 nodes). Only applies to namespaces with WebRTC provisioned |

## Severity Levels

| Level | When Used |
|-------|-----------|
| **CRITICAL** | Service completely down. Raft quorum lost, RQLite unresponsive, no leader. |
| **HIGH** | Service degraded. Olric down, gateway not responding, IPFS swarm key missing. |
| **MEDIUM** | Non-ideal but functional. Stale handshakes, elevated memory, log suspects. |
| **LOW** | Informational. Non-standard MTU, port mismatch, version skew. |

## Check Statuses

| Status | Meaning |
|--------|---------|
| **pass** | Check passed. |
| **fail** | Check failed — action needed. |
| **warn** | Degraded — monitor or investigate. |
| **skip** | Check could not run (insufficient data). |

## Output Formats

### Table (default)

```
Inspecting 14 devnet nodes...

## RQLITE
----------------------------------------------------------------------
  OK [CRITICAL] RQLite responding (ubuntu@10.0.0.1)
    responsive=true version=v8.36.16
  FAIL [CRITICAL] Cluster has exactly one leader
    leaders=0 (NO LEADER)
  ...

======================================================================
Summary: 800 passed, 12 failed, 31 warnings, 0 skipped (4.2s)
```

Failures sort first, then warnings, then passes. Within each group, higher severity checks appear first.

### JSON (`--format json`)

```json
{
  "summary": {
    "passed": 800,
    "failed": 12,
    "warned": 31,
    "skipped": 0,
    "total": 843,
    "duration_seconds": 4.2
  },
  "checks": [
    {
      "id": "rqlite.responsive",
      "name": "RQLite responding",
      "subsystem": "rqlite",
      "severity": 3,
      "status": "pass",
      "message": "responsive=true version=v8.36.16",
      "node": "ubuntu@10.0.0.1"
    }
  ]
}
```

## AI Analysis

When `--ai` is enabled, failures and warnings are sent to an LLM via OpenRouter for root-cause analysis.

```bash
# Use default model (kimi-k2.5)
orama inspect --env devnet --ai

# Use a different model
orama inspect --env devnet --ai --model openai/gpt-4o

# Pass API key directly
orama inspect --env devnet --ai --api-key sk-or-...
```

The API key can be set via:
1. `--api-key` flag
2. `OPENROUTER_API_KEY` environment variable
3. `.env` file in the current directory

The AI receives the full check results plus cluster metadata and returns a structured analysis with likely root causes and suggested fixes.

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | All checks passed (or only warnings). |
| `1` | At least one check failed. |

## Configuration

The inspector reads node definitions from a pipe-delimited config file (default: `scripts/nodes.conf`).

### Format

```
# environment|user@host|role
devnet|ubuntu@1.2.3.4|node
devnet|ubuntu@5.6.7.8|nameserver-ns1
```

| Field | Description |
|-------|-------------|
| `environment` | Cluster name (`devnet`, `testnet`) |
| `user@host` | SSH credentials |
| `role` | `node` or `nameserver-ns1`, `nameserver-ns2`, etc. |

SSH keys are resolved from rootwallet (`rw vault ssh get <host>/<user> --priv`).

Blank lines and lines starting with `#` are ignored.

### Node Roles

- **`node`** — Regular cluster node. Runs RQLite, Olric, IPFS, WireGuard, namespaces.
- **`nameserver-*`** — DNS nameserver. Runs CoreDNS + Caddy in addition to base services. System checks verify nameserver-specific services.

## Examples

```bash
# Full cluster inspection
orama inspect --env devnet

# Check only networking
orama inspect --env devnet --subsystem wireguard,network

# Quick RQLite health check
orama inspect --env devnet --subsystem rqlite

# Verbose mode (shows collection progress)
orama inspect --env devnet --verbose

# JSON for scripting / piping
orama inspect --env devnet --format json | jq '.checks[] | select(.status == "fail")'

# AI-assisted debugging
orama inspect --env devnet --ai --model anthropic/claude-sonnet-4

# Custom config file
orama inspect --config /path/to/nodes.conf --env testnet
```
