# Devnet Installation Commands

This document contains example installation commands for a multi-node devnet cluster.

Anyone is installed as a **client** on every node by default (SOCKS5 on `:9050` for `/v1/proxy/anon`). There is no relay/ORPort mode.

**Note:** Store credentials securely (not in version control).

## Installation Order

Install nodes **one at a time**, waiting for each to complete before starting the next:

1. ns1 (genesis nameserver)
2. ns2 (nameserver)
3. ns3 (nameserver)
4. Additional workers as needed (no `--nameserver`)

## ns1 - Genesis nameserver

```bash
# SSH: <user>@<ns1-ip>

sudo orama node install \
  --vps-ip <ns1-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --nameserver
```

After ns1 is installed, generate invite tokens:
```bash
sudo orama node invite --expiry 24h
```

## ns2 / ns3 - Joining nameservers

```bash
# SSH: <user>@<ns-ip>

sudo orama node install \
  --join http://<ns1-ip> --token <TOKEN> \
  --vps-ip <ns-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --nameserver
```

## Worker (non-nameserver)

Domain is auto-generated (e.g., `node-a3f8k2.<your-domain.com>`). No `--domain` flag needed.

```bash
# SSH: <user>@<node-ip>

sudo orama node install \
  --join http://<ns1-ip> --token <TOKEN> \
  --vps-ip <node-ip> \
  --base-domain <your-domain.com>
```

## Verification

After all nodes are installed, verify cluster health:

```bash
# Full cluster report (from local machine)
./bin/orama monitor report --env devnet

# Single node health
./bin/orama monitor report --env devnet --node <ip>

# Or manually from any VPS:
curl -s http://localhost:5001/status | jq -r '.store.raft.state, .store.raft.num_peers'
curl -s http://localhost:6001/health
systemctl status orama-anyone-client
```
