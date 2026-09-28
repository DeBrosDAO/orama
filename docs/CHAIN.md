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
| `staking` | bonds, delegation, unbonding, slashing/jailing bookkeeping (standard SDK module, but its own EndBlocker's CometBFT validator updates are discarded - see "x/power" below) |
| `slashing` | downtime/double-sign penalties, applied to every CometBFT validator including x/power's bootstrap committee (see "x/power") |
| `distribution` | wired for its authority-gated `MsgUpdateParams` plumbing and the SDK's standard proposer-reward accounting, but no longer the path either the emission mint or transaction fees actually take - see "x/power" and "x/fees" below and the Deviations section |
| `consensus` | on-chain consensus parameters |
| `upgrade` | coordinated binary upgrades |
| `genutil` | genesis tooling (its gentx machinery is unused - see "x/power": a bootstrap-committee genesis has no gentxs) |
| `evidence` | equivocation evidence handling |
| `feegrant` | fee sponsorship (still honored by x/fees' own fee ante decorator - see "x/fees") |
| `emission` (custom, `chain/x/emission`) | the halving-with-tail emission schedule |
| `power` (custom, `chain/x/power`) | voting power, the bootstrap committee, and the hand-over factor lambda (C4) |
| `fees` (custom, `chain/x/fees`) | the EIP-1559-style base fee, earnings accounts, and the state-deposit ledger (C2) |

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

A bank send of `norama` from one user account to another is refused, and so is a send from a
registered contract to a user. Module accounts can still move `norama`, and a user can pay a
registered contract. No contract address is registered yet. There is no shielded payment path:
proof verification fails closed until a verifier is linked, so a user cannot pay another user at all.

### Other genesis defaults

- **`x/distribution`'s `community_tax` defaults to `0`** (also via a small `AppModuleBasic`
  override in `chain/app/genesis_overrides.go`), not the SDK's own default: there is no `x/gov` and
  therefore no spend path for a community pool on this chain, so a nonzero tax would just
  accumulate norama nothing can ever claim.
- **The genesis consensus block `max_gas` is set to 100,000,000, not CometBFT's own unlimited
  default.** `x/consensus` has no genesis state of its own in this SDK version - the "consensus"
  block gas/size limits live in the top-level `consensus` field of `genesis.json`, which
  `oramad`'s own module wiring can't default - so `chain/scripts/localnet/localnet.sh` and
  `chain/scripts/stagenet/deploy.sh` both patch it into the generated genesis file directly.
  `localnet.sh` reads `BLOCK_MAX_GAS` (default `100000000`).
- **`app.toml`'s default `minimum-gas-prices` is `0.000001norama`**, not zero. This is a
  separate, per-validator local mempool admission policy (`x/fees/ante.checkValidatorMinGasPrice`,
  standing in for stock `x/auth/ante`'s own unexported `checkTxFeeWithValidatorMinGasPrices`),
  independent of - and on top of, not instead of - `x/fees`' own EIP-1559-style base fee, which is
  the chain-wide, consensus-enforced floor every tx actually pays against (see "`x/fees`" below). It
  only ever runs on `CheckTx`, and **never during simulation** (a `--gas auto` estimate submits a
  placeholder fee/gas before either is known, and must never be rejected by either this policy or
  `x/fees`' own base-fee check - mirroring stock `x/auth/ante`'s own `!simulate` guard).

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
is ever minted.** It is minted into `x/emission`'s own module account and immediately handed to
`x/power.Keeper.DistributeEpochRewards` (`x/emission`'s `PowerKeeper` dependency), which pays it
out on **capped power `P_i`** - not on raw stake, and not through `x/distribution` - split between
each validator's own share (commission, plus any of its own self-delegation's cut) and its
delegators' pro-rata shares, credited straight into every recipient's **earnings account**
(`x/fees`). See "`x/power`: voting power..." below for the full mechanism, including bootstrap
committee force-bonding.

The storage, relay and development shares are **never minted**: they are recorded as a
`CeilingRecord` (per epoch: storage/relay/development ceiling amounts plus the validator amount
actually minted) so that `x/storage`, `x/relay` and a future `x/houses` development spend (none of
which exist yet) can claim them within their own settlement window once they do. `x/emission` only
keeps the trailing 30 epochs of these records (`types.CeilingWindow`) and prunes older ones -
nothing currently reads an expired one, so nothing is lost by pruning it.

Remainders from each share's integer division always fold into the validator share, so the four
shares of any epoch's maximum sum back to that maximum exactly, to the norama.

### Genesis starts at exactly zero supply, and its premine gate

A real network genesis starts at exactly zero balance: `x/emission`'s bootstrap committee
(`x/power`, C4) gives every genesis validator equal voting power without any token needing to
exist first (see "`x/power`" below), so there is no more "at least one self-delegated validator"
requirement to work around. `x/emission`'s `InitGenesis` still actively enforces this with a
premine gate:

- If `Params.AllowBootstrapStake` is `false` (the default), **any nonzero genesis supply is
  rejected outright** - a normal (including mainnet) genesis simply cannot start with a balance.
- If it's `true`, InitGenesis additionally requires:
  - the chain-id to contain `-devnet-`, `-stagenet-` or `-localnet-` (checked against `ctx.ChainID()`
    - never trust a `chain-id` alone to mean "safe", but this at least stops the flag from being
    used silently on anything that looks like `orama-1`);
  - the entire genesis supply to sit in the staking bonded pool, to the last norama.

**`AllowBootstrapStake` is kept only for its other effect: relaxing the
`epoch_duration`/`min_blocks_per_epoch` production floors** (see "Genesis parameters" above) so a
devnet/localnet/stagenet chain can use a short epoch. Neither `chain/scripts/localnet/localnet.sh`
nor `chain/scripts/stagenet/deploy.sh` fund any genesis account any more (there is no more
`SELF_BOND`): every genesis validator is a member of `x/power`'s bootstrap committee instead, so
genesis supply is exactly zero either way, and the premine gate's nonzero-supply branch above is
simply never exercised by either script today. It remains available for a case this design does
not otherwise need: a devnet that wants to test pre-funded genesis accounts for some other reason.

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
bonded or not-bonded stake on a double-sign or downtime slash, and `x/fees`' ante decorator burns
100% of every transaction's base fee (see "`x/fees`" below). Since `x/emission`'s own `BeginBlock`
has already minted the block's validator share (and updated `cumulative_minted` to match) by the
time `EndBlock` runs, `x/emission`'s `EndBlock` (`Keeper.ReconcileBurns`, run last in `app.go`'s
end-blocker order) compares live bank supply against
`genesis_supply + cumulative_minted - cumulative_burned`: any shortfall it finds must be a burn
that happened elsewhere this block, and gets added to `cumulative_burned`. This keeps the supply
invariant holding without `x/emission` needing a direct dependency on `x/slashing` or `x/fees`.

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

## `x/power`: voting power, the bootstrap committee, and the hand-over factor lambda

`chain/x/power` implements plans/open-network/track-c-chain.md's C4: **the CometBFT validator set
comes from x/power, not from stock `x/staking`'s own `EndBlocker`.** `x/staking` still owns bonds,
delegations, unbonding and slashing/jailing exactly as it always has - only the question "what
voting power does CometBFT actually see" is answered elsewhere.

### The bootstrap committee needs no stake

Genesis names a fixed **bootstrap committee** (`Params.MinCommitteeSize` members, `plans/open-network.md`
D16): each member is declared directly in `x/power`'s own genesis state
(`BootstrapMember{operator_address, moniker, consensus_pubkey}` - a raw 32-byte ed25519 key, not an
`Any`, since every consensus key on this chain is ed25519). `InitGenesis`:

1. gives every member an **equal share of genesis voting power** (`types.EqualBootstrapShares`) and
   returns the corresponding `[]abci.ValidatorUpdate` directly - this is the only non-empty
   validator-update list any module's `InitGenesis` produces (`x/staking`'s own genesis validator
   list is empty: **there are no gentxs in this design** - see "Running a localnet" below);
2. creates a real (`Bonded`, zero-token, zero-`DelegatorShares`) `stakingtypes.Validator` record for
   each member directly via the staking keeper's own primitives (`SetValidator`,
   `SetValidatorByConsAddr`, and the `AfterValidatorCreated`/`AfterValidatorBonded` hooks) - **not**
   by declaring it in `x/staking`'s own genesis JSON (which `ValidateGenesis` would reject: a
   `Bonded` validator there must have nonzero `DelegatorShares`). This record is required for
   `x/slashing`'s existing downtime/double-sign machinery to work for a committee member at all:
   its `BeginBlocker` calls `IsValidatorJailed` (hence `GetValidatorByConsAddr`) for every block
   signer, and would otherwise **crash the whole chain** with `"validator does not exist"` the very
   next block, and then again with `"no validator signing info found"` once that gap is closed but
   `AfterValidatorBonded` (which creates the `ValidatorSigningInfo` record) still hasn't run. Both
   failure modes were hit and fixed during this build - see the "Deviations" section.
3. deliberately does **not** call `SetValidatorByPowerIndex` for these records, so they never show
   up in `GetBondedValidatorsByPower` (x/staking's own stake-ranked index) on the strength of their
   bootstrap seat alone - only a real delegation (e.g. from force-bonded rewards, below) ever gives
   one a `C_i` of its own.

An **outsider** (anyone not on the committee) needs no special treatment at all: they create a
validator the normal way (`MsgCreateValidator`), and `x/power` picks them up automatically the next
time it recomputes power.

**Outsiders can bond from earnings (security review B8).** Genesis starts at exactly zero supply
and every protocol payout (emission, fees) lands in a restricted earnings account, never a public
bank balance (see "`x/fees`" below) - so without a way to move earnings into a bondable bank
balance, nobody outside the genesis bootstrap committee could ever accumulate enough of a *public*
balance to self-bond a validator or delegate at all. `x/fees/ante.BondTopUpDecorator` runs after `FeeDecorator` and before signature
verification. It tops up a signer's own shortfall (the declared bond amount minus their current
spendable bank balance) from that same signer's own earnings, before the real
`MsgCreateValidator`/`MsgDelegate` handler runs. The fee has already been settled, so a tx pays its
fee and then bonds from whatever earnings remain. The top-up is in the same ante cache as the rest
of the tx and rolls back with it. It only ever moves an address's own earnings into its own bank
balance; a tx naming someone else's address fails signature verification.

### The power formula

Every block, `x/power`'s `EndBlock` (`Keeper.RunEndBlock` -> `Keeper.computePowers`) computes, for
every validator (every bootstrap committee member, plus every bonded outsider):

```
P_i = (1 - lambda) * B_i + lambda * C_i
```

- **`B_i`** (`types.EqualBootstrapShares`): `1/n` for each of the `n` committee members, `0` for
  everyone else. This is what makes a committee seat "worth" voting power without any stake.
- **`C_i`** (`types.ComputeCappedShares`): each validator's share of total bonded stake, **capped**
  at `Params.CapFractionNormal` (5%) or `Params.CapFractionReduced` (3%, once more than
  `Params.CapStepDownValidatorCount` validators are active), with the excess above the cap
  redistributed proportionally among not-yet-capped validators (an iterative "water-filling" pass,
  ICS power-shaping's algorithm) until it converges - or, if the cap can't be respected by *any*
  distribution (`cap * n < 1`), an **equal fallback** (every validator in that calculation gets
  `1/n`). The calculation sees only tokens the **30-epoch ramp** (`Params.RampEpochs`) has
  admitted. Tokens already admitted stay admitted, and any later increase starts its own ramp at
  zero, so a bond does not move `C_i` in the block it arrives. A validator with nothing admitted
  yet is left out of the equal fallback. A jailed or tombstoned validator is left out of `C_i`
  entirely, so a tombstone drops the seat even when the validator still has bonded tokens. If the
  capped share falls to zero while the validator is still bonded, or the validator leaves the
  bonded set, the ramp state is cleared and the next positive balance starts over. The admitted
  and still-ramping token amounts are part of exported genesis, so a restart continues the same ramp.
- **`lambda`** (`types.ComputeLambda`), the hand-over factor: recomputed once per **closed
  x/emission epoch** (not every block - see "epochs, not calendar time" below),

  ```
  lambda = min(1, max(lambda_prev, bonded / bootstrap_exit_stake, epochs_since_genesis / bootstrap_deadline_epochs))
  ```

  **Monotonic by construction** (`lambda_prev` is one of the three terms maxed together). Two
  further clamps apply after that formula, both only ever lowering the step, never raising it:

  - Until `activeValidatorCount` reaches `HandoverGateThreshold` (twice the number of
    validators it takes to fill 100% of power at the current cap), lambda is held at
    `Params.PreGateLambdaCap` (0.95). That count is every stake-indexed bonded validator plus
    every eligible committee member who is not already in that set, including a committee member
    with no stake. Crossing the threshold once sets `GateSatisfied` permanently.
  - Lambda advances in `EndBlock`, on the validator set this block will publish. The step is
    shortened until both of these shifts from the previous lambda stay within one third (total
    variation): the ramped shares this block publishes, and the unramped capped stake those
    validators already hold. The second measurement is what stops a jump that does not move this
    block's published weights, because a ramp is still zero, from becoming a large shift when the
    ramp later ticks. The deadline's own slope is about `1/365` per epoch, under the bound, so a
    chain that has passed the gate still reaches lambda 1 by `bootstrap_deadline_epochs`. A stake
    threshold that would otherwise jump lambda to 1 in one epoch is spread over the following epochs
    instead.
  - Separately, an increase in the voting power sent to CometBFT from lambda, the ramp, or a new
    bond stays within one third (total variation) in one block. A decrease is applied in that same
    block. Jailing, tombstoning, and unbonding drop that validator's power immediately. When the
    drop is larger than one third, the remaining validators absorb it in the same block. If the
    previous set and the new set do not overlap, the new set is published so the block still has
    validators.

  At `lambda = 1`, `(1 - lambda) = 0`, so **a committee-only member's power falls to exactly zero** -
  "committee seats lapse at lambda = 1" falls directly out of the formula, with no separate code
  path needed. A jailed, tombstoned, or unbonded committee member's bootstrap share is zero
  immediately, while the seat still exists.

`P_i` is mapped to a CometBFT integer power deterministically
(`types.PowerToCometBFT`): `floor(P_i * Params.CometPowerScale)` (scale `1e9` by default),
floored **up** to `1` for any strictly positive share so a validator with real (if tiny) power is
never silently dropped by rounding to zero (CometBFT reads power `0` as "remove this validator").
`RunEndBlock` diffs this block's powers against the last block's (`Keeper.LastPower`,
`Keeper.LastPubKey`) and returns only the `[]abci.ValidatorUpdate` entries that actually changed -
including a zero-power removal update, using the last pubkey on file, for anyone who dropped out of
this block's universe entirely (e.g. an outsider that fully unbonded, or a lapsed committee seat).

**Epochs, not calendar time.** Every one of `x/power`'s own time-based rules - the bootstrap
deadline, the cap's hysteresis window (`Params.CapHysteresisEpochs`: the cap only steps back up
from 3% to 5% after staying below `Params.CapStepUpValidatorCount` for this many *consecutive
closed epochs*), and the 30-epoch ramp - is measured in `x/emission` epoch numbers
(`power/types.EmissionKeeper.CurrentEpoch`), never block height or wall-clock time. This keeps them
scaled consistently with whatever epoch length a given chain is configured with (a 30-epoch ramp is
30 real days on a 24h-epoch mainnet, and 30 x the localnet's much shorter epoch on a localnet) - it
also means `x/power`'s `InitGenesis` must run **after** `x/emission`'s, so it can record
`x/emission`'s current epoch number as its own `genesis_epoch`.

### CometBFT/staking wiring

`app.stakingEndBlockOverride` (`chain/app/staking_override.go`) wraps `staking.NewAppModule(...)`:
its `EndBlock` still calls the real `stakingKeeper.EndBlocker` (maturing unbonding/redelegation
queues and the internal bonded/unbonded status transitions and pool accounting that come with it)
but always returns **no** validator updates, so `x/power`'s `EndBlock` is the only
`module.HasABCIEndBlock` in this app that ever returns a non-empty list - the SDK's module manager
errors if two do. `x/power` runs after `staking` in `app.go`'s end-blocker order, so its power
computation for a block always sees that block's freshest bonded set.

### Rewards, paid on capped power, not stock `x/distribution`

`x/emission`'s epoch-close mint is hand-delivered: after minting the validator/delegator share into
its own account, `x/emission` calls `PowerKeeper.DistributeEpochRewards` (implemented by
`power/keeper.Keeper`), passing itself back as an `EmissionKeeper` (so `x/power` can read the
current epoch without an import cycle). `DistributeEpochRewards` reuses the **exact same**
`computePowers` call with advancement disabled. Rewards therefore use the lambda stored
before this block. `RunEndBlock` may raise lambda afterwards, on the post-transaction
validator set, and that new lambda is what the next block's rewards use. The CometBFT
powers returned from that `EndBlock` can also be lower than these reward shares while an
increase is still inside the one-third per-block limit. Within one block the two can differ, and:

1. pulls the minted coins into `x/power`'s own module account;
2. for each validator with a positive share, floors `share * totalMint` into its amount (the tiny
   flooring remainder across all validators is folded into the first entry, mirroring
   `x/emission`'s own "remainders fold into the validator share" rule);
3. splits that amount into the validator's own share (commission, plus its own self-delegation's
   pro-rata cut) and its delegators' pro-rata shares (`Keeper.distributeValidatorReward`, reading
   `stakingKeeper.GetValidatorDelegations` directly - **not** `x/distribution`'s F1
   historical-rewards machinery);
4. credits every recipient's **earnings account** (`x/fees.Keeper.CreditEarnings`) - never a public
   bank balance.

If a validator has no real record yet, or (the common case for a fresh bootstrap committee member)
has zero `DelegatorShares`, its entire amount is treated as "its own".

### Force-bonding

50% of a bootstrap committee member's own share (`Params.ForceBondFraction`) is **force-bonded**
into its own self-delegation instead of being credited to earnings, until that self-bond reaches
`Params.SelfBondCapMultiplier * Params.MinSelfBond` (default: 2x 1,000 ORAMA)
(`Keeper.forceBondCommitteeReward`): the amount is sent from `x/power`'s module account into the
member's own account, then self-delegated via `stakingKeeper.Delegate` - the same keeper method
`MsgDelegate` uses. Because `InitGenesis` already gave every committee member a real (if
zero-token) validator record, this works from that member's **very first reward**, with no separate
`MsgCreateValidator` step required. A double-sign now has real stake to slash from day one.
The slash fraction is applied to the tokens still bonded plus the initial balance of unbonding
and redelegation entries created at or after the infraction height and not yet mature. Evidence
reports CometBFT power, and that power is not the slash base.

### Queries

`oramad query power ...`: `params`, `bootstrap-committee`, `lambda` (current lambda, cap state and
hysteresis streak), `validator-power [valoper-address]`. `validator-power` returns the last
CometBFT power assigned (`comet_power`). Its `bootstrap_share`, `capped_share` and `power_share`
fields are always zero: filling them would rerun the block's power computation, and the query
server does not hold an `EmissionKeeper`.

`x/power/ante.MinDelegationDecorator` applies every `MsgCreateValidator`, `MsgDelegate`,
`MsgUndelegate`, `MsgBeginRedelegate` and `MsgCancelUnbondingDelegation` in the transaction,
in order, and rejects the transaction if any delegation would sit strictly below
`Params.MinDelegationForRewards` (1 ORAMA by default). A withdrawal that consumes the
delegation's shares, the way `x/staking` caps `ValidateUnbondAmount`, is a full exit and is
allowed. A withdrawal that leaves a share whose truncated token value is still below the
minimum is rejected.
The per-epoch reward walk is still one pass over every delegation; the minimum keeps that walk
from being filled with dust delegations. `x/power/ante.UndelegateGuard` rejects a committee
member's own undelegation or redelegation that would take its self-bond below the amount
force-bonded so far, while lambda is below 1.

### Genesis tooling: `oramad genesis add-bootstrap-validator`

Appends one `BootstrapMember` to `genesis.json`, reading the member's consensus pubkey from one of
three sources (in priority order): `--consensus-pubkey-base64` (a raw base64 key, e.g. extracted
remotely without ever moving a private-key file - see the stagenet script below),
`--consensus-pubkey-file` (a `priv_validator_key.json` to read the *public* half from), or
`--home`'s own `priv_validator_key.json`. `--min-committee-size` overwrites `Params.MinCommitteeSize`
(needed on any devnet/localnet/stagenet chain-id running fewer than the 30-member production floor -
`Keeper.InitGenesis`'s chain-id gate only relaxes that floor, never the check that the *declared*
committee actually has at least `Params.MinCommitteeSize` members). Like
`set-emission-params`, this only makes sense before the chain's first `oramad start`.

## `x/fees`: base fee, earnings accounts, and state deposits

`chain/x/fees` implements plans/open-network/track-c-chain.md's C2.

### The base fee (EIP-1559-style)

`Keeper.AdvanceBaseFee`, run in `x/fees`' own `EndBlock`, adjusts the per-gas-unit base fee toward
`Params.TargetBlockGasFraction` (50%) fullness, by at most `Params.MaxBaseFeeChangeFraction` (12.5%)
per block, floored at `Params.MinBaseFee` (`types.NextBaseFee` - a pure function, unit-tested with
hardcoded expected values). Fullness is `ctx.BlockGasMeter().GasConsumed()` against
`ctx.ConsensusParams().Block.MaxGas`; if no block gas limit is configured (CometBFT's convention:
`<= 0` means unlimited, and a test genesis with no consensus params at all reads as `nil`), there is
no meaningful fullness to react to and the base fee is left unchanged.

### The fee ante decorator: burn the base fee, tip the proposer, fall back to earnings

`x/fees/ante.FeeDecorator` (`chain/app/app.go`'s `setAnteHandler`) replaces stock
`x/auth/ante`'s `NewDeductFeeDecorator` in this app's hand-assembled ante chain (every other
decorator is exactly `x/auth/ante`'s own). It requires a tx's declared fee to be at least
`base_fee * gas_limit`; anything above that is a **tip**. `Keeper.SettleFee`:

1. pays the **tip from the payer's bank balance only** - a tip may never draw on earnings (security
   review M4: a tip is never drawn from earnings; earnings pay the base fee, the signer's own
   bond, and the signer's own deposits), so a tip larger than
   the payer's spendable bank balance fails the tx outright;
2. pays as much of the **base fee** as possible from whatever bank balance is left after the tip,
   then falls back to the payer's **earnings account** for any remaining shortfall
   (`Keeper.DebitEarningsUpTo`) - but **only when the payer is paying with their own funds**; a fee
   granter sponsoring the tx may never draw on the signer's (or its own) earnings for the base fee
   either (security review, non-blocking "fee granter") - and fails the tx only if neither bank nor
   earnings (where allowed) cover it;
3. **burns the base-fee portion in full** (`BankKeeper.BurnCoins`);
4. **credits the tip to the current block proposer's earnings account** - the proposer is resolved
   from `ctx.BlockHeader().ProposerAddress` via a narrow local `StakingKeeper` interface
   (`GetValidatorByConsAddr`). If that ever fails to resolve (a malformed or missing header field -
   not expected in normal operation), the decorator folds the tip into the base fee and **burns the
   whole fee** instead of crediting any account (security review, non-blocking "unresolvable
   proposer": crediting `x/fees`' own module address would create an unattributed balance nobody can
   ever spend from) - so a broken header field can never block every transaction on the chain.

A `MsgCreateValidator`/`MsgDelegate` whose declared bond amount exceeds the signer's own spendable
bank balance is topped up from that same signer's earnings by `x/fees/ante.BondTopUpDecorator`
(security review B8; see "Outsiders can bond from earnings" under "`x/power`" above). That
decorator runs after this one, so the fee is settled before the bond is funded.

Feegrant sponsorship still works exactly as it does with the stock decorator (a fee granter, if one
is set and authorizes it, pays instead of the signer) - except that, as above, a granter-sponsored
tx's base fee may only be paid from bank, never from anyone's earnings.

**Simulation** (`--gas auto` and similar): before either the local minimum-gas-price policy or the
base-fee check above (both of which require a real, already-known fee/gas that a simulation is
trying to discover), `FeeDecorator` runs the same settlement logic on a branched, discarded context
(`ctx.CacheContext()`) purely so its gas consumption is reflected in the estimate, swallowing any
error (insufficient funds, an unresolvable payer/proposer) since the guessed fee/gas is not final
yet - mirroring stock `x/auth/ante`'s own `!simulate` guard around its equivalent fee check.

### Earnings accounts

`x/fees.Keeper.Earnings` is a `collections.Map[address, math.Int]`, backed by `x/fees`' own module
account: `CreditEarnings(ctx, senderModule, addr, coin)` moves `coin` from `senderModule`'s account
into `x/fees`' and increments the ledger; internal callers that already hold the coins in `x/fees`'
own account (the ante decorator's tip, a released deposit's refund) skip the transfer and just
touch the ledger. `SettleFee` also adds the fee to three counters: collected, burned, and distributed (the tip).
`Keeper.CheckInvariants` checks three equalities, exposed as `oramad query fees invariants`:

- sum of earnings balances == the `fees` module account balance;
- sum of open deposits == the `fees_deposits` module account balance;
- burned + distributed == collected.

A balance debited back to zero is removed from the earnings map rather than stored as a zero row.

Earnings today pay **tx fees** (the ante decorator), fund the signer's own **bond** (the bond
top-up decorator) and fund the signer's own **state deposits** (`LockDeposit` takes the bank
balance first and the shortfall from that same owner's earnings). `MsgShieldEarnings` is not
implemented yet. It depends on `x/shielded` (C12). There is no message that sends earnings to
another address.

### State deposits (implemented, not yet consumed)

`Keeper.LockDeposit`/`Keeper.ReleaseDeposit` implement the generic lock/refund/burn API C2 asks
for. `LockDeposit` takes the owner's spendable bank balance first and any shortfall from that
owner's earnings. The coins sit in a **second, separate module account** (`fees_deposits`, distinct from `fees` itself
- so "the deposit module balance == open deposits" stays an independently checkable invariant from
"sum of earnings balances == the earnings module balance"). `ReleaseDeposit` refunds
`Params.DepositRefundFraction` (99%) to the owner's earnings and burns the rest
(`types.SplitDeposit`, exact split, remainder to the burn side). **No module calls this API yet**:
`x/token`, `x/cnft`, `x/market`, `x/nodes` and CosmWasm storage metering (none of which exist yet)
are its intended callers.

### Queries

`oramad query fees ...`: `params`, `base-fee`, `earnings [address]`, `deposit [id]`, `invariants`.

`base-fee`'s `QueryBaseFeeResponse.base_fee` is a `cosmossdk.io/math.Int` (a **whole-number** count
of norama per gas unit, e.g. `"1000"` - never a decimal string like `"1000.0"` or `"0.001"`),
serialized as a JSON/proto string the way every `math.Int` field is. A client (including RootWallet)
must parse it as an integer, not a decimal: `x/fees.Keeper.BaseFee` is stored and adjusted as an
integer end to end (`types.NextBaseFee` truncates the EIP-1559 update to a whole norama and, when
that truncation would erase a real move, steps by one norama, floored at `Params.MinBaseFee` - see
"The base fee (EIP-1559-style)" above), never as a
`math.LegacyDec`.

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
`scripts/localnet/.localnet/nodeN` (gitignored), then builds genesis **at exactly zero norama
supply**: every node becomes an `x/power` bootstrap committee member
(`oramad genesis add-bootstrap-validator`, once per node, each reading that node's own
`priv_validator_key.json` - see "`x/power`" above), with `--min-committee-size` set to `N` so a
small localnet doesn't need the 30-member production floor. No genesis account is ever funded and
there are no gentxs at all. It also shortens the emission epoch and sets `allow_bootstrap_stake`
(`EPOCH_DURATION=30s EPOCH_MIN_BLOCKS=5` by default, both overridable env vars; `CHAIN_ID` must
contain `-localnet-`, `-devnet-` or `-stagenet-` or the script refuses to run - `allow_bootstrap_stake`
here relaxes only the epoch-duration/min-blocks floors, not any premine gate, since supply stays at
zero), patches a finite consensus block `max_gas` (`BLOCK_MAX_GAS`, default 100,000,000) into the generated genesis, distributes it, and
starts every node in the background on distinct localhost ports in the 31000-31099 range
(P2P/RPC/gRPC/API/Prometheus/pprof, ten ports per node so up to ten validators fit). Every setup
command's output goes to `scripts/localnet/.localnet/setup.log` rather than being discarded, so a
failure can actually be diagnosed.

Useful commands against a running localnet (or any `oramad` node):

```sh
oramad query emission current-epoch     --node tcp://127.0.0.1:31001 --home <node-home>
oramad query emission cumulative-minted --node tcp://127.0.0.1:31001 --home <node-home>
oramad query emission invariants        --node tcp://127.0.0.1:31001 --home <node-home>
oramad query power lambda               --node tcp://127.0.0.1:31001 --home <node-home>
oramad query power bootstrap-committee  --node tcp://127.0.0.1:31001 --home <node-home>
oramad query fees base-fee              --node tcp://127.0.0.1:31001 --home <node-home>
oramad query fees earnings <address>    --node tcp://127.0.0.1:31001 --home <node-home>
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
short of a coordinated hard fork. `--allow-bootstrap-stake` relaxes the 24h/14,400-block production
floors (its only remaining effect - see "Genesis starts at exactly zero supply" above); it is how
`chain/scripts/localnet/localnet.sh` and the stagenet deploy script
(`chain/scripts/stagenet/deploy.sh`) shorten the epoch for non-mainnet environments.

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
remote command (a validator address, a node ID, a consensus pubkey) against a strict format before
ever using it to build another remote command. Genesis is built the same zero-supply,
bootstrap-committee way the localnet script uses: each node's consensus pubkey is extracted
remotely with `oramad comet show-validator | python3 -c '...["key"]'` (reading only the *public*
half of `priv_validator_key.json` - its private key material never leaves the node, or touches this
script's own disk) and fed to `genesis add-bootstrap-validator --consensus-pubkey-base64` on the
first node. `reset` fully tears a node down - including one left over from an
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
- **`x/bank` refuses user-to-user and contract-to-user `norama` sends.** A user can pay a
  registered contract. None are registered yet. The shielded pool rules live in
  `chain/x/shielded`. No proof verifier is linked, so a shielded bundle is not accepted either.
  Payments are not private, and they are not possible between users.
- **The validator share now flows through `x/power`, not stock `x/distribution` - resolving a
  deviation from the first pass.** `x/emission` hands its epoch mint to
  `PowerKeeper.DistributeEpochRewards`, which pays it out on capped power `P_i`, split between each
  validator's own share and its delegators pro rata, credited to earnings accounts - see
  "`x/power`" and "`x/fees`" above.
- **Two `x/slashing`/`x/staking` chain-halting bugs were found and fixed while building the
  bootstrap committee, both from giving a committee member CometBFT voting power without going
  through the normal `MsgCreateValidator` path.** (1) `x/slashing`'s `BeginBlocker` calls
  `IsValidatorJailed` (hence `GetValidatorByConsAddr`) for every block signer; a committee member
  with no `stakingtypes.Validator` record at all crashed the chain with `"validator does not
  exist"` on the very next block after genesis. (2) Once fixed by creating that record directly
  (`SetValidator`/`SetValidatorByConsAddr`) and firing `AfterValidatorCreated`, the chain still
  crashed one block later with `"no validator signing info found"`: that hook only registers the
  consensus pubkey, and `x/slashing`'s `ValidatorSigningInfo` record is actually created by
  `AfterValidatorBonded`, which a validator whose `Status` was set directly to `Bonded` (rather than
  transitioning there through a real bonding flow) never received. `x/power`'s `InitGenesis` now
  fires both hooks explicitly. Both were caught by actually running a localnet through several
  epochs, not by unit tests alone - see "Verification" in the change that added `x/power`.
- **Committee members are given a validator record with zero tokens and zero `DelegatorShares` at
  genesis, bypassing `x/staking`'s own `ValidateGenesis` (which forbids a zero-share `Bonded`
  validator).** This is safe specifically because the record is created via direct runtime keeper
  calls (`SetValidator`, ...) from `x/power`'s `InitGenesis`, which runs *after* `x/staking`'s own
  `InitGenesis` (an empty declared validator list) rather than by declaring it in `x/staking`'s own
  genesis JSON, so `ValidateGenesis`'s check (which only inspects that JSON-declared list) never
  sees it. `oramad genesis validate` still passes on a bootstrap-committee genesis for the same
  reason.
- **A committee member's consensus pubkey cannot currently be rotated while keeping the same
  operator address.** `x/power` prefers a real staking validator's own `ConsPubKey()` once one
  exists for an operator, falling back to the genesis `BootstrapMember.ConsensusPubkey` only when it
  doesn't; there is no logic to detect or handle the underlying pubkey actually *changing* for the
  same operator later (a validator update keyed by operator address just gets overwritten, without
  ever emitting the old pubkey's removal). Not a regression from any prior design - stock Cosmos SDK
  chains don't support consensus-key rotation either without a dedicated module - but worth noting
  as a gap for a future pass.
- **The fee-free registration quota (C2, "bootstrap only")** is not implemented: it applies to
  `MsgRegisterOperator`/`MsgRegisterNode`, neither of which exists yet (`x/nodes`, C6).
  `MsgShieldEarnings` is likewise not implemented, per C2's own instruction ("NOT to be added yet" -
  pending `x/shielded`, C12).
- **`x/fees`' state-deposit ledger (`LockDeposit`/`ReleaseDeposit`) has no caller yet.** It is fully
  implemented and tested; `x/token`, `x/cnft`, `x/market`, `x/nodes` and CosmWasm storage metering
  (its intended callers) don't exist yet.
- **`x/power.DistributeEpochRewards` iterates every delegation of every validator once per closed
  epoch** (`Keeper.distributeValidatorReward`), rather than using `x/distribution`'s O(1)-per-block
  F1 historical-rewards accumulator. New delegations below `Params.MinDelegationForRewards` are
  rejected. The check runs on the whole transaction in order, so splitting the withdrawal
  across messages, cancelling an unbonding, or leaving a truncated share remainder cannot
  park a delegation between zero and that minimum. It is still O(delegations) per epoch
  (daily by default). An F1-style
  accumulator paid into earnings is future work if delegator counts make the walk matter.
- **`oramad query power validator-power`'s `bootstrap_share`/`capped_share`/`power_share` fields are
  always zero.** Recomputing them would need the same full block-universe computation `EndBlock`
  does, which needs an `EmissionKeeper` the query server doesn't hold; only the last CometBFT power
  actually assigned (`comet_power`) is populated. A future pass can wire that dependency through if
  the fractional breakdown is needed over gRPC.
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
