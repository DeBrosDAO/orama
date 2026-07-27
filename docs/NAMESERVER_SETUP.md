# Nameserver Setup Guide

This guide explains how to configure your domain registrar to use Orama Network nodes as authoritative nameservers.

## Overview

When you install Orama with the `--nameserver` flag, the node runs CoreDNS to serve DNS records for your domain. This enables:

- Dynamic DNS for deployments (e.g., `myapp.node-abc123.dbrs.space`)
- Wildcard DNS support for all subdomains
- ACME DNS-01 challenges for automatic SSL certificates

## Prerequisites

Before setting up nameservers, you need:

1. **Domain ownership** - A domain you control (e.g., `dbrs.space`)
2. **3+ VPS nodes** - Recommended for redundancy
3. **Static IP addresses** - Each VPS must have a static public IP
4. **Access to registrar DNS settings** - Admin access to your domain registrar

## Understanding DNS Records

### NS Records (Nameserver Records)
NS records tell the internet which servers are authoritative for your domain:
```
dbrs.space.  IN  NS  ns1.dbrs.space.
dbrs.space.  IN  NS  ns2.dbrs.space.
dbrs.space.  IN  NS  ns3.dbrs.space.
```

### Glue Records
Glue records are A records that provide IP addresses for nameservers that are under the same domain. They're required because:
- `ns1.dbrs.space` is under `dbrs.space`
- To resolve `ns1.dbrs.space`, you need to query `dbrs.space` nameservers
- But those nameservers ARE `ns1.dbrs.space` - circular dependency!
- Glue records break this cycle by providing IPs at the registry level

```
ns1.dbrs.space.  IN  A  141.227.165.168
ns2.dbrs.space.  IN  A  141.227.165.154
ns3.dbrs.space.  IN  A  141.227.156.51
```

### Record Lifecycle and Self-Healing

Per-namespace A records are round-robin sets: `ns-<namespace>.<base>` (the gateway
host — origin of the signaling WebSocket and all RPC), its `*.ns-<namespace>.<base>`
deployment wildcard, `turn.ns-<namespace>.<base>` (plain UDP/TCP TURN),
`turn-<namespace>.<base>` (TURNS), and the stealth TURNS host. Each carries one A
record per node serving that role.

These are created at provision / WebRTC-enable time, so two reconcilers keep them
true as the topology changes. Both run from **every** node — they are per-node and
idempotent, so no leader election is needed:

| Reconciler | When | What it does |
|---|---|---|
| Ensure (re-advertise) | Every 30s sweep, per hosted namespace | Additively inserts **this node's own** A record if absent, for every namespace where it is a `running` `gateway`. Never touches another node's record, and never re-enables a record that recovery deliberately disabled. Runs immediately after the heartbeat re-asserts `active`, so a just-recovered node is no longer purge-eligible when it re-advertises. |
| Purge | Every 30s DNS sweep | Deletes A records whose value is a node that is non-active **and** silent longer than the staleness window (15 min). |

Two safety properties matter:

- **The staleness window** is far longer than the 120s active→inactive threshold.
  That flag flips on a transient blip (rolling restart, brief rqlite unavailability),
  and a blip must never delete records.
- **The gateway-host purge never empties a name.** A record is removed only when the
  same FQDN still has another *resolvable* (`is_active = TRUE`) record. Without that
  guard, a cluster-wide heartbeat failure could delete every record for a namespace
  host — and the result would not be a clean failure: the resolver rewrites a
  3-label miss to the base wildcard, and `*.<base>` exists, so `ns-<namespace>.<base>`
  would silently fall through to the nameserver nodes, which may not host that
  namespace. Clients would connect, pass TLS on the wildcard cert, and reach the
  wrong backend. A silent misroute is harder to diagnose than an outright failure.
  The TURN purge is deliberately *not* guarded this way, on recoverability grounds:
  each live TURN node re-advertises itself on boot, so an over-purged TURN host
  repopulates, and for a relay-only client a host that resolves nowhere is no worse
  than one resolving to a dead node — ICE falls through to the next server either way.

If a namespace host is down to a single record pointing at a departed node, the
guard keeps it — deletion would take the namespace fully offline. That state is
resolved by adding a replacement node, which re-advertises itself on the next sweep.

## Installation

### Step 1: Install Orama on Each VPS

Install Orama with `orama node install` and the `--nameserver` flag on each VPS that will serve as a nameserver. The first node creates the cluster; every subsequent node must join it with `--join` and `--token`, otherwise each install bootstraps its own separate cluster.

```bash
# On VPS 1 (ns1) — first node, creates the cluster
sudo orama node install \
  --nameserver \
  --domain dbrs.space \
  --base-domain dbrs.space \
  --vps-ip 141.227.165.168

# On ns1, generate an invite token for each joining node
orama node invite --expiry 1h

# On VPS 2 (ns2) — joins the existing cluster
sudo orama node install \
  --join http://141.227.165.168 \
  --token <invite-token> \
  --nameserver \
  --domain dbrs.space \
  --base-domain dbrs.space \
  --vps-ip 141.227.165.154

# On VPS 3 (ns3) — joins the existing cluster (generate a fresh token)
sudo orama node install \
  --join http://141.227.165.168 \
  --token <invite-token> \
  --nameserver \
  --domain dbrs.space \
  --base-domain dbrs.space \
  --vps-ip 141.227.156.51
```

`--base-domain` sets the base domain used for DNS routing and record seeding; if omitted, the installer prompts for it interactively.

Alternatively, `orama node setup` provisions a fresh VPS end-to-end (SSH key, binary upload, install) in one command:

```bash
# Genesis nameserver
orama node setup --ip 141.227.165.168 --password '<vps-pass>' --env devnet \
  --base-domain dbrs.space --role nameserver --genesis

# Join as nameserver
orama node setup --ip 141.227.165.154 --password '<vps-pass>' --env devnet \
  --base-domain dbrs.space --role nameserver
```

### Step 2: Configure Your Registrar

#### For Namecheap

1. **Log into Namecheap Dashboard**
   - Go to https://www.namecheap.com
   - Navigate to **Domain List** → **Manage** (next to your domain)

2. **Add Glue Records (Personal DNS Servers)**
   - Go to **Advanced DNS** tab
   - Scroll down to **Personal DNS Servers** section
   - Click **Add Nameserver**
   - Add each nameserver with its IP:
     | Nameserver | IP Address |
     |------------|------------|
     | ns1.yourdomain.com | 141.227.165.168 |
     | ns2.yourdomain.com | 141.227.165.154 |
     | ns3.yourdomain.com | 141.227.156.51 |

3. **Set Custom Nameservers**
   - Go back to the **Domain** tab
   - Under **Nameservers**, select **Custom DNS**
   - Add your nameserver hostnames:
     - ns1.yourdomain.com
     - ns2.yourdomain.com
     - ns3.yourdomain.com
   - Click the green checkmark to save

4. **Wait for Propagation**
   - DNS changes can take 24-48 hours to propagate globally
   - Most changes are visible within 1-4 hours

#### For GoDaddy

1. Log into GoDaddy account
2. Go to **My Products** → **DNS** for your domain
3. Under **Nameservers**, click **Change**
4. Select **Enter my own nameservers**
5. Add your nameserver hostnames
6. For glue records, go to **DNS Management** → **Host Names**
7. Add A records for ns1, ns2, ns3

#### For Cloudflare (as Registrar)

1. Log into Cloudflare Dashboard
2. Go to **Domain Registration** → your domain
3. Under **Nameservers**, change to custom
4. Note: Cloudflare Registrar may require contacting support for glue records

#### For Google Domains

1. Log into Google Domains
2. Select your domain → **DNS**
3. Under **Name servers**, select **Use custom name servers**
4. Add your nameserver hostnames
5. For glue records, click **Add** under **Glue records**

## Verification

### Step 1: Verify NS Records

After propagation, check that NS records are visible:

```bash
# Check NS records from Google DNS
dig NS yourdomain.com @8.8.8.8

# Expected output should show:
# yourdomain.com.    IN  NS  ns1.yourdomain.com.
# yourdomain.com.    IN  NS  ns2.yourdomain.com.
# yourdomain.com.    IN  NS  ns3.yourdomain.com.
```

### Step 2: Verify Glue Records

Check that glue records resolve:

```bash
# Check glue records
dig A ns1.yourdomain.com @8.8.8.8
dig A ns2.yourdomain.com @8.8.8.8
dig A ns3.yourdomain.com @8.8.8.8

# Each should return the correct IP address
```

### Step 3: Test CoreDNS

Query your nameservers directly:

```bash
# Test a query against ns1
dig @ns1.yourdomain.com test.yourdomain.com

# Test wildcard resolution
dig @ns1.yourdomain.com myapp.node-abc123.yourdomain.com
```

### Step 4: Verify from Multiple Locations

Use online tools to verify global propagation:
- https://dnschecker.org
- https://www.whatsmydns.net

## Troubleshooting

### DNS Not Resolving

1. **Check CoreDNS is running:**
   ```bash
   sudo systemctl status coredns
   ```

2. **Check CoreDNS logs:**
   ```bash
   sudo journalctl -u coredns -f
   ```

3. **Verify port 53 is open:**
   ```bash
   sudo ufw status
   # Port 53 (TCP/UDP) should be allowed
   ```

4. **Test locally:**
   ```bash
   dig @localhost yourdomain.com
   ```

### Glue Records Not Propagating

- Glue records are stored at the registry level, not DNS level
- They can take longer to propagate (up to 48 hours)
- Verify at your registrar that they were saved correctly
- Some registrars require the domain to be using their nameservers first

### SERVFAIL Errors

Usually indicates CoreDNS configuration issues:

1. Check Corefile syntax
2. Verify RQLite connectivity
3. Check firewall rules

## Security Considerations

### Firewall Rules

Only expose necessary ports:

```bash
# Allow DNS from anywhere
sudo ufw allow 53/tcp
sudo ufw allow 53/udp
```

The generated Corefile does not enable CoreDNS's `health` or `prometheus` plugins, so there are no CoreDNS health/metrics ports to expose. The forward block for non-authoritative queries is bound to 127.0.0.1, so the node cannot be used as an open recursive resolver.

### Rate Limiting

Consider adding rate limiting to prevent DNS amplification attacks.
This can be configured in the CoreDNS Corefile.

## Multi-Node Coordination

When running multiple nameservers:

1. **All nodes share the same RQLite cluster** - DNS records are automatically synchronized
2. **Install in order** - First node bootstraps, others join with `--join` and `--token`
3. **Same domain configuration** - All nodes must use the same `--domain` and `--base-domain` values

## Related Documentation

- [CoreDNS RQLite Plugin](../core/pkg/coredns/README.md) - Technical details
- [Deployment Guide](./DEPLOYMENT_GUIDE.md) - Full deployment instructions
- [Architecture](./ARCHITECTURE.md) - System architecture overview
