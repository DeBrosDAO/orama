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

### 3. Binary Archive and RootWallet

`create` and `rollout` take `--archive <path>` (the path `orama build` printed), or build this checkout themselves. Either way the archive must be signed by the RootWallet account that is unlocked when you run them: `orama build` signs through the RootWallet agent, and that account is the only signer a sandbox trusts. An archive signed by anyone else, or unsigned (`--unsigned`), is refused before any server is created.

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

### `orama sandbox create [--name <name>] [--archive <path>]`

Creates a new 5-node cluster. If `--name` is omitted, a random name is generated (e.g., "swift-falcon"). A name is lowercase letters, digits and `-`, at most 40 characters.

**Cluster layout:**
- Nodes 1-2: Nameservers (CoreDNS + Caddy + all services)
- Nodes 3-5: Regular nodes (all services except CoreDNS)

`--archive <path>` names the build to deploy; without it, this checkout is built (and signed) first. Before any server is created, the archive must verify against your RootWallet account: it is signed by `orama build` through the RootWallet agent, and that account becomes the cluster's only archive signer.

**Phases:**
1. Provision 5 servers on Hetzner using the configured server type (parallel, ~90s), then pin each server's SSH host key as soon as its sshd answers, before anything is sent to it
2. Assign floating IPs to nameserver nodes (~10s)
3. Put the verified archive on every server the way `orama node setup` does: verified on your machine, uploaded as a canonical re-pack, and staged by `orama node stage-archive`, which creates the server's trust anchor (`/etc/orama/archive-signers`) from your wallet. The archive is uploaded from your machine to each server in turn
4. Install the genesis node with `--operator-wallet <your wallet>` and `--acme-ca letsencrypt-staging`, and wait until it serves a TLS certificate for the sandbox domain, which each invite pins (~120s)
5. Join remaining 4 nodes (serial with health checks, ~180s), each with an invite minted on genesis just before use and `--expect-archive-signers <your wallet>`
6. Verify cluster health (~15s)

**One sandbox at a time.** Since the floating IPs are shared, only one sandbox can own the nameservers. Destroy the active sandbox before creating a new one.

### `orama sandbox destroy [--name <name>] [--force]`

Tears down a cluster:
1. Unassigns floating IPs
2. Deletes all 5 servers (parallel)
3. Removes the state file and the sandbox's pinned host keys

Use `--force` to skip confirmation.

### `orama sandbox list`

Lists all sandboxes with their status. Also checks Hetzner for orphaned servers that don't have a corresponding state file.

### `orama sandbox status [--name <name>]`

Shows per-node health including:
- Service status (active/inactive)
- RQLite role (Leader/Follower)
- Cluster summary (commit index, voter count)

### `orama sandbox rollout [--name <name>] [--archive <path>]`

Deploys code changes:
1. Uses `--archive <path>`, or builds (and signs) this checkout
2. Pushes to all nodes the way `orama push` does: to the first node, which fans it out; each node verifies it with its installed `orama node stage-archive` against its trust anchor before anything under `/opt/orama` changes
3. Rolling upgrade with `orama node upgrade --restart`: followers first, leader last, 15s between nodes

A sandbox created before host keys were pinned has no `~/.orama/sandboxes/<name>.known_hosts` and is refused; destroy it and create a new one.

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

Nodes register under the `sandbox` environment (`orama node install --environment sandbox`) with your wallet as their operator.

### TLS: Let's Encrypt staging

Sandbox nodes get their certificates from Let's Encrypt's staging CA (`--acme-ca letsencrypt-staging`): a sandbox is rebuilt often, and production allows five certificates a week for the same names. No system trusts staging, so `create` records the `sandbox` environment (and switches to it) with `--ca-file` set to `~/.orama/sandboxes/letsencrypt-staging-roots.pem`: the CLI trusts those roots for the sandbox domain and names under it only, never for any other host. The roots — "(STAGING) Pretend Pear X1" and "(STAGING) Bogus Broccoli X2", from https://letsencrypt.org/docs/staging-environment/ — are built into `orama` and checked against their pinned SHA-256 fingerprints before they are written; nothing is fetched at create time. Browsers and other tools will not trust a sandbox's certificate.

### Host keys

The SSH host keys each server presents on first contact (trust on first use) are pinned in `~/.orama/sandboxes/<name>.known_hosts` — not in your `~/.ssh/known_hosts`, because Hetzner reuses addresses. They are read with `ssh-keyscan` right after the server is created, as soon as its sshd answers and before any command, archive or invite is sent to it; cloud-init writes the host keys before sshd starts. `create`'s floating-IP setup, archive uploads, staging, invites, installs and health waits, and `rollout`'s push to the first node, leader detection and upgrades check against them. The copies the first node fans out to the others use its own known_hosts, as `orama push` does; each node verifies the archive against its trust anchor regardless. `status`, `ssh` and create's final health report do not check host keys. See [SECURITY.md](SECURITY.md), "Build-archive signing and the trust anchor".

## Cost

| Resource | Cost | Qty |
|----------|------|-----|
| Servers (type chosen during setup) | depends on type | 5 |
| Floating IPv4 | €0.005/hr | 2 |

Servers are billed per hour at the rate for the chosen type (shown during `orama sandbox setup`). Floating IPs are billed as long as they exist (even unassigned). Destroy the sandbox when not in use to save on server costs.

## Troubleshooting

### "sandbox not configured"

Run `orama sandbox setup` first.

### "the archive does not verify against your wallet"

The archive is unsigned or signed by another RootWallet account than the one unlocked now. Rebuild it with `orama build` while the account you create sandboxes with is unlocked, or leave out `--archive` to build this checkout.

### "has no pinned SSH host keys"

The sandbox was created before sandboxes pinned host keys and verified archives. Destroy it and create a new one.

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
sudo orama node logs node -f
```

The sandbox will be left in "error" state. You can destroy and recreate it.

### DNS not resolving

1. Verify glue records are configured at your registrar
2. Check propagation: `dig NS sbx.dbrs.space @8.8.8.8`
3. Propagation can take 24-48 hours for new domains

### Orphaned servers

If `orama sandbox list` shows orphaned servers, delete them manually at [console.hetzner.cloud](https://console.hetzner.cloud). Sandbox servers are labeled `orama-sandbox=<name>` for easy identification.
