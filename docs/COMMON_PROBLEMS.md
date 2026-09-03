# Common Problems & Solutions

Troubleshooting guide for known issues in the Orama Network.

---

## 1. Namespace Gateway: "Olric unavailable"

> **First, check whether this is drift the reconciler has already fixed.** When
> a namespace member is replaced, the survivors used to keep the departed node's
> overlay address in `configs/olric-<node>.yaml` peers and `configs/gateway-<node>.yaml`
> `olric_servers` for ever — nothing rewrote them, so every gateway restart
> stalled for minutes timing out against a machine that was gone. The tenant
> reconciler now rewrites both within 60s of the membership changing, and
> restarts the service only when the config actually differs. If the config
> still names a node that is gone, the reconciler is not running or not
> converging; that is the bug, not the config.

**Symptom:** `ns-<name>.orama-devnet.network/v1/health` returns `"olric": {"status": "unavailable"}`.

**Cause:** The Olric memberlist gossip between namespace nodes is broken. Olric uses UDP pings for health checks — if those fail, the cluster can't bootstrap and the gateway reports Olric as unavailable.

### Check 1: WireGuard packet loss between nodes

SSH into each node and ping the other namespace nodes over WireGuard:

```bash
ping -c 10 -W 2 10.0.0.X   # replace with the WG IP of each peer
```

If you see packet loss over WireGuard but **not** over the public IP (`ping <public-ip>`), the WireGuard peer session is corrupted.

**This should no longer be needed.** The 60s peer sync now re-applies any peer
whose endpoint or allowed IPs drifted from what cluster membership says, and
persists the result to `/etc/wireguard/wg0.conf`. A peer whose public IP moved
converges on its own within a minute. If you still have to reset a peer by hand,
that is a bug worth filing rather than a routine fix.

Check what the node believes before reaching for `wg set`:

```bash
# what the interface holds, with endpoints (machine-readable)
wg show wg0 dump

# what membership says it should hold
sudo grep -A3 '\[Peer\]' /etc/wireguard/wg0.conf

# the sync's own account of the last round
journalctl -u orama-node --no-pager | grep 'WireGuard peer sync completed' | tail -3
```

The sync log line reports `added`, `updated`, `removed` and `persisted`
separately. `persisted=false` means the mesh is correct **now** but will regress
on the next `wg-quick up` — a different problem from a peer that never reached
the interface.

**Break-glass reset (both sides), if you genuinely need it:**

```bash
# On Node A — replace <pubkey> and <endpoint> with Node B's values
wg set wg0 peer <NodeB-pubkey> remove
wg set wg0 peer <NodeB-pubkey> endpoint <NodeB-public-ip>:51820 allowed-ips <NodeB-wg-ip>/32 persistent-keepalive 25

# On Node B — same but with Node A's values
wg set wg0 peer <NodeA-pubkey> remove
wg set wg0 peer <NodeA-pubkey> endpoint <NodeA-public-ip>:51820 allowed-ips <NodeA-wg-ip>/32 persistent-keepalive 25
```

The next sync round re-persists whatever the interface ends up holding, so you
do not need to edit `wg0.conf` by hand.

### Check 2: Olric bound to 0.0.0.0 instead of WireGuard IP

Check the Olric config on each node:

```bash
cat /opt/orama/.orama/data/namespaces/<name>/configs/olric-*.yaml
```

If `bindAddr` is `0.0.0.0`, the node will try to bind to IPv6 on dual-stack hosts, breaking memberlist gossip.

**Fix:** Edit the YAML to use the node's WireGuard IP (run `ip addr show wg0` to find it), then restart: `sudo orama node restart`

This was fixed in code (BindAddr validation in `SpawnOlric`), so new namespaces won't have this issue.

### Check 3: Olric logs show "Failed UDP ping" constantly

```bash
journalctl -u orama-namespace-olric@<name>.service --no-pager -n 30
```

If every UDP ping fails but TCP stream connections succeed, it's the WireGuard packet loss issue (see Check 1).

---

## 2. Namespace Gateway: Missing config fields

**Symptom:** Gateway config YAML is missing `global_rqlite_dsn`, has `olric_timeout: 0s`, or `olric_servers` only lists `localhost`.

**Cause:** Before the spawn handler fix, `spawnGatewayRemote()` didn't send `global_rqlite_dsn` or `olric_timeout` to remote nodes.

**Fix:** Edit the gateway config manually:

```bash
vim /opt/orama/.orama/data/namespaces/<name>/configs/gateway-*.yaml
```

Add/fix:
```yaml
global_rqlite_dsn: "http://10.0.0.X:10001"
olric_timeout: 30s
olric_servers:
  - "10.0.0.X:10002"
  - "10.0.0.Y:10002"
  - "10.0.0.Z:10002"
```

Then: `sudo orama node restart`

This was fixed in code, so new namespaces get the correct config.

---

## 3. Namespace not restoring after restart (missing cluster-state.json)

**Symptom:** After `orama node restart`, the namespace services don't come back because `RestoreLocalClustersFromDisk` has no state file.

**Check:**

```bash
ls /opt/orama/.orama/data/namespaces/<name>/cluster-state.json
```

If the file doesn't exist, the node can't restore the namespace **from disk** —
but that is no longer terminal. The disk pass runs once at boot for speed; the
tenant reconciler then converges from the database every 60s, so a node with no
state file recovers as soon as its rqlite has a leader.

Before this, the boot restore tried the database twelve times and gave up. A
node whose cluster had no leader for two minutes left every tenant down until
someone restarted the gateway by hand.

**Fix:** Create the file manually from another node that has it, or reconstruct it. The format is:

```json
{
  "namespace": "<name>",
  "rqlite": { "http_port": 10001, "raft_port": 10000, ... },
  "olric": { "http_port": 10002, "memberlist_port": 10003, ... },
  "gateway": { "http_port": 10004, ... }
}
```

This was fixed in code — `ProvisionCluster` now saves state to all nodes (including remote ones via the `save-cluster-state` spawn action).

**The state file is no longer trusted for raft membership.** `cluster-state.json`
is refreshed by a best-effort push, so the node most likely to hold a stale copy
is exactly the one that was down while the cluster changed. On restore, the peer
list written into `peers.json` (rqlite's force-recovery mechanism) now comes from:

1. **live membership in the index DB** when it is readable — authoritative, and
   it outranks anything on local disk;
2. **nothing at all** when the DB is unreadable but another member answers on
   its raft port — rqlited rejoins using its own raft state, and writing a guess
   would overwrite the real configuration;
3. **a single-node entry for this node** only when the DB is unreadable *and*
   no peer answers. That produces a working leader instead of a Candidate; the
   other members must be re-added once they return.

The state file is still used for everything else about the restore (ports, local
IP, WebRTC roles) — just not for asserting who the voters are.

---

## 4. Namespace gateway processes not restarting after upgrade

**Symptom:** After `orama node upgrade --restart` or `orama node restart`, namespace gateway/olric/rqlite services don't start.

**Cause:** `orama node stop` disables systemd template services (`orama-namespace-gateway@<name>.service`). They have `PartOf=orama-node.service`, but that only propagates restart to **enabled** services. Index host units (`@index`) are started by the supervisor on node start and do not need to be enabled.

**Fix:** Re-enable the **tenant** services before restarting:

```bash
systemctl enable orama-namespace-rqlite@<name>.service
systemctl enable orama-namespace-olric@<name>.service
systemctl enable orama-namespace-gateway@<name>.service
sudo orama node restart
```

This was fixed in code — the upgrade orchestrator now re-enables `@` services before restarting.

If a tenant service is still down after that, the tenant reconciler restarts it
within a minute; it no longer needs a hand-run restore. A service that stays
down across several sweeps is a real failure — check its unit's logs rather than
re-running the commands above.

---

## 5. SSH commands eating stdin inside heredocs

**Symptom:** When running a script that SSHes into multiple nodes inside a heredoc (`<<'EOS'`), only the first SSH command runs — the rest are silently skipped.

**Cause:** `ssh` reads from stdin, consuming the rest of the heredoc.

**Fix:** Add `-n` flag to all `ssh` calls inside heredocs:

```bash
ssh -n user@host 'command'
```

`scp` is not affected (doesn't read stdin).

---

---

## 6. RQLite returns 401 Unauthorized

**Symptom:** RQLite queries fail with HTTP 401.

**Cause:** `rqlited` is started with `-auth` only when `database.rqlite_enforce_auth` is set, which is off by default. Two possibilities:

1. **Enforcement is off** (the normal state) — then the 401 is not Orama's. Something else is in front of RQLite: a reverse proxy, or a hand-edited unit file.
2. **Enforcement was switched on** while some caller still has no credentials.

**Fix:**

```bash
grep -E 'rqlite_(auth_file|enforce_auth|username|password)' /opt/orama/.orama/configs/node.yaml
```

If `rqlite_enforce_auth: true`, every client must send the credentials from `/opt/orama/.orama/secrets/rqlite-auth.json`. The node's own admin calls do (`core/pkg/rqlite/adminclient.go` reads `rqlite_auth_file`), and so does its SQL DSN. **The gateway and namespace DSNs do not** — `gateway.Config.RQLiteUsername`/`RQLitePassword` are never assigned — so enforcement is not yet safe to switch on fleet-wide.

Errors from `AdminClient` name a 401 explicitly ("rqlite rejected the credentials (401)"). A 401 that reads instead as reconciliation or backups silently stopping means some caller is still bypassing `AdminClient`.

**Enabling enforcement is two passes, in this order** (doing them in one 401s every peer still on the old binary — see `docs/SECURITY.md`):

1. Roll out configs carrying `rqlite_auth_file`, enforcement off, to **every** node.
2. Only then set `rqlite_enforce_auth: true`, restarting followers first and the leader last.

---

## 7. Olric cluster split after upgrade

**Symptom:** Olric nodes can't gossip after enabling memberlist encryption.

**Cause:** Olric memberlist encryption is all-or-nothing. Nodes with encryption can't communicate with nodes without it.

**Fix:** All nodes must be restarted simultaneously when enabling Olric encryption. The cache will be lost (it rebuilds from DB). This is expected — Olric is a cache, not persistent storage.

---

## 8. OramaOS: LUKS unlock fails

**Symptom:** OramaOS node can't reconstruct its LUKS key after reboot.

**Cause:** Not enough peer vault-guardians are online to meet the Shamir threshold (K = max(2, floor(N/3))).

**Fix:** Ensure enough cluster nodes are online and reachable over WireGuard. The agent retries with exponential backoff. For genesis nodes before 5+ peers exist, use:

```bash
orama node unlock --genesis --node-ip <wg-ip>
```

---

## 9. OramaOS: Enrollment timeout

**Symptom:** `orama node enroll` hangs or times out.

**Cause:** The OramaOS node's port 9999 isn't reachable, or the Gateway can't reach the node's WebSocket.

**Fix:** Check that port 9999 is open in your VPS provider's external firewall (Hetzner firewall, AWS security groups, etc.). OramaOS opens it internally, but provider-level firewalls must be configured separately.

---

## 10. Binary signature verification fails

**Symptom:** `orama node install` rejects the binary archive with a signature error.

**Cause:** The archive was tampered with, or the manifest.sig file is missing/corrupted.

**Fix:** Rebuild the archive with `orama build` and re-sign with `make sign` (in the orama-os repo). Ensure you're using the rootwallet that matches the embedded signer address.

---

## 11. Function WASM timeouts / deploy 504 after node replace (bugboard #167)

**Symptom:**

- `failed to fetch WASM: wasm fetch from IPFS timed out` (~15s) on invoke after a nameserver replace or rolling restart
- Other functions still succeed in milliseconds (different CIDs already local)
- `orama function deploy` returns **504** / `TIMEOUT` / “proxy budget (30s)” while gateway health still shows `ipfs: ok`

**Cause:** Function **metadata** is in namespace RQLite; function **bytes** are IPFS blobs. A **new or wiped node** starts with an empty Kubo repo. Until every active `wasm_cid` is **locally pinned** (bitswap from peers), cold `cat`/`add` can hang under hard deadlines. Health checks only prove the daemon is up.

**Fix:** Follow **IPFS function-WASM backfill** in [NODE_REPLACEMENT.md](NODE_REPLACEMENT.md): export active CIDs from the namespace RQLite, `pin/add` on **every** nameserver, verify `cat` + ~1.2 MB `add` on the new node. Do not treat platform Raft 3/3 alone as cutover complete.

---

## 12. IPFS-Cluster: node starts but its pins never replicate

**Symptom:** `ipfs-cluster` is running and `/v1/health` reports `ipfs: ok`, but
content pinned on this node never appears on the others (or vice versa). The
ipfs-cluster log shows only generic connection failures to peers.

**Cause:** the shared secret differs from the rest of the fleet. It is the key to
the cluster's libp2p **private network**, so a node holding a different value
completes no handshake with any peer — while looking healthy to every local
check.

**Check:** compare the first characters across nodes (never paste the whole
value into a ticket):

```bash
sudo head -c 8 /opt/orama/.orama/secrets/cluster-secret; echo
```

They must be identical on every node.

**Fix:** copy the value from a healthy node and restart `orama-node`. The join
handshake distributes this secret, so a node that joined properly has the right
one.

**This should no longer happen on its own.** The node used to generate a fresh
secret whenever the file could not be read (permissions, a transient I/O error,
a file the join handshake had not written yet) or was not exactly 64 characters,
and it discarded write errors — so a failed write produced a *different* secret
on each restart. It now refuses to start ipfs-cluster in all of those cases and
says why. A secret is generated only when the file is genuinely absent **and**
the node holds no ipfs-cluster identity, i.e. it has never joined a cluster.

---

## General Debugging Tips

- **Always use `sudo orama node restart`** instead of raw `systemctl` commands
- **Namespace data lives at:** `/opt/orama/.orama/data/namespaces/<name>/`
- **Check service logs:** `journalctl -u orama-namespace-olric@<name>.service --no-pager -n 50`
- **Check WireGuard:** `wg show wg0` — look for recent handshakes and transfer bytes
- **Check gateway health:** `curl http://localhost:<port>/v1/health` from the node itself
- **Node IPs:** Check `scripts/remote-nodes.conf` for credentials, `wg show wg0` for WG IPs
- **OramaOS nodes:** No SSH access — use Gateway API endpoints (`/v1/node/status`, `/v1/node/logs`) for diagnostics
