# Clean Node — Full Reset Guide

How to completely remove all Orama Network state from a VPS so it can be reinstalled fresh.

> **Prefer the CLI.** For a node that is or was a cluster member, run
> `orama node decommission --env <env> --node <ip>`: it retires the node from
> raft, the mesh and the node registry from a *survivor* first, then erases it.
> Doing only the erase — which is all this guide, and the deprecated
> `orama node clean`, ever did — leaves the node a configured raft voter counted
> toward quorum, with its `wireguard_peers` row still applied to every
> survivor's interface. Use `orama node wipe` when the node is already retired,
> and the manual steps below only when the CLI cannot reach the machine.

> **OramaOS nodes:** This guide applies to Ubuntu-based nodes only. OramaOS has no SSH or shell access. To remove an OramaOS node: use `POST /v1/node/leave` via the Gateway API for graceful departure, or reflash the OramaOS image via your VPS provider's dashboard for a factory reset. See [ORAMAOS_DEPLOYMENT.md](ORAMAOS_DEPLOYMENT.md) for details.

## Quick Clean (Copy-Paste)

Run this as root or with sudo on the target VPS:

```bash
# 1. Stop supervisor (PartOf stops @index / @nameserver), then leftover host units
sudo systemctl stop orama-node 2>/dev/null
sudo systemctl stop 'orama-namespace-*@*' 2>/dev/null
sudo systemctl disable orama-node 2>/dev/null
sudo systemctl stop orama-vault orama-ipfs orama-ipfs-cluster orama-ipfs-gc.timer orama-olric orama-anyone-relay orama-anyone-client coredns caddy ntfy orama-sni-router wg-quick@wg0 2>/dev/null
sudo systemctl disable orama-vault orama-ipfs orama-ipfs-cluster orama-ipfs-gc.timer orama-olric orama-anyone-relay orama-anyone-client coredns caddy ntfy orama-sni-router wg-quick@wg0 2>/dev/null

# 1b. Kill leftover processes (binaries may run outside systemd)
sudo pkill -f orama-node 2>/dev/null; sudo pkill -f ipfs-cluster-service 2>/dev/null
sudo pkill -f "ipfs daemon" 2>/dev/null; sudo pkill -f olric-server 2>/dev/null
sudo pkill -f rqlited 2>/dev/null; sudo pkill -f coredns 2>/dev/null
sudo pkill -f vault-guardian 2>/dev/null
sleep 1

# 2. Remove systemd service files
sudo rm -f /etc/systemd/system/orama-*.service
sudo rm -f /etc/systemd/system/orama-*.timer
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

# 5. Remove orama data directory
sudo rm -rf /opt/orama
sudo rm -rf /var/lib/ntfy /run/ntfy
sudo rm -rf /etc/anon
# /var/lib/anon (Anyone identity) is preserved unless you also:
#   sudo rm -rf /var/lib/anon
sudo swapoff -a 2>/dev/null || true
# rm -rf is unlink, not cryptographic erase. Assume decommissioned provider disks are readable.

# 6. Remove orama system user (created by every install; services run as it,
#    so services must be stopped first or userdel fails with "user in use")
sudo userdel -r orama 2>/dev/null
sudo rm -rf /home/orama
sudo rm -f /etc/sudoers.d/orama-namespaces
sudo rm -f /etc/sudoers.d/orama-access
sudo rm -f /etc/sudoers.d/orama-deployments
sudo rm -f /etc/sudoers.d/orama-wireguard

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
| **App data** | `/opt/orama/.orama/` (configs, secrets, logs, IPFS, RQLite, Olric) |
| **Source code** | `/opt/orama/src/` |
| **Binaries** | `/opt/orama/bin/orama-node`, `/opt/orama/bin/gateway`, `/opt/orama/bin/vault-guardian` |
| **Systemd** | `orama-node.service`, `orama-namespace-*@index`, leftover `orama-*.service`, `coredns.service`, `caddy.service`, `orama-deploy-*.service` |
| **WireGuard** | `/etc/wireguard/wg0.conf`, `wg-quick@wg0` systemd unit |
| **Firewall** | All UFW rules (reset to default + SSH only) |
| **User** | `orama` system user, `/etc/sudoers.d/orama-*` (incl. `orama-namespaces`) |
| **CoreDNS** | `/etc/coredns/Corefile` |
| **Caddy** | `/etc/caddy/Caddyfile`, `/var/lib/caddy/` (TLS certs) |
| **Anyone** | `orama-namespace-anyone-client@index` (and leftover `orama-anyone-client.service` / `orama-anyone-relay.service` if present) |
| **Temp files** | `/tmp/orama`, `/tmp/network-source.*`, build dirs |

## What This Does NOT Remove

Binaries installed to `/usr/local/bin` (and Caddy in `/usr/bin`) are left in place. Remove manually if desired:

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
| Orama Node | `/usr/local/bin/orama-node` | `sudo rm /usr/local/bin/orama-node` |
| Gateway | `/usr/local/bin/gateway` | `sudo rm /usr/local/bin/gateway` |
| Identity | `/usr/local/bin/identity` | `sudo rm /usr/local/bin/identity` |
| SFU | `/usr/local/bin/sfu` | `sudo rm /usr/local/bin/sfu` |
| TURN | `/usr/local/bin/turn` | `sudo rm /usr/local/bin/turn` |

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
sudo rm -f /usr/local/bin/orama-node /usr/local/bin/gateway
sudo rm -f /usr/local/bin/identity /usr/local/bin/sfu /usr/local/bin/turn
sudo rm -f /usr/local/bin/orama-sni-router
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
sudo systemctl stop orama-node 2>/dev/null
sudo systemctl stop 'orama-namespace-*@*' 2>/dev/null
sudo systemctl disable orama-node 2>/dev/null
sudo systemctl stop orama-vault orama-ipfs orama-ipfs-cluster orama-ipfs-gc.timer orama-olric orama-anyone-relay orama-anyone-client coredns caddy ntfy orama-sni-router wg-quick@wg0 2>/dev/null
sudo systemctl disable orama-vault orama-ipfs orama-ipfs-cluster orama-ipfs-gc.timer orama-olric orama-anyone-relay orama-anyone-client coredns caddy ntfy orama-sni-router wg-quick@wg0 2>/dev/null
sudo rm -f /etc/systemd/system/orama-*.service /etc/systemd/system/orama-*.timer /etc/systemd/system/coredns.service /etc/systemd/system/caddy.service /etc/systemd/system/orama-deploy-*.service /etc/systemd/system/ntfy.service
sudo systemctl daemon-reload
sudo systemctl stop wg-quick@wg0 2>/dev/null
sudo wg-quick down wg0 2>/dev/null
sudo systemctl disable wg-quick@wg0 2>/dev/null
sudo rm -f /etc/wireguard/wg0.conf
sudo ufw --force reset && sudo ufw allow 22/tcp && sudo ufw --force enable
sudo rm -rf /opt/orama
sudo userdel -r orama 2>/dev/null
sudo rm -rf /home/orama
sudo rm -f /etc/sudoers.d/orama-namespaces /etc/sudoers.d/orama-access /etc/sudoers.d/orama-deployments /etc/sudoers.d/orama-wireguard
sudo rm -rf /etc/coredns /etc/caddy /var/lib/caddy
sudo rm -f /tmp/orama /tmp/network-source.tar.gz
sudo rm -rf /tmp/network-extract /tmp/coredns-build /tmp/caddy-build
echo "Done"
CLEAN
done
```
