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

Code: `core/pkg/tornet` (the network file and its one parser, torrc rendering,
key ceremony, consensus reader, vote archive, relay monitor file),
`core/pkg/onionnet` (runs a client tor on a network file), `core/pkg/txgate`
(the gate behind the onion service), `core/pkg/install/global_install_tor.go`
(the install), `core/cmd/orama/internal/cmd/globalcmd/tor*.go` and `txgate.go`
(the CLI), `core/cmd/orama/internal/cmd/vpncmd` (`orama vpn`) and
`chain/reporter` (the bandwidth reporter). Spike results and open questions:
`plans/open-network/decisions/E0.md`.

## Roles

| Role | Installed with | Unit | Public ports |
|---|---|---|---|
| Directory authority | `--services dirauth` | `orama-global-tor-dirauth.service` + `orama-global-tor-archive.timer` + `orama-global-tor-monitor.timer` | ORPort 31020/tcp, DirPort 31021/tcp |
| Authority's bandwidth reporter | `--services chain,dirauth,reporter` | `orama-global-reporter.service` | none |
| Relay | `--services relay` | `orama-global-tor-relay.service` + `orama-global-tor-monitor.timer` | ORPort 31020/tcp |
| Exit (opt-in) | `--services relay,exit` | the relay's unit, with an exit policy | ORPort 31020/tcp |
| Validator onion service | `--services onion` (chain installed) | `orama-global-tor-onion.service` + `orama-global-txgate.service` | none |
| Client | not a node role | `orama vpn`, `--onion-network`, or a wallet's own tor (`tornet.ClientTorrc`) | none (loopback SOCKS; `constants.TorNetSOCKSPort` 9052 by convention) |

A directory authority is a relay as well, so a host runs `dirauth` or `relay`,
never both. A dirauth or relay host needs no chain (only the authority's reporter does); it can be a machine that
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

There is one network file, `tor-network.json`, and one parser for it,
`tornet.ParseNetwork` (`tornet.Load` for a path). Everything that joins the
network reads it through that parser:

| Reader | How it names the file |
|---|---|
| `orama global install` (every Tor role) | `tor-network.json` in `--staged-dir`; installed as `/var/lib/orama-global/tor-network.json` |
| `orama vpn up`, `orama vpn check` | `--network`, or `ORAMA_ONION_NETWORK` |
| every chain transaction command | `--onion-network`, or `ORAMA_ONION_NETWORK` |
| `orama global tor onions add` | `--network-file` |
| the relay reporter | does not read the file itself (it is in the `chain` module, which does not import `core`); its `authority-id` is the `v3_ident` of its authority in this file and its `vote-interval` is this file's `voting_interval_minutes` |

The file is public. The ceremony writes it, it is staged beside the release
binaries, and the same file ships to wallets.

```json
{
  "name": "orama-stagenet",
  "private": true,
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
  ],
  "validator_onions": ["<56 base32>.onion", "<56 base32>.onion:80"]
}
```

The parser is strict, and every role and client gets the same strictness:
unknown fields are an error (a misspelt key must not leave a default in place),
data after the object is an error, and the file is at most 1 MiB.

- `private` must be true. The public Orama Tor network is not launched, and every
  role and client refuses a file that says otherwise ("the public Orama Tor
  network is not launched"). The ceremony always writes `true`. A file made by an
  earlier build of the ceremony has no `private` key: add `"private": true` to it
  (nothing defaults it), then re-install.
- `authorities`: at least three, on public IPv4 literals (no names, no IPv6, no
  zone, nothing a resolver or a torrc line could be made of), ports 31020/31021
  (the ports the global firewall opens). `fingerprint` is the SHA-1 of the
  authority's relay RSA identity key (what a `DirAuthority` line and a
  descriptor name), `v3_ident` the SHA-1 of its offline authority identity key
  (what its certificates are signed by), `ed25519_id` its relay ed25519
  identity. Nicknames, addresses and identities must be unique.
- `validator_onions`: the validator onion services that take transaction
  submissions, as `addr.onion` or `addr.onion:port` (v3 addresses only, port
  default 80, lower case, no duplicates). `--onion-network` picks one at random
  for each transaction and `orama vpn check` tries each. Relays and
  authorities ignore it. A validator's address exists only once its onion role
  has started, which is after the ceremony wrote the file, so it is added with
  `orama global tor onions add --network-file tor-network.json <addr.onion>...`
  (see Rolling it out, step 8). The command validates every address, does not
  list one twice, keeps the file's mode and replaces it atomically; a file that
  does not already load is left untouched.
- `voting_interval_minutes` must be at least 5, divide 24 hours and be at most
  480, the largest divisor of 24 hours whose shared-random run (24 intervals) fits
  Tor's longest onion service time period (14400 minutes), delays are at least
  20 s and `2 x (vote + dist)` must be under the interval: the same checks Tor makes, made at install so a bad file fails there
  and not on a restart. All authorities must run the same schedule, which is why
  it is in the file. The interval also sets the onion service time period the
  authorities vote (`hsdir_interval`, [Onion service time periods](#onion-service-time-periods)).
- `bootstrap`: a new network has no consensus yet, and its relays cannot prove
  themselves reachable through it. With `bootstrap` true every authority and
  relay sets `AssumeReachable 1` (the Tor manual's "used when bootstrapping a
  new Tor network"), which also makes authorities list every connected relay
  without testing it. **Set it to false once the first consensus is signed**,
  re-install and restart (Rolling it out, step 9).
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
  asks the public network's directories for anything. There is no fallback list
  in the file: clients bootstrap from the authorities.

A client's torrc (`tornet.ClientTorrc`) holds exactly the file's authorities, no
control port, no bridges, and loopback listeners only. Each value is written in
the canonical form it was parsed to: an address with an IPv6 zone or a newline
in it never reaches a torrc, because the parser refuses it first.

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
| `offline/<nick>/authority_identity_key` | the authority identity key, encrypted with the passphrase, mode `0400` (read-only for its owner, whatever `tor-gencert` left). It signs certificates and nothing else | **offline media**, encrypted, in two places (or an HSM), then deleted from the ceremony machine. Never on a server, never in a wallet vault |
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
- The monitor timer is the relay's (see "Relay health" below), run for the
  authority's own account in its own home: an authority is in the consensus as a
  relay is, so it writes `/var/lib/orama-global/tor-dirauth/monitor.json` too.
- With `--services chain,dirauth,reporter` the install also sets up the
  authority's bandwidth reporter ([below](#the-relay-bandwidth-reporter)), and the
  archive oneshot then also exports the authority's own vote for it
  ([where the votes come from](#the-relay-bandwidth-reporter)).

**Restarting authorities: one at a time, 30 minutes apart.** An authority that has
just started casts no Running vote for 30 minutes (Tor's
`TestingAuthDirTimeToLearnReachability`, whose default `AssumeReachable` does not
shorten and which only `TestingTorNetwork` can change), and a consensus needs the
Running flag in a majority of the votes: with three authorities, in two. Restarting
the second while the first is still inside its 30 minutes leaves a round with one
vote for the flag, and the authorities then make no consensus
("Nobody has voted on the Running flag ... Not generating a consensus!"). On
stagenet, three authorities restarted within four minutes of each other (02:25 to
02:29 UTC on 2026-10-10) made no consensus for 02:30 and 03:00 and the next one only
at 03:30; every node's consensus expired at 03:30, a relay fetched the next after its
retry delay (the exit node's full consensus was still the 02:00 one at 03:36 and its
microdescriptor consensus was replaced at 04:24), and the fleet e2e tests of that hour
failed. A restarted onion service built its descriptors from the older consensus it
still held (below). `orama global stop dirauth` and `restart dirauth` therefore refuse
while another authority of the network file published its descriptor (which it does
when it starts) less than 30 minutes ago, or is not in the consensus, or when this
authority holds no valid full consensus to judge by; `--force` overrides it. Verify
`orama global tor info` between restarts as before, and wait out the 30 minutes.

**Archive.** Every voting period is copied to
`/var/lib/orama-global/tor-dirauth/archive/<valid-after>/`:

| File | What |
|---|---|
| `consensus` | the consensus the authority holds (replaced only by the same consensus with more signatures) |
| `votes` | the votes that made it (`v3-status-votes`, only the documents of this period) |
| `bandwidth` | the bandwidth file the authority voted with, when one is configured |
| `MANIFEST.json` | `valid_after`, the SHA-256 of each file and `root` = SHA-256 over the lines `<name> <sha256>\n` in name order (`tornet.ManifestRoot`) |

`root` identifies the archived period; a relay report's `inputs_root` is a separate hash over the report entries and is not bound to it. The archive is local to
the authority host: **publication** (to global storage or a static site) is not
built, and the archive has no retention policy yet (a period is a few hundred
kilobytes at stagenet size and grows with the relay count).

**Bandwidth measurement (sbws) is not built.** Without a bandwidth file the
authorities weight relays by the bandwidth they report, capped by their
`RelayBandwidthRate`. That is gameable and is the reason the consensus weight
is not yet a reward basis.

Look at an authority with `orama global tor info` (as root on the host; `--json`
for scripts): the nickname, RSA fingerprint, ed25519 id, the consensus it holds
(flavour, validity, signature count, relay/exit/guard counts, the onion service
time period the authorities voted, `hsdir_interval_minutes` in `--json`) and whether it
lists the authority itself. When the consensus carries exit policy summaries (the full one an
authority or relay holds) and some exit's summary accepts no port, it also says how many exits
are in that state (`exits_without_ports` in `--json`): a client does not use them. A role whose DataDirectory cannot be read is shown
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

**Relay health.** `orama-global-tor-monitor.timer` (every five minutes) runs the oneshot
`orama-global-tor-monitor.service`, which is `orama global tor monitor --home
/var/lib/orama-global/tor-relay` as the relay's own account, with no network. It writes
`<home>/monitor.json` for the node report:

```json
{"in_consensus": true}
```

`in_consensus` is whether the consensus the relay holds lists its fingerprint. It is left out (the
file is `{}`) while the relay has no identity or consensus yet or the one it holds has expired, so an
unknown state is never reported as a no. This is the shape `report.ParseMonitor` reads: `orama
monitor node` shows `relay active (in the relay set)` or `(not in the relay set)` on the Global
line, the node report carries it, and the health check `global.relay.consensus` warns when the relay
says it is not listed. The unit the report watches for the relay is
`orama-global-tor-relay.service`.

A directory authority is in the consensus as a relay is, so it runs the same timer and oneshot
under its own account in its own DataDirectory: `orama global tor monitor --home
/var/lib/orama-global/tor-dirauth` as `orama-tor-dirauth`, writing
`/var/lib/orama-global/tor-dirauth/monitor.json` (a host runs a relay or an authority, never
both, so the unit name is the same). The node report, the inspector and `orama monitor node`
read the file from the home of whichever of the two units is installed: the Global line shows
`directory authority active (in the relay set)` or `(not in the relay set)`, and the health
check `global.dirauth.consensus` warns when the authority says it is not listed
(`global.dirauth.down` when its unit is not active).

## Exits

An exit is opt-in per node: `--services relay,exit`. Orama's stagenet runs exits
because the owner decided to; a network's file says so with `allow_exit` and an
install is refused otherwise, so a production network file that does not carry
it cannot be given an exit by a flag.

The policy (rendered by `tornet.ExitPolicyLines`, in this order): the operator's
own refusals; every reserved IPv4 range an exit can be asked to reach
(`netguard.Ranges`: private, loopback, link-local, carrier-grade `100.64.0.0/10`,
the `198.18.0.0/15` range that holds the co-located namespace's host address, the
documentation and benchmarking blocks); mail (`25`, `465`, `587`) and the
file-sharing and Windows-sharing ports Tor refuses by default; then accept the
rest. `ExitRelay 1`, `IPv6Exit 0`. Exit traffic leaves from the node's public
address (`--colocated`: through the host's masquerade).

Multicast (`224.0.0.0/4`) and the reserved class E block (`240.0.0.0/4`) are not
in the policy, as they are not in Tor's own: no TCP connection reaches them, and
listing them breaks the exit. The authorities summarise a policy into the `p`
line of the consensus, and clients choose exits by that summary. Tor's summary
lists a port as refused when the refusals for it (the private ranges Tor expands
itself are not counted) name more than two `/8` blocks of addresses, and these two
ranges are sixteen `/8` blocks each: with them the summary was `reject 1-65535`
for every port, the exit kept its `Exit` flag, and every client said "The
current consensus has no exit nodes" and built no path to the internet. The same
holds for an operator's reject list: `tor-exit-reject` entries that together
cover more than two `/8` blocks on every port (for example `0.0.0.0/1`) leave no
port, and `orama global install` refuses the exit then, naming the cause.
`orama global tor info` counts the exits whose summary accepts no port.

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

### Onion service time periods

A v3 onion service publishes its descriptor for a *time period*, under a key
derived from the period's number, and a client works the period out from the
consensus and asks for that one. Tor's shared-random protocol (SRV) runs in
**24 voting intervals** (12 commit rounds, 12 reveal rounds), and a service
**rotates its descriptors at the end of every run** (`rotate_all_descriptors` in
Tor's `hs_service.c`): it closes the introduction circuits of the descriptor it
serves, makes the descriptor it had prepared for the *next* period the current
one, and prepares another. Tor's time period is 1440 minutes (the consensus
parameter `hsdir_interval`) and starts half a run after the run does (Tor
derives that offset from the voting interval, `hs_get_time_period_num`). The
rotation does not fall on a period boundary. When the period is exactly one run
long, as on the public network (60-minute interval, 24-hour run and period), it
falls half a period after a period start: the descriptor it promotes
is the current period's and the one it drops is stale.

With a shorter interval a run is shorter than the period. Stagenet's 30 minutes
makes a run 12 hours: the service rotates at 00:00 and 12:00 UTC, the period
still changes at 06:00 UTC only. The rotation at 00:00 falls inside a period that
is longer than the run, promotes the descriptor for the next period and closes
the introduction circuits of the one that clients still ask for, so from 00:00 to 06:00 UTC the service answers nothing
(`INTRODUCE_ACK` "unknown service" from every introduction point, then "descriptor
not found" once the directories drop the old copy after its 3-hour lifetime).
The fleet e2e `TestNetwork_circuitsAndOnionServicesWorkThroughOurAuthorities`
passed at 23:05 UTC and timed out at 00:45 UTC for this reason, with every
service up, `txgate` answering, and all five tor processes healthy.

The fix is Tor's own parameter, voted by the authorities: every authority's torrc
carries `ConsensusParams hsdir_interval=<24 x voting_interval_minutes>`
(`Network.HSDirIntervalMinutes`; 720 for stagenet, 1440 for a 60-minute network),
which makes the period as long as the run and keeps the half-run offset Tor
already derives, so a rotation always falls half a period after a period start,
the same arrangement as the public network at any interval. The parameter
reaches the consensus when more than half of the authorities (integer division:
`votes > n / 2`) or at least 3 of them vote it (`dirvote_compute_params`), and then every service and every client of the
network, wallets included, reads it from the consensus: nobody needs a setting of
their own. The alternative Tor documents for test networks, `TestingTorNetwork 1`,
also sets the period to the run's length, but on every party's own torrc and
together with a bundle of unsafe defaults (`ExtendAllowPrivateAddresses 1`,
`ClientRejectInternalAddresses 0`, ...); it is not used. A voting interval above
480 minutes (the largest that divides 24 hours and keeps a run within the 14400
minutes Tor accepts for `hsdir_interval`) is refused with the network file.
Tor clamps a voted `hsdir_interval` to 30 through 14400 minutes.

`orama global tor info` shows the value Tor uses from the node's consensus
(`onion period`), clamped to Tor's range, and says when the voted value was
outside it. Onion services MUST be restarted after the consensus first carries the
parameter (Rolling it out, step 10): a running service keeps the period it
started with and stays on a stale descriptor for up to two rotations. A service
also builds its descriptors at the start from the consensus its tor then holds,
which can be the one cached before the restart: **restart an onion service only
once `orama global tor info` shows `onion period` for its own home
(`/var/lib/orama-global/tor-onion`)**, not only for the authority's. On stagenet
the mew service was restarted 35 seconds after the first consensus with the
parameter, before its tor held it (the cached file was replaced 3 minutes
later). Its descriptor for the voted period was on no HSDir while the other four
services' were, which is what a service built on the default period looks like; it
was the one onion service clients could not find.

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
8. **Publish the validator onions.** Each validator's onion address is in
   `orama global tor info` on its node (the `onion` line). Add them all to the
   network file on the operator's machine:

   ```bash
   orama global tor onions add --network-file tor-network.json \
     <mew>.onion <mewtwo>.onion <gengar>.onion <magicarp>.onion <froakie>.onion
   ```

   and ship the file to clients (`orama vpn`, wallets, `ORAMA_ONION_NETWORK`).
   Relays and authorities ignore `validator_onions`, so no node needs the new
   file or a restart for it; stage it with the next release or step 9.
9. **Leave bootstrap.** Set `"bootstrap": false` in `tor-network.json`, stage it
   on every node, re-run the same install command on each, then restart one
   node at a time, authorities first and verifying `tor info` between each
   (`orama global restart dirauth|relay|onion`). Three authorities hold a
   majority with one down; two restarting at once lose the consensus, and so do
   two restarted within 30 minutes of each other ([Directory authorities](#directory-authorities)).
10. **Onion service time period** (a network installed before the authorities
   voted `hsdir_interval`, [Onion service time periods](#onion-service-time-periods)).
   Stage the new `orama`, then on each authority in turn re-run its step 5
   command (it rewrites the torrc, the other services of the node are kept) and
   `sudo /root/orama-global-release/orama global restart dirauth`; wait until
   `orama global tor info` on it shows a fresh consensus with three signatures,
   and at least 30 minutes, before the next one (the command refuses sooner). When two authorities vote it, every node's consensus
   carries `hsdir_interval` (`onion period 720 minutes` in `tor info`). Then restart
   the onion services one node at a time (`orama global restart onion`), each only
   once its own `tor-onion` home shows `onion period 720 minutes`: they build
   their descriptors for the period the consensus they hold names.
11. **Verify** with the fleet e2e on the stagenet target: feature `tor-network`,
   stage 8 (`TestAuthorities_signAConsensusThatListsEveryRelay`,
   `TestNetwork_consensusVotesTheOnionTimePeriodOfOneSharedRandomRun`,
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

- `core/pkg/tornet`: `Load` and `ParseNetwork` (the network file),
  `Network.DirAuthorityLines`, `Network.RandomValidatorOnion`,
  `ClientTorrc(ClientConfig{Network, Home, SOCKSAddr, DNSAddr})` (loopback SOCKS
  with `IsolateSOCKSAuth`, no ORPort, no control port), `ParseConsensus`
  (consensus and microdescriptor consensus: validity, relays with RSA
  fingerprint, flags, bandwidth, measured), `Manifest`/`ManifestRoot` (the
  archive's digest), `NicknameFor`.
- `core/pkg/onionnet`: `Start` and `StartOnFreePort` run an unmodified `tor` on
  a network (`tornet.ClientTorrc`) and return once it has bootstrapped.
- `core/pkg/constants`: `TorNetworkFile` ("tor-network.json"), `TorNetSOCKSPort`
  (9052), `GlobalTorORPort` (31020), `GlobalTorDirPort` (31021),
  `GlobalTxGatePort` (31022), the unit names and state directories.
- Wallet transactions: `chainonion` (`--onion`, `--onion-socks`) speaks to the
  onion service's gate; the three calls above are all it needs, and all it uses.

## The client: `orama vpn`

`orama vpn up --network tor-network.json` starts an unmodified upstream `tor` (`--tor`, default
`tor` on the PATH) on the network and offers its SOCKS5 proxy on loopback (`--socks`, default
`127.0.0.1:9150`; `--dns` adds a DNS resolver that answers through the network). It runs until
interrupted. Tor's state (consensus and guards) is kept per network under the user cache
directory (`--data-dir` overrides), so the next start does not fetch the consensus again.

- It is a proxy, not a system-wide tunnel. Only applications pointed at the SOCKS port use the
  network, and they must use `socks5h` so that the proxy resolves names; the client never
  resolves a name locally.
- Kill switch: when tor stops, the proxy port closes and `up` exits with an error. Applications
  using the port fail; nothing is routed around the network.
- The proxy and DNS addresses must be loopback IP literals.
- The 3-hop Tauri/arti application with a system-wide tunnel (plan E6) is a separate piece of
  work; `orama vpn` is the command-line client for the same network file.

`orama vpn check --network tor-network.json` joins the network (tor is stopped when the check ends)
and reads an account through each validator onion service in the file (or the one in `--onion`),
each over a circuit of its own. It asks only what the tx gate serves: `GET
/cosmos/auth/v1beta1/accounts/<the all-zero address>`, which no key controls. The gate forwards it
and the chain answers either 200 or its own 404 "account not found" (a JSON error with a numeric
`code`), and either passes the check. The gate's own refusal (`404 {"error":"not served over the
onion service"}`, which is what `/status` gets), a 429 "busy", a 502 "the chain API did not answer"
and anything else fail it. The check passes when at least one onion service answers, and fails when
tor cannot bootstrap on the authorities, when none answers, or when there is nothing to try.

`ORAMA_ONION_NETWORK` supplies `--network` (and `--onion-network`); a flag wins over the variable. Tor keeps a lock on its state directory, so one client
runs per network at a time: `orama vpn up` and an `--onion-network` submission on the same
network (or `--data-dir`) cannot run together, and the second reports the lock in tor's log.

## Onion transaction submission

Chain transaction commands (`orama global register`, `bond`, `unbond`, `capacity`, `retire` and
the validator commands, `orama storage create|grant|revoke|prove`, `orama cluster
register-onchain|retire-onchain`) can send through a validator's onion service instead of
`--node`:

- `--onion addr.onion[:port]` uses a Tor SOCKS proxy that is already running (`--onion-socks`,
  loopback only, default `127.0.0.1:9050`).
- `--onion-network tor-network.json` (or `ORAMA_ONION_NETWORK`) starts a tor client for the network
  for this one command, stops it afterwards, and submits through it. With no `--onion` a
  validator onion from the file is picked at random for this transaction. `--onion-tor` names
  the tor binary. `--onion-socks` with `--onion-network` is a usage error: they are two Tor
  clients.
- `--node` together with either is a usage error.
- The account read and the broadcast both go over the onion service, each command run over a
  circuit of its own (one new SOCKS isolation credential per run), so two transactions never
  share a circuit.
- Failure returns the error: a network that cannot be joined ("the transaction was not sent"),
  a proxy that is down, or an onion service that does not answer ("nothing was tried outside
  Tor"). Nothing is sent on the clearnet and nothing is retried another way.

## The relay bandwidth reporter

`orama-global reporter` runs on a directory-authority host (unit `orama-global-reporter`, user
`orama-reporter`, after the chain). Each time `x/emission` closes an epoch it sends `x/relay` a
chunked `MsgReportEpoch` for that epoch.

**Installing it.** The reporter is a service of `orama global install`, beside `dirauth` and
`chain` (it signs and reads the epoch through the host's chain RPC, so the chain is on the same
install):

```bash
sudo orama global install --services chain,dirauth,reporter --staged-dir <dir> \
  --tor-address <ip> --tor-contact <who> --tor-authority-keys <bundle> \
  --tor-reporter-operator <orama1...>
```

`--tor-reporter-operator` is the account address of the operator the reporter runs for; it is
checked for shape (an `orama1...` account address) and the reporter checks the checksum when it
starts. The install, before anything on the host changes, reads the staged `tor-network.json`
and finds the authority published at `--tor-address` (the one the `dirauth` role of the same
install matches against its key bundle). Then it:

- creates the account `orama-reporter` and the home `/var/lib/orama-global/reporter` (0700,
  that account's);
- writes `authority-id` (that authority's `v3_ident`), `vote-interval` (the file's
  `voting_interval_minutes`, as `30m`) and `operator`, mode 0600, the account's, again on every
  install, so a changed `--tor-reporter-operator` takes effect;
- makes the votes directory `/var/lib/orama-global/tor-votes` (see "Where the votes come from");
- writes and enables `orama-global-reporter.service` under the shared global sandbox (private
  ranges denied, `/opt/orama` and `/etc/orama` hidden, loopback only to the chain's RPC, which
  moves to the namespace address with `--colocated`), after the chain and not part of
  `orama-node.service`; `orama global start` starts it with the other services.

The hot key is not touched: the reporter creates `hot-key` on its first start and logs its
address (`orama-global reporter --home /var/lib/orama-global/reporter --print-address` prints it
later, as the reporter's account, and creates nothing), which still has to be added to
`x/relay`'s reporter set and funded. A re-install leaves
the hot key, `state.json` and the reports in the home as they are.

State directory (`/var/lib/orama-global/reporter`):

| File | Holds |
|---|---|
| `hot-key` | The reporter's signing key, created on first start (mode 0600). Its address must be in `x/relay`'s reporter set, and funded. |
| `operator` | The operator address this reporter runs for (`--tor-reporter-operator`, written by the install). |
| `vote-interval` | The Tor network's voting interval as a duration (`30m`): `voting_interval_minutes` of `tor-network.json`, written by `orama global install`. The reporter measures an epoch against it, so a network that votes every 30 minutes is not judged by an hour. There is no default: a reporter without the file, or with one that is not a positive duration, does not start (re-run the install). |
| `authority-id` | The authority's v3 identity, 40 hex (the `dir-source` line of its votes): the `v3_ident` of this authority in `tor-network.json`. The reporter lives in the `chain` module and does not import the parser, so `orama global install` writes the file from the network file. |
| `votes/*.vote` | Only for a reporter run by hand without `--votes-dir`; the installed unit reads `/var/lib/orama-global/tor-votes` instead (below). The files are regular files, each a complete vote (one that ends before its `directory-footer` is still being written and is refused); other files in the directory are ignored. A `.vote` file that is not a vote, deterministically (it does not parse, is larger than a vote, or is swapped for a link or a FIFO after the listing; a link or FIFO is never followed or waited on) is logged as an error naming it and left out; the rest of the archive is still read. A file that cannot be opened or read for a reason that is not the file's (an I/O error, a descriptor limit, a file that vanished since the listing) fails the run instead, which is retried: a report is never built from a partial vote set. A header is read up to 64 MiB, the size limit of a vote, and no further. |
| `state.json` | The epoch in progress at the last pass, and the closed epochs still owed a report with their spans. |
| `report-<epoch>.json` | The entries chosen for an epoch, from before its first chunk is sent until the chain has all of them. |
| `monitor.json` | What the last report contained and left out. |

**Where the votes come from.** The authority archives one concatenated `votes` file per period
in its own home, which holds its keys and which the reporter's account cannot (and must not)
read. So the archive oneshot also exports the authority's *own* vote of each period into a
directory the two accounts share and nothing else uses:

| | |
|---|---|
| Directory | `/var/lib/orama-global/tor-votes`, owned by `orama-tor-dirauth`, group `orama-reporter`, mode 2750 (setgid) |
| Writer | `orama-global-tor-archive.service` (`orama global tor archive --export-votes-dir`), as the authority's account; it is allowed to write only this directory besides its own home |
| File | `<valid-after>.vote` (for example `20261008T120000Z.vote`), mode 0640 and the reporter's group (the setgid directory gives a new file its group), written atomically; a period already exported is left as it is |
| Content | the authority's own vote of the consensus's period, one complete vote, picked by the `dir-source` identity of the certificate in the authority's `keys/`; the votes of the other authorities are not copied, and neither is anything else of the home |
| Reader | `orama-global-reporter.service` (`--votes-dir`), through its own group: read-only, and it cannot enter the authority's home |

The export runs only on a host that has the reporter (in the same install or an earlier one: a
re-install of `dirauth` beside an installed reporter keeps it), so an authority with no reporter
has no such directory and an unchanged archive unit. An own vote that is not among the votes the
authority holds for the period, or that has no `directory-footer`, makes the oneshot fail (the
archive of that period is written first) instead of exporting something the reporter would
refuse; a period whose votes the authority no longer holds exports nothing, as the archive's
`votes_missing` says.

Why a copy in a shared directory, and not the archive itself or a root-side copy:

- The reporter parsing the archive directly would need to enter the authority's home (a Tor
  DataDirectory that must not be readable by others) and read a file that mixes every
  authority's votes. Opening the home to a second account, even for one subdirectory, puts the
  signing keys one permission mistake away from the account that holds a hot key.
- A root-side copy unit would be a second privileged program reading a directory the authority's
  unprivileged account controls (links and FIFOs), and its own timer to keep running.
- The archive oneshot already runs as the authority's account on this timer and already reads the
  votes. Writing one more file, to one directory it is given write access to and that the other
  account can only read, adds no privilege to either account. The reporter, which signs, opens
  each file without following a link and without waiting on a FIFO, because the authority's
  account (which faces the network through Tor) writes the directory it reads. A compromised
  authority account can already vote anything; what it cannot do is reach the hot key, and what it
  writes here is one authority's vote among the three or more whose median `x/relay` takes.

What a report contains, for each relay in the epoch's votes:

- `consensus_weight`: the median over the epoch of the `Measured=` value of the `w` line of the
  votes that list the relay `Running`, times 1,000,000 norama per unit (`NoramaPerWeight`; the
  per-relay cap of 100 ORAMA is reached at a measured 100000). The advertised `Bandwidth=` is
  never read, because a relay states it itself; a relay the authority did not measure weighs 0.
- `flags`: bit 0 is Exit (set when at least half of the votes listing the relay carry `Exit`
  and not `BadExit`; the only bit that changes pay), bits 1 to 3 are Guard, Stable, Fast.
- `uptime_fraction`: the share of this authority's votes in the epoch that list the relay
  `Running`. The plan says "the share of consensuses"; the reporter has only its own authority's
  votes, not the consensus, and reports that.
- `ed25519_id`: the one in the latest vote that lists the relay.

Rules that keep a node from paying itself:

- Only this authority's own votes are read (by `dir-source` identity), and only those whose
  `valid-after` is inside the epoch.
- The chain refuses a reporter address that operates a relay: `MsgRegisterRelay` refuses an
  operator that is in the reporter set, `MsgUpdateReporters` (and so a reporter proposal) refuses a
  set that names a relay operator, and genesis validation rejects both together
  (`ErrReporterOperatesRelay`; docs/CHAIN.md, "x/relay"). So the hot key cannot be paid as a
  relay operator. The chain does not know which operator *account* the hot key reports for, so
  the reporter itself leaves out the relays registered to the `operator` file's account (the
  authority's operator, a different address from the hot key). That second rule is the honest
  reporter's: a reporter that signs without this binary is held only by the median across
  reporters.
- Only relays `x/relay` has registered, with the registered ed25519 identity, are reported
  (`x/relay` refuses a whole chunk that holds any other); the monitor file counts the
  unregistered, the key mismatches, the own-operator relays and the relays with no ed25519
  identity.
- An epoch is not reported when the archive holds fewer than four fifths of the votes the
  voting interval implies (`vote-interval` in the home, the network file's `voting_interval_minutes`),
  so a gap in the archive is never read as relay downtime. The epoch stays owed and each pass tries it again (with its span
  kept in `state.json`) until the archive catches up or the chain stops taking reports for it
  (next paragraph).
- An epoch that lasted less than one voting interval holds no vote to judge uptime from, and never
  will. It is dropped at the first pass after it closes with `the epoch is shorter than one voting
  interval of the Tor network` (`reporter.ErrEpochTooShort`), not retried. The chain's epoch
  duration has to be at least the Tor network's voting interval for any epoch to be reported.
- A relay's pay is the median across reporters (`x/relay`; the default quorum is 3, one per
  initial authority), so one lying reporter cannot move it; see "Directory authority
  compromise" in [SECURITY_PLAYBOOKS.md](SECURITY_PLAYBOOKS.md).

**The report window.** `x/relay` takes a report for epoch `e` only while the chain is in epoch
`e+1` (`ReportWindowEpochs = 1`), and its end block settles the epoch in the first block of
`e+2`, paying through the 90/5/5 service split (docs/CHAIN.md, "x/relay"). An epoch nobody reported
is never settled and takes no later report. So the reporter reports epoch `e` at the first pass
after the boundary and keeps trying during `e+1`; a pass that finds the window over drops the
epoch with `the report window of the epoch has closed` (`reporter.ErrWindowClosed`), and one that
finds it already settled drops it with `the epoch is already settled on chain` (`ErrEpochSettled`).
Neither is retried, and the saved `report-<epoch>.json` is removed (a report that cannot be removed is a
failure and leaves the epoch owed until it can). A chunk the chain refuses is
read the same way: the reporter reads the chain's epoch and settlement again, and if the window
passed or the epoch settled in between, the error says so; otherwise the refusal is an ordinary
failure and the epoch stays owed. Run `--interval` (default 5 minutes) much shorter than an epoch,
so a report that needs a retry has the whole window to get one.

`x/emission` exposes only the start of the epoch in progress, so the span of a closed epoch is
the span between two passes that saw its boundaries. The reporter saves each boundary the moment
it sees it, before it tries any report. The first pass on a new home only records the epoch. If
more than one epoch closes between passes, the epochs in between cannot be bounded and are
skipped with an error (`epochs closed between two passes`). After a chain reset, remove
`state.json` and `report-*.json`.

**What the log says.** A pass that fails is logged `ERROR reporter pass failed`, each time it fails.
The states a healthy reporter meets are not failures: an epoch dropped because its window closed, it
was settled, it was shorter than a voting interval, or epochs were lost between two passes is a
`WARN reporter is not reporting an epoch` line, and an archive still filling in is an `INFO reporter
is waiting for the vote archive of an epoch` line. Each is logged once for its epoch, not again on the
next pass that meets it (`reporter.ExpectedState` names them).

The entries of an epoch are fixed when its first chunk is sent and saved in
`report-<epoch>.json`; a retry sends exactly those, because the registry can change in between
and `x/relay` completes a report only from chunks with the same `inputs_root`. A chunk of 1000
relays is about 150 KB; the chain accepts up to 32 chunks.

The observations are a pure function of the votes (`reporter.LoadVotes` and `Observe`); the
entries sent are those observations narrowed by the registry as it stood when they were chosen
(registered, matching ed25519 identity, not the operator's own), and `inputs_root` commits to the
entries sent.

## The public-launch gate

`chain/x/vpnlaunch` answers whether a public VPN beta may open. It is not linked into `oramad`
or any node. It requires, on each of the last 30 days: at least 5 directory authorities with an
independent majority, 100 relays from 40 operators, 15 exits in 5 countries, and no operator or
family above 10% of consensus weight (compared exactly, not rounded). `Failures` lists each
threshold that failed, with the day and the figures.

`Authorize` returns nil only if every threshold holds and the launch switch
(`vpnlaunch.LaunchEnabled`) is on. The switch is `false` in every build, and a test fails if it
is changed. Turning it on is a release decision made with the 30-day measurement in hand.

## Not built, and not verified

Stated so nothing here reads as more than it is (the spike's list is in
`plans/open-network/decisions/E0.md`):

- **Never run against a real network.** The code was written against Tor's
  manual and dir-spec; no tor process was started while writing it. The first
  stagenet bring-up is the first run, and E0 lists what it must confirm.
- Bandwidth measurement (sbws) is not built (see "Directory authorities").
- No independent authority: Orama runs all of them. While Orama runs a majority,
  Orama could sign a consensus that lists only its own relays and deanonymise
  users. The launch thresholds (a majority of independent authorities, 100
  relays from 40 operators, 15 exits in 5 countries) are plan item E7.
- The archive is not published, and neither it nor the exported votes have a retention policy.
- The install does not register a relay on chain: `MsgRegisterRelay` is a separate transaction
  (docs/CHAIN.md, "x/relay"), and a relay is paid only once it is registered and reported.
- The signed fallback list is not built: clients bootstrap from the
  authorities in the network file, which the release carries. The file is
  public and unsigned by itself; its integrity is the release's.
- Authority certificates are renewed by hand (above); nothing warns before
  expiry beyond `orama global tor info`.
- IPv6 is off everywhere.
