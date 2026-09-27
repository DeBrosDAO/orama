# The Orama L1 chain

**Status: first code, devnet/localnet only.** Nothing here has run on a public network. This
document describes only what `chain/` actually does today; the full design (including everything
not yet built - staking power caps, governance, storage, relay, shielding, CosmWasm, and so on) is
in `plans/open-network.md` and `plans/open-network/track-c-chain.md`.

`chain/` is its own Go module (`github.com/DeBrosOfficial/network/chain`). `core/go.mod` does not
require it, and nothing in `chain/` imports `core/`.

## What's running

`oramad` is a [Cosmos SDK](https://github.com/cosmos/cosmos-sdk) v0.54.4 +
[CometBFT](https://github.com/cometbft/cometbft) v0.39.4 application, wired by hand in
`chain/app/app.go` (not depinject), following the pattern of the SDK's own `simapp` at that tag.

### Modules wired

| Module | Purpose |
|---|---|
| `auth` | accounts |
| `bank` | balances and the `norama` denom |
| `staking` | validators, delegation, bonding (standard SDK module - see the devnet exception below) |
| `slashing` | downtime/double-sign penalties |
| `distribution` | pays block rewards (including x/emission's mint) out to bonded validators and delegators, proportional to voting power |
| `consensus` | on-chain consensus parameters |
| `upgrade` | coordinated binary upgrades |
| `genutil` | genesis/gentx tooling |
| `evidence` | equivocation evidence handling |
| `feegrant` | fee sponsorship |
| `emission` (custom, `chain/x/emission`) | the halving-with-tail emission schedule |

**Not wired**, on purpose: `x/gov`, `x/mint` (replaced by `x/emission`), `x/authz`, `x/epochs`
(x/emission tracks its own epochs directly in its `BeginBlock`), `x/group`, `x/nft`, `x/circuit`,
`x/crisis`, IBC, and anything EVM/CosmWasm. `x/auth/vesting` is not wired either: nothing in this
module's genesis or gentx flow needs it.

**No governance module exists yet.** Every module that the upstream SDK expects to be governed by
`x/gov` (upgrade, consensus params, bank, staking, slashing, distribution) is instead given an
"authority" address that is the hash of a dedicated, never-registered module name,
`"orama/no-authority"` - `app.UnreachableAuthority()` in `chain/app/app.go`. That name is
deliberate: using `"gov"` instead would mean that simply registering a standard `x/gov` module in
some future release silently hands it control of every authority-gated message on the chain today,
with no explicit migration step. Because no module by this name is ever registered, no private key
or module account can ever produce a valid signature for it, so every authority-gated message on
this chain (`MsgSoftwareUpgrade`, every module's `MsgUpdateParams`, ...) is permanently unreachable
until a future release wires real governance (`x/houses`, `plans/open-network.md` D17) and
deliberately migrates the authority. Today, the only way to change this chain's behavior is a
coordinated hard fork (a new binary, a halt height, and validators choosing to run it) - never an
on-chain vote or an admin key. `TestUnreachableAuthority_rejectsEveryAuthorityGatedMsg` in
`chain/app/app_test.go` proves bank, staking, distribution, consensus and upgrade's
authority-gated messages all reject a signer that isn't this address.

### Denom and accounts

- Base denom: **`norama`**. Display denom: **`ORAMA`**. **9 decimals**: 1 ORAMA = 10⁹ norama.
- Bech32 prefix: **`orama`** (accounts), with the conventional `oramavaloper`/`oramavalcons`
  suffixes for validator operator/consensus addresses.
- Keys are secp256k1, signed with `SIGN_MODE_DIRECT` (plus `SIGN_MODE_TEXTUAL` where the client is
  online).
- BIP-44 coin type: **118** (the same as the Cosmos Hub), as a placeholder. `plans/open-network.md`
  D8/D1 marks the final derivation as a still-open decision record; the value lives in exactly one
  place, `chain/app/params.CoinType`, so changing it later touches one line.

These constants live in `chain/app/params`, a small dependency-free package that both the app
wiring and `x/emission` import (`params.NoramaPerOrama` is the *one* place the 10⁹ conversion
factor is defined; `x/emission/types` imports it rather than keeping its own copy), instead of
being duplicated as magic strings.

`x/bank`'s default genesis registers `norama`/`ORAMA` denom metadata (base `norama`, display
`ORAMA`, exponent 9) via a small override of `x/bank`'s own `AppModuleBasic`
(`chain/app/genesis_overrides.go`), so wallets and block explorers that read denom metadata from
genesis see the display denom without hardcoding it.

There is **no send-restriction hook yet**. `plans/open-network.md` D7's "mandatory shielded
payments" (no public user-to-user ORAMA transfer) is not implemented in this first pass; `x/bank`
runs with its default send-enabled behavior. This is a known gap, not a design decision - see
"Deviations" below.

### Other genesis defaults

- **`x/distribution`'s `community_tax` defaults to `0`** (also via a small `AppModuleBasic`
  override in `chain/app/genesis_overrides.go`), not the SDK's own default: there is no `x/gov` and
  therefore no spend path for a community pool on this chain, so a nonzero tax would just
  accumulate norama nothing can ever claim.
- **The genesis consensus block `max_gas` is set to 100,000,000`, not CometBFT's own unlimited
  default.** `x/consensus` has no genesis state of its own in this SDK version - the "consensus"
  block gas/size limits live in the top-level `consensus` field of `genesis.json`, which
  `oramad`'s own module wiring can't default - so `chain/scripts/localnet/localnet.sh` and
  `chain/scripts/stagenet/deploy.sh` both patch it into the generated genesis file directly.
- **`app.toml`'s default `minimum-gas-prices` is `0.000001norama`**, not zero. This is a
  placeholder floor, not the real fee mechanism: `plans/open-network/track-c-chain.md` C2's
  EIP-1559-style base fee (with its 100%-burned floor) hasn't been built yet, so this just stops a
  validator from accepting free transactions by default in the meantime.

## `x/emission`: the halving-with-tail schedule

`chain/x/emission` is a small, queries-only module (**no `Msg` service at all** - nothing can ever
change its schedule after genesis short of a hard fork). It implements the schedule from
`plans/open-network.md` ("Supply and emission") and `plans/open-network/track-c-chain.md` (C3).

### Genesis parameters (immutable after genesis)

| Param | Default | Bounds | Meaning |
|---|---|---|---|
| `epoch_duration` | 24h | (0, 365 days]; ≥ 24h unless `allow_bootstrap_stake` | minimum BFT time that must pass since the current epoch started |
| `min_blocks_per_epoch` | 14,400 | (0, 1e9]; ≥ 14,400 unless `allow_bootstrap_stake` | minimum blocks that must be produced since the current epoch started |
| `allow_bootstrap_stake` | `false` | - | see "The devnet-only bootstrap-stake exception" below |

An epoch closes only when **both** the time and block-count conditions hold, so whichever is
slower binds. With blocks faster than 6 seconds, the 24h clock binds. The 14,400-block floor limits
how far timestamp manipulation can compress an epoch: validators that control block timestamps
(a stake-weighted median in CometBFT v0.39) could make BFT time run fast, but they still have to
produce 14,400 real blocks per epoch. Both floors apply unless `allow_bootstrap_stake` is set. `allow_bootstrap_stake` relaxes both floors (down to
the same `(0, 365 days]`/`(0, 1e9]` absolute bounds), which is why a devnet or localnet genesis sets
it alongside a much shorter epoch (e.g. 30s / 5 blocks) with `oramad genesis set-emission-params`
(below), so a chain doesn't have to run for a day to see emission happen.

**Missed epochs are never caught up.** If a halt or a long gap means far more than
`epoch_duration` has elapsed by the time a node next checks, exactly **one** epoch still closes,
and the next one starts from "now" - never backdated. The epoch *number* (and therefore the
halving bracket) advances by exactly one per check, regardless of how much wall-clock time that
took. The schedule below is defined in epoch count, not calendar time, for exactly this reason.

### The schedule

Halving boundaries count **completed epochs**:

| Epochs | Maximum per epoch |
|---|---|
| 1 – 730 | 14,848 ORAMA |
| 731 – 1,460 | 7,424 ORAMA |
| 1,461 – 2,190 | 3,712 ORAMA |
| 2,191 – 2,920 | 1,856 ORAMA |
| 2,921 – 3,650 | 928 ORAMA |
| 3,651 onward, forever | 274 ORAMA |

The cumulative maximum at epoch 3,650 is exactly **21,000,640 ORAMA**
(`x/emission/types.CumulativeScheduleMax(3650)`, checked by
`TestCumulativeScheduleMax_epoch3650Equals21_000_640Orama`). The table itself lives in one place,
`chain/x/emission/types/schedule.go`, alongside the closed-form `CumulativeValidatorMinted`, which
is exactly 60% of `CumulativeScheduleMax` for the same epoch count (exact, not approximate, because
every schedule amount is a whole ORAMA times 10⁹ norama and 10⁹ divides evenly by 100 - see the
invariants section below).

### The split, and what actually gets minted

Every closed epoch's maximum splits 60% validators/delegators, 25% storage, 10% relay, 5%
development (`chain/x/emission/types/split.go`). **Today, only the 60% validator/delegator share
is ever minted.** It is minted into `x/emission`'s own module account and immediately forwarded to
the fee collector account, so `x/distribution`'s existing `BeginBlock` pays it out to bonded
validators (and their delegators) on voting power, exactly like it already does for transaction
fees. `x/emission` runs its `BeginBlock` before `x/distribution`'s, so a mint lands in the fee
collector in time to be swept up the same block.

The storage, relay and development shares are **never minted**: they are recorded as a
`CeilingRecord` (per epoch: storage/relay/development ceiling amounts plus the validator amount
actually minted) so that `x/storage`, `x/relay` and a future `x/houses` development spend (none of
which exist yet) can claim them within their own settlement window once they do. `x/emission` only
keeps the trailing 30 epochs of these records (`types.CeilingWindow`) and prunes older ones -
nothing currently reads an expired one, so nothing is lost by pruning it.

Remainders from each share's integer division always fold into the validator share, so the four
shares of any epoch's maximum sum back to that maximum exactly, to the norama.

### The devnet-only bootstrap-stake exception, and its premine gate

Standard `x/staking` needs at least one validator with a self-delegation at genesis, but the
bootstrap-committee module that is supposed to give genesis validators power *without* tokens
(`plans/open-network.md` D16, tracked as C4, "x/power") does not exist yet. Until it does, a
devnet or localnet genesis funds each validator's account with a small amount of `norama` (see
`chain/scripts/localnet/localnet.sh`'s `SELF_BOND`) purely so `x/staking`'s genesis validation
passes. **This is a devnet-only stand-in, not part of the design**: a real network genesis starts
at exactly zero balance, and `x/emission`'s `InitGenesis` actively enforces that with a premine
gate:

- If `Params.AllowBootstrapStake` is `false` (the default), **any nonzero genesis supply is
  rejected outright** - a normal (including mainnet) genesis simply cannot start with a balance.
- If it's `true`, InitGenesis additionally requires:
  - the chain-id to contain `-devnet-`, `-stagenet-` or `-localnet-` (checked against `ctx.ChainID()`
    - never trust a `chain-id` alone to mean "safe", but this at least stops the flag from being
    used silently on anything that looks like `orama-1`);
  - the entire genesis supply to sit in the staking bonded pool, to the last norama - i.e. every
    genesis account was funded with **exactly** its self-bond amount, nothing left idle. Both
    `chain/scripts/localnet/localnet.sh` and `chain/scripts/stagenet/deploy.sh` fund each validator
    with exactly `SELF_BOND` for this reason.

`x/emission` accounts for the resulting supply explicitly rather than treating it as unexplained:
on a *fresh* genesis (detected as `current_epoch <= 1` and `cumulative_minted == 0`), its
`InitGenesis` reads whatever `norama` supply already exists right after `x/bank`'s own genesis has
run, checks it against the premine gate above, and records it as `genesis_supply`. On a genesis
produced by `ExportGenesis` to continue an existing chain (`current_epoch > 1` or
`cumulative_minted > 0`), the given `genesis_supply` is trusted as-is rather than recomputed or
re-gated, since recomputing it from live bank supply at that point would double-count everything
already minted. Either way, `InitGenesis` finishes by checking the full supply invariant (below)
and refuses to start the chain if it doesn't hold.

### Tracking burns from elsewhere in the chain

`x/emission` has no burn path of its own, but other modules do - `x/slashing` burns a validator's
bonded or not-bonded stake on a double-sign or downtime slash. Since `x/emission`'s own
`BeginBlock` has already minted the block's validator share (and updated `cumulative_minted` to
match) by the time `EndBlock` runs, `x/emission`'s `EndBlock` (`Keeper.ReconcileBurns`, run last in
`app.go`'s end-blocker order) compares live bank supply against
`genesis_supply + cumulative_minted - cumulative_burned`: any shortfall it finds must be a burn
that happened elsewhere this block, and gets added to `cumulative_burned`. This keeps the supply
invariant holding without `x/emission` needing a direct dependency on `x/slashing`.

### Queries

`oramad query emission ...`:

| Command | Returns |
|---|---|
| `params` | the genesis-only `epoch_duration`/`min_blocks_per_epoch` |
| `current-epoch` | the live `EpochState`: current epoch, when it started, cumulative minted/burned/genesis supply |
| `schedule-at [epoch]` | that epoch's maximum mint and its four-way split |
| `cumulative-minted` | all-time cumulative minted and burned |
| `supply-cap-so-far [epoch]` | the schedule's maximum possible cumulative supply through that epoch |
| `invariants` | runs the two supply invariant checks below and reports both |

### Invariants

1. **Minted matches the schedule exactly:** `cumulative_minted` must equal exactly
   `CumulativeValidatorMinted` for the epochs already completed - not merely "no more than", since
   `CloseEpoch` mints that exact amount unconditionally every time an epoch closes. Every
   `CeilingRecord` is checked the same way: its four amounts must match `SplitEpochMint` for its
   epoch exactly.
2. **Supply matches minted:** `bank_supply == genesis_supply + cumulative_minted - cumulative_burned`,
   with `cumulative_burned` kept current by `ReconcileBurns` (above).

Both are exposed as `oramad query emission invariants` and as a keeper-level Go function
(`Keeper.CheckSupplyInvariant`) any test can call directly. They are checked on every `InitGenesis`
(a chain refuses to start from a genesis that doesn't satisfy them) and covered by unit tests,
including a 4,000-epoch simulated run (`TestCheckSupplyInvariant_simulated4000EpochRun`) that spans
every halving boundary and well into the permanent tail, and a slashing-burn simulation
(`TestInitGenesis_reconcileBurnsAfterASlashKeepsInvariantHolding`).

## A known infrastructure gotcha: use pebbledb, not goleveldb

`oramad`'s default `app-db-backend` is **`pebbledb`**, not the SDK's own default (`goleveldb`).

This isn't a preference - it works around a real bug. With this exact combination
(`cosmos-sdk` v0.54.4's `store/v2` v2.0.0 + `cosmos-db` v1.1.3's GoLevelDB driver), **every**
height-versioned query (`CacheMultiStoreWithVersion`, the mechanism behind every
`oramad query ...` and gRPC query) fails with `"version does not exist"` - even for the
just-committed latest height, on a node whose blocks are otherwise committing correctly with
changing app hashes. An isolated repro against `store/v2` directly (mount a few `KVStoreKey`s,
commit five versions, query each one back) reproduces the failure against GoLevelDB and succeeds,
with byte-for-byte identical code, against both MemDB and PebbleDB. PebbleDB is pure Go (no cgo
or external RocksDB dependency), so it's the default until this is fixed upstream.

If you ever see `"failed to load state at height N; version does not exist (latest height: N)"`
from `oramad`, check `app-db-backend` in `config/app.toml` before looking anywhere else.

## Running a localnet

```sh
cd chain
make localnet              # builds oramad, starts a fresh 4-validator localnet
make localnet N=6          # or however many validators you want
./scripts/localnet/localnet.sh status   # height + catching-up per node
make localnet-stop         # stop every node, keep chain data
make localnet-clean        # stop every node and delete chain data
```

`scripts/localnet/localnet.sh` builds `oramad` once, `init`s N node homes under
`scripts/localnet/.localnet/nodeN` (gitignored), funds and self-delegates each validator with
exactly its devnet-only bootstrap self-bond (see the premine gate above - nothing is left idle
outside the bonded pool), shortens the emission epoch and sets `allow_bootstrap_stake`
(`EPOCH_DURATION=30s EPOCH_MIN_BLOCKS=5` by default, both overridable env vars; `CHAIN_ID` must
contain `-localnet-`, `-devnet-` or `-stagenet-` or the script refuses to run), patches a finite
consensus block `max_gas` into the generated genesis, collects every node's gentx into one genesis,
distributes it, and starts every node in the background on distinct localhost ports in the
31000-31099 range (P2P/RPC/gRPC/API/Prometheus/pprof, ten ports per node so up to ten validators
fit). Every setup command's output goes to `scripts/localnet/.localnet/setup.log` rather than being
discarded, so a failure can actually be diagnosed.

Useful commands against a running localnet (or any `oramad` node):

```sh
oramad query emission current-epoch    --node tcp://127.0.0.1:31001 --home <node-home>
oramad query emission cumulative-minted --node tcp://127.0.0.1:31001 --home <node-home>
oramad query emission invariants       --node tcp://127.0.0.1:31001 --home <node-home>
```

### `oramad genesis set-emission-params`

Rewrites `x/emission`'s `Params` directly in `genesis.json`:

```sh
oramad genesis set-emission-params \
  --epoch-duration 60s --min-blocks-per-epoch 5 --allow-bootstrap-stake \
  --home <node-home>
```

This only makes sense **before** the chain's first `oramad start`: `x/emission` has no `Msg`
service, so once a chain has produced its first block these parameters can never change again
short of a coordinated hard fork. `--allow-bootstrap-stake` is required on any devnet/localnet
genesis that funds genesis accounts (see the premine gate above) and is also what relaxes the
24h/14,400-block production floors. It is how `chain/scripts/localnet/localnet.sh` and the stagenet
deploy script (`chain/scripts/stagenet/deploy.sh`) shorten the epoch for non-mainnet environments.

Standard `oramad` commands work as on any Cosmos SDK chain, e.g. `oramad init <moniker> --chain-id
<id> --default-denom norama` (the default denom is already `norama` even without the flag - see
`chain/app/config.go` - but the flag still works to override it) and `oramad comet show-node-id`.

## Building

```sh
cd chain
make build   # linux/amd64, darwin/amd64, darwin/arm64, all CGO_ENABLED=0 (no cgo dependency)
make test    # go test ./...    (chain-test is an alias)
make lint    # go vet ./...
```

`make build` passes `-trimpath` and version `ldflags` (from `git describe` and the short commit
hash), so `oramad version --long` reports something meaningful and binaries don't embed local
filesystem paths.

## The stagenet deploy script

`chain/scripts/stagenet/deploy.sh up|status|reset` is an **interim** deployment path for the
project's own stagenet nodes, standing in until plan B2/B3 (the `orama` CLI's global-node role)
exists to manage `oramad` the same way it manages cluster services. Until then, this script drives
its own systemd unit directly. It refuses to run unless `CHAIN_ID` contains `-stagenet-` or
`-devnet-`, builds `oramad` with the same `-trimpath`/version `ldflags` as `make build`, transfers
it gzip-compressed straight into `sudo install` via `/dev/stdin` (no intermediate file of any name,
predictable or not, ever touches the remote disk), and validates every value it reads back from a
remote command (a validator address, a node ID) against a strict format before ever using it to
build another remote command. `reset` fully tears a node down - including one left over from an
aborted `up` (binary and/or state present but no systemd unit yet, or vice versa) - by removing the
unit, the state directory and the binary, each step tolerating the thing it removes already being
absent. Like the localnet script, its genesis flow uses `keyring-backend test` (an unencrypted,
on-disk keyring): appropriate for a devnet/stagenet operator key that holds no real value, never
for anything that does.

## Deviations from the task spec, and why

- **No `x/mint`, `x/gov`, `x/epochs` wiring**, as specified. `x/emission` tracks epochs directly
  in its own `BeginBlock` rather than depending on `x/epochs`, since it only ever needs one epoch
  definition.
- **`x/auth/vesting` is not wired.** Nothing in genutil's gentx flow or this module's genesis
  needs vesting account types; adding the module with nothing that uses it would be dead wiring.
- **No SDK-native simulation framework (`x/simulation`, `make chain-sim`-style fuzzing) is
  wired.** `make chain-sim` is aliased to the same unit tests. The task's "simulate a long run"
  requirements are met by a fast, direct 4,000-epoch keeper-level loop
  (`TestCheckSupplyInvariant_simulated4000EpochRun`) instead, since x/emission's entire state
  machine is a handful of deterministic functions with no message-based fuzz surface yet (there is
  no `Msg` service to fuzz).
- **`app-db-backend` defaults to `pebbledb`, not `goleveldb`** - see the gotcha section above.
  This is a workaround for a real bug in the pinned dependency versions, not a stylistic choice.
- **No `x/bank` send restriction yet.** D7's "mandatory shielded ORAMA" is out of scope for this
  first pass (it depends on `x/shielded`, which does not exist); `docs/SECURITY.md` should note
  this once it exists for the chain, so nobody assumes payments are private today.
- **The validator share still flows through stock `x/distribution`, not a custom reward path.**
  `plans/open-network/track-c-chain.md` C3 eventually wants `x/emission` to pay validators
  directly (bypassing `x/distribution`'s reliance on a live validator set / voting-power history,
  which the stock module also has to skip on the very first block after genesis - see
  `x/distribution/keeper/abci.go`'s `height > 1` check). Building that custom path is out of scope
  for this first pass; today the validator/delegator share is minted into the fee collector and
  paid out however stock `x/distribution` already pays out transaction fees.
- **Two genesis-only settings are patched into `genesis.json` by the localnet/deploy scripts,
  not defaulted by any module.** `x/consensus` has no genesis state of its own in this SDK
  version - the block gas/size limits live in the top-level `consensus` field of `genesis.json`,
  outside every module's `AppModuleBasic.DefaultGenesis` - so the finite block `max_gas` described
  above is set by a small script-level JSON patch rather than in Go.
- **Known, accepted `govulncheck` findings**, none fixable without breaking a pinned dependency
  this task requires: `GO-2024-2584` (a slashing-evasion advisory against `cosmos-sdk`, pinned at
  v0.54.4 per this task's spec, with no fixed version yet); `GO-2026-5932` (`golang.org/x/crypto`'s
  unmaintained `openpgp` package, pulled in transitively by the SDK's keyring code, with no fix
  available); and `GO-2026-6443` (a `grpc-go` server panic on malformed headers, fixed only in an
  unreleased `v1.85.0` dev pseudo-version as of this writing, not a stable tag). `govulncheck`
  found no other reachable vulnerabilities.
