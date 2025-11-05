# IPFS Cluster Setup Guide

This guide explains how IPFS Cluster is configured to run on every DeBros Network node.

## Overview

Each DeBros Network node runs its own IPFS Cluster peer, enabling distributed pinning and replication across the network. The cluster uses CRDT consensus for automatic peer discovery.

## Architecture

- **IPFS (Kubo)**: Runs on each node, handles content storage and retrieval
- **IPFS Cluster**: Runs on each node, manages pinning and replication
- **Cluster Consensus**: Uses CRDT (instead of Raft) for simpler multi-node setup

## Automatic Setup

When you run `network-cli setup`, the following happens automatically:

1. IPFS (Kubo) and IPFS Cluster are installed
2. IPFS repository is initialized for each node
3. IPFS Cluster service.json config is generated
4. Systemd services are created and started:
   - `debros-ipfs` - IPFS daemon
   - `debros-ipfs-cluster` - IPFS Cluster service
   - `debros-node` - DeBros Network node (depends on cluster)
   - `debros-gateway` - HTTP Gateway (depends on node)

## Configuration

### Node Configs

Each node config (`~/.debros/bootstrap.yaml`, `~/.debros/node.yaml`, etc.) includes:

```yaml
database:
  ipfs:
    cluster_api_url: "http://localhost:9094" # Local cluster API
    api_url: "http://localhost:5001" # Local IPFS API
    replication_factor: 3 # Desired replication
```

### Cluster Service Config

Cluster service configs are stored at:

- Bootstrap: `~/.debros/bootstrap/ipfs-cluster/service.json`
- Nodes: `~/.debros/node/ipfs-cluster/service.json`

Key settings:

- **Consensus**: CRDT (automatic peer discovery)
- **API Listen**: `0.0.0.0:9094` (REST API)
- **Cluster Listen**: `0.0.0.0:9096` (peer-to-peer)
- **Secret**: Shared cluster secret stored at `~/.debros/cluster-secret`

## Verification

### Check Cluster Peers

From any node, verify all cluster peers are connected:

```bash
sudo -u debros ipfs-cluster-ctl --host http://localhost:9094 peers ls
```

You should see all cluster peers listed (bootstrap, node1, node2, etc.).

### Check IPFS Daemon

Verify IPFS is running:

```bash
sudo -u debros ipfs daemon --repo-dir=~/.debros/bootstrap/ipfs/repo
# Or for regular nodes:
sudo -u debros ipfs daemon --repo-dir=~/.debros/node/ipfs/repo
```

### Check Service Status

```bash
network-cli service status all
```

Should show:

- `debros-ipfs` - running
- `debros-ipfs-cluster` - running
- `debros-node` - running
- `debros-gateway` - running

## Troubleshooting

### Cluster Peers Not Connecting

If peers aren't discovering each other:

1. **Check firewall**: Ensure ports 9096 (cluster swarm) and 9094 (cluster API) are open
2. **Verify secret**: All nodes must use the same cluster secret from `~/.debros/cluster-secret`
3. **Check logs**: `journalctl -u debros-ipfs-cluster -f`

### Not Enough Peers Error

If you see "not enough peers to allocate CID" errors:

- The cluster needs at least `replication_factor` peers running
- Check that all nodes have `debros-ipfs-cluster` service running
- Verify with `ipfs-cluster-ctl peers ls`

### IPFS Not Starting

If IPFS daemon fails to start:

1. Check IPFS repo exists: `ls -la ~/.debros/bootstrap/ipfs/repo/`
2. Check permissions: `chown -R debros:debros ~/.debros/bootstrap/ipfs/`
3. Check logs: `journalctl -u debros-ipfs -f`

## Manual Setup (If Needed)

If automatic setup didn't work, you can manually initialize:

### 1. Initialize IPFS

```bash
sudo -u debros ipfs init --profile=server --repo-dir=~/.debros/bootstrap/ipfs/repo
sudo -u debros ipfs config --json Addresses.API '["/ip4/localhost/tcp/5001"]' --repo-dir=~/.debros/bootstrap/ipfs/repo
```

### 2. Initialize Cluster

```bash
# Generate or get cluster secret
CLUSTER_SECRET=$(cat ~/.debros/cluster-secret)

# Initialize cluster (will create service.json)
sudo -u debros ipfs-cluster-service init --consensus crdt
```

### 3. Start Services

```bash
systemctl start debros-ipfs
systemctl start debros-ipfs-cluster
systemctl start debros-node
systemctl start debros-gateway
```

## Ports

- **4001**: IPFS swarm (LibP2P)
- **5001**: IPFS HTTP API
- **8080**: IPFS Gateway (optional)
- **9094**: IPFS Cluster REST API
- **9096**: IPFS Cluster swarm (LibP2P)

## Replication Factor

The default replication factor is 3, meaning content is pinned to 3 cluster peers. This requires at least 3 nodes running cluster peers.

To change replication factor, edit node configs:

```yaml
database:
  ipfs:
    replication_factor: 1 # For single-node development
```

## Security Notes

- Cluster secret is stored at `~/.debros/cluster-secret` (mode 0600)
- Cluster API (port 9094) should be firewalled in production
- IPFS API (port 5001) should only be accessible locally
