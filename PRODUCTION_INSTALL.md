# Production Installation Guide - DeBros Network

This guide covers production deployment of the DeBros Network using the `dbn prod` command suite.

## System Requirements

- **OS**: Ubuntu 20.04 LTS or later, Debian 11+, or other Linux distributions
- **Architecture**: x86_64 (amd64) or ARM64 (aarch64)
- **RAM**: Minimum 4GB, recommended 8GB+
- **Storage**: Minimum 50GB SSD recommended
- **Ports**:
  - 4001 (P2P networking)
  - 4501 (IPFS HTTP API - bootstrap), 4502/4503 (node2/node3)
  - 5001-5003 (RQLite HTTP - one per node)
  - 6001 (Gateway)
  - 7001-7003 (RQLite Raft - one per node)
  - 9094 (IPFS Cluster API - bootstrap), 9104/9114 (node2/node3)
  - 3320/3322 (Olric)
  - 80, 443 (for HTTPS with Let's Encrypt)

## Installation

### Prerequisites

1. **Root access required**: All production operations require sudo/root privileges
2. **Supported distros**: Ubuntu, Debian, Fedora (via package manager)
3. **Basic tools**: `curl`, `git`, `make`, `build-essential`, `wget`

### Single-Node Bootstrap Installation

Deploy the first node (bootstrap node) on a VPS:

```bash
sudo dbn prod install --bootstrap
```

This will:

1. Check system prerequisites (OS, arch, root privileges, basic tools)
2. Provision the `debros` system user and filesystem structure at `~/.debros`
3. Download and install all required binaries (Go, RQLite, IPFS, IPFS Cluster, Olric, DeBros)
4. Generate secrets (cluster secret, swarm key, node identity)
5. Initialize repositories (IPFS, IPFS Cluster, RQLite)
6. Generate configurations for bootstrap node
7. Create and start systemd services

All files will be under `/home/debros/.debros`:

```
~/.debros/
├── bin/                    # Compiled binaries
├── configs/                # YAML configurations
├── data/
│   ├── ipfs/               # IPFS repository
│   ├── ipfs-cluster/       # IPFS Cluster state
│   └── rqlite/             # RQLite database
├── logs/                   # Service logs
└── secrets/                # Keys and certificates
```

## Service Management

### Check Service Status

```bash
sudo systemctl status debros-node-bootstrap
sudo systemctl status debros-gateway
sudo systemctl status debros-rqlite-bootstrap
```

### View Service Logs

```bash
# Bootstrap node logs
sudo journalctl -u debros-node-bootstrap -f

# Gateway logs
sudo journalctl -u debros-gateway -f

# All services
sudo journalctl -u "debros-*" -f
```

## Health Checks

After installation, verify services are running:

```bash
# Check IPFS
curl http://localhost:4501/api/v0/id

# Check RQLite cluster
curl http://localhost:5001/status

# Check Gateway
curl http://localhost:6001/health

# Check Olric
curl http://localhost:3320/ping
```

## Port Reference

### Development Environment (via `make dev`)

- IPFS API: 4501 (bootstrap), 4502 (node2), 4503 (node3)
- RQLite HTTP: 5001, 5002, 5003
- RQLite Raft: 7001, 7002, 7003
- IPFS Cluster: 9094, 9104, 9114
- P2P: 4001, 4002, 4003
- Gateway: 6001
- Olric: 3320, 3322

### Production Environment (via `sudo dbn prod install`)

- Same port assignments as development for consistency

## Configuration Files

Key configuration files are located in `~/.debros/configs/`:

- **bootstrap.yaml**: Bootstrap node configuration
- **node.yaml**: Regular node configuration
- **gateway.yaml**: HTTP gateway configuration
- **olric.yaml**: In-memory cache configuration

Edit these files directly for advanced configuration, then restart services:

```bash
sudo systemctl restart debros-node-bootstrap
```

## Troubleshooting

### Port already in use

Check which process is using the port:

```bash
sudo lsof -i :4501
sudo lsof -i :5001
sudo lsof -i :7001
```

Kill conflicting processes or change ports in config.

### RQLite cluster not forming

Ensure:

1. Bootstrap node is running: `systemctl status debros-rqlite-bootstrap`
2. Network connectivity between nodes on ports 5001+ (HTTP) and 7001+ (Raft)
3. Check logs: `journalctl -u debros-rqlite-bootstrap -f`

---

**Last Updated**: November 2024
**Compatible with**: Network v1.0.0+
