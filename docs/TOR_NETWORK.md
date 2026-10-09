# The Orama Tor network

The Orama Tor network is a separate anonymity network built from **unmodified
upstream Tor** (the `tor` package of the Tor Project's apt repository) and run by
Orama's own directory authorities. Orama nodes relay and, where an operator
opts in, exit its traffic; wallets submit transactions to validators' onion
services through it. It is **not** the public Tor network: a client of the Orama
network knows only the authorities in the network file and ignores Tor's
built-in ones.

The node's own Tor client (`orama-namespace-tor@index`, SOCKS 9050) is a
different thing and stays on the public Tor network, because the anonymity proxy
(`/v1/proxy/anon`, `anon_fetch`) needs public exits. Nothing here changes it.

The network's product name is not decided (plan decision P8; it cannot be
"Tor"). The working name in network files is the network's id, for example
`orama-stagenet`.

Code: `core/pkg/tornet` (network file, torrc rendering, key ceremony, consensus
reader, vote archive), `core/pkg/txgate` (the gate behind the onion service),
`core/pkg/install/global_install_tor.go` (the install), `core/cmd/orama/internal/cmd/globalcmd/tor.go`
and `txgate.go` (the CLI). Spike results and open questions:
`plans/open-network/decisions/E0.md`.

## Roles

| Role | Installed with | Unit | Public ports |
|---|---|---|---|
| Directory authority | `--services dirauth` | `orama-global-tor-dirauth.service` + `orama-global-tor-archive.timer` | ORPort 31020/tcp, DirPort 31021/tcp |
| Relay | `--services relay` | `orama-global-tor-relay.service` | ORPort 31020/tcp |
| Exit (opt-in) | `--services relay,exit` | the relay's unit, with an exit policy | ORPort 31020/tcp |
| Validator onion service | `--services onion` (chain installed) | `orama-global-tor-onion.service` + `orama-global-txgate.service` | none |
| Client | not a node role | a wallet's own tor, `tornet.ClientTorrc` | none (loopback SOCKS, `constants.TorNetSOCKSPort` 9052) |

A directory authority is a relay as well, so a host runs `dirauth` or `relay`,
never both. A dirauth or relay host needs no chain; it can be a machine that
runs nothing else (`orama global install --services dirauth`), or it can share a
cluster node (`--colocated`: the tor process then runs in the `orama-global`
network namespace like every global service, and its traffic leaves through the
host's public address). Adding a Tor role to a machine whose global services are
already installed keeps the namespace's published ports: the rulesets are built
from the installed units plus the ones being added.

Every Tor unit runs `/usr/bin/tor -f /var/lib/orama-global/tor-<role>.torrc` under the shared global
sandbox (private ranges denied, `/opt/orama`, `/etc/orama`, `/etc/wireguard`
hidden, `NoNewPrivileges`, empty capability set, no `PartOf=orama-node.service`),
plus `MemoryDenyWriteExecute`, netlink for tor's interface listing,
`SystemCallErrorNumber=EPERM` so a refused syscall fails the call instead of
killing tor, and `IPAddressDeny=198.18.0.0/15` (the co-located namespace's
address range, where the chain's RPC and REST API listen). In the co-located
namespace a relay or authority also loses loopback, which holds the chain's gRPC
and metrics listeners (its resolver there is public). The installer writes the
unit and the torrc; nobody edits either on a node. The torrc is root's (0644)
and sits beside the DataDirectory, not in it: the role's account owns its
DataDirectory and could otherwise rewrite its own exit policy there and have it
survive a restart.

**Each role has an account of its own**: `orama-tor-dirauth`, `orama-tor-relay`,
`orama-tor-onion` (and `orama-txgate`), not the Tor package's `debian-tor`: an
onion service that anyone on the network can reach must not share a uid with an
authority's signing key on the same machine. The distro's own tor units stay
masked.

State directories (each the unit's `StateDirectory` and tor's DataDirectory, the
role's account, 0700): `/var/lib/orama-global/tor-dirauth`, `tor-relay`,
`tor-onion`; their torrc files are `tor-dirauth.torrc`, `tor-relay.torrc` and
`tor-onion.torrc` beside them. The network file as installed is
`/var/lib/orama-global/tor-network.json`.

## The network file

`tor-network.json` is public. It is staged beside the release binaries and read
by `orama global install`; the same file ships to wallets.

```json
{
  "name": "orama-stagenet",
  "bootstrap": true,
  "allow_exit": true,
  "allow_shared_subnets": true,
  "hsdir_min_uptime_hours": 1,
  "voting_interval_minutes": 30,
  "vote_delay_seconds": 300,
  "dist_delay_seconds": 300,
  "authorities": [
    {"nickname": "OramaAuthMew", "address": "57.129.166.16", "or_port": 31020, "dir_port": 31021,
     "v3_ident": "<40 hex>", "fingerprint": "<40 hex>", "ed25519_id": "<43 base64>"}
  ]
}
```

- `authorities`: at least three, on public IPv4 addresses, ports 31020/31021 (the
  ports the global firewall opens). `fingerprint` is the SHA-1 of the authority's
  relay RSA identity key (what a `DirAuthority` line and a descriptor name),
  `v3_ident` the SHA-1 of its offline authority identity key (what its
  certificates are signed by), `ed25519_id` its relay ed25519 identity.
- `voting_interval_minutes` must divide 24 hours, delays are at least 20 s and
  `2 x (vote + dist)` must be under the interval: the same checks Tor makes,
  made at install so a bad file fails there and not on a restart. All
  authorities must run the same schedule, which is why it is in the file.
- `bootstrap`: a new network has no consensus yet, and its relays cannot prove
  themselves reachable through it. With `bootstrap` true every authority and
  relay sets `AssumeReachable 1` (the Tor manual's "used when bootstrapping a
  new Tor network"), which also makes authorities list every connected relay
  without testing it. **Set it to false once the first consensus is signed**,
  re-install and restart (Rolling it out, step 8).
- `allow_exit`: the network's owner runs exits. An install with the exit role
  is refused when this is false (see Exits).
- `allow_shared_subnets` writes `EnforceDistinctSubnets 0`: Tor avoids two
  relays of one /16 in a circuit, and a network with fewer /16 networks than
  hops cannot build one otherwise. It weakens path selection; only a small
  network sets it.
- `hsdir_min_uptime_hours` writes `MinUptimeHidServDirectoryV2` on authorities.
  Tor's default is 96 hours before a relay may be an HSDir, and a validator's
  onion service needs HSDirs to publish. Zero keeps Tor's default.
- Every torrc also sets `UseDefaultFallbackDirs 0`: a node of this network never
  asks the public network's directories for anything.

## Key ceremony

Run on an **air-gapped machine** that has `tor` and `tor-gencert` (the `tor`
package: `apt install tor`, or `brew install tor`). The operator holds a
passphrase file (`0600`, one line, at least 16 characters):

```bash
orama global tor ceremony --name orama-stagenet --out ./tor-ceremony \
  --authority OramaAuthMew=57.129.166.16 \
  --authority OramaAuthMewtwo=57.129.166.17 \
  --authority OramaAuthGengar=161.97.184.199 \
  --voting-interval-minutes 30 --allow-exit --allow-shared-subnets --hsdir-min-uptime-hours 1 \
  --passphrase-file ./passphrase
```

For each authority it runs `tor --list-fingerprint` (making the relay identity
keys in a throwaway DataDirectory) and `tor-gencert --create-identity-key -m 12`
(the authority identity key, the signing key and a 12-month certificate). It
writes below `--out`:

| Path | What | Where it goes |
|---|---|---|
| `offline/<nick>/authority_identity_key` | the authority identity key, encrypted with the passphrase. It signs certificates and nothing else | **offline media**, encrypted, in two places (or an HSM), then deleted from the ceremony machine. Never on a server, never in a wallet vault |
| `deploy/<nick>/keys/` | the signing key, its certificate, the relay identity keys | the authority host, via `--tor-authority-keys` |
| `tor-network.json` | the network file | every node and wallet |
| `TRANSCRIPT.txt` | fingerprints, v3 identities, certificate expiry | read aloud against the host's own output, signed, kept |

`--out` must be new or empty; a ceremony never writes over keys. The passphrase
goes to `tor-gencert` on stdin, never on a command line.

**The passphrase protects a legacy file.** `tor-gencert` encrypts the identity key
with OpenSSL's old PEM scheme (3DES, an MD5-based single-iteration key
derivation), so a stolen file is cheap to attack offline and the passphrase must
carry the strength: use a long random one per custodian, not a phrase, keep the
file on encrypted media, and keep the copies in two places. The passphrase file
must be a regular file you own (not a link) with mode 0600.

The ceremony cross-checks tor's own output: the fingerprint tor prints must equal
the SHA-1 the ceremony computes from the key file it wrote, and the certificate
must carry a parsable identity and expiry.

**The ceremony refuses an unencrypted identity key**: if `tor-gencert` did not take
the passphrase, the key it wrote lacks the PEM `ENCRYPTED` marker and the
ceremony stops.

**Install checks the bundle.** `orama global install --services dirauth` refuses a
bundle whose relay identity key does not hash to the fingerprint the network
file publishes for `--tor-address`, whose ed25519 master key is not the
published `ed25519_id`, or whose certificate names another v3 identity. It
never replaces an installed identity key with different bytes (`secret_id_key`,
`ed25519_master_id_secret_key`): that would give the host a new identity in the
network. The identity checks run before anything is written, so a refused bundle
leaves the installed keys as they were; an expired signing certificate is
refused. The ed25519 master *secret* is not checked against the public key, and
the certificate's signature is not verified: a damaged bundle that passes shows
up as tor refusing to start. A rotated signing key and certificate (`authority_signing_key`,
`authority_certificate`) do replace the old ones. The keys tor rotates by itself
(`ed25519_signing_secret_key`, `ed25519_signing_cert`, `secret_onion_key`,
`secret_onion_key_ntor`) are installed from the bundle once and never put back
over the live ones. The certificate's signature is not verified at install.

### Rotating a signing certificate (before month 12)

On the offline machine, with the identity key and passphrase:

```bash
tor-gencert --reuse -m 12 -a <ip>:31021 -i authority_identity_key \
  -s authority_signing_key -c authority_certificate --passphrase-fd 0 < passphrase
```

Put the new `authority_signing_key` and `authority_certificate` in a copy of the
authority's `deploy/<nick>/keys/`, re-run `orama global install --services dirauth ... --tor-authority-keys <copy>`
and `orama global restart dirauth`. Do it one authority at a time, with a month
of margin: an authority whose certificate expired stops voting. The certificate
expiry is in `TRANSCRIPT.txt`; nothing reads it back from a host yet.

### A compromised authority

Its identity key is offline, so a compromise of the host is a compromise of the
signing key and the relay identity only. Remove the authority from
`tor-network.json`, ship the new file in an emergency release (nodes and wallets
re-read it on install and update), and run a ceremony for its replacement. The
remaining authorities keep voting meanwhile: three authorities tolerate one
loss. See [SECURITY_PLAYBOOKS.md](SECURITY_PLAYBOOKS.md#directory-authority-compromise).

## Directory authorities

`orama global install --services dirauth --tor-address <ip> --tor-contact <who> --tor-authority-keys <bundle> --staged-dir <dir>`:

- `--tor-address` must be one of the network file's authorities; its nickname
  and ports come from the file. A host that already runs a relay is refused the
  authority role, and the reverse, whichever install came first.
- The torrc sets `AuthoritativeDirectory 1`, `V3AuthoritativeDirectory 1`, the
  voting schedule, `ExitPolicy reject *:*` and the Sybil control
  `AuthDirMaxServersPerAddr 1` (at most one relay is listed per IP address).
  Other listing rules are Tor's defaults: reachability is tested by the
  authorities, and Guard, Stable, Fast and HSDir flags are earned from uptime
  and bandwidth relative to the network.
- The archive timer runs `orama global tor archive` every minute (the shortest
  voting interval is five, and a period must not pass unseen).

**Archive.** Every voting period is copied to
`/var/lib/orama-global/tor-dirauth/archive/<valid-after>/`:

| File | What |
|---|---|
| `consensus` | the consensus the authority holds (replaced only by the same consensus with more signatures) |
| `votes` | the votes that made it (`v3-status-votes`, only the documents of this period) |
| `bandwidth` | the bandwidth file the authority voted with, when one is configured |
| `MANIFEST.json` | `valid_after`, the SHA-256 of each file and `root` = SHA-256 over the lines `<name> <sha256>\n` in name order (`tornet.ManifestRoot`) |

`root` is the `inputs_root` a relay report commits to. The archive is local to
the authority host: **publication** (to global storage or a static site) is not
built, and the archive has no retention policy yet (a period is a few hundred
kilobytes at stagenet size and grows with the relay count).

**Bandwidth measurement (sbws) is not built.** Without a bandwidth file the
authorities weight relays by the bandwidth they report, capped by their
`RelayBandwidthRate`. That is gameable and is the reason the consensus weight
is not yet a reward basis. `orama-global-sbws` and `orama-global-reporter` have
unit renderers but no installer.

Look at an authority with `orama global tor info` (as root on the host; `--json`
for scripts): the nickname, RSA fingerprint, ed25519 id, the consensus it holds
(flavour, validity, signature count, relay/exit/guard counts) and whether it
lists the authority itself. A role whose DataDirectory cannot be read is shown
with its error beside the others, and the command then exits 1. The files are
read without following a link and within a size bound, and an onion hostname is
shown only if it is a v3 address, because the directory belongs to the Tor
account and the reader is root.

## Relays

`orama global install --services relay --tor-address <public ip> --tor-contact <who> --tor-node-id <id> --staged-dir <dir>`

- The nickname is `Orama` + 14 hex of SHA-256(node id): stable, 19 characters,
  and not the id itself.
- `ContactInfo` is the operator, and where an abuse complaint should go. It is
  published in the descriptor.
- `--tor-bandwidth-mbit N` sets `RelayBandwidthRate`/`Burst` (what the relay
  carries for others; the operator's own use is not limited). `--tor-family`
  lists the RSA fingerprints of the operator's other relays (`MyFamily`); list
  each relay in the others' families.
- IPv4 only: `ORPort 31020 IPv4Only`, `IPv6Exit 0`.
- The exit reject list is read only for an exit: a malformed list does not stop
  a relay from installing.
- The relay's RSA fingerprint appears after its first start:
  `orama global tor info`. That fingerprint, with the node's bond, is what
  `MsgRegisterRelay` takes (plan C8); registering relays on chain is not part of
  this change.

A relay that was not installed as an exit has `ExitRelay 0` and
`ExitPolicy reject *:*` in its torrc, always.

## Exits

An exit is opt-in per node: `--services relay,exit`. Orama's stagenet runs exits
because the owner decided to; a network's file says so with `allow_exit` and an
install is refused otherwise, so a production network file that does not carry
it cannot be given an exit by a flag.

The policy (rendered by `tornet.ExitPolicyLines`, in this order): the operator's
own refusals; every reserved IPv4 range (`netguard.Ranges`: private, loopback,
link-local, carrier-grade `100.64.0.0/10`, the `198.18.0.0/15` range that holds
the co-located namespace's host address, multicast, reserved); mail (`25`,
`465`, `587`) and the file-sharing and Windows-sharing ports Tor refuses by
default; then accept the rest. `ExitRelay 1`, `IPv6Exit 0`. Exit traffic leaves
from the node's public address (`--colocated`: through the host's masquerade).

Three layers keep an exit away from the node's own services, and the first is
Tor's: the policy above, with `ExitPolicyRejectPrivate 1` and
`ExitPolicyRejectLocalInterfaces 1` written into the torrc rather than left to
Tor's defaults, the
unit's `IPAddressDeny` of private ranges and of `198.18.0.0/15` (the namespace
address of the chain's RPC and REST API, the indexer and Kubo's RPC), and, in the
namespace, the kernel firewall that drops RFC 1918, link-local and carrier-grade
destinations and, on the host, everything that arrives from the namespace except
replies. Loopback inside the namespace is denied to an exit's unit as well. The
policy is a single layer for one thing: a non-co-located exit host
(`--services relay,exit` without `--colocated`) keeps loopback, for its resolver
stub, and so protects its own loopback services only with Tor's policy. Do not
put an exit on a host that runs the chain without `--colocated`; stagenet's
exit runs in the namespace, where the chain's RPC and REST API are on the
namespace address, which is denied to it, and loopback is denied as well.

**Operator guidance**

- *Before you start:* tell the hosting provider. Many forbid exits; stagenet's
  providers have not been asked and that is an open question for the owner.
  Put a reverse DNS name on the address that says "this is a Tor exit"
  (`tor-exit.<your domain>`), and publish a notice page on that name.
- *Logs:* tor runs with `SafeLogging 1` and logs at `notice` to the journal. It
  records no destination. Do not add destination logging: you could be asked to
  hand it over.
- *Complaints:* answer with the template below. Add a destination that keeps
  being reported to `/var/lib/orama-global/tor-exit-reject` (one IPv4 address
  or CIDR per line, optionally `:port` or `:lo-hi`; `#` comments) and run
  `orama global install` again with the same flags, then `orama global restart relay`.
  The list is read at install and checked line by line, because it is written
  into a torrc.
- *Stopping:* `orama global install --services relay ...` (without `exit`) with
  the same flags rewrites the torrc without the exit policy; `orama global restart relay`
  applies it. The node keeps relaying.

> *Abuse complaint reply.* Thank you for your report. The address you wrote about
> is a Tor exit relay of the Orama Tor network. Traffic that leaves it comes from
> the users of the network, not from us, and we keep no record of who sent it
> or where it went, so we cannot identify a user. We have added the destination
> you named to the relay's refusal list [if it is a service they run]. More
> about what an exit is: <notice page>. Contact: <ContactInfo>.

> *Notice to the hosting provider.* This server runs a Tor exit relay of the Orama
> Tor network, opt-in and by the account holder. Its exit policy refuses mail
> and file-sharing ports and all private ranges. Complaints reach us at
> <ContactInfo>; we answer within <time>. Please forward a complaint to us rather
> than acting on the server first.

## The validator onion service

`orama global install --services onion` (with the chain installed) writes two
units:

- `orama-global-tor-onion.service`: tor as a **client** of the network
  (`ClientOnly 1`, `ORPort 0`, `SocksPort 0`) with one v3 hidden service,
  `HiddenServicePort 80 127.0.0.1:31022`, and Tor's introduction-point DoS
  defense (`HiddenServiceEnableIntroDoSDefense 1`), a stream cap per circuit
  (`HiddenServiceMaxStreams 20`, closing the circuit past it).
- `orama-global-txgate.service`: `orama global txgate --listen 127.0.0.1:31022
  --upstream http://127.0.0.1:31003`, running as its own account `orama-txgate`
  with no key and no access to the chain home. Co-located, the upstream is the
  chain REST API on the namespace address.

The gate serves **three calls** and nothing else of the chain's REST API:

| Call | Path |
|---|---|
| read the signer's account | `GET /cosmos/auth/v1beta1/accounts/{orama1...}` |
| broadcast | `POST /cosmos/tx/v1beta1/txs` (JSON, at most 512 KiB) |
| look the transaction up | `GET /cosmos/tx/v1beta1/txs/{64 hex}` |

Everything else answers `404 {"error":"not served over the onion service"}`
without reaching the chain: the node's queries, the validator list, the
transaction search, the node and consensus services. A wrong method gets 405, a
broadcast that is not JSON 415, an oversized one 413. Requests all arrive from
the local tor process, so the limits are on the whole gate (20 requests per
second with a burst of 40, 16 in flight; past them, 429 and the wallet tries
another validator) and **no request is logged**: not its path, address or body.
The caller's headers do not cross to the chain API, and the chain's headers do
not cross back.

The onion address is in `/var/lib/orama-global/tor-onion/onion/hostname` and in
`orama global tor info`; it is what a validator publishes as an endpoint and
what a wallet passes as `--onion` (docs/CHAIN.md, "Onion submission"). The
wallet's SOCKS proxy for it must be a client of **this** network: a client of the
public Tor network resolves no address of it. No unit on a node runs such a
client; a wallet runs one with `tornet.ClientTorrc`.

The onion service needs HSDir-flagged relays to publish its descriptor, which is
why a new network sets `hsdir_min_uptime_hours`.

## Rolling it out on stagenet

Stagenet is five machines that each run a cluster node and, co-located, the
global services (`chain/scripts/stagenet/deploy.sh`). Two deviations from the
plan are deliberate and need the owner's agreement: the authorities share
machines with cluster nodes (mainnet authorities are separate machines), and
only two providers exist (OVH, Contabo), not three. The roles:

| Node | Address | Provider | Role |
|---|---|---|---|
| mew | 57.129.166.16 | OVH | dirauth |
| mewtwo | 57.129.166.17 | OVH | dirauth |
| gengar | 161.97.184.199 | Contabo | dirauth |
| magicarp | 161.97.184.202 | Contabo | relay |
| froakie | 161.97.151.255 | Contabo | relay,exit |

All five also run `onion` (all five are validators). The stagenet nodes fall in
two /16 networks, hence `allow_shared_subnets`.

1. **Build** the `orama` CLI for linux/amd64 (`make -C core build-linux`,
   `core/bin-linux/orama`). It carries the new commands.
2. **Ceremony** on an offline machine with tor, as above (`--allow-exit
   --allow-shared-subnets --hsdir-min-uptime-hours 1`, three `--authority`
   pairs for mew, mewtwo, gengar). Check the transcript against step 5's output.
3. **Offline media:** move `tor-ceremony/offline/` to encrypted media in two
   places; delete it from the ceremony machine.
4. **Stage**, on each node, in the root-owned release directory (stagenet:
   `/root/orama-global-release`): the new `orama` (replacing the old),
   `tor-network.json`, and on each authority its own `deploy/<nick>` directory
   (the whole directory; it holds `keys/`), root-owned and not writable by
   others (`install -d -m 0700`).
5. **Install** on every node, the same way `deploy.sh` runs the global install:
   as root, the staged `orama`, `--colocated`, `--staged-dir`.
   - dirauth (mew, mewtwo, gengar), with its own nickname's bundle and the other
     global services' names unchanged because the namespace rulesets keep them:

     ```bash
     sudo /root/orama-global-release/orama global install --colocated \
       --services dirauth --staged-dir /root/orama-global-release \
       --tor-address <this node's address> --tor-contact "<operator> <contact>" \
       --tor-authority-keys /root/orama-global-release/<nickname>
     ```

   - relay (magicarp): `--services relay --tor-node-id <id> --tor-address ... --tor-contact ...`
   - exit (froakie): `--services relay,exit --tor-node-id <id> ...`
   - onion, on every node (the chain unit is not rewritten because `chain` is
     not named): `--services onion --colocated --staged-dir ...`

   Each run prints what it wrote. A refusal (a bundle that is not this
   authority's, a missing network file, `allow_exit` false) changes nothing.
6. **Start**, authorities first and all three within a few minutes of each
   other (they must be up to vote): `sudo /root/orama-global-release/orama global start dirauth`
   on mew, mewtwo and gengar; then `global start relay` on magicarp and froakie;
   then `global start onion` on every node.
7. **Wait** for the first consensus: up to one voting interval plus the delays
   (about 40 minutes at 30). On each node, `sudo /root/orama-global-release/orama global tor info`
   shows a fresh consensus with at least two signatures that lists the node.
   Read the authorities' fingerprints in the output against `TRANSCRIPT.txt`.
8. **Leave bootstrap.** Set `"bootstrap": false` in `tor-network.json`, stage it
   on every node, re-run the same install command on each, then restart one
   node at a time, authorities first and verifying `tor info` between each
   (`orama global restart dirauth|relay|onion`). Three authorities hold a
   majority with one down; two restarting at once lose the consensus.
9. **Verify** with the fleet e2e on the stagenet target: feature `tor-network`,
   stage 8 (`TestAuthorities_signAConsensusThatListsEveryRelay`,
   `TestAuthorities_archiveMatchesItsManifest`, `TestRelays_orPortIsReachableAndTheGateIsNot`,
   `TestRelays_onlyAnInstalledExitHasTheExitFlag`,
   `TestNetwork_circuitsAndOnionServicesWorkThroughOurAuthorities`,
   `TestExit_leavesFromTheNodeAndRefusesWhatItShould`, plus the ceremony,
   gate and refusal tests that run on any target).

If a step fails, do not fix the node by hand: fix the source, and install again
(the install is idempotent).

`deploy.sh reset` removes every `orama-global-*` unit, `/var/lib/orama-global`
and the staging directory, so it removes the Tor roles with the rest, including a
relay's identity (the relay gets a new fingerprint when it is installed again; an
authority keeps its identity because the bundle is in the ceremony output, which
is why that output stays with the operator). After a reset and `deploy.sh up`,
repeat steps 4 to 7. `deploy.sh up` installs `chain,ipfs,provider,archiver`
without Tor flags; it does not remove a Tor role, and the namespace keeps the
Tor ports because the layout is built from the installed units.

## For clients of the network (wallets, the VPN app)

- `core/pkg/tornet`: `ParseNetwork` (the network file), `Network.DirAuthorityLines`,
  `ClientTorrc(ClientConfig{Network, Home, SOCKSPort})` (loopback SOCKS with
  `IsolateSOCKSAuth`, no ORPort), `ParseConsensus` (consensus and microdescriptor
  consensus: validity, relays with RSA fingerprint, flags, bandwidth, measured),
  `Manifest`/`ManifestRoot` (the archive's digest), `NicknameFor`.
- `core/pkg/constants`: `TorNetworkFile` ("tor-network.json"), `TorNetSOCKSPort`
  (9052), `GlobalTorORPort` (31020), `GlobalTorDirPort` (31021),
  `GlobalTxGatePort` (31022), the unit names and state directories.
- Wallet transactions: `chainonion` (`--onion`, `--onion-socks`) speaks to the
  onion service's gate; the three calls above are all it needs.

## Not built, and not verified

Stated so nothing here reads as more than it is (the spike's list is in
`plans/open-network/decisions/E0.md`):

- **Never run against a real network.** The code was written against Tor's
  manual and dir-spec; no tor process was started while writing it. The first
  stagenet bring-up is the first run, and E0 lists what it must confirm.
- sbws and the bandwidth reporter (E5) have no installer.
- No independent authority: Orama runs all of them. While Orama runs a majority,
  Orama could sign a consensus that lists only its own relays and deanonymise
  users. The launch thresholds (a majority of independent authorities, 100
  relays from 40 operators, 15 exits in 5 countries) are plan item E7.
- The archive is not published and has no retention.
- Relays are not registered on chain (C8), and nothing pays them.
- The signed fallback list is not built: clients bootstrap from the
  authorities in the network file, which the release carries. The file is
  public and unsigned by itself; its integrity is the release's.
- Authority certificates are renewed by hand (above); nothing warns before
  expiry beyond `orama global tor info`.
- IPv6 is off everywhere.
