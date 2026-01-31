# Clean Node — Full Reset Guide

How to completely remove all Orama Network state from a VPS so it can be reinstalled fresh.

## Quick Clean (Copy-Paste)

Run this as root or with sudo on the target VPS:

```bash
# 1. Stop and disable all services
sudo systemctl stop debros-node debros-ipfs debros-ipfs-cluster debros-olric coredns caddy 2>/dev/null
sudo systemctl disable debros-node debros-ipfs debros-ipfs-cluster debros-olric coredns caddy 2>/dev/null

# 2. Remove systemd service files
sudo rm -f /etc/systemd/system/debros-*.service
sudo rm -f /etc/systemd/system/coredns.service
sudo rm -f /etc/systemd/system/caddy.service
sudo systemctl daemon-reload

# 3. Tear down WireGuard
# Must stop the systemd unit first — wg-quick@wg0 is a oneshot with
# RemainAfterExit=yes, so it stays "active (exited)" even after the
# interface is removed. Without "stop", a future "systemctl start" is a no-op.
sudo systemctl stop wg-quick@wg0 2>/dev/null
sudo wg-quick down wg0 2>/dev/null
sudo systemctl disable wg-quick@wg0 2>/dev/null
sudo rm -f /etc/wireguard/wg0.conf

# 4. Reset UFW firewall
sudo ufw --force reset
sudo ufw allow 22/tcp
sudo ufw --force enable

# 5. Remove debros user and home directory
sudo userdel -r debros 2>/dev/null
sudo rm -rf /home/debros

# 6. Remove sudoers files
sudo rm -f /etc/sudoers.d/debros-access
sudo rm -f /etc/sudoers.d/debros-deployments
sudo rm -f /etc/sudoers.d/debros-wireguard

# 7. Remove CoreDNS config
sudo rm -rf /etc/coredns

# 8. Remove Caddy config and data
sudo rm -rf /etc/caddy
sudo rm -rf /var/lib/caddy

# 9. Remove deployment systemd services (dynamic)
sudo rm -f /etc/systemd/system/orama-deploy-*.service
sudo systemctl daemon-reload

# 10. Clean temp files
sudo rm -f /tmp/orama /tmp/network-source.tar.gz /tmp/network-source.zip
sudo rm -rf /tmp/network-extract /tmp/coredns-build /tmp/caddy-build

echo "Node cleaned. Ready for fresh install."
```

## What This Removes

| Category | Paths |
|----------|-------|
| **User** | `debros` system user and `/home/debros/` |
| **App data** | `/home/debros/.orama/` (configs, secrets, logs, IPFS, RQLite, Olric) |
| **Source code** | `/home/debros/src/` |
| **Binaries** | `/home/debros/bin/orama-node`, `/home/debros/bin/gateway` |
| **Systemd** | `debros-*.service`, `coredns.service`, `caddy.service`, `orama-deploy-*.service` |
| **WireGuard** | `/etc/wireguard/wg0.conf`, `wg-quick@wg0` systemd unit |
| **Firewall** | All UFW rules (reset to default + SSH only) |
| **Sudoers** | `/etc/sudoers.d/debros-*` |
| **CoreDNS** | `/etc/coredns/Corefile` |
| **Caddy** | `/etc/caddy/Caddyfile`, `/var/lib/caddy/` (TLS certs) |
| **Temp files** | `/tmp/orama`, `/tmp/network-source.*`, build dirs |

## What This Does NOT Remove

These are shared system tools that may be used by other software. Remove manually if desired:

| Binary | Path | Remove Command |
|--------|------|----------------|
| RQLite | `/usr/local/bin/rqlited` | `sudo rm /usr/local/bin/rqlited` |
| IPFS | `/usr/local/bin/ipfs` | `sudo rm /usr/local/bin/ipfs` |
| IPFS Cluster | `/usr/local/bin/ipfs-cluster-service` | `sudo rm /usr/local/bin/ipfs-cluster-service` |
| Olric | `/usr/local/bin/olric-server` | `sudo rm /usr/local/bin/olric-server` |
| CoreDNS | `/usr/local/bin/coredns` | `sudo rm /usr/local/bin/coredns` |
| Caddy | `/usr/bin/caddy` | `sudo rm /usr/bin/caddy` |
| xcaddy | `/usr/local/bin/xcaddy` | `sudo rm /usr/local/bin/xcaddy` |
| Go | `/usr/local/go/` | `sudo rm -rf /usr/local/go` |
| Orama CLI | `/usr/local/bin/orama` | `sudo rm /usr/local/bin/orama` |

## Nuclear Clean (Remove Everything Including Binaries)

```bash
# Run quick clean above first, then:
sudo rm -f /usr/local/bin/rqlited
sudo rm -f /usr/local/bin/ipfs
sudo rm -f /usr/local/bin/ipfs-cluster-service
sudo rm -f /usr/local/bin/olric-server
sudo rm -f /usr/local/bin/coredns
sudo rm -f /usr/local/bin/xcaddy
sudo rm -f /usr/bin/caddy
sudo rm -f /usr/local/bin/orama
```

## Multi-Node Clean

To clean all nodes at once from your local machine:

```bash
# Define your nodes
NODES=(
  "ubuntu@141.227.165.168:password1"
  "ubuntu@141.227.165.154:password2"
  "ubuntu@141.227.156.51:password3"
)

for entry in "${NODES[@]}"; do
  IFS=: read -r userhost pass <<< "$entry"
  echo "Cleaning $userhost..."
  sshpass -p "$pass" ssh -o StrictHostKeyChecking=no "$userhost" 'bash -s' << 'CLEAN'
sudo systemctl stop debros-node debros-ipfs debros-ipfs-cluster debros-olric coredns caddy 2>/dev/null
sudo systemctl disable debros-node debros-ipfs debros-ipfs-cluster debros-olric coredns caddy 2>/dev/null
sudo rm -f /etc/systemd/system/debros-*.service /etc/systemd/system/coredns.service /etc/systemd/system/caddy.service /etc/systemd/system/orama-deploy-*.service
sudo systemctl daemon-reload
sudo systemctl stop wg-quick@wg0 2>/dev/null
sudo wg-quick down wg0 2>/dev/null
sudo systemctl disable wg-quick@wg0 2>/dev/null
sudo rm -f /etc/wireguard/wg0.conf
sudo ufw --force reset && sudo ufw allow 22/tcp && sudo ufw --force enable
sudo userdel -r debros 2>/dev/null
sudo rm -rf /home/debros
sudo rm -f /etc/sudoers.d/debros-access /etc/sudoers.d/debros-deployments /etc/sudoers.d/debros-wireguard
sudo rm -rf /etc/coredns /etc/caddy /var/lib/caddy
sudo rm -f /tmp/orama /tmp/network-source.tar.gz
sudo rm -rf /tmp/network-extract /tmp/coredns-build /tmp/caddy-build
echo "Done"
CLEAN
done
```
