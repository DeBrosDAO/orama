# Node Replacement Runbook (Nameserver VPS Swap)

How to replace **one** Orama nameserver VPS with a new machine **without** losing
platform Raft quorum — and without breaking namespaces like `anchat-test`.

Written from the **devnet cutover on 2026-08-03** (replaced old ns3 `51.38.128.56`
with Contabo `169.58.118.206`). Use the same process for **testnet**.

Related docs:

- [DEV_DEPLOY.md](DEV_DEPLOY.md) — install, join, rolling upgrades, recover-raft
- [DEVNET_INSTALL.md](DEVNET_INSTALL.md) — install command examples
- [CLEAN_NODE.md](CLEAN_NODE.md) — full wipe of a VPS after it leaves the cluster
- [NAMESERVER_SETUP.md](NAMESERVER_SETUP.md) — NS / glue DNS
- [COMMON_PROBLEMS.md](COMMON_PROBLEMS.md) — WireGuard / Olric / vault issues

Inventory file: `core/scripts/nodes.conf`

---

## Golden rules (do not skip)

1. **Join the new node first. Never clean/remove the old node first.**
2. **Only replace one platform voter at a time.** Quorum on 3 voters = 2.
3. **Prefer replacing a follower, not the Raft leader.**
4. **Platform health ≠ namespace health.** Namespaces have their own RQLite/Olric/gateway.
5. **Do not point namespace DNS at a node that is not running that namespace.**
6. **Drive ops with `orama` CLI / documented recovery.** Avoid parallel restarts.
7. **Match binary version** to the live cluster (`/opt/orama/manifest.json`).
8. **Wait for the new node to fully catch up** (applied index, `voter: true` on both sides) before removing anyone.
9. **Platform join ≠ serverless-ready.** A new node starts with an **empty IPFS blob store**. Function WASM CIDs live in IPFS, not only in RQLite. Until you **backfill/pin every active function WASM** onto every peer that may serve invokes (including the new node), cold nodes will 15s-timeout on `ipfs cat` and deploys can 504 on upload — see [bugboard #167](https://bugboard.ai) / `docs/COMMON_PROBLEMS.md`.

---

## Mental model

```
                    ┌─────────────────────────────────────┐
  Public clients ──►│  Caddy :443  →  platform gateway     │  (each nameserver)
                    │  reverse_proxy → localhost:10104     │
                    └──────────────┬──────────────────────┘
                                   │ proxies to
                                   ▼
                    ┌─────────────────────────────────────┐
                    │  Namespace gateways (:10004, …)     │
                    │  Namespace RQLite / Olric / SFU     │  ← separate Raft/cluster
                    └─────────────────────────────────────┘

  Platform RQLite (port 10100 / raft 10101) = cluster membership, DNS DB, routing
  Namespace RQLite (e.g. 10000/10001)     = that namespace's app data
```

Replacing a **nameserver VPS** touches:

| Layer | What changes |
|-------|----------------|
| Platform Raft | Add new voter, remove old voter |
| WireGuard | New peer `10.0.0.x` |
| Zone DNS (`ns1`/`ns2`/`ns3`, apex) | A records for the NS slot |
| **IPFS / IPFS-Cluster** | New peer joins with an **empty local Kubo repo** — backfill function WASM (see below) |
| **Each namespace** hosted on the old IP | Must rebalance / recover — **not automatic enough** |
| Registrar glue (optional) | Parent-zone glue for `nsN` if you control it |

---

## Preflight checklist

### 1. Identify the three nameservers

```bash
# From laptop
cat core/scripts/nodes.conf | grep '^devnet\|^testnet'

# Live Raft (run on any healthy node)
curl -sS http://127.0.0.1:10100/nodes | python3 -m json.tool
curl -sS http://127.0.0.1:10100/status | python3 -c \
  "import sys,json; r=json.load(sys.stdin)['store']['raft']; print(r.get('state'), r.get('num_peers'))"
```

Pick:

- **Victim** = follower (not `leader: true`)
- **Join hub** = current leader or any healthy nameserver public IP
- **New VPS** = fresh Ubuntu, root/ubuntu SSH, enough RAM/disk (devnet boxes were ~12 vCPU / 45 GB / 290 GB; Contabo was smaller but worked)

### 2. Confirm env health

```bash
curl -sS https://orama-<env>.network/v1/health   # or your gateway_url
# expect status healthy

# For critical namespaces:
curl -sS https://ns-<name>.orama-<env>.network/v1/health
```

### 3. List which namespaces run on the victim

On each nameserver:

```bash
systemctl list-units 'orama-namespace-*' --no-pager --state=running
```

In platform RQLite (auth: `orama` + `/opt/orama/.orama/secrets/rqlite-password`):

```sql
-- namespace_cluster_nodes joined to dns_nodes
SELECT ncn.role, ncn.status, dn.ip_address, dn.internal_ip, dn.status
FROM namespace_cluster_nodes ncn
JOIN namespace_clusters nc ON nc.id = ncn.namespace_cluster_id
LEFT JOIN dns_nodes dn ON dn.id = ncn.node_id
WHERE nc.namespace_name = 'anchat-test';  -- repeat per namespace
```

If the victim is a **namespace RQLite voter**, removing it without recovery can leave the namespace at **1/3 voters → no leader**. Plan namespace recovery **before** Raft-remove.

### 4. Match install binary version

```bash
# On an existing node
cat /opt/orama/manifest.json   # version, e.g. 0.122.99

# Locally you need the same archive, e.g.
# /tmp/orama-0.122.99-linux-amd64.tar.gz
# or: orama build  (then use the produced archive)
```

### 5. Secrets / SSH

- Prefer SSH key on the new VPS (`debros-nodes` or rootwallet vault).
- `orama node setup` needs unlocked rootwallet; manual path below does not.

---

## Phase A — Join new node (old node stays fully up)

### A1. Invite token (on an existing installed node)

```bash
sudo /opt/orama/bin/orama node invite --expiry 2h
```

Save:

- `--token …`
- `--ca-fingerprint …` (if printed)
- Join URL hint (HTTPS domain or `http://<hub-public-ip>`)

Tokens are **single-use**. Generate one per join.

### A2. Bootstrap new VPS

```bash
# On new VPS as root
mkdir -p /opt/orama
tar -xzf /tmp/orama-<version>-linux-amd64.tar.gz -C /opt/orama
install -m 755 /opt/orama/bin/orama /usr/local/bin/orama

# Stop Docker if present (port fights with IPFS)
systemctl stop docker docker.socket 2>/dev/null || true
systemctl disable docker docker.socket 2>/dev/null || true
```

### A3. Install as nameserver joining the cluster

```bash
sudo orama node install \
  --join http://<HUB_PUBLIC_IP> \
  --token <TOKEN> \
  --ca-fingerprint <FP> \
  --vps-ip <NEW_PUBLIC_IP> \
  --domain <base-domain> \
  --base-domain <base-domain> \
  --nameserver \
  --environment <devnet|testnet> \
  --ssh-user ubuntu
```

Notes:

- Prefer **`http://<hub-ip>`** if DNS/TLS is flaky during cutover (docs allow this).
- Never join via `:10104` (blocked by UFW).
- Installer may warn that the base domain does not yet resolve to the new IP — expected until DNS update.
- Assigned WG IP example: `10.0.0.17`.

### A4. Wait until fully synced (mandatory gate)

On **leader**:

```bash
curl -sS http://127.0.0.1:10100/nodes | python3 -m json.tool
# NEW wg address must show: voter true, reachable true
```

On **new node**:

```bash
curl -sS http://127.0.0.1:10100/status | python3 -c "
import sys,json
r=json.load(sys.stdin)['store']['raft']
print('state', r.get('state'), 'voter', r.get('voter'),
      'peers', r.get('num_peers'), 'applied', r.get('applied_index'))
"
# Need: voter True, peers >= 3 (for 4-node temp), applied_index catching leader
```

Also:

```bash
# From leader
ping -c 2 10.0.0.<new>
systemctl is-active orama-node coredns caddy
curl -sS http://127.0.0.1:10104/health   # or /v1/health
```

**Do not continue until the new node is a healthy platform voter with a non-zero applied index.** First minutes often show `voter: false` / empty `/nodes` while snapshotting — wait (can take several minutes on large DBs).

Temporary topology: **4 platform voters**. Quorum = 3. Still safe.

---

## Phase B — DNS (platform zone)

Auth for RQLite:

```bash
PASS=$(sudo cat /opt/orama/.orama/secrets/rqlite-password)
AUTH="orama:$PASS"
```

### B1. Inspect current NS / apex records

```bash
curl -sS -u "$AUTH" -G 'http://127.0.0.1:10100/db/query' \
  --data-urlencode "q=SELECT id,fqdn,value,is_active FROM dns_records WHERE fqdn IN (
    'ns1.orama-devnet.network.','ns2.orama-devnet.network.','ns3.orama-devnet.network.',
    'orama-devnet.network.','*.orama-devnet.network.'
  ) ORDER BY fqdn,value"
```

(Adjust domain for testnet.)

Also:

```bash
curl -sS -u "$AUTH" -G 'http://127.0.0.1:10100/db/query' \
  --data-urlencode "q=SELECT * FROM dns_nameservers"
```

### B2. Point the replaced NS slot at the new public IP

Example: replacing **ns3**:

```sql
-- ns3 A → new IP
UPDATE dns_records SET value='<NEW_PUBLIC_IP>', updated_at=CURRENT_TIMESTAMP
  WHERE fqdn='ns3.<domain>.' AND record_type='A' AND value='<OLD_PUBLIC_IP>';

-- remove accidental dual A records on ns3
DELETE FROM dns_records WHERE fqdn='ns3.<domain>.' AND value NOT IN ('<NEW_PUBLIC_IP>');

-- apex + wildcard: replace old IP with new
UPDATE dns_records SET value='<NEW_PUBLIC_IP>', updated_at=CURRENT_TIMESTAMP
  WHERE value='<OLD_PUBLIC_IP>'
    AND fqdn IN ('<domain>.','*.<domain>.','push.<domain>.')
    AND is_active=1;

-- dns_nameservers table
UPDATE dns_nameservers
  SET ip_address='<NEW_PUBLIC_IP>',
      node_id='<NEW_LIBP2P_NODE_ID>',
      updated_at=CURRENT_TIMESTAMP
  WHERE hostname='ns3';
```

Execute via:

```bash
curl -sS -u "$AUTH" -X POST 'http://127.0.0.1:10100/db/execute?pretty' \
  -H 'Content-Type: application/json' \
  -d '["<SQL>"]'
```

Verify **authoritative** (not only public cache):

```bash
dig +short A ns3.<domain> @<ns1-public-ip>
# expect NEW_PUBLIC_IP only
```

### B3. Registrar glue (if applicable)

If the domain registry has glue for `ns3.<domain>` → old IP, update to new IP.
Zone DNS alone is not always enough for resolvers that only have glue.

---

## Phase C — Namespace safety (the part that bit us on devnet)

### Problem

Namespace DNS A records (`ns-<namespace>.…`) were bulk-updated when we rewrote every row with `value=<old_ip>`. That made clients hit the **new** public IP, which was a platform nameserver but **did not run** `orama-namespace-*@<ns>` units → TLS timeouts + platform circuit breakers (`all upstream circuits are open`).

Also, **namespace RQLite** still listed dead voters (`10.0.0.6`, `10.0.0.11`). After removing the last peer, membership became **1/3 → Candidate, no leader**.

**IPFS content re-replicates on its own.** A pin fixes its replication factor
at pin time and nothing revisited the allocation, so every CID a discarded node
held stayed below RF permanently and a node joining later received nothing. One
gateway per 15 minutes — elected through the `ipfs-pin-sweep` cluster lock —
now walks the pin inventory and re-issues the pin for anything short, which is
what forces ipfs-cluster to re-allocate onto live peers. Content already at its
factor is left alone.

**The tenant reconciler now does this.** Every node converges its own namespace
services on a 60s loop, and one elected member per namespace prunes departed
members, releases their ports and removes them from that namespace's raft. So
this phase is now **verification**, not a set of steps: the survivors'
`memberlist.peers` and `olric_servers` drop a departed node on the next sweep,
and the namespace raft loses it once it is past the staleness cutoff. What
follows is what to check, and what to do if the reconciler has not converged.

### Safe DNS rule for namespaces

**Only advertise IPs that currently run that namespace's gateway.**

**Nodes now do this themselves.** Each node probes the namespaces it hosts every
30s and, after 3 consecutive unhealthy probes (~90s), withdraws its own
`ns-<ns>` and `*.ns-<ns>` records. It restores them after 3 consecutive healthy
probes. A withdrawal never removes the **last** active record for a name —
advertising a node that might still answer beats having no answer at all — and
the guard is evaluated inside the UPDATE, so two nodes withdrawing at the same
moment cannot both believe they are not the last.

So the manual SQL below is a fallback for the case the probe cannot cover:
records pointing at a node that is **gone entirely** and therefore not probing.

```sql
-- After cutover: keep only live gateway IP(s) for a namespace
UPDATE dns_records SET is_active=0, updated_at=CURRENT_TIMESTAMP
  WHERE fqdn LIKE '%anchat-test%' AND value != '<LIVE_GATEWAY_PUBLIC_IP>' AND is_active=1;

UPDATE dns_records SET is_active=1, updated_at=CURRENT_TIMESTAMP
  WHERE fqdn IN (
    'ns-anchat-test.<domain>.',
    '*.ns-anchat-test.<domain>.'
  ) AND value='<LIVE_GATEWAY_PUBLIC_IP>';
```

Do **not** blindly map old IP → new IP for all namespace rows unless the new node is already hosting those services.

### Ideal path (HA preserved)

1. Join new platform node (Phase A) — done.
2. **Before** removing old node: rebalance each namespace so the new node (or another survivor) hosts gateway/rqlite/olric, **or** ensure ≥2 live namespace voters remain after remove.
3. Only then remove old platform voter.
4. Update namespace DNS to the live set.

If automatic cluster recovery does not reassign in time, use manual recovery below.

### Emergency: namespace RQLite lost quorum (single survivor)

On the **only live** namespace host (example ports `10000` HTTP / `10001` raft — check `namespace_cluster_nodes`):

```bash
NS=anchat-test
NODE_ID=$(grep ^NODE_ID= /opt/orama/.orama/data/namespaces/$NS/rqlite.env | tail -1 | cut -d= -f2)
DATA=/opt/orama/.orama/data/namespaces/$NS/rqlite/$NODE_ID
RAFT=$DATA/raft
ADV=$(grep RAFT_ADV_ADDR /opt/orama/.orama/data/namespaces/$NS/rqlite.env | cut -d= -f2)
# e.g. ADV=10.0.0.2:10001

sudo systemctl stop orama-namespace-gateway@$NS
sudo systemctl stop orama-namespace-rqlite@$NS

# backup first
sudo cp -a "$RAFT/peers.info" "$RAFT/peers.info.bak-$(date +%Y%m%d)"

# rqlite recovery: peers.json is consumed at startup then removed
echo "[{\"id\":\"$ADV\",\"address\":\"$ADV\",\"non_voter\":false}]" | sudo tee "$RAFT/peers.json"
sudo chown orama:orama "$RAFT/peers.json"

sudo systemctl start orama-namespace-rqlite@$NS
# wait until Leader
curl -sS http://127.0.0.1:10000/status | python3 -c \
  "import sys,json; print(json.load(sys.stdin)['store']['raft'].get('state'))"
```

Then fix **Olric + gateway** configs so they do not dial dead WG IPs:

```yaml
# configs/olric-<node>.yaml — single node
memberlist:
  peers: []

# configs/gateway-<node>.yaml
olric_servers:
  - 10.0.0.<live>:10002
```

```bash
sudo systemctl restart orama-namespace-olric@$NS
sudo systemctl restart orama-namespace-gateway@$NS
curl -sS http://127.0.0.1:10004/v1/health   # expect healthy, rqlite ok, olric ok
```

Mark old assignment rows stopped in platform DB:

```sql
UPDATE namespace_cluster_nodes
  SET status='stopped', updated_at=CURRENT_TIMESTAMP,
      error_message='node replaced <date>'
  WHERE node_id='<OLD_LIBP2P_ID>' AND status='running';
```

**Later:** re-provision HA (add second/third namespace peers) so you are not single-node forever.

### Required: IPFS re-replication (automatic) (bugboard #167)

**Why:** Namespace gateways load function code with `POST http://localhost:10107/api/v0/cat?arg=<wasm_cid>`. Metadata (function name → CID) is in **namespace RQLite** and is fine after replace. The **bytes** are in **Kubo**. A replaced VPS has a nearly empty repo (`repo/stat` shows tens of objects vs thousands on old peers). The first invoke of each function on that node (or any cold peer after restart) can hang until the IPFS deadline (**~15s** → function 100% errors). The same IPFS layer hanging on **`add`** surfaces as **`orama function deploy` 504** (proxy budget 30s) while other invokes still succeed.

Gateway `/v1/health` `ipfs: ok` only means the daemon answers — **not** that every registered WASM is local.

**When to run:** After the new node is a platform voter **and** IPFS + IPFS-Cluster are up on all nameservers. Re-run after any full IPFS repo wipe.

**Steps (run from any host that can SSH to all nameservers; example uses anchat-test ports):**

```bash
# 1) On a live namespace RQLite host (e.g. ns that runs orama-namespace-rqlite@anchat-test)
curl -sS -G 'http://127.0.0.1:10000/db/query?level=none' \
  --data-urlencode "q=SELECT DISTINCT wasm_cid FROM functions WHERE status='active' AND wasm_cid IS NOT NULL AND wasm_cid != ''" \
  | python3 -c 'import sys,json; v=json.load(sys.stdin)["results"][0].get("values")or[]; open("/tmp/cids.txt","w").write("\n".join(r[0] for r in v)+"\n"); print(len(v),"cids")'

# 2) Copy /tmp/cids.txt to EVERY nameserver, then on EACH node:
#    Local pin (bitswap from peers that already hold the blocks) — this is the critical step.
while IFS= read -r cid; do
  [ -z "$cid" ] && continue
  curl -sS -m 180 -X POST "http://127.0.0.1:10107/api/v0/pin/add?arg=${cid}&recursive=true" >/dev/null \
    || echo "FAIL $cid"
done < /tmp/cids.txt

# Optional: also ask cluster to pin everywhere (RF=-1). Useful but not sufficient alone
# if a peer stays "unpinned" in peer_map — still do local pin/add above.
# curl -sS -X POST "http://127.0.0.1:10108/pins/${cid}?replication-factor-min=-1&replication-factor-max=-1"

# 3) Verify on EACH node (including the new one)
curl -sS -X POST http://127.0.0.1:10107/api/v0/repo/stat   # new node repo size should jump (MB→100s MB)
# Hot CID from a real function (example from #167):
curl -sS -m 20 -o /dev/null -w "%{http_code} %{size_download} %{time_total}\n" \
  -X POST "http://127.0.0.1:10107/api/v0/cat?arg=<HOT_WASM_CID>"
# Expect http=200, size ~1MB+, time well under 1s after backfill.

# 4) Upload path smoke test (same size class as AnChat deploys)
dd if=/dev/urandom of=/tmp/big.bin bs=1024 count=1200 status=none
curl -sS -m 60 -X POST -F file=@/tmp/big.bin http://127.0.0.1:10107/api/v0/add
```

**Done for IPFS only when:**

1. Local pin audit: almost all active `wasm_cid`s return `Keys` on **every** nameserver (investigate any remaining 500s — often a dead/orphan CID; redeploy that function).
2. Hot function CID `cat` is fast on the **new** node, not only on survivors.
3. ~1.2 MB `ipfs add` succeeds quickly on all peers.
4. `https://ns-<namespace>.…/v1/health` stays healthy **and** AnChat can `orama function deploy` + invoke a hot function.

**Do not** mark a cutover complete because platform Raft is 3/3 alone.

### Circuit breakers

Platform gateway tracks `ns:<ip>` breakers. Dead backends open circuits → HTTP 503
`namespace gateway unavailable: all upstream circuits are open`.

**Breakers now clear themselves.** A breaker opens after 5 consecutive backend
failures, admits one probe every 30s, and closes on the first success. A probe
that never reports an outcome falls back to open after 30s instead of holding
the single probe slot — that latch is what previously made restarting
`orama-node` the only cure, and it was reachable through any WebSocket upgrade,
because the WS path recorded neither success nor failure.

Fix: correct DNS so only live gateways are advertised, then wait. Recovery
should take at most one 30s open-duration once the backend is healthy.

If it does not recover, that is a bug worth filing rather than a restart. As a
last resort `orama node restart` **one follower at a time** still clears
in-memory breakers (never restart all voters at once).

---

## Phase D/E — Retire and erase the old node

Only when:

- New node is synced voter
- Zone NS DNS points at new IP
- Namespace DNS only lists live gateway IPs
- You accept namespace HA state (rebalanced or recovered)

One command does both phases, from a survivor:

```bash
orama node decommission --env <env> --node <OLD_PUBLIC_IP>
```

It takes the old node out of the raft configuration — refusing if that would
cost the cluster its quorum — writes an eviction tombstone so orphan recovery
does not put it back within five minutes, deletes its `wireguard_peers` and
`dns_nodes` rows, and then erases the machine. Add `--offline` if the VPS is
already gone.

**Raft identity.** A node whose raft id has been migrated to its libp2p peer id
keeps that id across an address change, so replacing the machine's overlay
address no longer mints a second raft member. On a cluster that has not run
`orama node migrate-raft-id` yet, the id is still the raft advertise address and
a changed address DOES create a duplicate voter that the old entry never leaves
— which is what the manual `DELETE /remove` steps below exist to clean up. Check
which you are on with:

```bash
orama node migrate-raft-id --env <env> --dry-run
```

Verify afterwards on the platform leader:

```bash
PASS=$(sudo cat /opt/orama/.orama/secrets/rqlite-password)
curl -sS -u "orama:$PASS" http://127.0.0.1:10100/nodes | python3 -m json.tool
# expect exactly the surviving voters, all reachable; leader still elected
```

<details>
<summary>Manual equivalent, if the CLI cannot reach a survivor</summary>

On the **platform leader**:

```bash
PASS=$(sudo cat /opt/orama/.orama/secrets/rqlite-password)
AUTH="orama:$PASS"

# Confirm the voter set first
curl -sS -u "$AUTH" http://127.0.0.1:10100/nodes | python3 -m json.tool

# Remove old WG raft id, e.g. 10.0.0.6:10101
curl -sS -u "$AUTH" -X DELETE http://127.0.0.1:10100/remove \
  -H 'Content-Type: application/json' \
  -d '{"id":"10.0.0.6:10101"}'
```

Then write the tombstone, or orphan recovery re-adds the node within five
minutes:

```sql
INSERT INTO raft_evicted_nodes (node_id, raft_addr, peer_id, reason, evicted_by)
  VALUES ('10.0.0.6:10101','10.0.0.6:10101','<OLD_LIBP2P_ID>','operator','<THIS_NODE>');
DELETE FROM wireguard_peers WHERE wg_ip = '10.0.0.6';
DELETE FROM dns_nodes WHERE id = '<OLD_LIBP2P_ID>';
```

Finally erase the box with `orama node wipe --env <env> --node <OLD_PUBLIC_IP>`,
or follow [CLEAN_NODE.md](CLEAN_NODE.md) on it directly.

</details>

Optional: repurpose the erased box (e.g. new `jarvis` operator host).

---

## Phase F — Inventory & SSH

1. Update `core/scripts/nodes.conf` — victim IP → new IP for that role.
2. Update `~/.ssh/config` host aliases (and keep a break-glass alias for any leftover public workloads).
3. Optional: store SSH key in rootwallet vault for the new host.

---

## Verification matrix (must all pass)

```bash
# Platform Raft = 3
curl -sS http://127.0.0.1:10100/nodes   # 3 voters, all reachable

# Platform public
curl -sS https://orama-<env>.network/v1/health
# status: healthy — rqlite, olric, ipfs, vault, wireguard ok

# NS glue / zone
dig +short A ns1.<domain> @8.8.8.8
dig +short A ns2.<domain> @8.8.8.8
dig +short A ns3.<domain> @8.8.8.8
dig +short A ns3.<domain> @<ns1-ip>   # authoritative truth

# Each critical namespace
curl -sS https://ns-<name>.orama-<env>.network/v1/health
# 200 healthy; rqlite ok; olric ok preferred

# Namespace must not resolve to a node without orama-namespace-gateway@<name>
dig +short A ns-<name>.orama-<env>.network @<ns1-ip>

# Optional: hit each A record with --resolve and confirm 200
```

---

## What we did on devnet (2026-08-03) — reference

| Item | Value |
|------|--------|
| Env | `orama-devnet.network` |
| Kept | ns1 `storm` `57.129.7.232` (`10.0.0.1`, leader) |
| Kept | ns2 `wolverine` `57.131.41.160` (`10.0.0.2`) |
| New ns3 | Contabo `169.58.118.206` (`10.0.0.17`) — SSH alias `magneto` |
| Old ns3 | `51.38.128.56` cleaned → new **jarvis** operator host |
| Binary | `0.122.99` archive, join via `http://57.129.7.232` |
| Mistake | Bulk-rewrote namespace DNS to new IP before namespace services existed; lost namespace RQLite quorum |
| Fix | DNS → only wolverine for `anchat-test`; single-node rqlite `peers.json` recovery; olric/gateway peers → local only |

Post-fixover (until rebalanced):

- Platform: **3/3 healthy**
- `anchat-test`: **healthy but single-node HA** on wolverine only (namespace gateway/rqlite not on Contabo)
- **IPFS follow-up (bugboard #167, same day):** Contabo joined with an empty blob store → cold WASM `cat` timeouts + deploy 504s. **Mitigation:** local `pin/add` of **120/121** active `wasm_cid`s on all three nameservers (Contabo repo ~empty → ~150 MB). Orphan CID `QmQeDv5K…` (`group-key-fetch-shares` v2) unpinnable cluster-wide — **redeploy that function**. Hot CID `QmNayszfin…` `cat` ~5–10 ms on all peers; ~1.2 MB `add` ~15–20 ms.

---

## Testnet tomorrow — condensed checklist

Current testnet lines in `nodes.conf` (verify live before starting):

```
testnet|ubuntu@51.195.109.238|nameserver-ns1   # ironman
testnet|ubuntu@57.131.41.159|nameserver-ns1    # thor  (role label may need cleanup)
testnet|ubuntu@51.38.130.69|nameserver-ns1     # hulk
```

1. [ ] `curl` testnet health + list namespaces + which nodes host them  
2. [ ] Pick **follower** victim; note WG IP + libp2p id + public IP  
3. [ ] Same binary version as testnet  
4. [ ] Invite + install join on new VPS (`--environment testnet`, correct `--base-domain`)  
5. [ ] Wait until new node `voter:true` + applied_index caught up  
6. [ ] Update `nsN` / apex / `dns_nameservers` in **testnet** DB  
7. [ ] **Namespace pass:** either rebalance services onto new node, or keep DNS only on remaining live gateways  
8. [ ] **IPFS WASM backfill** on **all** nameservers (export CIDs from each critical namespace RQLite; local `pin/add`; verify `cat` + ~1.2 MB `add`) — see section above  
9. [ ] Platform `DELETE /remove` old raft id  
10. [ ] Clean old VPS ([CLEAN_NODE.md](CLEAN_NODE.md))  
11. [ ] Update `nodes.conf` + RootWallet SSH vault entry `IP/ubuntu` + `authorized_keys`  
12. [ ] Verification matrix (platform + every critical `ns-*` health 10×)  
13. [ ] If any namespace RQLite is Candidate → single-node recovery before declaring done  
14. [ ] Schedule HA rebalance (restore 3 namespace peers) if you recovered to 1 node  
15. [ ] AnChat (or app owner): `function deploy` + invoke a hot function (e.g. receipt path) after backfill

---

## Explicit anti-patterns

| Don't | Why |
|-------|-----|
| Clean old node first | Can lose platform or namespace quorum |
| Restart all `orama-node` at once | Platform Raft split |
| Map all DNS `old_ip → new_ip` blindly | Clients hit empty Caddy/gateway |
| Assume install complete when process exits | May still be snapshotting |
| Ignore `olric unavailable` on namespace health | Often dead peer lists after topology change |
| Skip namespace rqlite leader check | App writes/auth can 503 while `/v1/health` still looks “ok” under weak reads |
| Skip IPFS WASM backfill after join | Cold node: function invoke 15s timeouts; deploy 504 while “ipfs: ok” |
| Trust cluster RF=-1 alone without local pin check | Peer_map can show `unpinned` on the new node; Kubo repo stays empty |
| Declare done when only platform Raft is 3/3 | Serverless still broken for cold CIDs |

---

## Recovery quick refs

| Symptom | Action |
|---------|--------|
| Platform no leader / Candidate | [DEV_DEPLOY.md](DEV_DEPLOY.md) `orama node recover-raft --env … --leader <ip>` |
| New node never becomes voter | Check WG ping, logs, re-invite + reinstall if partial |
| Namespace health 503 circuit open | Fix DNS to live gateways; restart one platform gateway |
| Namespace rqlite `leader not found` | Single-node `peers.json` recovery on survivor |
| Dual A on nsN | Delete extra rows; dig authoritative |
| Function WASM `cat` 15s timeout after replace/restart | Export active `wasm_cid`s; **local pin/add on every node** (section above) |
| `function deploy` 504 / proxy budget while invokes work | Same IPFS layer — check `add` latency on each peer; fix blob path before blaming function.yaml |
| New node `repo/stat` still tiny after hours | Backfill never ran or bitswap blocked — re-run pin/add; check WG + swarm peers |

---

## Done criteria

You are done only when **all** of these are true:

1. Exactly **3** platform voters, all reachable, stable leader  
2. `ns1`/`ns2`/`ns3` resolve correctly (authoritative dig)  
3. Platform `/v1/health` → healthy  
4. Every critical namespace `/v1/health` → **200** with **rqlite ok** (and olric ok if required)  
5. No active DNS A records for decommissioned public IPs on those namespaces  
6. **IPFS:** every nameserver has pinned (or can fast-`cat`) the active function WASM set; new node repo size is not empty  
7. **RootWallet:** `IP/ubuntu` SSH vault entry exists and is in `authorized_keys` on the new VPS  
6. `nodes.conf` + SSH match reality  
7. Old VPS cleaned **or** intentionally repurposed  

If (4) fails, the swap is **not** finished — fix namespace layer before walking away.
