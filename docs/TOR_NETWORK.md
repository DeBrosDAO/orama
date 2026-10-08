# Orama Tor network: clients, reporter and launch gate

This describes the client side of an Orama Tor network and the two pieces that sit beside it:
the relay bandwidth reporter and the public-launch gate. The network itself (directory
authorities, relays, validator onion services) is built separately; what a client needs of it is
one file, below.

Everything here is private-network only. The public Orama Tor network is not launched, and no
code path joins one.

## The network file

A network is a JSON file, `network.json`. `core/pkg/onionnet` loads it and refuses anything that
is not exactly this shape (unknown fields, names where an IP address is required, a malformed
fingerprint, fewer than three authorities, a network not marked `private`):

```json
{
  "name": "stagenet",
  "private": true,
  "authorities": [
    {"nickname": "auth1", "address": "192.0.2.1:31021", "orport": 31020,
     "v3ident": "<40 hex>", "fingerprint": "<40 hex>"}
  ],
  "fallbacks": [
    {"address": "198.51.100.7:31021", "orport": 31020, "id": "<40 hex>"}
  ],
  "validator_onions": ["<56 base32 characters>.onion:80"]
}
```

- `name` is 1 to 19 letters, digits, `-` or `_`; it names the client's state directory.
- `authorities` become `DirAuthority` lines and `fallbacks` become `FallbackDir` lines. The
  client's tor uses these and no others (`UseDefaultFallbackDirs 0`): it cannot join the public
  Tor network by accident.
- `validator_onions` are validator onion services that accept transaction submissions. A
  submission with no `--onion` picks one uniformly at random with a cryptographic random source.
  The list is a bootstrap list; it is not read from `x/nodes` yet.

Every field is validated before it reaches a torrc, so a value cannot add a line to it.

## The client: `orama vpn`

`orama vpn up --network network.json` starts an unmodified upstream `tor` (`--tor`, default
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

`orama vpn check --network network.json` joins the network (tor is stopped when the check ends)
and requests the status route of each validator onion service in the file (or the one in
`--onion`), each over a circuit of its own. It passes when at least one answers HTTP 200, and
fails when tor cannot bootstrap on the authorities, when none answers, or when there is nothing
to try.

`ORAMA_ONION_NETWORK` supplies `--network`. Tor keeps a lock on its state directory, so one client
runs per network at a time: `orama vpn up` and an `--onion-network` submission on the same
network (or `--data-dir`) cannot run together, and the second reports the lock in tor's log.

## Onion transaction submission

Chain transaction commands (`orama global register`, `bond`, `unbond`, `capacity`, `retire` and
the validator commands, `orama storage create|grant|revoke|prove`, `orama cluster
register-onchain|retire-onchain`) can send through a validator's onion service instead of
`--node`:

- `--onion addr.onion[:port]` uses a Tor SOCKS proxy that is already running (`--onion-socks`,
  loopback only, default `127.0.0.1:9050`).
- `--onion-network network.json` (or `ORAMA_ONION_NETWORK`) starts a tor client for the network
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

State directory (`/var/lib/orama-global/reporter`):

| File | Holds |
|---|---|
| `hot-key` | The reporter's signing key, created on first start (mode 0600). Its address must be in `x/relay`'s reporter set, and funded. |
| `operator` | The operator address this reporter runs for. |
| `authority-id` | The authority's v3 identity, 40 hex (the `dir-source` line of its votes). |
| `votes/*.vote` | The archived votes: regular files, each a complete vote (one that ends before its `directory-footer` is still being written and is refused). Other files in the directory (consensus documents, bandwidth files) are ignored. `--votes-dir` overrides the directory. The directory belongs to the reporter's user, so whatever syncs the archive (E2) must be able to write it. |
| `state.json` | The epoch in progress at the last pass, and the closed epochs still owed a report with their spans. |
| `report-<epoch>.json` | The entries chosen for an epoch, from before its first chunk is sent until the chain has all of them. |
| `monitor.json` | What the last report contained and left out. |

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
- Relays registered to the reporter's own operator are not reported by it. This is the honest
  reporter's rule: the chain does not enforce it, so a reporter that signs without this binary is
  held only by the median across reporters.
- Only relays `x/relay` has registered, with the registered ed25519 identity, are reported
  (`x/relay` refuses a whole chunk that holds any other); the monitor file counts the
  unregistered, the key mismatches, the own-operator relays and the relays with no ed25519
  identity.
- An epoch is not reported when the archive holds fewer than four fifths of the votes the
  voting interval implies (`--vote-interval`, default 1 hour), so a gap in the archive is never
  read as relay downtime. The epoch stays owed and each pass tries it again (with its span
  kept in `state.json`) until the archive catches up or `x/relay` settles the epoch; the pass then
  reports that the epoch is settled and drops it. (The plan has `x/relay` settle an epoch a
  fixed number of epochs after it closes; nothing in `oramad` calls `SettleEpoch` yet, so until it
  does, no epoch is ever settled and a reporter keeps retrying an epoch whose archive stays short.)
- A relay's pay is the median across reporters (`x/relay`), so one reporter cannot move it
  alone once there are three; with the default quorum of two, see "Directory authority
  compromise" in [SECURITY_PLAYBOOKS.md](SECURITY_PLAYBOOKS.md).

`x/emission` exposes only the start of the epoch in progress, so the span of a closed epoch is
the span between two passes that saw its boundaries. The reporter saves each boundary the moment
it sees it, before it tries any report. The first pass on a new home only records the epoch. If
more than one epoch closes between passes, the epochs in between cannot be bounded and are
skipped with an error (`epochs closed between two passes`). After a chain reset, remove
`state.json` and `report-*.json`.

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
