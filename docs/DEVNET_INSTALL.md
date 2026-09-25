# Installing a Devnet Cluster

Every node installs a **Tor client** (client only, from `deb.torproject.org`;
SOCKS5 on `127.0.0.1:9050` for `/v1/proxy/anon`, `/v1/proxy/tunnel` and the
`anon_fetch` host function). There is no relay mode and no flag to choose one.

A single VPS (index + one tenant, not HA) is [EVAL.md](EVAL.md). This page is
the three-nameserver cluster path.

**Supported OS:** Ubuntu 22.04, 24.04 or 26.04, or Debian 12 or 13. The
installer refuses anything else in its first phase.

**Note:** Store credentials securely (not in version control).

## Installation Order

Install nodes **one at a time**, waiting for each to complete before starting
the next:

1. ns1 (genesis nameserver)
2. ns2 (nameserver)
3. ns3 (nameserver)
4. Additional workers as needed (no `--role nameserver`)

---

## The path: `orama node setup`

One command per node, from your own machine. It creates an SSH key in
RootWallet, installs it on the VPS, uploads the binary archive, mints an invite
where one is needed, and runs the install. You never SSH in yourself.

It needs an unlocked RootWallet.

```bash
# ns1 — genesis nameserver, creates the cluster
orama node setup --ip <ns1-ip> --password '<vps-pass>' --env devnet \
  --base-domain <your-domain.com> --role nameserver --genesis

# ns2 / ns3 — join as nameservers; the invite is minted on ns1 over SSH
orama node setup --ip <ns-ip> --password '<vps-pass>' --env devnet \
  --base-domain <your-domain.com> --role nameserver --join-via root@<ns1-ip>

# Worker — domain is auto-generated, e.g. node-a3f8k2.<your-domain.com>
orama node setup --ip <node-ip> --password '<vps-pass>' --env devnet \
  --base-domain <your-domain.com>
```

Setup pins the VPS host key before it uses any credential — pass
`--host-key SHA256:...` to pin it non-interactively, or confirm the fingerprint
it shows against your provider's console — and every connection of the run
(enrollment, archive upload, install) uses only that key.

`--genesis` records the environment as `https://<base-domain>` in
`~/.orama/environments.json` and leaves the active environment alone; name it
with `--env` in later commands. Joins need the genesis node reachable at that
name with its certificate issued, because the invite carries the gateway URL
and its certificate fingerprint — delegate the domain to the cluster
([NAMESERVER_SETUP.md](NAMESERVER_SETUP.md)) before joining more nodes.

`--join-via <user>@<ip>` mints the invite on a node already in the cluster, over
SSH with its RootWallet key, so joining needs no `orama auth login`. That node's
host key must already be in your `known_hosts` (from its own setup). Without
`--join-via`, setup asks the environment's gateway for an invite and needs a
login. `--archive <path>` picks the build to install (default: the newest in
`/tmp`); a node already running that exact build is not re-uploaded.

**Key-only VPS images** (a non-root user such as `ubuntu` or `debian`, no
password login — the default on OVH and most clouds): replace `--password` with
the private key that opens the VPS today. It is used once to install the
RootWallet key and never stored. The user needs passwordless sudo; setup checks
it before installing.

```bash
orama node setup --ip <ip> --user ubuntu --bootstrap-key ~/.ssh/id_ed25519 \
  --env devnet --base-domain <your-domain.com> --role nameserver
```

---

## Appendix: installing by hand

Use this when `orama node setup` cannot be used — no RootWallet, or a VPS you
reach some other way. It does the same thing with more steps.

### 1. Mint an invite (not needed for the genesis node)

From your own machine:

```bash
orama invite --expiry 24h
```

Or from an existing node:

```bash
sudo orama node invite --expiry 24h
```

Either prints one string. It carries the gateway to join **and** the
fingerprint of that gateway's TLS certificate, which the joining node pins
instead of trusting whatever certificate it is first shown. There is nothing
else to copy across, and nothing to get the wrong way round.

Invites are **single-use**. Mint one per join.

### 2. Genesis node

```bash
# SSH: <user>@<ns1-ip>

sudo orama node install \
  --vps-ip <ns1-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --nameserver
```

### 3. Joining nodes

```bash
# SSH: <user>@<ns-ip>

sudo orama node install \
  --token <INVITE> \
  --vps-ip <ns-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --nameserver
```

A worker is the same without `--nameserver` and without `--domain`; the domain
is auto-generated.

Or drive it from your own machine over SSH:

```bash
orama node install --remote --token <INVITE> \
  --vps-ip <node-ip> --base-domain <your-domain.com>
```

`--remote` is required to install a machine other than the one you are on. It
used to be inferred from whether you had used sudo, so the same command line
meant two different things on two different machines.

## Verification

`orama node install` verifies the node itself before printing `✅` — supervisor active and not crash-looping, rqlite in `Leader`/`Follower`, `wg0` up, gateway `/health` 200 — and exits non-zero naming the first component that did not come up. A successful install therefore already means the node works; the checks below verify the **cluster**.

After all nodes are installed, verify cluster health:

```bash
# Full cluster report (from local machine)
./bin/orama monitor report --env devnet

# Single node health
./bin/orama monitor report --env devnet --node <ip>

# Or manually from any VPS:
curl -s http://localhost:10100/status | jq -r '.store.raft.state, .store.raft.num_peers'
curl -s http://localhost:10104/health
systemctl status orama-namespace-tor@index
```
