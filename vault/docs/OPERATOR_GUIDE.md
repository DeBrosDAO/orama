# Orama Vault -- Operator Guide

## Monitoring

### Health Endpoint

The simplest way to check if a guardian is running:

```bash
curl -s http://127.0.0.1:7500/v1/vault/health | jq .
```

Expected response:
```json
{
  "status": "ok",
  "version": "0.1.0"
}
```

If this endpoint does not respond, the guardian process is not running or the port is blocked. Check systemd status first.

### Status Endpoint

Provides runtime configuration:

```bash
curl -s http://127.0.0.1:7500/v1/vault/status | jq .
```

Expected response:
```json
{
  "status": "ok",
  "version": "0.1.0",
  "data_dir": "/opt/orama/.orama/data/vault",
  "client_port": 7500,
  "peer_port": 7501
}
```

### Guardians Endpoint

Lists known guardian nodes in the cluster:

```bash
curl -s http://127.0.0.1:7500/v1/vault/guardians | jq .
```

> **Note (v0.1.0):** This currently returns only the local node. Full cluster listing requires RQLite integration (Phase 2).

### Systemd Journal

The guardian logs to stderr using Zig's structured logging, which is captured by the systemd journal:

```bash
# View recent logs
sudo journalctl -u orama-vault -n 50 --no-pager

# Follow live logs
sudo journalctl -u orama-vault -f

# View logs since last boot
sudo journalctl -u orama-vault -b

# View error-level logs only
sudo journalctl -u orama-vault -p err
```

Log messages include:
- `vault-guardian v0.1.0 starting` -- startup confirmation
- `config: <path>` -- config file path
- `listening on <addr>:<port> (client)` -- client listener bound
- `listening on <addr>:<port> (peer)` -- peer listener bound
- `data directory: <path>` -- data directory path
- `guardian ready -- starting HTTP server` -- initialization complete
- `stored share for identity <hex> (<n> bytes, version <v>)` -- successful push
- `served share for identity <hex> (<n> bytes)` -- successful pull
- `rejected rollback for <hex>: version <v> <= current <v>` -- anti-rollback rejection
- `accept error: <err>` -- TCP accept failure (non-fatal, retried)
- `connection error: <err>` -- individual connection handling error
- `failed to write share for <hex>: <err>` -- disk write failure

### Service Status

```bash
sudo systemctl status orama-vault
```

Check for:
- `Active: active (running)` -- service is up
- `Main PID: <pid>` -- process ID
- Memory and CPU usage in the status output

---

## Troubleshooting

### Port Already In Use

**Symptom:** Guardian fails to start with `failed to bind 0.0.0.0:7500: error.AddressInUse`

**Diagnosis:**
```bash
# Find what's using the port
sudo ss -tlnp | grep 7500
```

**Resolution:**
- If another vault-guardian is running: `sudo systemctl stop orama-vault` first.
- If another service is using port 7500: change the vault port with `--port <other>`.
- If the port is in TIME_WAIT state from a recent restart: wait 30-60 seconds. The guardian sets `SO_REUSEADDR` which should handle most cases.

### Data Directory Permissions

**Symptom:** `failed to create data directory <path>: error.AccessDenied`

**Diagnosis:**
```bash
ls -la /opt/orama/.orama/data/vault/
```

**Resolution:**
```bash
sudo chown -R orama:orama /opt/orama/.orama/data/vault
sudo chmod 700 /opt/orama/.orama/data/vault
```

The systemd service uses `ProtectSystem=strict` with `ReadWritePaths=/opt/orama/.orama/data/vault`, so the data directory must be under this exact path or a CLI override must be used.

### RQLite Connectivity

**Symptom:** Log shows `failed to fetch node list from RQLite, running in single-node mode`

**Diagnosis:**
```bash
# Check if RQLite is running
sudo systemctl status orama-*-rqlite

# Test RQLite endpoint
curl -s http://127.0.0.1:4001/status | jq .store.raft.state
```

**Resolution:**
- This warning is non-fatal. The guardian continues in single-node mode.
- Ensure RQLite is started before the vault guardian (normal dependency ordering).
- Verify the `rqlite_url` in config matches the actual RQLite address.

> **Note (v0.1.0):** RQLite node discovery is a stub. The guardian always falls back to single-node mode. This warning is expected in the current version.

### Share Write Failures

**Symptom:** Push returns 500 Internal Server Error, logs show `failed to write share for <hex>: <err>`

**Diagnosis:**
```bash
# Check disk space
df -h /opt/orama/.orama/data/vault

# Check inode usage
df -i /opt/orama/.orama/data/vault

# Check directory permissions
ls -la /opt/orama/.orama/data/vault/shares/
```

**Resolution:**
- If disk is full: free space or expand the partition.
- If inodes are exhausted (unlikely but possible with millions of users): clean up orphaned temp files.
- If permissions are wrong: fix ownership as shown above.

### Anti-Rollback Rejections

**Symptom:** Push returns 400 with `"version must be greater than current stored version"`

This is normal behavior -- the client tried to push an older version of a share. Common causes:
- Client retry after a network timeout (the first push actually succeeded).
- Client software bug sending stale version numbers.

**Diagnosis:**
```bash
# Check current stored version for an identity
cat /opt/orama/.orama/data/vault/shares/<identity_hex>/version
```

**Resolution:** The client must send a version number strictly greater than the stored value. This is not a guardian bug.

### Guardian Crash Loop

**Symptom:** `systemctl status` shows rapid restarts.

**Diagnosis:**
```bash
# View recent crash logs
sudo journalctl -u orama-vault -n 100 --no-pager | tail -50

# Check for OOM kills
sudo journalctl -k | grep -i "oom\|kill"
```

**Resolution:**
- If OOM killed: the 512 MiB memory limit may be too low. Check if share data has grown unexpectedly.
- If config parse error: check the config file syntax (or remove it to use defaults).
- If bind error: another process is using the port.

---

## Manual Operations

### Check Stored Shares

List all identities with stored shares:

```bash
ls /opt/orama/.orama/data/vault/shares/
```

Check a specific identity's share:

```bash
# View version
cat /opt/orama/.orama/data/vault/shares/<identity_hex>/version

# View share size
ls -la /opt/orama/.orama/data/vault/shares/<identity_hex>/share.bin

# View checksum
xxd /opt/orama/.orama/data/vault/shares/<identity_hex>/checksum.bin
```

### Count Total Shares

```bash
ls -d /opt/orama/.orama/data/vault/shares/*/ 2>/dev/null | wc -l
```

### Verify Share Integrity Manually

The guardian verifies HMAC integrity on every read. To manually check if a share file has been corrupted:

```bash
# If you know the integrity key, you can compute HMAC externally:
# (The integrity key is internal to the guardian and not stored on disk in the current version)

# Check file exists and is non-empty
test -s /opt/orama/.orama/data/vault/shares/<identity>/share.bin && echo "OK" || echo "MISSING/EMPTY"
test -s /opt/orama/.orama/data/vault/shares/<identity>/checksum.bin && echo "OK" || echo "MISSING/EMPTY"
```

### Test Push/Pull

```bash
# Push a test share
curl -X POST http://127.0.0.1:7500/v1/vault/push \
  -H "Content-Type: application/json" \
  -d '{
    "identity": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "share": "dGVzdCBzaGFyZSBkYXRh",
    "version": 1
  }'

# Expected: {"status":"stored"}

# Pull it back
curl -X POST http://127.0.0.1:7500/v1/vault/pull \
  -H "Content-Type: application/json" \
  -d '{
    "identity": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  }'

# Expected: {"share":"dGVzdCBzaGFyZSBkYXRh"}
```

### Delete a Share (Emergency)

> **Warning:** Deleting a share is destructive and cannot be undone. Only do this if you understand the implications for the user's recovery capability.

```bash
# Remove a specific identity's share directory
rm -rf /opt/orama/.orama/data/vault/shares/<identity_hex>
```

---

## Disaster Recovery

### Failure Scenarios

| Scenario | Impact | Recovery |
|----------|--------|----------|
| 1 node dies | No impact. K-1 other nodes can still reconstruct. | Replace node, shares will be re-pushed by clients. |
| K-1 nodes die | No impact. K remaining nodes can still reconstruct. | Replace nodes, reshare when quorum recovers. |
| N-K nodes die | No impact. K+1 or more nodes survive. | Replace nodes. |
| N-K+1 nodes die | **CRITICAL.** Only K-1 nodes remain. Cannot reconstruct. | Data loss for users who only have vault-based recovery. Users with mnemonic (Path A) are unaffected. |
| All nodes die | **TOTAL LOSS.** All shares destroyed. | Users must recover via mnemonic (Path A). Vault recovery (Path B) is permanently lost. |
| Data directory corrupted on 1 node | HMAC integrity check fails on read. Node returns errors for affected shares. | Delete corrupted share directory. Repair protocol will re-distribute once implemented. |

### Key Insight

The system can tolerate losing up to **N - K** nodes without any data loss. With default thresholds:

| Cluster Size | K | Max Node Loss |
|-------------|---|---------------|
| 5 nodes | 3 | 2 nodes |
| 14 nodes | 4 | 10 nodes |
| 50 nodes | 16 | 34 nodes |
| 100 nodes | 33 | 67 nodes |

### Backup Strategy

The data directory can be backed up with standard tools:

```bash
# Rsync backup
rsync -av /opt/orama/.orama/data/vault/ /backup/vault/

# Tarball backup
tar czf vault-backup-$(date +%Y%m%d).tar.gz -C /opt/orama/.orama/data/vault .
```

However, backups are generally unnecessary because:
1. Every node stores a share, so the cluster itself is the redundancy.
2. Shares are re-pushed by clients on updates.
3. The proactive re-sharing protocol (when fully wired) will redistribute shares automatically.

Backups are only useful if you fear simultaneous catastrophic failure of many nodes.

---

## Capacity Planning

### Per-Share Storage

Each user's share directory contains:

| File | Typical Size | Description |
|------|-------------|-------------|
| `share.bin` | ~1 KB | Encrypted share data (same size as original secret) |
| `version` | ~1-20 bytes | Version counter as ASCII digits |
| `checksum.bin` | 32 bytes | HMAC-SHA256 checksum |
| Directory entry | ~4 KB | Filesystem overhead (depends on filesystem) |

**Total per user per node: ~5 KB** (including filesystem overhead).

### Cluster-Wide Storage

With all-node replication, each user has one share on every node:

| Users | Per Node | Per Cluster (14 nodes) |
|-------|----------|------------------------|
| 1,000 | ~5 MB | ~70 MB |
| 10,000 | ~50 MB | ~700 MB |
| 100,000 | ~500 MB | ~7 GB |
| 1,000,000 | ~5 GB | ~70 GB |

At 1 million users, each node stores approximately 5 GB. This is well within the capability of any modern VPS.

### Memory Usage

The guardian uses minimal memory:
- Static binary: ~2-5 MB RSS.
- Per-connection: ~128 KB (64 KB read buffer + 64 KB write buffer).
- Single-threaded: only one connection is active at a time (MVP).
- No in-memory caching of shares: every read/write goes to disk.

The systemd `MemoryMax=512M` limit is generous for the current architecture. Actual usage is typically under 20 MB.

### Inode Usage

Each user creates one directory and 2-3 files. At 1 million users:
- ~4 million inodes.
- Most Linux filesystems default to millions of inodes (ext4: 1 inode per 16 KB).
- A 100 GB partition with ext4 defaults has ~6.5 million inodes.

This is unlikely to be a bottleneck but is worth monitoring on small partitions.

---

## Cluster Scaling

### Adding Nodes

When a new node joins the Orama network:

1. The vault guardian starts and registers itself via RQLite.
2. Other guardians detect the new node via the discovery module (join event).
3. The new node initially has zero shares.
4. Shares are populated in two ways:
   - **Client push:** When clients push new versions, they include the new node.
   - **Repair protocol:** The re-sharing protocol redistributes shares to include the new node (Phase 2).

The threshold K is recomputed based on the alive count: `K = max(3, floor(N/3))`.

### Removing Nodes

When a node leaves (graceful shutdown or failure):

1. Other guardians detect the departure via missed heartbeats (suspect at 15s, dead at 60s).
2. The departed node's shares are lost.
3. If N - K nodes remain alive, no data is lost.
4. If the departure drops below the safety threshold (K+1 alive), the repair protocol triggers re-sharing to adjust.

### Threshold Adjustment

The threshold is dynamic and automatic:

```
K = max(3, floor(alive_count / 3))
```

- Adding nodes generally does not change K until the cluster grows significantly.
- Removing nodes may reduce K if the alive count drops enough.
- K never drops below 3, ensuring a minimum collusion resistance.

### Write Quorum

Write quorum requires supermajority acknowledgment:

```
W = ceil(2/3 * alive_count)
```

| Alive | Write Quorum (W) |
|-------|-------------------|
| 1 | 1 |
| 2 | 2 |
| 3 | 2 |
| 4 | 3 |
| 5 | 4 |
| 14 | 10 |
| 100 | 67 |

A push succeeds only if W guardians acknowledge storage. This ensures consistency even with some nodes being slow or temporarily unreachable.

> **Note (v0.1.0):** Write quorum is computed but not enforced in the current single-node push handler. Multi-guardian fan-out is Phase 2.

---

## Operational Checklist

### Pre-Deploy

- [ ] Binary built with `-Doptimize=ReleaseSafe` for the correct target.
- [ ] Data directory exists with correct ownership and permissions.
- [ ] systemd service file installed and enabled.
- [ ] Firewall rules allow port 7500 (client) and 7501 (peer, WireGuard only).
- [ ] WireGuard is up and peers are connected.

### Post-Deploy

- [ ] Health endpoint responds: `curl http://127.0.0.1:7500/v1/vault/health`
- [ ] Status endpoint shows correct config: `curl http://127.0.0.1:7500/v1/vault/status`
- [ ] No error-level log messages: `sudo journalctl -u orama-vault -p err -n 10`
- [ ] Test push/pull cycle works (see "Test Push/Pull" section above).

### Periodic Checks

- [ ] Health endpoint responds on all nodes.
- [ ] Share count is consistent across nodes (same identities on each).
- [ ] Disk usage is within expected bounds.
- [ ] No repeated error messages in the journal.
- [ ] systemd reports `active (running)` with uptime matching expectations.
