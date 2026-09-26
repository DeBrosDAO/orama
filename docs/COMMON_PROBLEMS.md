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
sudo orama node logs node --since -10min | grep 'WireGuard peer sync completed' | tail -3
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
sudo orama node logs orama-namespace-olric@<name> -n 30
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

**The state file is not trusted for raft membership.** `cluster-state.json`
is refreshed by a best-effort push, so the node most likely to hold a stale copy
is exactly the one that was down while the cluster changed. A stopped namespace
rqlite is restored on this evidence instead:

- **Holding raft state**, it restarts on its own raft configuration. It writes
  `peers.json` (rqlite's force-recovery mechanism) only when the live
  membership in the index DB differs from the membership this node recorded
  while its rqlited ran — `data/namespaces/<ns>/rqlite/cluster-membership.json`,
  beside the node's rqlite directory, refreshed on every restore pass that
  finds it running. It used to write `peers.json` on every such restart,
  forcing a configuration onto a namespace that was live elsewhere. With no
  record, or the DB unreadable, nothing is written: raft waits for its peers.
- **Without raft state**, a node that has a record was a member and lost its
  data: it joins the other members and never bootstraps, even with the lowest
  node id. A lowest-id node used to bootstrap an empty cluster beside the
  members holding the data. With no other member it refuses and names the
  record; delete the record only to bootstrap an empty namespace on purpose.
  A node without a record takes part in the usual election (lowest id
  bootstraps, the rest join it).

The state file is still used for everything else about the restore (ports, local
IP, WebRTC roles) — just not for asserting who the voters are.

---

## 4. Namespace gateway processes not restarting after upgrade

**Symptom:** After `orama node upgrade --restart` or `orama node restart`, namespace gateway/olric/rqlite services don't start.

**Cause:** `orama node stop` disables systemd template services (`orama-namespace-gateway@<name>.service`). They have `PartOf=orama-node.service`, but that only propagates restart to **enabled** services. Index host units (`@index`) are started by the supervisor on node start and do not need to be enabled.

**Fix:** `sudo orama node restart` — the upgrade orchestrator re-enables `@`
services before restarting them, so nothing has to be enabled by hand.

If you are on a node old enough not to do that, re-enable the tenant services
first. Raw `systemctl` bypasses the CLI's dependency ordering, quorum checks and
health verification, so this is a last resort, not a routine step:

```bash
systemctl enable orama-namespace-rqlite@<name>.service
systemctl enable orama-namespace-olric@<name>.service
systemctl enable orama-namespace-gateway@<name>.service
sudo orama node restart
```

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

**Cause:** This release starts rqlited with `-auth` (auth JSON copied into the instance data dir). Unauthenticated calls are 401 by design.

**Fix:**

```bash
grep -E 'rqlite_(auth_file|username|password)' /opt/orama/.orama/configs/node.yaml
sudo cat /opt/orama/.orama/secrets/rqlite-password
```

Every client must send `orama` + that password. Gateway and SFU YAML embed it in `rqlite_dsn` (and carry `rqlite_username` / `rqlite_password`). The node process, the `orama` CLI and the installer take it from `node.yaml` (`database.rqlite_username` / `rqlite_password`). The CoreDNS Corefile always has `username` / `password`.

A 401 during a mixed-fleet upgrade means an old binary (no DSN creds) is talking to a new rqlited. Finish the rolling upgrade, followers first, leader last.

Errors from `AdminClient` name a 401 explicitly ("rqlite rejected the credentials (401)"). A 401 that reads instead as reconciliation or backups silently stopping means some caller is still bypassing `AdminClient`.

### Pre-upgrade: "served by this node … has no rqlite.env"

**Symptom:** `orama node pre-upgrade` (or the upgrade's restart step, or post-upgrade) stops with `namespace(s) <ns> are served by this node … but … has no rqlite.env for them`.

**Cause:** the node has `data/namespaces/<ns>/cluster-state.json` and `data/namespaces/<ns>/rqlite/`, so it restores that namespace's rqlite at boot, but the env file that says where the instance listens is missing from the tree the step reads — `/var/lib/orama-unit-env/<ns>/rqlite.env`, or `data/namespaces/<ns>/rqlite.env` on a node that has not yet started on the new layout. Without it the step cannot hand the namespace's leadership over, and restarting the node would take its leader down blind.

**Fix:**
- If `orama-node` is still moving the old layout, its log names the path it stopped on (`Legacy layout: …`, or a `legacy-layout` component error naming two paths). Resolve that; it retries on its own and writes the env file.
- If the namespace no longer runs on this node, its directory is a leftover from an unfinished teardown. Remove the namespace from the node through the normal deprovision path; do not delete `cluster-state.json` by hand while the namespace is still assigned here.

### Connection refused on `localhost:10100`

**Symptom:** A manual `curl http://localhost:10100/...` (or an old script) gets connection refused while `orama-namespace-rqlite@index` is running.

**Cause:** rqlited binds only this node's WireGuard advertise address (`discovery.http_adv_address` in `node.yaml`, e.g. `10.0.0.1:10100`); nothing listens on loopback. Namespace instances bind the `HTTP_ADDR` in their `/var/lib/orama-unit-env/<ns>/rqlite.env` (root-owned, group `orama`; read it with `sudo`).

**Fix:** Address rqlite where it binds, with credentials:

```bash
RQ="http://$(sudo sed -n 's/^ *http_adv_address: *"\(.*\)"/\1/p' /opt/orama/.orama/configs/node.yaml)"
# Credentials go to curl on stdin (-K -), never on its command line where ps shows them.
rqcurl() { printf 'user = "orama:%s"\n' "$(sudo cat /opt/orama/.orama/secrets/rqlite-password)" | curl -K - "$@"; }
rqcurl -sS "$RQ/status"
```

Every Go client resolves the same address: the node from its config (`rqlite.IndexEndpoint`), the `orama` CLI and the installer from `node.yaml` (`rqlite.EndpointFromNodeConfig`), and operator commands that run over SSH read `node.yaml` on the node (`rqlite.NodeShellCurl`). A missing or wildcard advertise address, or missing credentials, is an error — there is no localhost default.

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

**Cause:** The OramaOS node's port 9999 isn't reachable from the gateway, `--code` is missing or wrong, or `--node-ip` is not the node's public IPv4.

**Fix:** Read the registration code from the node's console (it is not served on port 9999). Check that port 9999 is open in your VPS provider's external firewall (Hetzner firewall, AWS security groups, etc.). OramaOS opens it internally, but provider-level firewalls must be configured separately. There is no WebSocket enrollment path.

---

## 10. Binary signature verification fails

**Symptom:** `orama push`, `orama node install` or `orama node upgrade` refuses the build archive; the error names the cause.

**Causes and fixes:**

- *"is unsigned"* — built with `--unsigned`, or `manifest.sig` is missing. Rebuild with `orama build` (signs by default).
- *"signed by 0x…, which this node does not trust"* — the RootWallet account that signed is not in `/etc/orama/archive-signers`. Build with a trusted account active, or rotate signers with a build signed by a trusted one (`orama build --signers`, [DEV_DEPLOY.md](DEV_DEPLOY.md#signed-archives)).
- *"does not match the signed manifest"* / *"not in its signed manifest"* — the archive was changed after signing. Rebuild it; if it happens only on fanned-out nodes, suspect the hub.
- *"no archive trust anchor"* — the node was installed before archives were signed. Push once with `--trust-signers <your address>`.
- *`unknown command "stage-archive"`* — the node's installed CLI predates archive signing; roll out that one upgrade with the previous release's CLI ([DEV_DEPLOY.md](DEV_DEPLOY.md#signed-archives)).
- *"a genesis install needs --operator-wallet"* — pass the wallet that signed the archive.
- *"leaves out its own signer"* — a `--signers` rotation must include the account signing it; retire a key in two builds ([DEV_DEPLOY.md](DEV_DEPLOY.md#signed-archives)).
- *"an old build is being replayed — rotate with a new build"* — the node already took a rotation from a newer build; make the rotation with a new build.
- *"… ahead of this node's clock"* (a build with `--signers`, or a joined node's rotation time) — the builder's clock or this node's clock is wrong; fix it and rebuild or rejoin.
- *"… linux/… and this node is linux/…"* (or *"… and the node is linux/…"* from setup) — build with `--arch` matching the node.
- *"no build archive at /opt/orama"* — nothing was pushed or set up there; install and upgrade never compile on the node.
- *join refused with 409 "this cluster trusts archive signers …"* — the cluster trusts more (or other) signers than you expected; join with `orama node setup --join-via user@ip` so the expectation is read from that node. The invite was not used.

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

## 13. RootWallet agent: locked, waiting, or unreachable

Commands that need an SSH key or a wallet signature — `orama node setup`,
`orama push`, `orama auth approve` — talk to the RootWallet desktop app's agent
over a Unix socket at `~/.rootwallet/agent.sock`. Override the path with
`RW_AGENT_SOCK`.

`orama auth login` uses the agent when it is reachable and does not need it when
it is not: on a machine with no wallet it prints a code and waits for
`orama auth approve <code>` on one that has. So "the agent is unreachable" is a
reason to approve from elsewhere rather than a reason the login cannot happen.

The agent answers with a code, and the CLI turns each one into an instruction:

| Code | What happened | What to do |
|------|---------------|------------|
| `AGENT_LOCKED` (423) | A vault operation waited for an unlock and gave up | Unlock the desktop app and run the command again |
| `AGENT_LOCKED` (401) | A wallet operation refused at once; these do not wait | Unlock the desktop app first, then run the command |
| `APPROVAL_TIMEOUT` | The approval prompt went unanswered for two minutes | Run it again and approve it |
| `APPROVAL_DENIED` | Someone refused the request | Approve this application in the desktop app |
| `PERMISSION_DENIED` | The application lacks the capability | Grant it under app permissions |
| `PEER_VANISHED` | The `orama` binary changed while the request was open | Run it again |
| `NOT_FOUND` | No such vault entry | Nothing to do; the CLI creates SSH entries on demand |

**A first run against a locked wallet can take four minutes.** The agent waits
up to two minutes for approval, then up to two more for the unlock, and the CLI
waits longer than both so the agent's own answer arrives instead of a timeout
from this side. If `orama node setup` seems to hang, look at the desktop app:
there is probably a prompt on it. `orama sandbox` reports how many prompts are
waiting.

**"rootwallet agent is not reachable"** means the socket is not there: the
desktop app is closed. Open it.

---

## 14. Index rqlite refuses to start: "holds raft state but no raft-node-id"

**Symptom:** `orama-node` stays down; its log says `refusing to start the index rqlite: … holds raft state but no raft-node-id`.

**Cause:** the node predates recorded raft ids and was upgraded without the orama CLI of this release. Its raft id is the address it last ran under (on 0.122.x, `<wg-ip>:7001`), and the regenerated `node.yaml` no longer says what that was. Starting under the current address would leave the node outside its own raft configuration.

**Fix:** find the node's id in the configuration on a live member (`orama node report` or `/nodes` on a voter: the entry whose `addr` is this node's WireGuard IP), write it as the orama user to `/opt/orama/.orama/data/rqlite/raft-node-id`, and write the address it lists to `raft-adv-addr` beside it. `orama-node` retries on its own. Upgrade the remaining nodes with `orama node upgrade --env <env> --yes` from this release's CLI, which records both before stopping anything.

---

## 15. Index rqlite refuses to start: "no other member to join so that the leader re-registers it"

**Symptom:** after an upgrade that moved the index raft port, the log says the configuration holds this node at one address, it listens on another, and there is no member to join.

**Cause:** the node's address changed and rqlite only moves a member to a new address when the node joins the leader again. This node has nobody recorded to join: it is a cluster of one, or its membership record was lost.

**Fix:** on a cluster of one, reform it at the new address: `orama node recover-raft --env <env> --leader-raft-addr <wg-ip>:10101`. On a larger cluster, set `database.rqlite_join_address` in `node.yaml` to a live member's raft address and let `orama-node` retry.

---

## 16. Namespace rqlite refuses to bootstrap: "this node has been a member … but holds no raft state"

**Symptom:** a tenant namespace's rqlite is not started on a node; the log names `data/namespaces/<ns>/rqlite/cluster-membership.json`.

**Cause:** the node was a member of that namespace and has lost its rqlite data, and the namespace has no other member to join. Bootstrapping would create an empty namespace database.

**Fix:** restore the node's rqlite data for that namespace, or — if the namespace's data is gone everywhere and an empty one is what you want — delete the record; the next restore pass bootstraps it.

---

## 17. A 0.122.x tenant deployment is down after the upgrade

**Symptom:** a deployment made on 0.122.x does not answer after its node was upgraded; `systemctl status orama-deploy-<ns>-<name>` says the unit does not exist.

**Cause:** 0.122.x ran each deployment from a unit of its own with its environment inline. `orama-node` moves the deployment's files to `data/deployments/<ns>-<name>/` (with the owner marker the gateways check) and stages the environment the unit ran with; the upgrade then stops, disables and deletes the old unit. The template unit that replaces it (`orama-deploy-<runtime>@<ns>-<name>`) also needs a workload token, which only the gateway can mint, so nothing starts it on its own.

**Fix:** deploy it again from scratch — delete the deployment, then create it — which starts it under the template with its environment and a fresh token. `--update` is not enough: an update restarts the existing unit, and the template unit has no token to start with.

---

## General Debugging Tips

- **Always use `sudo orama node restart`** instead of raw `systemctl` commands
- **Namespace data lives at:** `/opt/orama/.orama/data/namespaces/<name>/`
- **Check service logs:** `sudo orama node logs orama-namespace-olric@<name>` — it wraps `journalctl`, resolves template instances, and takes `-n` and `-f`
- **Check WireGuard:** `wg show wg0` — look for recent handshakes and transfer bytes
- **Check gateway health:** `curl http://localhost:<port>/v1/health` from the node itself
- **Node IPs:** `orama nodes --env <env>` lists them from the network API. The local fallback inventory is `nodes.conf`, looked for in the working directory, then `../scripts/`, then `~/.orama/` — see [INSPECTOR.md](INSPECTOR.md#configuration) for the full search order. `wg show wg0` for WG IPs. SSH keys come from the RootWallet vault, never from a file.
- **OramaOS nodes:** No SSH access — use Gateway API endpoints (`/v1/node/status`, `/v1/node/logs`) for diagnostics
