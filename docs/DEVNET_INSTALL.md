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

It needs an unlocked RootWallet, and the archive to install: build it once
with `orama build` and pass the path it prints as `--archive` (there is no
default — the newest archive in `/tmp` may be another checkout's build).

**The first archive must be signed by the wallet that creates the cluster.**
The genesis install writes the cluster's archive trust anchor,
`/etc/orama/archive-signers`, from `--operator-wallet` — setup passes your
RootWallet's active address — and only then verifies the archive against it.
`orama build` signs with that same RootWallet account by default, so build and
setup with the same wallet unlocked. Setup verifies the archive on your machine
against that account before uploading anything — the new machine has no
verified binary of its own to check it with — so an archive signed by anyone
else, or built with `--unsigned`, is refused before it leaves your machine. Joining nodes take the
anchor from the node that minted their invite, so every node trusts the same
signers; see [DEV_DEPLOY.md](DEV_DEPLOY.md#signed-archives) for rotating them.

`--password` bootstraps over password login. The password is read from your
RootWallet vault — store the VPS login once with `rw vault add <ip>` (username
as on the VPS) — and never appears on a command line, where `ps` and shell
history would keep it.

```bash
orama build     # prints the archive path, e.g. /tmp/orama-0.200.0-linux-amd64.tar.gz
ARCHIVE=/tmp/orama-0.200.0-linux-amd64.tar.gz

# ns1 — genesis nameserver, creates the cluster
orama node setup --ip <ns1-ip> --password --env devnet --archive $ARCHIVE \
  --base-domain <your-domain.com> --role nameserver --genesis

# ns2 / ns3 — join as nameservers; the invite is minted on ns1 over SSH
orama node setup --ip <ns-ip> --password --env devnet --archive $ARCHIVE \
  --base-domain <your-domain.com> --role nameserver --join-via root@<ns1-ip>

# Worker — domain is auto-generated, e.g. node-a3f8k2.<your-domain.com>
orama node setup --ip <node-ip> --password --env devnet --archive $ARCHIVE \
  --base-domain <your-domain.com>
```

Setup pins the VPS host key before it uses any credential — pass
`--host-key SHA256:...` to pin it non-interactively, or confirm the fingerprint
it shows against your provider's console — and every connection of the run
(enrollment, archive upload, install) uses only that key.

`--genesis` records the environment as `https://<base-domain>` in
`~/.orama/environments.json` and leaves the active environment alone; name it
with `--env` in later commands. Joins need the minting node's certificate
issued: the invite names that node by its public IP and its site name, and the
joiner pins the fingerprint of the certificate it serves — delegate the domain
to the cluster ([NAMESERVER_SETUP.md](NAMESERVER_SETUP.md)) before joining more
nodes.

**Test clusters that you redeploy from scratch:** pass
`--acme-ca letsencrypt-staging` (or any https ACME directory URL) to every node.
Let's Encrypt issues at most five certificates per week for the same set of
names, and every node of a cluster requests the same `*.<base-domain>`
wildcard, so a second full redeploy in a week would otherwise be refused.
Staging certificates are not browser-trusted. The CA is stored in `node.yaml`
(`tls.acme_ca`) and kept across upgrades; Caddy uses it for every certificate
on the node.

The CLI does not trust staging certificates either. Give the environment
Let's Encrypt's staging roots, trusted for that environment's domain only:

```bash
curl -sf https://letsencrypt.org/certs/staging/letsencrypt-stg-root-x1.pem  > le-staging.pem
curl -sf https://letsencrypt.org/certs/staging/letsencrypt-stg-root-x2.pem >> le-staging.pem
orama env add <env> https://<base-domain> --ca-file le-staging.pem
```

Every command then verifies `<base-domain>` and the names under it against
those roots as well as the system's, and every other host against the
system's alone. A missing CA file is an error naming the environment.

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
  --env devnet --archive $ARCHIVE --base-domain <your-domain.com> --role nameserver
```

---

## Appendix: installing by hand

Use this when `orama node setup` cannot be used — no RootWallet, or a VPS you
reach some other way. It does the same thing with more steps.

### 1. Mint an invite (not needed for the genesis node)

From your own machine:

```bash
orama invite                 # usable for 1h, the gateway's cap
```

Or from an existing node:

```bash
sudo orama node invite --expiry 24h
```

Either prints one string. It names **one node** of the cluster — its public
address, the domain to present to it, and the fingerprint of the TLS
certificate it serves — which the joining node connects to and pins instead of
resolving the cluster's domain (which reaches any nameserver, each with a
certificate of its own) or trusting whatever certificate it is first shown.
`orama invite` picks the lowest address the domain resolves to; name another
with `--node <public IP>`. There is nothing else to copy across, and nothing to
get the wrong way round.

Invites are **single-use**. Mint one per join.

### 2. Genesis node

```bash
# SSH: <user>@<ns1-ip>

sudo orama node install \
  --vps-ip <ns1-ip> \
  --domain <your-domain.com> \
  --base-domain <your-domain.com> \
  --operator-wallet <0xYourWallet> \
  --nameserver
```

`--operator-wallet` is required on the genesis node: it becomes the only
signer of `/etc/orama/archive-signers`, so the archive extracted in
`/opt/orama` must be signed by that wallet (`orama build` with it as the active
RootWallet account). The anchor is written before the archive is verified.

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

# Or manually from any VPS. rqlited listens only on the node's WireGuard IP and
# always requires basic auth; both come from the node's own config:
RQ="http://$(sudo sed -n 's/^ *http_adv_address: *"\(.*\)"/\1/p' /opt/orama/.orama/configs/node.yaml)"
# Credentials go to curl on stdin (-K -), never on its command line where ps shows them.
rqcurl() { printf 'user = "orama:%s"\n' "$(sudo cat /opt/orama/.orama/secrets/rqlite-password)" | curl -K - "$@"; }
rqcurl -s "$RQ/status" | jq -r '.store.raft.state, .store.raft.num_peers'
curl -s http://localhost:10104/health
systemctl status orama-namespace-tor@index
```
