# Devnet Installation Commands

This document contains example installation commands for a multi-node devnet cluster.

**Wallet:** `<YOUR_WALLET_ADDRESS>`
**Contact:** `@anon: <YOUR_WALLET_ADDRESS>`

## Node Configuration

| Node | Role | Nameserver | Anyone Relay |
|------|------|------------|--------------|
| ns1 | Genesis | Yes | No |
| ns2 | Nameserver | Yes | Yes (relay-1) |
| ns3 | Nameserver | Yes | Yes (relay-2) |
| node4 | Worker | No | Yes (relay-3) |
| node5 | Worker | No | Yes (relay-4) |
| node6 | Worker | No | No |

**Note:** Store credentials securely (not in version control).

## MyFamily Fingerprints

If running multiple Anyone relays, configure MyFamily with all your relay fingerprints:
```
<FINGERPRINT_1>,<FINGERPRINT_2>,<FINGERPRINT_3>,...
```

## Installation Order

Install nodes **one at a time**, waiting for each to complete before starting the next:

1. ns1 (genesis, no Anyone relay)
2. ns2 (nameserver + relay)
3. ns3 (nameserver + relay)
4. node4 (non-nameserver + relay)
5. node5 (non-nameserver + relay)
6. node6 (non-nameserver, no relay)

## ns1 - Genesis Node (No Anyone Relay)

```bash
# SSH: <user>@<ns1-ip>

sudo orama install \
  --vps-ip <ns1-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --nameserver
```

After ns1 is installed, generate invite tokens:
```bash
orama invite --expiry 24h
```

## ns2 - Nameserver + Relay

```bash
# SSH: <user>@<ns2-ip>

sudo orama install \
  --join http://<ns1-ip> --token <TOKEN> \
  --vps-ip <ns2-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --nameserver \
  --anyone-relay --anyone-migrate \
  --anyone-nickname <relay-name> \
  --anyone-wallet <wallet-address> \
  --anyone-contact "<contact-info>" \
  --anyone-family "<fingerprint1>,<fingerprint2>,..." \
  --anyone-bandwidth 30
```

## ns3 - Nameserver + Relay

```bash
# SSH: <user>@<ns3-ip>

sudo orama install \
  --join http://<ns1-ip> --token <TOKEN> \
  --vps-ip <ns3-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --nameserver \
  --anyone-relay --anyone-migrate \
  --anyone-nickname <relay-name> \
  --anyone-wallet <wallet-address> \
  --anyone-contact "<contact-info>" \
  --anyone-family "<fingerprint1>,<fingerprint2>,..." \
  --anyone-bandwidth 30
```

## node4 - Non-Nameserver + Relay

```bash
# SSH: <user>@<node4-ip>

sudo orama install \
  --join http://<ns1-ip> --token <TOKEN> \
  --vps-ip <node4-ip> \
  --domain node4.<your-domain.com> \
  --base-domain <your-domain.com> \
  --skip-checks \
  --anyone-relay --anyone-migrate \
  --anyone-nickname <relay-name> \
  --anyone-wallet <wallet-address> \
  --anyone-contact "<contact-info>" \
  --anyone-family "<fingerprint1>,<fingerprint2>,..." \
  --anyone-bandwidth 30
```

## node5 - Non-Nameserver + Relay

```bash
# SSH: <user>@<node5-ip>

sudo orama install \
  --join http://<ns1-ip> --token <TOKEN> \
  --vps-ip <node5-ip> \
  --domain node5.<your-domain.com> \
  --base-domain <your-domain.com> \
  --skip-checks \
  --anyone-relay --anyone-migrate \
  --anyone-nickname <relay-name> \
  --anyone-wallet <wallet-address> \
  --anyone-contact "<contact-info>" \
  --anyone-family "<fingerprint1>,<fingerprint2>,..." \
  --anyone-bandwidth 30
```

## node6 - Non-Nameserver (No Anyone Relay)

```bash
# SSH: <user>@<node6-ip>

sudo orama install \
  --join http://<ns1-ip> --token <TOKEN> \
  --vps-ip <node6-ip> \
  --domain node6.<your-domain.com> \
  --base-domain <your-domain.com> \
  --skip-checks
```

## Verification

After all nodes are installed, verify cluster health:

```bash
# Check RQLite cluster (from any node)
curl -s http://localhost:5001/status | jq -r '.store.raft.state, .store.raft.num_peers'
# Should show: Leader (on one node) and N-1 peers

# Check gateway health
curl -s http://localhost:6001/health

# Check Anyone relay (on nodes with relays)
systemctl status orama-anyone-relay
```
