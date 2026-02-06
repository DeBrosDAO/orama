# CoreDNS RQLite Plugin

This directory contains a custom CoreDNS plugin that serves DNS records from RQLite, enabling dynamic DNS for Orama Network deployments.

## Architecture

The plugin provides:
- **Dynamic DNS Records**: Queries RQLite for DNS records in real-time
- **Caching**: In-memory cache to reduce database load
- **Health Monitoring**: Periodic health checks of RQLite connection
- **Wildcard Support**: Handles wildcard DNS patterns (e.g., `*.node-xyz.orama.network`)

## Building CoreDNS with RQLite Plugin

CoreDNS plugins must be compiled into the binary. Follow these steps:

### 1. Install Prerequisites

```bash
# Install Go 1.21 or later
wget https://go.dev/dl/go1.21.6.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.21.6.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# Verify Go installation
go version
```

### 2. Clone CoreDNS

```bash
cd /tmp
git clone https://github.com/coredns/coredns.git
cd coredns
git checkout v1.11.1  # Match the version in install script
```

### 3. Add RQLite Plugin

Edit `plugin.cfg` in the CoreDNS root directory and add the rqlite plugin in the appropriate position (after `cache`, before `forward`):

```
# plugin.cfg
cache:cache
rqlite:github.com/DeBrosOfficial/network/pkg/coredns/rqlite
forward:forward
```

### 4. Copy Plugin Code

```bash
# From your network repository root
cd /path/to/network
cp -r pkg/coredns/rqlite /tmp/coredns/plugin/
```

### 5. Update go.mod

```bash
cd /tmp/coredns

# Add your module as a dependency
go mod edit -replace github.com/DeBrosOfficial/network=/path/to/network

# Get dependencies
go get github.com/DeBrosOfficial/network/pkg/coredns/rqlite
go mod tidy
```

### 6. Build CoreDNS

```bash
make
```

This creates the `coredns` binary in the current directory with the RQLite plugin compiled in.

### 7. Verify Plugin

```bash
./coredns -plugins | grep rqlite
```

You should see:
```
dns.rqlite
```

## Installation on Nodes

### Using the Install Script

```bash
# Build custom CoreDNS first (see above)
# Then copy the binary to the network repo
cp /tmp/coredns/coredns /path/to/network/bin/

# Run install script on each node
cd /path/to/network
sudo ./scripts/install-coredns.sh

# The script will:
# 1. Copy coredns binary to /usr/local/bin/
# 2. Create config directories
# 3. Install systemd service
# 4. Set up proper permissions
```

### Manual Installation

If you prefer manual installation:

```bash
# 1. Copy binary
sudo cp coredns /usr/local/bin/
sudo chmod +x /usr/local/bin/coredns

# 2. Create directories
sudo mkdir -p /etc/coredns
sudo mkdir -p /var/lib/coredns
sudo chown debros:debros /var/lib/coredns

# 3. Copy configuration
sudo cp configs/coredns/Corefile /etc/coredns/

# 4. Install systemd service
sudo cp configs/coredns/coredns.service /etc/systemd/system/
sudo systemctl daemon-reload

# 5. Configure firewall
sudo ufw allow 53/tcp
sudo ufw allow 53/udp
sudo ufw allow 8080/tcp  # Health check
sudo ufw allow 9153/tcp  # Metrics

# 6. Start service
sudo systemctl enable coredns
sudo systemctl start coredns
```

## Configuration

### Corefile

The Corefile at `/etc/coredns/Corefile` configures CoreDNS behavior:

```corefile
orama.network {
    rqlite {
        dsn http://localhost:5001    # RQLite HTTP endpoint
        refresh 10s                   # Health check interval
        ttl 300                       # Cache TTL in seconds
        cache_size 10000              # Max cached entries
    }

    cache {
        success 10000 300             # Cache successful responses
        denial 5000 60                # Cache NXDOMAIN responses
        prefetch 10                   # Prefetch before expiry
    }

    log { class denial error }
    errors
    health :8080
    prometheus :9153
}

. {
    forward . 8.8.8.8 8.8.4.4 1.1.1.1
    cache 300
    errors
}
```

### RQLite Connection

Ensure RQLite is running and accessible:

```bash
# Test RQLite connectivity
curl http://localhost:5001/status

# Test DNS record query
curl -G http://localhost:5001/db/query \
  --data-urlencode 'q=SELECT * FROM dns_records LIMIT 5'
```

## Testing

### 1. Add Test DNS Record

```bash
# Via RQLite
curl -XPOST 'http://localhost:5001/db/execute' \
  -H 'Content-Type: application/json' \
  -d '[
    ["INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active) VALUES (?, ?, ?, ?, ?, ?, ?)",
     "test.orama.network.", "A", "1.2.3.4", 300, "test", "system", true]
  ]'
```

### 2. Query CoreDNS

```bash
# Query local CoreDNS
dig @localhost test.orama.network

# Expected output:
# ;; ANSWER SECTION:
# test.orama.network. 300 IN A 1.2.3.4

# Query from remote machine
dig @<node-ip> test.orama.network
```

### 3. Test Wildcard

```bash
# Add wildcard record
curl -XPOST 'http://localhost:5001/db/execute' \
  -H 'Content-Type: application/json' \
  -d '[
    ["INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active) VALUES (?, ?, ?, ?, ?, ?, ?)",
     "*.node-abc123.orama.network.", "A", "1.2.3.4", 300, "test", "system", true]
  ]'

# Test wildcard resolution
dig @localhost app1.node-abc123.orama.network
dig @localhost app2.node-abc123.orama.network
```

### 4. Check Health

```bash
# Health check endpoint
curl http://localhost:8080/health

# Prometheus metrics
curl http://localhost:9153/metrics | grep coredns_rqlite
```

### 5. Monitor Logs

```bash
# Follow CoreDNS logs
sudo journalctl -u coredns -f

# Check for errors
sudo journalctl -u coredns --since "10 minutes ago" | grep -i error
```

## Monitoring

### Metrics

CoreDNS exports Prometheus metrics on port 9153:

- `coredns_dns_requests_total` - Total DNS requests
- `coredns_dns_responses_total` - Total DNS responses by rcode
- `coredns_cache_hits_total` - Cache hit rate
- `coredns_cache_misses_total` - Cache miss rate

### Health Checks

The health endpoint at `:8080/health` returns:
- `200 OK` if RQLite is healthy
- `503 Service Unavailable` if RQLite is unhealthy

## Troubleshooting

### Plugin Not Found

If CoreDNS fails to start with "plugin not found":
1. Verify plugin was compiled in: `coredns -plugins | grep rqlite`
2. Rebuild CoreDNS with plugin included (see Build section)

### RQLite Connection Failed

```bash
# Check RQLite is running
sudo systemctl status rqlite

# Test RQLite HTTP API
curl http://localhost:5001/status

# Check firewall
sudo ufw status | grep 5001
```

### DNS Queries Not Working

```bash
# 1. Check CoreDNS is listening on port 53
sudo netstat -tulpn | grep :53

# 2. Test local query
dig @127.0.0.1 test.orama.network

# 3. Check logs for errors
sudo journalctl -u coredns --since "5 minutes ago"

# 4. Verify DNS records exist in RQLite
curl -G http://localhost:5001/db/query \
  --data-urlencode 'q=SELECT * FROM dns_records WHERE is_active = TRUE'
```

### Cache Issues

If DNS responses are stale:

```bash
# Restart CoreDNS to clear cache
sudo systemctl restart coredns

# Or reduce cache TTL in Corefile:
# cache {
#     success 10000 60  # Reduce to 60 seconds
# }
```

## Production Deployment

### 1. Deploy to All Nameservers

Install CoreDNS on all 4 nameserver nodes (ns1-ns4).

### 2. Configure Registrar

At your domain registrar, set NS records for `orama.network`:

```
orama.network.  IN  NS  ns1.orama.network.
orama.network.  IN  NS  ns2.orama.network.
orama.network.  IN  NS  ns3.orama.network.
orama.network.  IN  NS  ns4.orama.network.
```

Add glue records:

```
ns1.orama.network.  IN  A  <node-1-ip>
ns2.orama.network.  IN  A  <node-2-ip>
ns3.orama.network.  IN  A  <node-3-ip>
ns4.orama.network.  IN  A  <node-4-ip>
```

### 3. Verify Propagation

```bash
# Check NS records
dig NS orama.network

# Check from public DNS
dig @8.8.8.8 test.orama.network

# Check from all nameservers
dig @ns1.orama.network test.orama.network
dig @ns2.orama.network test.orama.network
dig @ns3.orama.network test.orama.network
dig @ns4.orama.network test.orama.network
```

### 4. Monitor

Set up monitoring for:
- CoreDNS uptime on all nodes
- DNS query latency
- Cache hit rate
- RQLite connection health
- Query error rate

## Security

### Firewall

Only expose necessary ports:
- Port 53 (DNS): Public
- Port 8080 (Health): Internal only
- Port 9153 (Metrics): Internal only
- Port 5001 (RQLite): Internal only

```bash
# Allow DNS from anywhere
sudo ufw allow 53/tcp
sudo ufw allow 53/udp

# Restrict health and metrics to internal network
sudo ufw allow from 10.0.0.0/8 to any port 8080
sudo ufw allow from 10.0.0.0/8 to any port 9153
```

### DNS Security

- Enable DNSSEC (future enhancement)
- Rate limit queries (add to Corefile)
- Monitor for DNS amplification attacks
- Validate RQLite data integrity

## Performance Tuning

### Cache Optimization

Adjust cache settings based on query patterns:

```corefile
cache {
    success 50000 600  # 50k entries, 10 min TTL
    denial 10000 300   # 10k NXDOMAIN, 5 min TTL
    prefetch 20        # Prefetch 20s before expiry
}
```

### RQLite Connection Pool

The plugin maintains a connection pool:
- Max idle connections: 10
- Idle timeout: 90s
- Request timeout: 10s

Adjust in `client.go` if needed for higher load.

### System Limits

```bash
# Increase file descriptor limit
# Add to /etc/security/limits.conf:
debros soft nofile 65536
debros hard nofile 65536
```

## Next Steps

After CoreDNS is operational:
1. Implement automatic DNS record creation in deployment handlers
2. Add DNS record cleanup for deleted deployments
3. Set up DNS monitoring and alerting
4. Configure domain routing middleware in gateway
5. Test end-to-end deployment flow
