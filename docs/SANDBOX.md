# Sandbox: Ephemeral Hetzner Cloud Clusters

Spin up temporary 5-node Orama clusters on Hetzner Cloud for development and testing. Cost depends on the server type you pick during setup — a few euro cents per hour for a small shared-vCPU cluster.

## Quick Start

```bash
# One-time setup (API key, domain, floating IPs, SSH key)
orama sandbox setup

# Create a cluster (~5 minutes)
orama sandbox create --name my-feature

# Check health
orama sandbox status

# SSH into a node
orama sandbox ssh 1

# Deploy code changes
orama sandbox rollout

# Tear it down
orama sandbox destroy
```

## Prerequisites

### 1. Hetzner Cloud Account

Create a project at [console.hetzner.cloud](https://console.hetzner.cloud) and generate an API token with read/write permissions under **Security > API Tokens**.

### 2. Domain with Glue Records

You need a domain (or subdomain) that points to Hetzner Floating IPs. The `orama sandbox setup` wizard will guide you through this.

**Example:** Using `sbx.dbrs.space`

At your domain registrar:
1. Create glue records (Personal DNS Servers):
   - `ns1.sbx.dbrs.space` → `<floating-ip-1>`
   - `ns2.sbx.dbrs.space` → `<floating-ip-2>`
2. Set custom nameservers for `sbx.dbrs.space`:
   - `ns1.sbx.dbrs.space`
   - `ns2.sbx.dbrs.space`

DNS propagation can take up to 48 hours.

### 3. Binary Archive

Build the binary archive before creating a cluster:

```bash
orama build
```

This creates `/tmp/orama-<version>-linux-amd64.tar.gz` with all pre-compiled binaries.

## Setup

Run the interactive setup wizard:

```bash
orama sandbox setup
```

This will:
1. Prompt for your Hetzner API token and validate it
2. Ask for your sandbox domain
3. Let you pick a datacenter location (default: `nbg1`)
4. Let you pick a server type from what's available at that location, with prices (default: the cheapest option)
5. Create or reuse 2 Hetzner Floating IPs (~€0.005/hr each)
6. Create a firewall with sandbox rules
7. Create a rootwallet SSH entry (`sandbox/root`) if it doesn't exist and upload the wallet-derived public key to Hetzner
8. Display DNS configuration instructions and optionally verify glue records

Config is saved to `~/.orama/sandbox.yaml`.

## Commands

### `orama sandbox create [--name <name>]`

Creates a new 5-node cluster. If `--name` is omitted, a random name is generated (e.g., "swift-falcon").

**Cluster layout:**
- Nodes 1-2: Nameservers (CoreDNS + Caddy + all services)
- Nodes 3-5: Regular nodes (all services except CoreDNS)

**Phases:**
1. Provision 5 servers on Hetzner using the configured server type (parallel, ~90s)
2. Assign floating IPs to nameserver nodes (~10s)
3. Upload binary archive to all nodes (parallel, ~60s)
4. Install genesis node + generate invite tokens (~120s)
5. Join remaining 4 nodes (serial with health checks, ~180s)
6. Verify cluster health (~15s)

**One sandbox at a time.** Since the floating IPs are shared, only one sandbox can own the nameservers. Destroy the active sandbox before creating a new one.

### `orama sandbox destroy [--name <name>] [--force]`

Tears down a cluster:
1. Unassigns floating IPs
2. Deletes all 5 servers (parallel)
3. Removes state file

Use `--force` to skip confirmation.

### `orama sandbox list`

Lists all sandboxes with their status. Also checks Hetzner for orphaned servers that don't have a corresponding state file.

### `orama sandbox status [--name <name>]`

Shows per-node health including:
- Service status (active/inactive)
- RQLite role (Leader/Follower)
- Cluster summary (commit index, voter count)

### `orama sandbox rollout [--name <name>]`

Deploys code changes:
1. Uses the latest binary archive from `/tmp/` (run `orama build` first)
2. Pushes to all nodes
3. Rolling upgrade: followers first, leader last, 15s between nodes

### `orama sandbox ssh <node-number>`

Opens an interactive SSH session to a sandbox node (1-5).

```bash
orama sandbox ssh 1    # SSH into node 1 (genesis/ns1)
orama sandbox ssh 3    # SSH into node 3 (regular node)
```

## Architecture

### Floating IPs

Hetzner Floating IPs are persistent IPv4 addresses that can be reassigned between servers. They solve the DNS chicken-and-egg problem:

- Glue records at the registrar point to 2 Floating IPs (configured once)
- Each new sandbox assigns the Floating IPs to its nameserver nodes
- DNS works instantly — no propagation delay between clusters

### SSH Authentication

Sandbox uses a rootwallet-derived SSH key (`sandbox/root` vault entry), the same mechanism as production. The RootWallet desktop app must be open and unlocked before running sandbox commands that use SSH; it also prompts to approve access on first use. The public key is uploaded to Hetzner during setup and injected into every server at creation time.

### Server Naming

Servers: `sbx-<name>-<N>` (e.g., `sbx-swift-falcon-1` through `sbx-swift-falcon-5`)

### State Files

Sandbox state is stored at `~/.orama/sandboxes/<name>.yaml`. This tracks server IDs, IPs, roles, and cluster status.

## Cost

| Resource | Cost | Qty |
|----------|------|-----|
| Servers (type chosen during setup) | depends on type | 5 |
| Floating IPv4 | €0.005/hr | 2 |

Servers are billed per hour at the rate for the chosen type (shown during `orama sandbox setup`). Floating IPs are billed as long as they exist (even unassigned). Destroy the sandbox when not in use to save on server costs.

## Troubleshooting

### "sandbox not configured"

Run `orama sandbox setup` first.

### "no binary archive found"

Run `orama build` to create the binary archive.

### "sandbox X is already active"

Only one sandbox can be active at a time. Destroy it first:
```bash
orama sandbox destroy --name <name>
```

### Server creation fails

Check:
- Hetzner API token is valid and has read/write permissions
- You haven't hit Hetzner's server limit (default: 10 per project)
- The selected location has capacity for the configured server type

### Genesis install fails

SSH into the node to debug:
```bash
orama sandbox ssh 1
journalctl -u orama-node -f
```

The sandbox will be left in "error" state. You can destroy and recreate it.

### DNS not resolving

1. Verify glue records are configured at your registrar
2. Check propagation: `dig NS sbx.dbrs.space @8.8.8.8`
3. Propagation can take 24-48 hours for new domains

### Orphaned servers

If `orama sandbox list` shows orphaned servers, delete them manually at [console.hetzner.cloud](https://console.hetzner.cloud). Sandbox servers are labeled `orama-sandbox=<name>` for easy identification.
