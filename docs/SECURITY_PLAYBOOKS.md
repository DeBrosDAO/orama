# Chain security playbooks

This page covers what an operator of the Orama L1 (`oramad`) does when a chain-level
fault is found. It describes the code as it is today. No external audit has been done.
There is no bounty programme; `docs/BOUNTY.md` is the scope, severity and disclosure policy, and payouts
are the owner's to set. Nothing here is an admin key: no module has a pause, a
freeze, or an authority that can change state outside a normal transaction.

## Invariant checks

Each module that holds or moves norama checks its own books.

| Module | Check | How to run it |
|---|---|---|
| `x/emission` | Minted matches the schedule. Bank supply equals genesis supply + validator mints + development mints + storage/relay service mints − burned | `oramad query emission invariants` |
| `x/fees` | Earnings total equals the fees account, open deposits equal the deposits account, burned + distributed equals collected | `oramad query fees invariants` |
| `x/storage` | Escrow conserved; the storage account holds exactly the queued mint payments (minted at epoch close, within the ceiling); distinct operators; reserved within declared; settlement queue well formed | `oramad query storage invariants` |
| `x/nodes` | Module balance equals role bonds plus unbonding entries; active and capacity rules | `oramad query nodes invariants` |
| `x/relay` | Each settled epoch mints within its relay ceiling and its payouts sum to what was minted | `oramad query relay invariants` |
| `x/houses` | Locked house bonds equal the houses account | `oramad query houses invariants` |
| `x/token` | Each token's issued supply equals its bank supply, and its metadata deposit matches x/fees | `oramad query token invariants` |
| `x/market` | Market balance equals the open bids | `oramad query market invariants` |
| `x/power` | The module account holds no norama: it passes an epoch's validator share through inside one call | `oramad query power invariants` |

`x/cnft` and `x/archive` hold no norama of their own (cNFT deposits sit in `x/fees`' deposits account,
archive payments go through `x/storage`), so they have no invariant query. Every module above is in
`INVARIANT_MODULES` in `chain/scripts/stagenet/deploy.sh`, and `deploy.sh invariants` fails when any of
them reports a broken check on any node.

Only `x/emission` can mint norama. `x/storage` and `x/relay` payments are minted by
`x/emission` and moved to them. `x/token` holds the Minter permission for the denoms it
creates, and its bank keeper refuses a norama mint (`chain/app/mint_policy.go`,
`TestGetMaccPerms_onlyEmissionMintsNorama`).

`x/emission`'s two checks also run on every `InitGenesis`. So does the G1 locked-parameter check
(`docs/CHAIN.md`, "Genesis parameters (G1)"). A chain refuses to start from a
genesis that breaks them.

Run every query above against each validator after any upgrade, and whenever a report
says a balance looks wrong. A broken invariant is a halt-height fix (below). It is never
patched on one node.

## Coordinated halt-height fix

A fix to the state machine changes the app hash. All validators must switch at the same
height, or the chain splits. There is no on-chain switch that can do it for them.

1. **Disclose privately.** Keep the finding with the release signers until the fix is
   built. Do not open a public issue before step 4.
2. **Build the fix reproducibly** from a tagged commit: `make build` in `chain/`, same
   `-trimpath` and version ldflags as `chain/scripts/stagenet/deploy.sh`.
3. **Sign the release.** The TUF release root verifies the archive (`orama node
   stage-archive --release-metadata --release-target`, see `docs/DEV_DEPLOY.md`). The
   production signer ceremony has not happened; stagenet uses a test root.
4. **Announce a halt height** far enough ahead for every validator to act, in blocks, not
   time.
5. **Each validator sets that height.** Either set `halt-height = <H>` in
   `config/app.toml`, or start `oramad` with `--halt-height <H>`. At `H` the node commits
   the block and stops.
6. **Each validator stages the new binary** with `orama global stage-oramad --upgrade
   <name> ...` (cosmovisor layout, `docs/CHAIN.md`); the installed chain unit runs
   under cosmovisor. Validators stay in notify mode; nothing installs by itself.
7. **Restart after `H`.** Blocks resume once validators holding more than two thirds of
   voting power run the fix. Before the λ hand-over that means the bootstrap committee.
8. **Check** every invariant query above on every validator.

This procedure has not been rehearsed on a live chain yet.

## Playbooks

Each one says what can be seen, what the operators can do with commands that exist today, and what
is not possible. Where a step has not been rehearsed, it says so. None of them uses an admin key,
because there is none. Every fix to the state machine is the coordinated halt-height fix above.

### Bootstrap committee failure

Until lambda reaches 1, a permissioned committee of at least 30 seats signs blocks
(`docs/CHAIN.md`, `x/power`). CometBFT needs more than two thirds of voting power online.

**What can be seen**
- `oramad status` on any node: the latest height stops advancing.
- `oramad query power bootstrap-committee` (the seats) and `oramad query power lambda` (lambda, the cap
  and the epoch it was last updated). A halt stops epochs, so lambda stops too.
- CometBFT's `/validators` RPC: which keys hold power.

**What the chain does by itself**
- A member that misses more than half of a 10,000-block window is jailed for 600 seconds and slashed
  0.01%. A jailed or tombstoned member's bootstrap share is zero at once and the rest absorb it, so
  a committee that keeps more than two thirds of power online repairs itself.
- Downtime slashing and jailing need blocks. If less than two thirds of power is offline at once, no
  block commits, nobody is jailed, and the chain stays halted. Nothing on chain can fix that.
- Seats lapse at lambda 1. Lambda rises with bonded stake and with epochs since genesis (365 by
  default), but is held at 0.95 (`pre_gate_lambda_cap`) until enough validators are active
  (`docs/CHAIN.md`, "The power formula"). The deadline is a genesis parameter: no message extends it.

**Recovery (a halt with less than two thirds online)**
1. Reach the members. Wait for enough of them to return if that is possible: the chain resumes by
   itself when more than two thirds of power is back, with no fix.
2. If members are gone for good, this is a coordinated hard fork over exported state. The surviving
   validators stop `oramad`, agree on one height, and each runs
   `oramad export --height <H> --output-document exported.json` on their own copy. They compare the
   files (hash them); a difference means someone's state diverged and nobody proceeds.
3. The exported `power` section carries `power_records`. On an exported genesis `x/power` takes the
   validator set from those records, not from the committee list, so a seat that must stop signing
   has its `comet_power` set to 0 there. A production chain-id refuses a genesis that declares fewer
   than 30 committee members (`checkCommitteeSizeChainIDGate`), so the dead seats cannot be
   deleted from `bootstrap_committee`.
4. `oramad genesis validate exported.json` runs the module checks and the locked-parameter check. Every
   validator starts the same edited file with the same fix binary (the halt-height procedure above:
   build, sign, announce, restart).
5. Check every invariant query on every validator.

Not rehearsed: step 3's edit has not been run on a live chain, and whether a fork also needs a new
chain-id is not settled. Rehearse it on stagenet first. Not possible: adding a seat to a running chain
(the committee is genesis-only), or shortening the deadline.

### Directory authority compromise

`x/relay` pays relays from reports signed by a reporter set. The set is separate from the `DIRAUTH`
role in `x/nodes`, which is only a registered, bonded role. Nothing on chain links a reporter to a
`DIRAUTH` node. No code runs a directory authority yet (`core/pkg/tornet` validates a parameter
set and nothing more), so a compromise today means a compromised reporter key.

**What can be seen**
- `oramad query relay reporters`, `params`, `epoch <n>`, `relay <fingerprint>` and `invariants`.
- A reporter that lies moves the median only when there are at least three reporters. With the default
  quorum of 2 and two reports, the median of two values is their average, so one bad reporter can
  skew a relay's paid weight by half the difference.

**What can be done**
- Remove or replace a reporter with a structural proposal: `oramad tx houses submit-proposal` with a
  `relay_reporters` content (`add`, `remove`), voted with `vote-token` and `vote-operator`, executed with
  `execute-proposal` after the timelock. The change goes through `x/relay`'s own
  `MsgUpdateReporters` handler, which nothing else can call. A change that would leave no reporter
  fails, and the set stays as it was.
- Removal takes effect on unsettled epochs too: settlement counts only reports from addresses in
  the set at that time.
- The structural tier must be open (lambda 1 and 21 eligible operators over 7 /16 networks and 5
  ASNs), the proposal must pass both houses, and the timelock is 60 days. There is no faster path.
  Before the tier opens, or when 60 days is too long, the only lever is the coordinated hard fork with
  a new reporter set in genesis.

**Not possible today**
- Jailing a relay or a dirauth from a transaction: `Keeper.JailRelay` has no caller and no proposal type
  reaches it. There is no unjail.
- Slashing or unbonding another operator's `DIRAUTH` node: node messages are signed by the owner only.
- Changing the quorum, caps or uptime minimum: `x/relay` parameters are genesis-only.
- Revoking or rotating directory-authority certificates: no code issues them.
- Until a majority of authorities is independent, Orama could list only its own relays. That is a
  declared trust point, and the VPN is not public until it is fixed.

### Release-key compromise (TUF root rotation)

A node verifies a staged `oramad` or archive against the TUF root at `/etc/orama/release-root.json`
(`orama global stage-oramad`, `orama node stage-archive`, each with `--release-metadata <dir>
--release-target <name>`). Validators are on notify and nothing installs by itself: no command reaches
`autoupdate.Upgrade`, so no node installs a release without an operator running a command.

**What is not implemented**
- No code rotates the TUF root. `releaseverify` loads the root file it is given and does not walk a
  chain of root versions, so a new root is not checked against the old one, and there is no key
  revocation or threshold change. No command writes `release-root.json`, and no command signs TUF
  metadata.
- The compromised keys' metadata stays valid for any node that still trusts the old root.

**What an operator can do**
1. Stop staging. Do not run `stage-oramad` or `stage-archive` for a release you cannot verify out of band,
   and tell the other validators. Validators stay on notify, so a bad release reaches only a node whose
   operator stages it.
2. Build a new root and new targets on a machine you trust, with new signing keys and reproducible builds
   (`make build` in `chain/`).
3. On each node, replace `/etc/orama/release-root.json` by hand with the new root, after checking its
   hash with the other operators over a channel that does not depend on the release repository.
   Nothing verifies the new root, so this is the trust step.
4. `release-seen.json` records the highest snapshot version seen and refuses a lower one as a rollback. If
   the new repository restarts numbering below it, remove or reset that file on each node. No command
   does that and it is not documented elsewhere.
5. Re-stage the fix with the new root's metadata. If the compromise was used to ship a bad binary,
   this is the coordinated halt-height fix, with the new root.

**A separate rotation that works**
- The wallet-signer anchor for archives (`/etc/orama/archive-signers`) rotates with
  `orama build --signers 0xA,0xB`: a build signed by a currently trusted signer replaces the list.
  Retiring a key takes two builds, and a recorded build date stops an older signed build from
  replaying a retired key. It does not cover the TUF root or the chain binary.

### Mass provider failure

**What can be seen**
- `oramad query storage queue` (pending settlements), `invariants`, `deal <id>` and `params`. The
  gRPC `Challenges` query (epoch, optional node id) lists what was challenged and what is unproved;
  `Slot` shows one slot's state and miss count. There is no CLI for those two.
- Events `storage_slot_evicted`, `storage_slot_assigned`, `storage_slot_declined` and
  `storage_slot_accepted`.

**What the chain does by itself**
- An unproved challenge is a miss only when its settlement item is applied. Two consecutive misses
  slash 10% of the epoch price, and four (`miss_threshold`) evict the slot. Below that the slot is
  challenged again every epoch. A proof clears the count.
- An evicted slot goes back to `Pending` and `assignDue` reassigns it every block to a node with a
  distinct operator, /16 network and ASN. If no candidate fits it stays pending and is retried each
  block, and an open deal with no holder is refunded.
- When active distinct operators fall below `s_min_providers` (8), the private-deal subsidy for that
  epoch is 0.
- A jailed, under-bonded or retired provider is skipped by assignment, but stays tracked until its
  last replica ends.

**What operators and owners can do**
- A deal owner reads the replica back with `orama storage get --deal-id N --out ... --rpc ...`, and
  re-uploads a slot to a new provider with `orama storage put --deal-id N --dir <seal output>`.
- A repair delegate (`orama-global repair`) rebuilds a lost slot from a surviving replica with the
  repair seed the owner installed. It never sees plaintext and does nothing for a deal with no
  accepted replica.
- A provider that cannot serve a slot answers `orama storage decline` (no penalty, the slot is
  reassigned) before its accept window closes, or `MsgReleaseReplica` (no slash, two per epoch).
  `MsgReleaseReplica` has no CLI.

**Not possible**
- Changing `miss_threshold`, `s_min_providers` or any storage parameter: they are genesis-only.
- Recovering a deal whose every replica is gone without the original `seal` output.
- Listing the rechallenge set or tracked nodes in one query.

### Settlement or archive backlog

**Settlement.** The queue drains `max_settlements_per_block` (100) items per block, and each epoch
adds one item per challenge.
- Detect: `oramad query storage queue` gives `pending`, `head` and `tail`; `oramad query storage
  invariants` checks `queue_well_formed` and `subsidy_within_ceiling`.
- Effects: eviction, slashing and payment are all applied at settlement, so a backlog delays them. A deal
  is not expired and its escrow is not returned while it has a pending item.
- Payment does not depend on the emission ceiling window. At epoch close `x/storage` mints the epoch's
  whole payment into its own module account, and settlement pays from that, so a queue that lags
  past the 30-epoch window still settles. Items written by a binary older than that change were
  not reserved: that switch needs a new genesis or an empty queue.
- Fix: the drain rate is a genesis parameter, so a queue that grows faster than 100 items a block needs a
  binary that raises it, staged with the halt-height procedure. No message changes it.

**Archive.** Validators keep 14 days of blocks; `orama-global archiver` bundles older ones, and
`x/archive` marks a range `Archived` when at least 3 distinct operators attest the same root and 3 live
`ARCHIVE` deals hold it.
- Detect: `oramad query archive last-archived-height` (the contiguous archived prefix; one unarchived
  range stalls it), `range <start> <end>`, `retain-height` and `params`.
- Archivers must use the same `--range-blocks` (default 1000) or ranges overlap and are refused. An
  archiver that finds a root conflict writes `<home>/conflicts/<start>-<end>.json`.
- Verify a bundle: `orama-global history get --from <archiver home or URL> --height H --out F --rpc ...`
  checks the file hash, each block against its header, and the root against `x/archive`.
- The retain height never rises above the last archived height, but **nothing feeds it into a node's
  pruning**: no node prunes by it (`docs/CHAIN.md`, "History archiver"). A validator's real block
  retention is its own `min-retain-blocks` in `app.toml`, which nothing here sets. During an archive
  stall, keep it at `0` (retain every block) on every validator until the stalled ranges are archived.
  This is by hand, per node.
- Not possible: creating an `ARCHIVE` deal for a given bundle from x/storage yet, or slashing an archiver
  for a wrong root.

### Unshield run

**There is nothing to run today.** No shielded module, keeper, message or store is in the app. `x/shielded`
is libraries: the pool and turnstile accounting (`pool`), the nullifier set, the unshield target policy
and the verifier interface. There is no `MsgShield` or `MsgUnshield`, `MsgShieldEarnings` is not
implemented, `oramad query shielded` does not exist, and proof verification fails closed: only the
Orchard verifier is linked and two independent verifiers are required, so every bundle is refused
(`verify.ErrVerifierNotLinked`). User-to-user norama sends are refused by the bank send restriction, so
an unshield run has no path to begin with.

**What the design requires once a shielded module is wired** (this is the code in `x/shielded/pool`, not a
procedure):
- Net unshield from a pool is capped at 2% of it (at least 1 ORAMA) per 24 hours. Over the cap, a
  contract or adapter unshield and a fee top-up fail, and bond and deposit unshields queue and are
  paid pro rata with a per-address limit, so one large holder cannot take the head of the queue.
- Each vintage and each asset has a turnstile: a pool cannot pay out more than went in, and a
  vintage migrates only to a newer pool.
- An unshield is signed by the target's owner and goes only to that owner's own contract, bond,
  deposit or fee balance, never to another user's account.
- The cap is an ossified constant. It cannot be changed or bypassed by a vote, and there is no
  pause. If a circuit bug is found, the response is the coordinated halt-height fix (a patched
  build that validators holding two thirds of power choose to run), and the cap bounds the loss
  meanwhile.

**Before a shielded module ships, this playbook needs**: the queries that show a pool's balance, its
turnstile and the cap used, a shielded invariant in `INVARIANT_MODULES`, and a rehearsal on stagenet.
