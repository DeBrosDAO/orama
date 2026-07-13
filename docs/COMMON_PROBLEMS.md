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

Then restart services: `sudo orama node restart`

You can find peer public keys with `wg show wg0`.

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

**Symptom:** After `orama node upgrade --restart` or `orama node restart`, namespace gateway/olric/rqlite services don't start.

**Cause:** `orama node stop` disables systemd template services (`orama-namespace-gateway@<name>.service`). They have `PartOf=orama-node.service`, but that only propagates restart to **enabled** services.

**Fix:** Re-enable the services before restarting:

```bash
systemctl enable orama-namespace-rqlite@<name>.service
systemctl enable orama-namespace-olric@<name>.service
systemctl enable orama-namespace-gateway@<name>.service
sudo orama node restart
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

---

## 6. RQLite returns 401 Unauthorized

**Symptom:** RQLite queries fail with HTTP 401 after security hardening.

**Cause:** RQLite now requires basic auth. The client isn't sending credentials.

**Fix:** Ensure the RQLite client is configured with the credentials from `/opt/orama/.orama/secrets/rqlite-auth.json`. The central RQLite client wrapper (`pkg/rqlite/client.go`) handles this automatically. If using a standalone client (e.g., CoreDNS plugin), ensure it's also configured.

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

## General Debugging Tips

- **Always use `sudo orama node restart`** instead of raw `systemctl` commands
- **Namespace data lives at:** `/opt/orama/.orama/data/namespaces/<name>/`
- **Check service logs:** `journalctl -u orama-namespace-olric@<name>.service --no-pager -n 50`
- **Check WireGuard:** `wg show wg0` — look for recent handshakes and transfer bytes
- **Check gateway health:** `curl http://localhost:<port>/v1/health` from the node itself
- **Node IPs:** Check `scripts/remote-nodes.conf` for credentials, `wg show wg0` for WG IPs
- **OramaOS nodes:** No SSH access — use Gateway API endpoints (`/v1/node/status`, `/v1/node/logs`) for diagnostics
