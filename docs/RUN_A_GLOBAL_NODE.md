# Run a global node

A global node runs the public Orama L1 (`oramad`) and, optionally, the services
beside it: the storage provider, the history archiver, or the repair delegate.
It needs no domain, no WireGuard, and no cluster. This guide covers what the
`orama` CLI does today on such a machine. The chain itself is described in
[CHAIN.md](CHAIN.md).

Every command on the node runs as root. None of them needs raw `systemctl`.

## Hardware

The code does not check hardware. These are the sizes the plan gives
(`plans/open-network/track-b-global-node.md`, B7):

| Service | Size |
|---|---|
| chain (validator) | 4 vCPU, 16 GB RAM, NVMe. The chain uses pebbledb and keeps its state in `/var/lib/orama-global/chain`. |
| chain (non-validator) | Same disk class; the chain's own state is the same size. |
| provider | Disk at least the capacity you declare with `orama global capacity`. The piece store is `/var/lib/orama-global/provider/store`. |
| public Kubo | Disk for its `StorageMax`: the capacity you declare plus 10%, in the repo `/var/lib/orama-global/ipfs`. Every pinned public piece is stored twice on the host, once in the provider's store and once in Kubo. |
| archiver | Disk for `bundles/` (one file per archived range). |
| repair | Small; it holds repair seeds and rebuilds one replica at a time. |

## Install

Stage the release's `oramad`, `orama-global` and `orama` (this CLI; the chain
unit runs its sign-floor check), Kubo's `ipfs` (v0.38.2, when you install the
`ipfs` service) and the official cosmovisor release tarball
`cosmovisor-v1.7.3-linux-<amd64|arm64>.tar.gz` (from the cosmos-sdk release
`cosmovisor/v1.7.3`) in a directory that root owns
and nobody else may write (for example `/root/orama-global-release`). The
installer copies from there and refuses a symlink or a directory another account
could change.

**You are trusting these binaries.** The installer does not verify `oramad`, `orama`,
`orama-global` or `ipfs` (it does verify the cosmovisor tarball against a pinned
SHA-256, and runs `ipfs --version` as an unprivileged account to require Kubo
v0.38.2). It does not verify them
against the release root or any signature: the release archive does not carry
`oramad` or `orama-global` yet, so there is nothing to check them against. The
chain unit runs the staged `orama` as root before every start (the sign-floor
check), `oramad` holds the validator key, and `orama-global` holds hot keys and
repair seeds. Put in the staged directory only binaries you built or verified
yourself.

```bash
sudo orama global install \
  --services chain,ipfs,provider \
  --public-storage-gb 500 \
  --staged-dir /root/orama-global-release \
  --persistent-peers <node-id>@<host>:31000,<node-id>@<host>:31000 \
  --init-chain --chain-id <chain-id> --moniker <name> --genesis /root/genesis.json \
  --enable-firewall --ssh-port 22
```

- `--services` is `chain` plus any of `ipfs`, `provider`, `archiver`, `indexer`,
  `repair`. The chain is required: the other services reach it only through its
  RPC on `127.0.0.1:31001`. `provider` needs `ipfs` beside it, since it pins
  public deals through this host's public Kubo. `provider` and `repair` are never
  on the same host: a repair delegate holds repair seeds, and a provider must
  not. `indexer` is optional; add it on a node that serves the chain read API
  (loopback 31015, proxied by a gateway).
- Each service gets its own system account (`orama-chain`, `orama-ipfs-pub`,
  `orama-provider`, `orama-archiver`, `orama-indexer`, `orama-repair`; the
  `ipfs` service also creates the `orama-ipfs-pub-rpc` group, which its unit and
  the provider's unit name). Binaries go to `/usr/lib/orama-global/bin`,
  root-owned, 0755.
- Units are written to `/etc/systemd/system/orama-global-*.service` and enabled,
  not started. None of them is part of `orama-node.service`. The public Kubo,
  provider, archiver, indexer and repair units are ordered after the chain unit
  and want it (`After=`/`Wants=`); the provider is also ordered after the public
  Kubo. The chain unit runs the sign-floor check before every start (see the
  double-sign guard below), with the `orama` CLI installed beside `oramad`.
- The chain unit runs **cosmovisor v1.7.3** (`DAEMON_ALLOW_DOWNLOAD_BINARIES=false`),
  which runs `oramad` from `/var/lib/orama-global/chain/cosmovisor/current/bin`.
  Install checks the staged tarball's SHA-256 against the pin built into the CLI
  (an unofficial or altered tarball is refused), installs only its `cosmovisor`
  file, and places the staged `oramad` as the genesis binary in that layout. The
  chain home must therefore already have a genesis: use `--init-chain` on the
  first install. A second install with the same `oramad` changes nothing; one
  with different `oramad` bytes is refused. To change the chain binary use
  `orama global stage-oramad --upgrade <plan>` and let cosmovisor switch at the
  plan's height; a plan with no staged binary halts the chain until one is staged.
  A patch that does not change consensus has no installed update path yet (the
  updater is not built): stage it as an upgrade plan.
- The public Kubo (`ipfs`) is a second daemon, never the private cluster's: its
  own repo in `/var/lib/orama-global/ipfs` created with `ipfs init
  --profile=server` as `orama-ipfs-pub`, no `swarm.key`, the swarm on 31010
  tcp+udp (open in ufw), private address ranges filtered and not announced,
  and only pinned content announced. Its RPC is `127.0.0.1:31011` behind a
  bearer token in `api-token` (mode 0640, group `orama-ipfs-pub-rpc`, which only
  the provider joins; a second install keeps the token; the token allows only
  `add`, `cat`, `pin/add`, `pin/rm` and `repo/gc`). The GC unit passes the token
  to `ipfs --api-auth`, so it is on that process's command line for the length of a run. `--public-storage-gb` is the capacity you will
  declare with `orama global capacity`; Kubo's `StorageMax` is that plus 10%,
  and `orama-global-ipfs-gc.timer` (20 minutes after start, then every 6 hours)
  garbage-collects through the daemon's RPC, so a provider is not a free public
  cache. The timer starts and stops with the `ipfs` service.
- With `provider` installed, PUBLIC_PIN and ARCHIVE deals are pinned in the
  public Kubo before the slot is accepted, so the bytes are fetchable by CID
  (details in [CHAIN.md](CHAIN.md#storage-provider-orama-global-provider)).
  PRIVATE deals never touch it.
- `--init-chain` runs `oramad init` as `orama-chain` and puts `--genesis` in
  place (its `chain_id` must equal `--chain-id`). Without the flag the chain
  home is never created or changed, and the flag is refused when the home
  already has a genesis.
- ufw: the chain's 31000 tcp+udp, the public Kubo's 31010 tcp+udp and the
  provider's 31013 tcp are allowed with the comment `orama-global`. A cluster reconcile never removes those rules. An
  inactive ufw is refused unless `--enable-firewall` is given; then incoming is
  denied by default, the SSH port is allowed, and ufw is enabled. `--ssh-port`
  must be a port sshd listens on according to `sshd -T` (its `listenaddress`
  ports when there are any, its `port` lines otherwise); otherwise the install
  is refused before anything changes. Loopback
  listeners (RPC 31001, gRPC 31002, REST 31003, Prometheus 31004) are not opened.
  The installer does not change IPv6.
- Running the command again with the same flags changes nothing but the
  binaries' bytes. A service left out of `--services` is not removed.

The repair delegate needs `<home>/operator` (the address deals name as
`repair_delegate`) and `<home>/deals/<id>.json` files; the provider needs
`node-id` after registration. See [CHAIN.md](CHAIN.md#global-services-orama-global).

## Start, stop, status

```bash
sudo orama global start            # chain, wait for its RPC, then the rest
sudo orama global start provider   # needs the chain running
sudo orama global stop chain       # stops the provider/archiver/repair first
sudo orama global restart chain    # restarts everything installed, in order
sudo orama global status
```

The chain always starts first and stops last. Before it starts, the double-sign
guard below runs. The other services start only after
`http://127.0.0.1:31001/status` answers (up to five minutes); if it does not,
they are left stopped and the command fails.

## Register and bond

The operator's wallet never touches the node. On the operator's machine:

1. `orama global bind` signs each service key's binding.
2. `orama global register` builds `MsgRegisterNode` (roles, hot key, bindings,
   endpoints, and `--asn`, the autonomous system number this node declares; the
   chain refuses reserved, documentation and private-use numbers, and cannot
   verify the number. A protocol deal slot goes only to a node with a declared
   ASN, and slots go to distinct ASNs, so declare the real one). With `--node` the RootWallet agent signs it and it is broadcast.
3. `orama global bond --role <role> --amount <norama>` bonds each role.
4. `orama global capacity` declares storage bytes for a provider.

A role is active only while its bond is at least `min_bond`. Until the genesis
parameters are signed off, every role's minimum is 1 ORAMA, `bond_per_gib` is
1 ORAMA (1 ORAMA of storage bond backs 1 GiB), and unbonding takes 21 days.

## Validators

### Bootstrap committee

The first validators are a **bootstrap committee** named in genesis
(`oramad genesis add-bootstrap-validator`, `x/power`). A member needs no stake:
each has an equal share of voting power while the hand-over factor lambda is
below 1. There is no on-chain application or scoring for a seat; the committee
is whoever genesis names. 50% of a member's own reward share is force-bonded
into its self-delegation until that reaches 2 × 1,000 ORAMA. At lambda = 1 a
committee-only seat has no power.

Anyone else becomes a validator with a normal `MsgCreateValidator`, bonded from
earnings if needed. The `orama` CLI does not build that message.

### Rewards

`x/emission` mints once per epoch. The validator share is paid by `x/power` on
capped power (5%, or 3% once many validators are active), split between the
validator (commission and its own delegation) and its delegators, and credited
to **earnings accounts**, not bank balances. A delegation below 1 ORAMA is
refused. Details: [CHAIN.md](CHAIN.md), section `x/power`.

### Slashing and tombstoning

| Fault | Penalty |
|---|---|
| Downtime: fewer than 50% of the last 10,000 blocks signed | 0.01% slash, jailed for 10 minutes |
| Double sign (two different votes for one height, round and step) | 5% slash, jailed, **tombstoned** |

A tombstoned validator can never unjail, and `x/power` gives it no power even
while its tokens stay bonded. The slash applies to the bonded tokens and to
unbonding started at or after the infraction.

For storage, `x/storage` slashes 10% of a replica's epoch price once two
consecutive proof misses accrue.

### Unjail and edit

```bash
orama global validator unjail --chain-id <id> --operator orama1... --fee <n> --gas <n> [--node http://127.0.0.1:31003]
orama global validator edit   --chain-id <id> --operator orama1... --moniker <name> --commission-rate 0.05 --fee <n> --gas <n>
```

Both are signed by the operator account (its bytes are the `oramavaloper`
address). Without `--node` they print the sign document; with it the RootWallet
agent signs and the transaction is broadcast. Unjail is refused while the jail
runs, without a self-delegation at least the validator's minimum, and for a
tombstoned validator. A bootstrap member with no self-delegation yet cannot
unjail until force-bonded rewards give it one. `edit` sends every field you do
not give as `[do-not-modify]`; one commission change is allowed per 24 hours.

### The hot key

The provider and the archiver create their own signing key (`hot-key`, 0600)
on first start and log its address. Registering a node needs a binding signed by this key
(`orama global bind --service hot-key`, with the hot key's secret) so the chain knows the address is
yours; a hot key that is another node's, or any operator, is refused. It pays its transaction fees
from its own bank balance or earnings, or from a fee-only balance the operator gives it with
`oramad tx nodes fund-hot-key [node-id] [amount-norama]` (`MsgFundHotKey`); that balance can pay
base fees and nothing else. A bank send from another user account to it is refused. `orama node
report` and the inspector read the provider's hot-key balance from its `monitor.json`.

### Back up the consensus key

```bash
sudo orama global validator export-key --recipient <operator X25519 public key, hex> --to /root/validator-key.orbk
```

This seals `priv_validator_key.json` to your public key with the same ORBK seal
as a namespace backup. The node never holds the private half. Keep the file off
the node.

### Move a validator: the double-sign guard

Never run two copies of one key. The guard is the **sign floor**: a sign state
root records per validator key in `/var/lib/orama-global/validator-sign-floor.json`
(`{"floors":{"<public key>":<priv_validator_state.json>}}`). The public key is
the one CometBFT signs as, derived from `priv_key`; a key file whose `pub_key`
does not match its `priv_key` is refused. The chain unit
runs `orama global validator check-sign-floor` as root before every start
(`ExecStartPre`), so it applies at boot, on `Restart=always` and on any manual
start, not only to `orama global start`. The check refuses while a migration
export is in progress. With any floor recorded, the chain starts only when a
`priv_validator_key.json` is in the chain home, and, when a floor is recorded
for that key, only when `priv_validator_state.json` is not behind it. Each key
keeps its own floor: importing another key adds its entry and never removes
one, and an import never lowers a key's floor; an export also refuses a chain
home whose state is behind that key's floor. Exports and imports hold an
exclusive lock (`validator-sign-floor.lock`) while they update the file.

A floor file in any other format (anything but one JSON object of that shape)
stops the chain. A bundle cannot be imported again (its one-time key is gone),
so repair it by hand: move the file aside, then write it again, root-owned,
mode 0600, with one entry per migrated key. The key is the `pub_key` value of
its `priv_validator_key.json`, or of its `validator-key-*.json` copy in
`/var/lib/orama-global`. The state is its last known
`priv_validator_state.json`: the newest of the chain home's
`data/priv_validator_state.json`, the `validator-state-*.json` copies in
`/var/lib/orama-global`, and the state file of any other host that ran the key.
Check that state before you write it: a floor below what the key signed does
not protect it.

The floor, the migration key and the key copies are
trusted only while `/var/lib/orama-global` is root's and not writable by its
group or others; otherwise every one of these commands, and the check, refuses.
`orama global install` and `orama global start` print a warning when this
host's key was migrated away.

1. New host: `sudo orama global validator migrate prepare` prints a one-time
   key. The private half stays in `/var/lib/orama-global/migrate-recipient.key`,
   root's, 0600, until an import uses it or
   `sudo orama global validator migrate cancel` removes it.
2. Old host: `sudo orama global validator migrate export --recipient <key> --to /root/move.orbk`
   (`--to` must not exist). It stops the chain and the services that need it and
   disables the chain unit. It writes an export-in-progress marker
   (`/var/lib/orama-global/validator-export-in-progress`) that makes the check
   refuse any start, reads the key and `priv_validator_state.json`, confirms the
   chain is still stopped, and seals both in memory. Then it records that state as the old host's floor, copies the state
   to `/var/lib/orama-global/validator-state-migrated-<time>-<random>.json`,
   moves the key to `/var/lib/orama-global/validator-key-migrated-<time>-<random>.json`,
   removes the marker, and finally writes the bundle. From then on the old host's chain does not
   start: the floor is recorded and the key is gone, so oramad cannot generate a
   fresh key and a zero state in its place.
3. Copy the bundle to the new host.
4. New host: `sudo orama global validator migrate import --from /root/move.orbk`.
   It refuses while the chain runs. It records the old host's state as the floor
   first, then writes that state (unless the new host's is already ahead), and
   installs the key last; a different key already there is moved aside, never
   overwritten. If a step fails, the migrated key is not installed, and the
   floor refuses its state should it be put in place by hand, so the chain
   cannot start signing as the validator below the old host.
5. New host: `sudo orama global start`.

What this does not cover: a copy of the key made any other way (a disk image,
a manual copy) or a key put back by hand together with an older state file.
The floor is only on the hosts that ran these commands.

To abandon a migration, on the old host put both files back, owned by
`orama-chain`, mode 0600: the key copy as `config/priv_validator_key.json` and
the state copy as `data/priv_validator_state.json` in
`/var/lib/orama-global/chain`. Then `sudo orama global start` enables and starts
the chain; the floor passes because the state is the one it recorded. Do this
only to abandon a migration, and only if the new host never started the chain
with the key.

An export that was killed part-way can leave the marker behind; the chain then
refuses to start until root removes it, after checking whether the key is still
in the chain home.

### Restore from a backup

Only when the old host is gone for good:

1. New host: `sudo orama global validator migrate prepare`.
2. Your machine: `orama global validator reseal --from validator-key.orbk --identity-file <private key file, 0600> --recipient <key> --to restore.orbk`.
3. New host: `sudo orama global validator migrate import --from restore.orbk --old-host-destroyed --floor-height <the network's latest committed height>`.

A backup carries no sign state, so the import refuses it without both flags.
Give the network's **latest committed height** H, read from a node you trust
right before the import. The floor and the state become H+1, round 0, before
any step: the restored key signs nothing at or below H, in any round. It can
sign at H+1, so a vote the lost host cast at H+1 is excluded only if that host
stopped before H+1 began. An import never lowers a floor recorded for the same key on
the host; a bundle or height below it is refused. If the old host can still start with its copy of the
key, it and the new host will double sign; the flag is your statement that it
cannot.

## Sharing a machine with a cluster node

A machine can be a cluster node and a global node at once (`role: both`). The
global services run in their own Linux network namespace, `orama-global`; the
cluster node stays in the root namespace. Inside the namespace `127.0.0.1` is the
global services' own loopback, so the cluster's loopback (Kubo RPC 10107, the
gateway's loopback trust, the Caddy admin socket) is not reachable from a global
unit, and the two sides have separate port spaces: both can listen on the same
port number. Users were already separate (`orama-chain`, `orama-provider`, ...
against `orama`), and so are the state directories (`/var/lib/orama-global`
against `/opt/orama/.orama`), which each unit already hides from the other.

```bash
sudo orama global install --colocated --services chain,ipfs,provider \
  --public-storage-gb 500 --staged-dir /root/orama-global-release --enable-firewall --ssh-port 22
```

Run it on a machine where `orama node setup` has already installed the cluster
node. It refuses, before changing anything, when:

- the machine has no cluster node (`/opt/orama/.orama/preferences.yaml` is
  missing; `orama node setup` would overwrite the co-located role if it ran
  later) or its role is `global`;
- it is not Linux, its kernel has no network namespaces or veth, `ip` (iproute2),
  `nft` (nftables) or `sysctl` is missing, or systemd is older than 242
  (`NetworkNamespacePath=`). Each message says what to install. The check
  creates and deletes a throwaway namespace and veth pair to prove the kernel
  allows them, so a container without `CAP_NET_ADMIN` is refused here and not
  halfway through;
- `198.18.0.0/24` is already routed on the machine.

Without `--colocated`, an install on a machine whose role is `both` is refused:
it would put the units back in the root namespace.

**What it writes**, on top of a global-only install:

| File | Purpose |
|------|---------|
| `/etc/systemd/system/orama-global-netns.service` | A oneshot that creates the namespace, the veth pair `ogl-host` (root side, `198.18.0.1/30`) and `ogl-ns` (inside, `198.18.0.2/30`), the default route, and loads both rulesets; it takes them down on stop. No sandboxing on this unit, because `ip netns add` binds into the host's mount namespace. |
| `/etc/orama-global/netns-host.nft` | Root-namespace ruleset (table `ip orama_global`), see below |
| `/etc/orama-global/netns.nft` | Ruleset loaded inside the namespace (table `ip orama_global_ns`) |
| `/etc/orama-global/resolv.conf` | `9.9.9.9` and `1.1.1.1`. The host's stub resolver is on the host's loopback, which the namespace cannot reach. |
| `/etc/sysctl.d/60-orama-global-netns.conf` | `net.ipv4.ip_forward = 1`, also set when the namespace unit starts |

Every `orama-global-*` unit (the public Kubo, its GC oneshot, the indexer and the cosmovisor chain unit included; the GC timer only triggers the oneshot and stays in the root namespace) gains `BindsTo=` and `After=orama-global-netns.service`,
`NetworkNamespacePath=/run/netns/orama-global`, and a read-only bind of the
namespace's resolv.conf over `/etc/resolv.conf`. It also records `role: both` and
`global_netns: orama-global` in `preferences.yaml`, after everything else. The
node refuses `role: both` at boot unless that field and every file above exist;
it then runs the same graph as a cluster node, because the global services are
their own units, not components of `orama-node`. Units are enabled and not
started, like a global-only install: `orama global start` starts them, and its
wait for the chain's RPC dials `127.0.0.1:31001` from inside the namespace. The
provider reaches the public Kubo's RPC on `127.0.0.1:31011` in the same
namespace; the cluster's own Kubo (10107) is a different daemon on the other side.

**The rulesets.**

- Root namespace: the public ports of the chosen services (31000 tcp+udp for the
  chain, 31010 tcp+udp for the public Kubo swarm, 31013 tcp for the provider) are DNAT'd to `198.18.0.2`; the namespace's
  outbound traffic is masqueraded; nothing from the namespace may be forwarded to
  `10.0.0.0/8` (the WireGuard mesh is in it), `172.16.0.0/12`, `192.168.0.0/16`,
  `169.254.0.0/16` (cloud metadata) or `100.64.0.0/10`; nothing arriving on the
  veth may reach a service on the host itself, so the namespace cannot get to the
  cluster through the host's public or WireGuard address; and only DNAT'd
  connections and their replies are forwarded into the namespace.
- Inside the namespace: input is default-drop except loopback, replies and the
  published ports; forwarding is off; output to the private ranges above is
  dropped. The units' own `IPAddressDeny=` on the same ranges is a third layer.
- ufw: input rules cannot see DNAT'd traffic, so the install adds
  `ufw route allow` rules tagged `orama-global` (one for the namespace's own
  outbound traffic on `ogl-host`, one per published port to `198.18.0.2`) instead
  of the `allow` rules a global-only install adds. A cluster reconcile does not
  remove them.

**What this does not isolate.** It is one kernel and one root. The cluster node
can reach the global services only through their published ports at `198.18.0.2`,
and a global service reaches the outside only through the masqueraded veth. The
namespace has no IPv6 (the units may open only IPv4 and Unix sockets). A
loopback-only listener of a global service (the chain's RPC 31001, gRPC 31002,
REST 31003) is reachable from the machine only from inside the namespace:
`sudo ip netns exec orama-global curl http://127.0.0.1:31001/status`. Commands
that take `--node http://127.0.0.1:31003` (`orama global register` and the other
signed transactions) must run there, or on the operator's machine against the
node's public address once one is published.

Removing the layout is not built: stop and disable the `orama-global-*` units and
`orama-global-netns.service`, delete the files above and the `ufw route` rules
tagged `orama-global`, and set `role` back to `cluster` in `preferences.yaml`.

**Tests.** The rendering of the unit and rulesets, the machine checks, the install
plan and the boot-time role check are unit tests in `make test`. The layout built
for real is `make -C core test-netns` (tag `netns_integration`, Linux and root
only, skipped elsewhere): it creates the namespace and checks that the same
address binds on both sides, that a cluster listener on the root loopback, on
`0.0.0.0` and on `198.18.0.1` is unreachable from inside, that only published
ports are reachable into the namespace, and that private ranges are unreachable
from it. It has not been run on a real machine from the environment this change
was written in.

## Not built yet

- `orama node setup --role global` from the operator's machine; install runs on
  the node.
- TUF verification of the staged binaries by the installer, and an installed
  update path for a chain patch that changes no consensus behaviour.
- An `orama global update-node` (`MsgUpdateNode`): an ASN is set at
  registration only.
- Removing a service, or its firewall rule, that a later install leaves out; removing the co-located layout.
- The relay and Tor units.
- A remote signer (TMKMS, Horcrux) or sentry topology.
