# Nameserver Setup Guide

How to make the internet reach an Orama cluster's own nameservers, so its base
domain — and every name under it — is answered by the cluster.

## Overview

When you install Orama with the `--nameserver` flag, `orama-node` starts
`orama-namespace-coredns@nameserver` after index rqlite is up. CoreDNS still
binds `:53` and reads zone data from index RQLite `dns_records` via the
existing `/etc/coredns/Corefile`. The installer writes the rqlite plugin's
`dsn` as this node's WireGuard address (`http://<wg-ip>:10100`, from
`discovery.http_adv_address` in `node.yaml`) with `username`/`password`:
rqlited listens on nothing else and always requires auth. The plugin refuses a
Corefile without `dsn`. This enables:

- Dynamic DNS for deployments (e.g., `myapp.node-abc123.dbrs.space`)
- Wildcard DNS support for all subdomains
- ACME DNS-01 challenges for automatic SSL certificates

For any of that to work, the **parent zone** of the base domain has to delegate
to the cluster: NS records naming the cluster's nameservers, and glue A
records giving their addresses. This guide is about creating those records.

## Prerequisites

1. **A domain you control** — either a whole registered domain (`dbrs.space`)
   or a subdomain of one (`stagenet.dbrsteting.bid`). See
   [Choosing the base domain](#choosing-the-base-domain).
2. **One or more VPS nodes installed with `--nameserver`**, each with a static
   public IPv4 address. Three or more is recommended for redundancy; the
   cluster publishes exactly as many nameservers as it has (see below).
3. **Access to the parent zone** — the registrar's nameserver settings for a
   whole domain, or the DNS zone of the parent domain for a subdomain.

## Understanding DNS Records

### Nameserver slots

Each `--nameserver` node claims a slot — `ns1`, `ns2`, … — for the base domain
the first time its DNS sweep runs (every 30 seconds, once it has registered).
Slots are claimed dynamically, lowest free number first, by whichever node gets
there first, so **you cannot know in advance which address holds which name**.
A node keeps its slot for as long as it keeps heartbeating; a nameserver that
stops for more than two minutes releases it, and a node removed with
`orama node remove` releases it for good.

Each slot holder writes its **glue** record, `nsN.<base>` → its public IP
(`node.public_ip` in `node.yaml`). The zone's own NS set and SOA are derived
from the slots: the cluster publishes an NS record for every slot that has
glue, and nothing else (while no slot is glued at all — every nameserver missed
its heartbeat at once — the last known NS set is kept rather than leaving the
zone with none) — a one-nameserver cluster publishes only `ns1`, a
five-nameserver cluster `ns1`..`ns5`. The SOA names the lowest glued slot as the
primary. Every node re-derives both on its 30-second DNS sweep, so a record set
written any other way is brought back to the slots within one sweep. Install
and upgrade write no zone records at all: a new nameserver's zone appears on its
first sweep after `orama-node` starts. There are at most 13 slots.

A negative answer (NXDOMAIN) carries the zone's own SOA from `dns_records` in
its authority section — the one naming the lowest glued slot. A zone with no SOA
yet (no slot glued) answers SERVFAIL rather than inventing one.

### Seeing which address holds which slot

Read it from the cluster:

```bash
orama node dns delegation --env <env>
```

It prints exactly the records to create at the parent zone, e.g.:

```
; stagenet.dbrsteting.bid — create these in the parent zone dbrsteting.bid: as records in that zone, wherever its DNS is hosted
stagenet.dbrsteting.bid.        IN  NS  ns1.stagenet.dbrsteting.bid.
stagenet.dbrsteting.bid.        IN  NS  ns2.stagenet.dbrsteting.bid.
stagenet.dbrsteting.bid.        IN  NS  ns3.stagenet.dbrsteting.bid.
ns1.stagenet.dbrsteting.bid.    IN  A   203.0.113.10
ns2.stagenet.dbrsteting.bid.    IN  A   203.0.113.11
ns3.stagenet.dbrsteting.bid.    IN  A   203.0.113.12
```

`--json` prints the same as data. It reads the registry over SSH from the
environment's first node, and lists only slots whose glue exists — the same
set the cluster's zone publishes. **Run it again after adding or removing a
nameserver, and update the parent zone to match.**

### NS records and glue

The NS records tell resolvers which servers are authoritative for the base
domain. Because those servers are named *inside* the domain they serve
(`ns1.<base>` serves `<base>`), resolving them would need the very servers they
name — so the parent also carries **glue**: the A record for each nameserver
name, handed out alongside the delegation.

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

## Choosing the base domain

### A subdomain (recommended, and the only option with Cloudflare Registrar)

Delegate a subdomain such as `stagenet.dbrsteting.bid` from the zone of the
domain you registered. The NS and glue records are ordinary records inside the
parent zone, created wherever that zone's DNS is hosted; the registrar is not
involved, and the rest of the domain keeps working as it does.

This is the only way to use a domain registered with **Cloudflare Registrar**:
Cloudflare Registrar does not let a domain it registers use custom
nameservers, so the whole-domain setup below is impossible there. Subdomain
delegation inside the Cloudflare zone works — verified 2026-09-25 with
`stagenet.dbrsteting.bid` delegated from the `dbrsteting.bid` zone.

In the Cloudflare dashboard, for the parent zone (`dbrsteting.bid`) → **DNS** →
**Records**, add for each line `orama node dns delegation` printed:

| Type | Name | Content | Proxy status |
|------|------|---------|--------------|
| NS | `stagenet` | `ns1.stagenet.dbrsteting.bid` | — |
| NS | `stagenet` | `ns2.stagenet.dbrsteting.bid` | — |
| A | `ns1.stagenet` | the IP printed for ns1 | **DNS only** |
| A | `ns2.stagenet` | the IP printed for ns2 | **DNS only** |

The glue A records must be **DNS only** (grey cloud): a proxied record answers
with Cloudflare's addresses, and a resolver would send the cluster's queries
there. Any DNS host works the same way — the records are what matter.

### A whole registered domain

Use the registered domain itself (`dbrs.space`) as the base domain. The NS and
glue records then live at the registry, and are set through the registrar:
glue first (often called *host records*, *child nameservers* or *personal DNS
servers*), then the domain's nameservers switched to the `nsN` names.

- **Namecheap:** Domain List → Manage → **Advanced DNS** → *Personal DNS
  Servers*: add each `nsN.<domain>` with its IP. Then **Domain** →
  *Nameservers* → **Custom DNS**, and enter the `nsN.<domain>` names.
- **GoDaddy:** **DNS** → *Hostnames*: add each `nsN` with its IP. Then
  *Nameservers* → **Change** → *Enter my own nameservers*.
- **Cloudflare Registrar:** not possible — use a subdomain (above).

Registry changes can take hours to propagate (up to 48); most are visible
within 1–4 hours.

## Installation

### Order: delegation before certificates

Every node's HTTPS certificate is issued through ACME DNS-01, and the challenge
record is served by the cluster's own CoreDNS. The certificate authority finds
that CoreDNS through the delegation — so **the genesis node cannot get a
certificate until the parent zone delegates to it**. Caddy keeps retrying, so
nothing needs restarting; it just waits.

The order is therefore:

1. Install the genesis node with `--nameserver`.
2. Once it has registered (its first DNS sweep, ~30 s later) it holds `ns1`.
   Run `orama node dns delegation --env <env>` and create the records.
3. Wait for the delegation to be visible (`dig NS <base> @8.8.8.8`); the
   genesis node's certificate is issued then.
4. Join the other nameservers — the invite pins the genesis node's
   certificate, so it must have one — then run `orama node dns delegation`
   again and add their records.

### Installing the nodes

`orama node setup` provisions a fresh VPS end to end. It reads the VPS
password from your RootWallet vault (`rw vault add <ip>`) — never from the
command line — and installs the archive `orama build` printed:

```bash
# Genesis nameserver — creates the cluster
orama node setup --ip <ip> --password --env <env> --archive <archive path> \
  --base-domain <base-domain> --role nameserver --genesis

# Each further nameserver — joins it
orama node setup --ip <ip> --password --env <env> --archive <archive path> \
  --base-domain <base-domain> --role nameserver
```

To install by hand on the VPS instead, the genesis node is installed without a
token and every other node with an invite (`orama invite` from your machine, or
`sudo orama node invite` on a node already in the cluster):

```bash
# Genesis — creates the cluster
sudo orama node install --nameserver \
  --domain <base-domain> --base-domain <base-domain> --vps-ip <genesis public IP>

# Every other nameserver — joins it; the invite carries the node to join and
# the certificate to pin
sudo orama node install --token <invite> --nameserver \
  --domain <base-domain> --base-domain <base-domain> --vps-ip <this node's public IP>
```

A node installed without a token creates a new cluster — only ever do that for
the genesis node. `--base-domain` sets the base domain used for DNS routing
and the zone CoreDNS serves; if omitted, the installer prompts for it.

## Verification

### Step 1: Verify NS Records

After propagation, check that the delegation is visible and matches what the
cluster says:

```bash
orama node dns delegation --env <env>   # what it should be
dig NS <base-domain> @8.8.8.8            # what the internet sees
```

The two must list the same `nsN` names.

### Step 2: Verify Glue Records

Check that glue records resolve:

```bash
# Check glue records, one per slot the delegation command printed
dig A ns1.<base-domain> @8.8.8.8

# Each should return the IP the delegation command printed for it
```

### Step 3: Test CoreDNS

Query your nameservers directly:

```bash
# Test a query against ns1
dig @ns1.<base-domain> test.<base-domain>

# Test wildcard resolution
dig @ns1.<base-domain> myapp.node-abc123.<base-domain>
```

### Step 4: Verify from Multiple Locations

Use online tools to verify global propagation:
- https://dnschecker.org
- https://www.whatsmydns.net

## Troubleshooting

### DNS Not Resolving

1. **Check CoreDNS is running:**
   ```bash
   sudo orama node status
   ```

2. **Check CoreDNS logs:**
   ```bash
   sudo orama node logs coredns -f
   ```

3. **Verify port 53 is open:**
   ```bash
   sudo ufw status
   # Port 53 (TCP/UDP) should be allowed
   ```

4. **Test locally:**
   ```bash
   dig @localhost <base-domain>
   ```

### Glue Records Not Propagating

- For a whole registered domain, glue is stored at the registry, not in any
  DNS zone, and can take up to 48 hours to propagate. Verify at your registrar
  that it was saved.
- For a subdomain, the glue is an ordinary A record in the parent zone; check
  it is not proxied (Cloudflare: **DNS only**).
- A slot moves when its holder stops heartbeating for two minutes. If a glue
  record names an address that no longer answers, run
  `orama node dns delegation --env <env>` and update the parent zone.

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

The generated Corefile does not enable CoreDNS's `health` or `prometheus` plugins, so there are no CoreDNS health/metrics ports to expose. The recursive block for non-authoritative queries answers only loopback clients (an `acl` allowing 127.0.0.0/8 and ::1), so the node cannot be used as an open recursive resolver. It shares one listener with the authoritative zone rather than binding 127.0.0.1 separately: a socket bound to 127.0.0.1:53 takes every local query, which sent the node's own zone out to a public resolver — and Caddy's ACME DNS-01 propagation check, which resolves through the node, never saw its challenge record.

### Rate Limiting

Consider adding rate limiting to prevent DNS amplification attacks.
This can be configured in the CoreDNS Corefile.

## Multi-Node Coordination

When running multiple nameservers:

1. **All nodes share the same RQLite cluster** - DNS records are automatically synchronized
2. **Install in order** - First node bootstraps, others join with an invite (`--token`)
3. **Same domain configuration** - All nodes must use the same `--domain` and `--base-domain` values
4. **The parent zone follows the slots** - after adding or removing a nameserver, re-run
   `orama node dns delegation --env <env>` and update the parent zone to match

## Related Documentation

- [CoreDNS RQLite Plugin](../core/pkg/coredns/README.md) - Technical details
- [Deployment Guide](./DEPLOYMENT_GUIDE.md) - Full deployment instructions
- [Architecture](./ARCHITECTURE.md) - System architecture overview
