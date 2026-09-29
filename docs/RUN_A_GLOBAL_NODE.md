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
| archiver | Disk for `bundles/` (one file per archived range). |
| repair | Small; it holds repair seeds and rebuilds one replica at a time. |

## Install

Stage the release's `oramad`, `orama-global` and `orama` (this CLI; the chain
unit runs its sign-floor check) in a directory that root owns
and nobody else may write (for example `/root/orama-global-release`). The
installer copies from there and refuses a symlink or a directory another account
could change.

**You are trusting these three binaries.** The installer does not verify them
against the release root or any signature: the release archive does not carry
`oramad` or `orama-global` yet, so there is nothing to check them against. The
chain unit runs the staged `orama` as root before every start (the sign-floor
check), `oramad` holds the validator key, and `orama-global` holds hot keys and
repair seeds. Put in the staged directory only binaries you built or verified
yourself.

```bash
sudo orama global install \
  --services chain,provider \
  --staged-dir /root/orama-global-release \
  --persistent-peers <node-id>@<host>:31000,<node-id>@<host>:31000 \
  --init-chain --chain-id <chain-id> --moniker <name> --genesis /root/genesis.json \
  --enable-firewall --ssh-port 22
```

- `--services` is `chain` plus any of `provider`, `archiver`, `repair`. The chain
  is required: the other services reach it only through its RPC on
  `127.0.0.1:31001`. `provider` and `repair` are never on the same host: a repair
  delegate holds repair seeds, and a provider must not.
- Each service gets its own system account (`orama-chain`, `orama-provider`,
  `orama-archiver`, `orama-repair`; the provider also gets the
  `orama-ipfs-pub-rpc` group its unit names). Binaries go to
  `/usr/lib/orama-global/bin`, root-owned, 0755.
- Units are written to `/etc/systemd/system/orama-global-*.service` and enabled,
  not started. None of them is part of `orama-node.service`. The provider,
  archiver and repair units are ordered after the chain unit and want it
  (`After=`/`Wants=`). The chain unit runs the sign-floor check before every
  start (see the double-sign guard below), with the `orama` CLI installed beside
  `oramad`.
- The chain unit runs `oramad start` directly. **cosmovisor is not installed**:
  no cosmovisor release is pinned. `orama global stage-oramad` places binaries
  in the cosmovisor layout, which this unit does not run. To change the chain
  binary, stage the new `oramad`, run the same install command again, then
  `orama global restart chain`. A chain upgrade plan halts `oramad` at its height
  until the new binary runs.
- `--init-chain` runs `oramad init` as `orama-chain` and puts `--genesis` in
  place (its `chain_id` must equal `--chain-id`). Without the flag the chain
  home is never created or changed, and the flag is refused when the home
  already has a genesis.
- ufw: the chain's 31000 tcp+udp and the provider's 31013 tcp are allowed with
  the comment `orama-global`. A cluster reconcile never removes those rules. An
  inactive ufw is refused unless `--enable-firewall` is given; then incoming is
  denied by default, the SSH port is allowed, and ufw is enabled. `--ssh-port`
  must be a port `sshd -T` reports; otherwise the install is refused before
  anything changes. Loopback
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
   endpoints). With `--node` the RootWallet agent signs it and it is broadcast.
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
on first start and log its address. It pays its transaction fees from its own
bank balance or earnings account. A bank send from another user account to it
is refused, and `MsgFundHotKey` does not exist yet, so a hot key can only pay
from what it earns itself. `orama node report` and the inspector read the
provider's hot-key balance from its `monitor.json`.

### Back up the consensus key

```bash
sudo orama global validator export-key --recipient <operator X25519 public key, hex> --to /root/validator-key.orbk
```

This seals `priv_validator_key.json` to your public key with the same ORBK seal
as a namespace backup. The node never holds the private half. Keep the file off
the node.

### Move a validator: the double-sign guard

Never run two copies of one key. The guard is the **sign floor**: a sign state
root records in `/var/lib/orama-global/validator-sign-floor.json`. The chain unit
runs `orama global validator check-sign-floor` as root before every start
(`ExecStartPre`), so it applies at boot, on `Restart=always` and on any manual
start, not only to `orama global start`. The check refuses while a migration
export is in progress. With a floor recorded, the chain starts only when
`priv_validator_key.json` is in the chain home and `priv_validator_state.json`
is not behind the floor. The floor, the migration key and the key copies are
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
   overwritten. If a step fails, the key is either not installed or the floor
   refuses the state, so the chain cannot start signing below the old host.
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
3. New host: `sudo orama global validator migrate import --from restore.orbk --old-host-destroyed --floor-height <the network's current height>`.

A backup carries no sign state, so the import refuses it without both flags.
The height you give (round 0, step 3) becomes the floor and the state: the
restored key signs nothing at or below it. Read the height from a node you trust
right before the import. If the old host can still start with its copy of the
key, it and the new host will double sign; the flag is your statement that it
cannot.

## Not built yet

- `orama node setup --role global` from the operator's machine; install runs on
  the node.
- cosmovisor under the installed unit, and TUF verification of the staged
  binaries by the installer.
- Removing a service, or its firewall rule, that a later install leaves out.
- The public Kubo, relay and Tor units.
- A remote signer (TMKMS, Horcrux) or sentry topology.
