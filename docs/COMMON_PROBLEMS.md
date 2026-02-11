# Common Problems & Solutions

Troubleshooting guide for known issues in the Orama Network.

---

## 1. Namespace Gateway: "Olric unavailable"

**Symptom:** `ns-<name>.orama-devnet.network/v1/health` returns `"olric": {"status": "unavailable"}`.

**Cause:** The Olric memberlist gossip between namespace nodes is broken. Olric uses UDP pings for health checks — if those fail, the cluster can't bootstrap and the gateway reports Olric as unavailable.

### Check 1: WireGuard packet loss between nodes

SSH into each node and ping the other namespace nodes over WireGuard:

```bash
ping -c 10 -W 2 10.0.0.X   # replace with the WG IP of each peer
```

If you see packet loss over WireGuard but **not** over the public IP (`ping <public-ip>`), the WireGuard peer session is corrupted.

**Fix — Reset the WireGuard peer on both sides:**

```bash
# On Node A — replace <pubkey> and <endpoint> with Node B's values
wg set wg0 peer <NodeB-pubkey> remove
wg set wg0 peer <NodeB-pubkey> endpoint <NodeB-public-ip>:51820 allowed-ips <NodeB-wg-ip>/32 persistent-keepalive 25

# On Node B — same but with Node A's values
wg set wg0 peer <NodeA-pubkey> remove
wg set wg0 peer <NodeA-pubkey> endpoint <NodeA-public-ip>:51820 allowed-ips <NodeA-wg-ip>/32 persistent-keepalive 25
```

Then restart services: `sudo orama prod restart`

You can find peer public keys with `wg show wg0`.

### Check 2: Olric bound to 0.0.0.0 instead of WireGuard IP

Check the Olric config on each node:

```bash
cat /home/debros/.orama/data/namespaces/<name>/configs/olric-*.yaml
```

If `bindAddr` is `0.0.0.0`, the node will try to bind to IPv6 on dual-stack hosts, breaking memberlist gossip.

**Fix:** Edit the YAML to use the node's WireGuard IP (run `ip addr show wg0` to find it), then restart: `sudo orama prod restart`

This was fixed in code (BindAddr validation in `SpawnOlric`), so new namespaces won't have this issue.

### Check 3: Olric logs show "Failed UDP ping" constantly

```bash
journalctl -u debros-namespace-olric@<name>.service --no-pager -n 30
```

If every UDP ping fails but TCP stream connections succeed, it's the WireGuard packet loss issue (see Check 1).

---

## 2. Namespace Gateway: Missing config fields

**Symptom:** Gateway config YAML is missing `global_rqlite_dsn`, has `olric_timeout: 0s`, or `olric_servers` only lists `localhost`.

**Cause:** Before the spawn handler fix, `spawnGatewayRemote()` didn't send `global_rqlite_dsn` or `olric_timeout` to remote nodes.

**Fix:** Edit the gateway config manually:

```bash
vim /home/debros/.orama/data/namespaces/<name>/configs/gateway-*.yaml
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

Then: `sudo orama prod restart`

This was fixed in code, so new namespaces get the correct config.

---

## 3. Namespace not restoring after restart (missing cluster-state.json)

**Symptom:** After `orama prod restart`, the namespace services don't come back because `RestoreLocalClustersFromDisk` has no state file.

**Check:**

```bash
ls /home/debros/.orama/data/namespaces/<name>/cluster-state.json
```

If the file doesn't exist, the node can't restore the namespace.

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

---

## 4. Namespace gateway processes not restarting after upgrade

**Symptom:** After `orama upgrade --restart` or `orama prod restart`, namespace gateway/olric/rqlite services don't start.

**Cause:** `orama prod stop` disables systemd template services (`debros-namespace-gateway@<name>.service`). They have `PartOf=debros-node.service`, but that only propagates restart to **enabled** services.

**Fix:** Re-enable the services before restarting:

```bash
systemctl enable debros-namespace-rqlite@<name>.service
systemctl enable debros-namespace-olric@<name>.service
systemctl enable debros-namespace-gateway@<name>.service
sudo orama prod restart
```

This was fixed in code — the upgrade orchestrator now re-enables `@` services before restarting.

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

## General Debugging Tips

- **Always use `sudo orama prod restart`** instead of raw `systemctl` commands
- **Namespace data lives at:** `/home/debros/.orama/data/namespaces/<name>/`
- **Check service logs:** `journalctl -u debros-namespace-olric@<name>.service --no-pager -n 50`
- **Check WireGuard:** `wg show wg0` — look for recent handshakes and transfer bytes
- **Check gateway health:** `curl http://localhost:<port>/v1/health` from the node itself
- **Node IPs:** Check `scripts/remote-nodes.conf` for credentials, `wg show wg0` for WG IPs
