# The Orama L1 chain

**Status: first code, devnet/localnet only.** Nothing here has run on a public network. This
document describes only what `chain/` actually does today. The design that is still
unbuilt is in `plans/open-network.md` and `plans/open-network/track-c-chain.md`.

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

**Wired** in addition to the table: `x/token` (factory denoms), `x/archive` (history registry),
and `x/nodes` (operator and global-node registry). Its end block pays matured role-bond
unbondings.
`x/storage` and `x/relay` are registered. A node with no literal-IP endpoint or no declared ASN
cannot take a protocol-deal slot, and an operator without both cannot be eligible for the operator
house (see "Node network identity" under `x/nodes`). A relay payout cannot exceed that epoch's relay ceiling
minus what was already minted. `chain/x/inclusion` orders its own transaction
bytes. Those bytes are not SDK transactions, and this CometBFT ProcessProposal
commit does not carry vote extensions, so `oramad` does not put them in a block.
`orama storage grant` builds a deal allowance that is not SDK authz. It caps spend,
piece size, duration, and replica count. `orama storage revoke` removes it.
`orama storage create` opens a PRIVATE or PUBLIC_PIN deal from piece roots the
caller already has. It does not encrypt the bytes and it does not upload them.
`orama storage extend` adds epochs. `orama storage accept` and
`orama storage decline` answer one assigned slot. The signer of those two is
the node's hot key. `orama storage prove` builds `MsgSubmitProofs` from a JSON
file of proofs. It does not choose the challenged leaf. Without `--node`, each
of these commands prints the sign document and does not submit it.
Also unwired: `x/gov`,
`x/mint` (replaced by `x/emission`), `x/authz`, `x/epochs` (x/emission tracks its own epochs),
`x/group`, `x/nft`, `x/circuit`, `x/crisis`, IBC, and an EVM. `x/auth/vesting` is not wired.
wasmd's `x/wasm` is wired when the binary is built with cgo and libwasmvm. A `-tags nowasm`
build does not link it and refuses a genesis that contains it. `x/wasmpolicy` is always wired:
upload is closed until `upload_sunset_height`, and a contract cannot bank-send norama to a user.

**`x/houses` is registered, and it is not the SDK `x/gov` authority.** Stock modules still use
`app.UnreachableAuthority()`. Every module that the upstream SDK expects to be governed by `x/gov` (upgrade,
consensus params, bank, staking, slashing, distribution) is instead given an "authority" address
that is the hash of a dedicated, never-registered module name, `"orama/no-authority"` -
`app.UnreachableAuthority()` in `chain/app/app.go`. That name is deliberate: using `"gov"` instead
would mean that simply registering a standard `x/gov` module in some future release silently hands
it control of every authority-gated message on the chain today, with no explicit migration step.
Because no module by this name is ever registered, no private key or module account can ever
produce a valid signature for it, so every authority-gated message on this chain
(`MsgSoftwareUpgrade`, every module's `MsgUpdateParams`, ...) is permanently unreachable until a
future release deliberately migrates that authority to `x/houses`. On a node running
this binary, the only way to change the chain's behavior is still a coordinated hard fork (a new
binary, a halt height, and validators choosing to run it) - never an on-chain vote or an admin key.
`TestUnreachableAuthority_rejectsEveryAuthorityGatedMsg` in `chain/app/app_test.go` proves bank,
staking, distribution, consensus and upgrade's authority-gated messages all reject a signer that
isn't this address. What `x/houses` does is described under "`x/houses`" below. Its `EndBlock` runs.

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
development (`chain/x/emission/types/split.go`). **`closeEpoch` mints only the 60%
validator/delegator share.** It is minted into `x/emission`'s own module account and immediately
handed to `x/power.Keeper.DistributeEpochRewards` (`x/emission`'s `PowerKeeper` dependency), which
pays it out on **capped power `P_i`** - not on raw stake, and not through `x/distribution` - split
between each validator's own share (commission, plus any of its own self-delegation's cut) and its
delegators' pro-rata shares, credited straight into every recipient's **earnings account**
(`x/fees`). See "`x/power`: voting power..." below for the full mechanism, including bootstrap
committee force-bonding.

The storage and relay shares are **never minted**: they are recorded on a `CeilingRecord`. The
development share is also recorded there and stays unminted at epoch close.
`Keeper.MintDevelopmentSpend` is the only later mint, and only for an amount that is positive and
no greater than that epoch's 5% development ceiling minus what this method has already minted for
the same epoch. It refuses every other amount, mints into the emission module account, and does
not pay a recipient itself. `x/houses` calls it from `EndBlock` after a passed spend.
The structural tier stays closed until its opening rules hold, so a fresh chain does not
take this path. Each `CeilingRecord` also stores `development_minted`. The
all-time total is `cumulative_development_minted`, which is **not** part of `cumulative_minted`
(that field stays the validator share, so the schedule equality check is unchanged). Supply is
`genesis_supply + cumulative_minted + cumulative_development_minted + cumulative_service_minted - cumulative_burned`.
`x/emission` keeps the trailing 30 epochs of ceiling records (`types.CeilingWindow`) and prunes
older ones. Pruning does not reduce `cumulative_development_minted`. Nothing reads an expired
ceiling, so a spend against a pruned epoch is refused.

Storage and relay payments are the other mints, and `x/emission` makes them too.
`Keeper.MintStorageService` and `Keeper.MintRelayReward` each mint at most the epoch's storage
or relay ceiling minus what was already minted against it (`storage_minted`, `relay_minted`).
`x/storage` calls `MintStorageService` once when an epoch closes, for that epoch's whole
payment, while its ceiling record is certain to exist. It holds the coins in the storage module
account, and settlement pays each queued item from that reserve, so a queue that lags past the
30-epoch ceiling window still settles. A proof is paid only if the node that proved it still
holds the slot at close. The storage invariant `subsidy_within_ceiling` checks that the
account holds exactly what the queue still owes. Queue items written by an older binary were
not reserved, so this needs a new genesis or an empty settlement queue at the switch.
`MintRelayReward` is called by `x/relay`. They mint into the emission module
account, move the coins to the paying module, and add the amount to `cumulative_service_minted`.
`x/emission` is the only module account that can mint norama. `x/token` holds Minter for the
denoms it creates, but its bank keeper refuses a norama mint (`app/mint_policy.go`).

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
`genesis_supply + cumulative_minted + cumulative_development_minted + cumulative_service_minted - cumulative_burned`: any shortfall it finds must be a burn
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
2. **Supply matches minted:** `bank_supply == genesis_supply + cumulative_minted + cumulative_development_minted + cumulative_service_minted - cumulative_burned`,
   with `cumulative_burned` kept current by `ReconcileBurns` (above). `cumulative_development_minted`
   is zero until `MintDevelopmentSpend` runs; `cumulative_service_minted` is zero until a storage
   or relay payment is minted.

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
`MsgCreateValidator`/`MsgDelegate` handler - or the x/nodes `MsgBondNode` handler, keyed on the
message's `operator` - runs. The fee has already been settled, so a tx pays its
fee and then bonds from whatever earnings remain. All bond messages from one signer in a tx are
summed first, so two `MsgBondNode` (or a delegate plus a node bond) are both funded. The top-up
happens only when the signer's earnings cover the whole shortfall; otherwise nothing is debited and
the message fails with the ordinary insufficient-funds error (ante writes survive a failed message,
so a partial top-up would only move earnings into the bank for nothing). It only ever moves an
address's own earnings into its own bank balance; a tx naming someone else's address fails
signature verification.

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
top-up decorator, for staking messages and `x/nodes` `MsgBondNode`) and fund the signer's own **state deposits** (`LockDeposit` takes the bank
balance first and the shortfall from that same owner's earnings). `MsgShieldEarnings` is not
implemented yet. It depends on `x/shielded` (C12). The only message that moves earnings to another
address is `x/nodes` `MsgFundHotKey`: it moves an operator's own earnings to the earnings balance of
the hot key registered on the operator's own node (see `x/nodes`). It is a ledger move between two
earnings entries (`Keeper.MoveEarnings`); no coins leave the `fees` module account and nothing
reaches a bank balance, so the earnings invariant is untouched. The hot key then pays base fees
from that balance through the ante decorator; a tip still needs a bank balance.

### State deposits (implemented, not yet consumed)

`Keeper.LockDeposit`/`Keeper.ReleaseDeposit` implement the generic lock/refund/burn API C2 asks
for. `LockDeposit` takes the owner's spendable bank balance first and any shortfall from that
owner's earnings. The coins sit in a **second, separate module account** (`fees_deposits`, distinct from `fees` itself
- so "the deposit module balance == open deposits" stays an independently checkable invariant from
"sum of earnings balances == the earnings module balance"). `ReleaseDeposit` refunds
`Params.DepositRefundFraction` (99%) to the owner's earnings and burns the rest
(`types.SplitDeposit`, exact split, remainder to the burn side). `x/token` calls `LockDeposit` /
`ReleaseDeposit` and is registered, so a token
create locks a metadata deposit. `x/nodes` calls the same interface and is registered.
`x/cnft` locks a tree deposit through the same interface and is registered. `x/market` is
registered and does not lock a listing deposit. A sale credits earnings.
`x/storage` is registered. Its deal escrow is its own module account,
not this deposit ledger. The per-byte contract deposit meter is not hooked into wasmd's store.

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

## `x/token`: factory denoms

`chain/x/token` is the tokenfactory-style module from
plans/open-network/track-c-chain.md C10. It is registered in `chain/app/app.go`. The module
account may mint and burn. Creation-fee burns and metadata deposits go through `x/fees`.
The transfer hook wired today does nothing.

Denoms are `factory/{creator bech32}/{subdenom}`. Balances live in x/bank, not in this module.
`Token.issued` is the module's running total. `CheckInvariants` requires that total to equal
bank supply of the denom, and requires each metadata deposit to equal what
`FeesKeeper.GetDeposit` returns for id `token/{denom}`, owned by the creator.

`MsgCreateToken` burns `Params.CreationFee` norama. The genesis default is the named constant
`types.CreationFee`: 10 ORAMA, `10_000_000_000` norama. That figure does not track a dollar
price. plans/open-network.md P4 only says about $10–25 equivalent and does not fix a norama
amount. The same message locks a metadata deposit through `FeesKeeper.LockDeposit`:
`Params.DepositPerByte` norama for each byte of subdenom, name, symbol, and description. The
genesis default `types.DepositPerByte` is 68359, from P3's ≈0.07 ORAMA/KB by integer division
(`0.07 * 10^9 / 1024`). It is not a price oracle. `MsgDeleteToken` (creator only, and only when
issued supply and bank supply are both zero) calls `FeesKeeper.ReleaseDeposit`. x/token does not
compute the split; x/fees' genesis params refund 99% to the creator's earnings and burn 1%.

Capabilities are chosen in `MsgCreateToken` and can only be renounced afterwards. There is no
message that adds or reassigns one, and no module admin key:

- mint authority stays with the creator;
- freeze lets the creator freeze or unfreeze accounts (a freeze blocks send, receive, mint-to, and burn);
- permanent delegate may transfer the token out of any holder;
- transfer fee is a basis-point share of the token itself, burned on `MsgTransfer`;
- non-transferable blocks every transfer, including the permanent delegate;
- pause blocks transfers only (mint and burn still work) until the creator unpauses;
- transfer hook calls a Go `TransferHook`, not CosmWasm, under a gas meter capped at 100_000.
  Asking for more gas fails the transfer and moves nothing.

Renouncing freeze or pause does not clear an existing freeze or the paused flag. Only the
permanent delegate can renounce that capability. `MsgSetShieldable` may be signed by anyone. It
sets a one-way flag and is refused while freeze, permanent delegate, or pause is still held.
There is no shielded transfer here, and no on-chain verified registry. A shieldable token still
moves by the public `MsgTransfer` bank send.

`MsgTransfer` does that send after the pause, non-transferable, signer, and freeze checks.
`from` must be the signer unless the signer is the current permanent delegate. Because the
module is not wired, `x/bank` `MsgSend` does not run these checks. The `token` module account
would also need minter and burner permissions before a real bank would accept mint, burn, or
the creation-fee burn. Neither is granted in `app.go`.

State is one record per token plus one key per frozen account. A transfer is a single bank send,
plus a burn when a fee is due. The invariant walks every token, the same shape as x/fees'
deposit walk.

## `x/houses`: two-house governance

`chain/x/houses` implements plans/open-network/track-c-chain.md C5 and decisions D17 and D18.
It is registered. `EndBlock` closes elapsed votes. An operator is eligible only with a /16 network
and an ASN, which `chain/app/houses_view.go` reads from `x/nodes` (see "Node network identity"):
the identity of the operator's lowest-id ACTIVE node that has both. An operator with no such node
is skipped.

Nobody governs during bootstrap. The parameter tier opens only when bonded stake is at least
`bootstrap_exit_stake` (genesis default 271000 ORAMA) **or** lambda is at least 1, **and** the
eligible operator house has at least 21 members. The structural tier and development spends open
only when lambda is at least 1 **and** those 21 sit across at least 7 distinct /16 networks and 5
ASNs. Lambda comes from a `PowerKeeper` interface; this module does not import `x/power` or
`x/nodes`. Before a tier is open, its proposals are rejected and parameters stay at genesis.

The token house is stake-weighted. Quorum is a fraction of bonded stake (default 0.4, bounds
0.334–0.667). A pass also needs yes > no and yes at least half of the weight that voted. Stake
that inherits a validator's vote is capped at 3% of bonded stake per validator. A direct vote
removes that delegator's stake from the validator's bucket and counts the full amount.

The operator house is one vote per identity with at least 90 days of proven service and a locked
`house_bond` (default 1000 ORAMA). At most 3 identities per /16 and 5 per ASN are eligible
(genesis defaults; a parameter vote can move the caps inside 1–21). Earlier locks win ties.
A second, different vote on the same proposal burns the bond and does not change the first vote.
That vote no longer counts, even if the operator locks a new bond. The bond cannot be unlocked
while the operator has a vote on a proposal still in voting or the veto window.

Parameter proposals pass in the token house, then the operator house has 7 days to veto with NO
votes from at least 30% of the eligible set. Structural proposals need a token-house pass and a
yes from more than half of the eligible operator house. There is no expedited status and no
expedited message. After passage, execution waits 14 days for parameters, 60 days for upgrades
and the other structural actions, and 7 days for spends.

A passed spend calls `x/emission.Keeper.MintDevelopmentSpend` and then
`EarningsKeeper.CreditEarnings` (`x/fees`; unit tests use a fake). If the
mint refuses the amount, the proposal is marked failed and nothing is credited. Other structural
decisions are stored on `Enacted` only: a software-upgrade name and height, an emission split
within ±10 points of 60/25/10/5, a one-way M activation and an `m_max` in [0.75, 1.25], and
relay-reporter / code-upload / adapter allow-lists. `x/emission`'s schedule, tail and mint math
do not read the split. `x/power` does not read M. No allow-list is enforced outside this module.

Ossified rules have no message and no field a message can set: the emission schedule and tail,
the burn rule, privacy-by-default, and the absence of freeze, blacklist, halt, pause, circuit
breaker, multisig or authority. `bootstrap_exit_stake` is genesis-only. `TestNoMessageReachesAnOssifiedField`
walks every `sdk.Msg` and rejects a field that is not on the allow-list.

The bond invariant is: sum of locked house bonds equals the `houses` module account balance.
Queries, once the module is wired, are `oramad query houses params|proposal|tiers|invariants`.

## `x/nodes`: operators, global nodes, bonds, and an optional cluster registry

`chain/x/nodes` implements plans/open-network/track-c-chain.md C6. It is registered in
`chain/app/app.go`. EndBlock pays matured role-bond unbondings and records service days.
There is no authority address, no pause, and no message that changes parameters after genesis
(plans/open-network.md D18).

Bonds and unbonding escrow sit in the `nodes` module account. The bank genesis must already hold
`bonds + unbonding` norama there; `InitGenesis` checks that and does not mint.

### Records

- **Operator.** An account that may own nodes and cluster rows (`MsgRegisterOperator`).
- **Node.** Roles `VALIDATOR`, `STORAGE`, `RELAY`, `EXIT`, `DIRAUTH`, `ARCHIVER` (fixed at
  registration); a hot key that must differ from the operator; service-key bindings; public
  endpoints; an operator-declared `asn` (0 means undeclared); per-role bonds; declared and reserved STORAGE capacity; status `registered`,
  `active`, `jailed`, `retired`, or `tombstoned`. A role is active only while the node is
  `active` and that role's bond is at least `min_bond`.
- **Cluster.** An optional public row: base domain, public endpoints, metadata URI. No member
  list, no tenant list, no secrets. Registering one does not join any node to a cluster (D1).
  A cluster is not required to run a node, and retiring one does not change any node.
  `orama cluster register-onchain` builds that message as a SIGN_MODE_DIRECT sign
  document. With `--node` it asks the RootWallet agent to sign the document and
  broadcasts the transaction to that REST API. Without `--node` it prints the
  sign document and does not submit it.
- **Unbonding queue, revoked pubkeys, service days, and a STORAGE free-capacity index.** The
  index key is `(class, operator, node id)`. Class `0` is unused; any free byte count uses
  `bits.Len64(free)`. Jailed, retired, and tombstoned nodes are not indexed.

### Messages

Every message is signed by the owning operator. `MsgUpdateNode`, `MsgBondNode`, `MsgUnbondNode`,
`MsgDeclareCapacity`, `MsgFundHotKey`, `MsgRetireNode`, and the cluster update/retire messages
fail when the signer is not that operator.

`MsgRegisterNode` verifies each binding over the ASCII string
`orama-global-bind-v1|chain-id|operator|service|hex(pubkey)`. secp256k1 uses the Cosmos SHA-256
digest. ed25519 verifies a normal 64-byte signature over the 32-byte public key, which is what
Tor's expanded ed25519 secret produces. `orama global bind` signs that statement from a local
key file (secp256k1, an ed25519 seed, Tor's 64-byte expanded secret, or a CometBFT `priv_key`
JSON) and prints the public key and signature. `orama global register` checks those files and
builds `MsgRegisterNode` as a SIGN_MODE_DIRECT sign document. With `--node` it asks the
RootWallet agent to sign and broadcasts the transaction. Without `--node` it prints the
document and does not submit it. A service pubkey is unique on the network. Retiring a
node, rotating a binding (`MsgUpdateNode` replaces the whole set when one is provided), or
tombstoning (keeper `Tombstone`, not a message) records the old pubkey so it cannot be bound
again.

`MsgBondNode` moves norama from the operator's bank balance into the module account. The
operator's own earnings fund any shortfall first (`x/fees/ante.BondTopUpDecorator`, see
"Outsiders can bond from earnings"), so an operator whose payouts sit in earnings can bond a node
with a zero bank balance. The top-up happens only when the message names an existing node whose
operator is the signer; otherwise nothing moves and the bond handler rejects the message, so a
failed `MsgBondNode` cannot leave the signer's earnings sitting in its bank balance.
`MsgRegisterNode` and the cluster messages move no bond.
`orama global bond` and `orama global unbond` build those messages. With `--node` they
sign through the RootWallet agent and broadcast; without it they print the sign document.
`orama global capacity` declares storage bytes, `orama global retire` retires a node,
and `orama cluster retire-onchain` retires the public cluster row. Same signing rule.
`MsgUnbondNode` moves it onto the queue. `EndBlock` pays an entry back to that operator when
`completion_unix <=` the block time. The delay is `Params.UnbondingSeconds` (genesis default
21 days, the same period D16 and C4 state for stake; C6 does not give a second duration).
Unbonding that would leave declared STORAGE capacity above the remaining bond's backing is
rejected.

`MsgDeclareCapacity` sets STORAGE `declared_capacity_bytes`. Backing is
`bond * 1 GiB / bond_per_gib` when the STORAGE bond is positive, and
`probation_capacity_bytes` when it is zero (C2's probation cap). `Slash` burns the same
fraction of the role's bond and of that role's unbonding entries. It clamps declared capacity
down to the new backing, and it fails without writing if reserved bytes would then exceed that
backing. `Jail` / `Unjail` are keeper methods: a jailed node is not active and leaves the
capacity index. `ReserveCapacity` / `ReleaseCapacity` move free capacity for a later storage
module. `CreditRoleBond` increases a role bond only when the module account already holds the
coins; it does not mint.

`MsgFundHotKey{operator, node_id, amount}` (C2 item 5) moves `amount` from the operator's own
earnings account to the earnings balance of `node.hot_key`. The message has no destination field:
the target is always the hot key registered on the operator's own node, so earnings cannot be aimed
at any other account. It fails for a node the signer does not operate, an unknown, retired or
tombstoned node, a zero amount, or an amount above the operator's earnings. It follows a rotated hot
key (`MsgUpdateNode`). `oramad tx nodes fund-hot-key [node-id] [amount-norama]` builds it. The
bank balance is not involved, so the hot key holds a fee balance, not a public one.

Node and cluster creates, and later writes that grow the record, call
`DepositKeeper.LockDeposit` (x/fees' C2 deposit). Retire releases every part of that deposit.
The 99%/1% split stays inside x/fees.

### Node network identity

The storage distinct-network rule (C7) and the operator-house caps (C5) need a /16 network and an
ASN per node. Neither can be proven on chain, so both are **declarations by the operator**, and this
is the trust model:

- **/16 network.** Derived, not stored: the first endpoint (in registered order) whose host is a
  literal public IPv4 address gives `a.b.0.0/16`; a literal IPv6 address gives its `/32`
  (`types.NetworkOf`). Hostnames are skipped, because the chain cannot resolve DNS
  deterministically. A node with no literal-IP endpoint has no network. Endpoints are validated as
  public at `MsgRegisterNode` and `MsgUpdateNode`. The chain does not check that the node is
  actually reachable at that address.
- **ASN.** `MsgRegisterNode.asn`, and `MsgUpdateNode` with `set_asn` (0 clears it). The value is
  stored on the node. Zero, `AS_TRANS` (23456), documentation ranges (64496-64511, 65536-65551),
  private-use ranges (64512-65535, 4200000000-4294967295) are refused at the boundary and in
  genesis validation. The spec gives no on-chain source for an ASN (an oracle would be a new trust
  point) and this module implements no challenge or dispute for a wrong one.

An operator that lies about its endpoint or ASN can make its nodes look diverse. What bounds that
today is economics: every node needs a bond and a deposit, and protocol-deal slots also need
distinct operators. It is a declared limit, not a detected one. The keeper reads it through
`NodeNetwork(node id)`; `chain/app/storage_view.go` gives x/storage the same values, and
`chain/app/houses_view.go` gives x/houses an operator's identity. A slot records the /16 and ASN
at assignment time, so a later endpoint change does not move an existing slot.

### Feeding x/storage

x/storage assigns slots only to nodes in its own tracked set (`x/storage` `Nodes`). `x/nodes`
keeps that set current without a hook or a per-block scan: every write to a node with the STORAGE
role (`saveNode`: bond, unbond, jail, unjail, retire, slash, capacity, endpoints, hot key) queues
its id in `StorageDirty`, and imported genesis nodes are queued the same way. At the start of its
`BeginBlock`, x/storage drains the queue (`NodeView.TakeStorageChanges`) and reconciles each id:

- **Tracked** while the STORAGE role is bonded at `min_bond` and the node is not jailed, retired
  or tombstoned (`Keeper.StorageEligible`, which is `IsRoleActive(STORAGE)`). A node bonded only on
  another role is not tracked and is never picked.
- **Untracked** when it stops qualifying and holds no replicas. A node that still holds replicas
  stays tracked, because challenge sampling and settlement read its state, and is re-queued each
  block until its last replica is released, evicted or expired. Assignment already skips it
  because it no longer qualifies. A probation record deposit is released on untracking.
- Tracking never resets the storage state of an already tracked node.

The tracked set is derived from x/nodes, so genesis export/import round-trips it: x/storage exports
its `NodeState` rows, and x/nodes re-queues every imported STORAGE node so the first block
reconciles them. Tracking is not marked probation: the fee-free probation registration (a node with
no bond) cannot be assigned at all today, because assignment needs a bonded STORAGE role.

A protocol-deal slot additionally needs a node with a known /16 and a declared ASN; a node without
either can still hold PRIVATE and PUBLIC_PIN user deals, where only distinct operators are required.

### Service days

`EndBlock` records at most one UTC day per operator. The day counts when the operator has an
active STORAGE role whose declared capacity is at least `min_service_volume_bytes`, or an active
RELAY role. Several blocks on the same day count once. Days with no block are not backfilled.
`OperatorServiceDays` returns that count. It is the input later operator-house eligibility reads;
this module does not vote or lock a house bond.

### Genesis defaults

G1 has not signed `min_bond[role]` or `bond_per_gib`. Until it does, every role's minimum is
1 ORAMA and `bond_per_gib` is 1 ORAMA, so 1 ORAMA of STORAGE bond backs exactly 1 GiB.
`unbonding_seconds` is 21 days. `deposit_per_byte` is 68359 norama (68359 × 1024 = 69,999,616
norama, about 0.07 ORAMA/KiB, P3). `probation_capacity_bytes` is 1 GiB (C2 states no byte
count). `min_service_volume_bytes` is 1 (C5/G4 state no byte count); an active relay still
counts without it.

### Invariants

`Keeper.CheckInvariants`, also `oramad query nodes invariants` once the module is wired:

- the `nodes` module balance equals role bonds plus unbonding entries;
- a node marked active has some role bonded at `min_bond`, and a node marked registered does not;
- declared capacity is within backing, and the free-capacity index matches.

### Not built here

The C2 fee-free registration quota is an ante rule and is not implemented. Queries, once wired: `params`, `operator [address]`, `node [id]`,
`cluster [id]`, `unbondings [node-id]`, `invariants`.

## Global services: `orama-global`

`chain/cmd/orama-global` is the binary the `orama-global-*` units run beside
`oramad`. Each subcommand reaches the chain only through the loopback CometBFT
RPC (`--rpc`, default `tcp://127.0.0.1:31001`) via `chain/client/node`. That
client runs module queries as ABCI queries (at the latest height or a given
one), reads a block, its results and its events, and the node's earliest and
latest heights, and signs with SIGN_MODE_DIRECT. It simulates for gas, adds 50%, pays gas × the x/fees
base fee with no tip, broadcasts, and waits up to a minute for a block to
include the transaction. A transaction that is not included returns
`ErrNotIncluded`. The next step rebuilds it from chain state.

### Storage provider (`orama-global provider`)

The files live in `--home` (the unit's state directory):
- `hot-key`: a hex secp256k1 key, created on first start with mode 0600. A
  key file that other users can read is refused. The address is logged on
  creation. Fund it and name it as the node's hot key in x/nodes.
- `node-id`: the x/nodes id, written after registration.
- `denylist` (optional).
- `store/`.
- `state.json`: the block cursor and the slots waiting for a piece.
- `monitor.json`: hot-key balance, unanswered challenges, and bytes stored,
  in the fields `core/pkg/telemetry/report` reads.

Each step, `chain/provider.Runner`:
1. Proves every unproved challenge of the current x/emission epoch first.
   It proves only slots the chain still assigns to this node, at most 32 per
   `MsgSubmitProofs`. A dropped proof tx is rebuilt on the next step,
   because the chain still lists the challenge as unproved. A failure
   anywhere else in the step never holds back a proof.
2. Reads at most 500 blocks past the cursor and records `storage_slot_assigned`
   events for this node. The first run starts at `--start-height`; the
   default is the node's x/nodes `registered_at_height`.
3. Reads each waiting slot. Once a piece with the slot's root has been
   uploaded, it sends `MsgAcceptDeal`. If nothing has arrived
   `DeclineMarginBlocks` (2) blocks before the accept window closes, it sends
   `MsgDeclineDeal` "piece not stored". A slot the chain no longer assigns to
   this node is dropped. One slot that fails does not stop the others. After
   an accept, the step proves again, because an accept opens a challenge in
   the current epoch.
4. Once per epoch, releases a bound slot one epoch after the chain stops
   naming this node for it (eviction, expiry, or reassignment). The piece
   bytes go when no other binding names them and no waiting slot needs them.

Uploads are `POST /pieces/<hex piece root>` with `X-Piece-Root` set to the
same hex; any other header is refused. They are accepted only for a root the
runner is waiting on, and at most two at a time. A stored piece is never
replaced. The same bytes again are a no-op. Each client address (an IPv6
/64) has its own rate limit. A full address table drops buckets idle for 10
minutes before it refuses a new address.
Retrieval is `GET`/`HEAD /pieces/<hex piece root>` on port 31013, with a
per-address rate limit. `chain/provider` does not pin through Kubo.

### Repair delegate (`orama-global repair`)

`<home>/operator` holds the address that deals name as `repair_delegate`.
x/storage never assigns a slot of such a deal to that operator, and the
delegate refuses a deal where it finds one. It dials only public addresses
and follows no redirects, since provider endpoints are whatever a node
registered in x/nodes. `<home>/deals/<id>.json`
(mode 0600) holds `{"deal_id": N, "repair_seed": "<hex>"}`. The delegate's
operator installs these files; no network path hands a seed to the delegate.

Each pass, for every such deal:
- A slot that is assigned but not accepted, while another slot is active, is
  rebuilt: fetch the surviving replica, check its root, and apply
  `chain/storagekey.Rewrap` (strip one slot layer, apply the other).
- The result must hash to the new slot's root. If it doesn't, the repair seed
  is wrong and nothing is uploaded.
- The result is uploaded to the new provider.
- A new deal with no accepted replica is left alone.
- The delegate never recovers plaintext.

`TestRepairChaos_killedProviderIsEvictedAndTheDelegateRestoresTheReplica`
runs the whole path against the x/storage keeper: a provider stops, misses
evict its slot, the delegate restores it, and the new provider proves it.

### History archiver (`orama-global archiver`, `orama-global history get`)

The archiver reads each finalised range of `--range-blocks` (default 1000)
blocks over RPC. Ranges start at 1, 1+w, and so on. x/archive refuses
overlapping ranges, so every archiver of a chain must use the same width.

For each range, it writes `<home>/bundles/<start>-<end>.orbh`. This is not a
CAR file. The layout is magic `ORBH`, version 1, the start height and the
count. Each block follows as its hash, a length, and the `tendermint.types.Block`
protobuf.

It then submits `MsgAttest`, signed by `<home>/hot-key` for the node named
in `<home>/node-id`, with:
- the bundle CID: CIDv1, raw codec, sha2-256 of the file;
- the file's SHA-256;
- the block-hash Merkle root.

`<home>/cursor` is the last attested height, and a restart resumes after it.
x/archive pins the first attestation of a range. If a range is already
pinned with a different root, bundle hash or CID, the archiver keeps its own
bundle. It writes `<home>/conflicts/<start>-<end>.json` with both sets of
values, reports `ErrRootConflict` once, and moves on to the next range. A
range of a different width that overlaps is refused by x/archive on every
attempt.

`history get --height H --from <home or http base>` loads the range's
bundle. It checks the file hash, each block's bytes against its header hash,
and the Merkle root against the x/archive record, and only then writes the
block. The archiver does not create ARCHIVE storage deals or move CometBFT's
retain height.

x/archive accepts `MsgAttest` and `MsgAttachReplicas` only from the hot key of
the x/nodes node the message names, and only while that node is active with an
ARCHIVER role bond. Each operator counts once toward a range: a second node of
an operator, or the same operator under a rotated key, is accepted and
counts nothing, and a key that already attested stays idempotent after its
node loses the role. Every id in `MsgAttachReplicas` must be a decimal x/storage
deal id of an active ARCHIVE deal (`ErrNotArchiveDeal`) that backs no other
range (`ErrDealAttached`), and only an operator that attested the range may
attach deals to it (`ErrNotAttester`). When the three-operator, three-deal
threshold is reached, the deals are read again: ended ones are dropped from the
range and freed, and the range is archived only if three live deals remain.
After that `archived` is permanent. The deal's stored bytes are not checked against
the bundle: x/storage cannot yet create an ARCHIVE deal for a given bundle, so
the three bonded operators are what vouch for it. Nothing slashes a wrong root
yet; the root is checkable by anyone against the chain's own block hashes.

### Chain indexer (`orama-global indexer`)

`chain/indexer` follows oramad over the loopback RPC and keeps an index in
Pebble (pure Go, no cgo) under `<home>/index`. The unit
(`RenderGlobalIndexerUnit`, user `orama-indexer`, home
`/var/lib/orama-global/indexer`) runs it with `--listen 127.0.0.1:31015`
(`constants.GlobalIndexerPort`). `--listen` must be a loopback IP; anything
else is refused at start.

Following:
- It reads each block and its block results from `--start-height` (default
  1). Each block is written in one synced Pebble batch together with the
  cursor (the last indexed height), so a restart resumes after the cursor and
  never indexes a block twice or half.
- An index keeps the start height it was created with. Another
  `--start-height` against the same `--home` is refused.
- If the next block is below the node's earliest block (`earliest_block_height`
  in `/status`), the indexer stops with `ErrPruned`, naming the block it needs
  and the node's earliest. It does not skip ahead. Point `--rpc` at a node that
  keeps the block, or start a new `--home` from a later height.
- A block whose result count does not match its transaction count, or a
  successful transaction whose bytes do not decode, stops the pass and the
  block is not committed.

What is indexed:
- **Blocks:** height, hash, time, proposer address (hex), transaction count,
  transaction hashes.
- **Transactions** (failed ones too): hash (SHA-256 of the bytes, lowercase
  hex), height, index in the block, code, codespace, log, gas wanted and used,
  the type URL of each message, and the events of the `ExecTxResult`. A
  transaction whose bytes are not a Cosmos transaction (the chain refused it)
  is kept with no message type URLs.
- **Address → transactions:** every distinct attribute value, in the
  transaction's events, that is exactly a lowercase bech32 `orama1…` account
  address. That includes `message.sender`, `transfer.recipient` and
  `tx.fee_payer` as the SDK emits them.
- **cNFT assets.** `x/cnft` and `x/market` emit no events, so the index
  replays their messages and message responses from successful transactions:
  `MsgCreateTree`, `MsgMint`, `MsgTransfer`, `MsgBurn`, `MsgUpdateMetadata`,
  `MsgDecompress`, `MsgCompress`, and `x/market`'s `MsgList`, `MsgBid`,
  `MsgCancelListing`, `MsgCancelBid` and `MsgSettle` (the sale moves the leaf
  to the signer, or to the bidder of the accepted bid, and clears the
  delegate). An asset record holds the chain's leaf fields (asset id, owner,
  delegate, metadata CID, creator hash, nonce, hash id), its tree, leaf index
  and collection, a state (`compressed`, `decompressed` or `burned`), the leaf
  hash while it is a live compressed leaf (the same `LeafHash` x/cnft uses),
  and the height and transaction of the last change. x/cnft does not make
  asset ids unique across mints, so one id can have several records, keyed by
  tree and leaf.
- Addresses in cNFT records are stored lowercase. The chain accepts either
  single case and any length from 1 to 255 bytes and hashes the bytes, so
  the index canonicalises the spelling instead of refusing it.
- A failed transaction whose bytes repeat a successful one does not replace
  the successful record.
- Three facts can predate `--start-height`: a tree's collection, a listing or
  bid being settled, and a decompressed asset being compressed. The index
  uses what it recorded. If it did not see the message that created one, it
  reads the chain: a tree from the latest state (x/cnft never changes or
  deletes a tree), a listing, bid or decompressed asset from the state at the
  block's height minus one (settle and compress delete them). A node that
  pruned that state fails the block loudly.

What is not indexed:
- Finalize-block events (rewards, emission, epoch transitions). Only
  transaction events feed the address index.
- An address that appears only inside a message body, or inside a longer
  attribute value, is not in the address index. The new owner of a
  `MsgTransfer` is in the cNFT owner index, not in the address index.
- cNFT Merkle proofs (DAS `getAssetProof`) and tree snapshots. A proof needs
  every leaf of the tree since its creation, and the index may start after
  that. Collections (name, royalty) are not indexed; the asset carries the
  collection id.
- Metadata JSON. The chain stores a CID only.
- Assets minted before `--start-height` that no later message touches.
- x/cnft or x/market messages that a CosmWasm contract dispatches. The index
  sees only the top-level `MsgExecuteContract`, and neither module emits
  events, so an asset changed that way keeps a stale record.
- A transient RPC error, or chain state at height minus one that the node
  pruned, is logged and the same block is retried every `--interval`; only a
  pruned block stops the process. Watch `cursor` against `tip` in
  `/index/v1/status`.
- No installer writes `orama-global-indexer.service` yet, and nothing creates
  the `orama-indexer` user, so `/v1/chain/index/…` answers `502` until the
  unit is installed. On a state-synced node, set `--start-height` to a height
  the node keeps.
- Shielded activity beyond its public transaction bytes and events.

Read API on the loopback listener. Everything is `GET` and JSON; any other
method, path, or query parameter is refused (`404`, `405`, `400`):

| Path | Answer |
|---|---|
| `/index/v1/status` | `start_height`, `cursor`, and the node's `earliest` and `tip` (`502` if the node is unreachable) |
| `/index/v1/blocks/{height}` | the block, or `404` `not indexed` |
| `/index/v1/txs/{hash}` | the transaction (64 hex, either case, no `0x`) |
| `/index/v1/accounts/{address}/txs?page=&limit=` | the address's transactions, newest first |
| `/index/v1/cnft/assets/{id}` | `{"id", "records": [...]}`, each record a DAS-style asset (`ownership`, `compression`, `grouping` by collection, `burnt`, `content.metadata_cid`, `last_update`) |
| `/index/v1/cnft/owners/{address}/assets?page=&limit=` | the compressed and decompressed assets the address owns, by asset id |

`page` is 1–1000 (default 1) and `limit` 1–100 (default 20). An address must
be a lowercase bech32 `orama1…` account with a valid checksum. The gateway
serves these routes at `/v1/chain/index/…` (see Explorer).

### Client side

`core/pkg/storagefile` seals a private file before upload:
- The file key is wrapped, with XChaCha20-Poly1305, by the owner's 32-byte
  storage key: RootWallet's `orama-storage-v1` HKDF branch
  (HKDF-SHA256, IKM = BIP-39 seed, salt = `orama-storage-v1`, empty info).
  This side only ever holds that derived key, never the wallet seed.
  `storagefile.DeriveStorageKey` recomputes it from the seed for recovery.
- Each slot XORs that blob with a ChaCha20 keystream (all-zero nonce). Its
  32-byte key is HKDF-SHA256 of the repair seed with info `deal_nonce` ||
  slot (4 bytes, big-endian). Every (nonce, slot) has its own key.
  `chain/storagekey` is the same construction for the repair delegate. Both
  are locked to `chain/storagekey/testdata/outer_vectors.json`.
- The piece root is `core/pkg/pieceroot`, checked against `chain/piece`
  vectors.
- A wrong storage key, repair seed, or slot fails closed.

The storage commands:
- Keys are read from files: `--storage-key-file` (the `orama-storage-v1` key
  from RootWallet, hex, exactly 32 bytes; the wallet seed is refused) and
  `--repair-seed-file`. A file that other users can read is refused; the
  commands take no key as an argument.
- `orama storage seal` writes one ciphertext per slot and prints each piece
  root. `orama storage open` reads one of those files.
- `orama storage rewrap` rebuilds one slot from another with the repair seed.
- `orama storage put --deal-id N --dir <seal output> --rpc <oramad RPC>` checks
  every slot file's root against the chain before sending anything. It waits
  for each slot's assignment and uploads to the node's first http(s) endpoint
  in x/nodes. It dials only public addresses and follows no redirects.
- `orama storage get` fetches the first accepted slot whose bytes match the
  on-chain root, then opens it.
- None of these commands creates the deal. That is `orama storage create`,
  signed through the RootWallet agent.

`core/pkg/storageclient` is the Go client these commands use.

### Other fail-closed pieces

- `chain/x/confidential` refuses every attestation report. It does not treat
  a blob as a TEE measurement.
- `core/pkg/tornet` accepts a parameter set only when it names at least three
  authorities, the exit policy is `reject *:*`, and signing certificates last
  12 months. `StartExit` refuses to launch an exit.

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
(P2P/RPC/gRPC/API/Prometheus/pprof, ten ports per node so up to ten validators fit). That packing
is localnet only. A production global node uses 31000–31004 for the chain (p2p public, RPC, gRPC,
REST and Prometheus on loopback), 31010–31013 for public storage (swarm public, Kubo RPC and
gateway on loopback, provider HTTP public), 31014 for relay metrics on loopback, 31015 for the
chain indexer's read API on loopback, and 31020–31021
for a Tor relay and a dirauth. The public Kubo on a global node has no swarm.key,
announces only pinned content (`Provide.Strategy=pinned` for Kubo v0.38), and
does not dial or announce private ranges, so it cannot join a cluster's mesh.
Every setup
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

## Explorer

The website explorer (`website/src/pages/explorer.tsx`, `website/src/explorer`) reads the
chain through the gateway. The browser calls `/v1/chain/…` on the same origin. It does not
open CometBFT (`127.0.0.1:31001`), the SDK REST API (`127.0.0.1:31003`), or the chain
indexer (`127.0.0.1:31015`).

`core/pkg/gateway/routes.go` mounts the read-only `core/pkg/gateway/handlers/chainread` proxy
at `/v1/chain/` (an open route in `route_policy.go`). The upstream bases are
`ORAMA_CHAIN_RPC_URL`, `ORAMA_CHAIN_REST_URL` and `ORAMA_CHAIN_INDEX_URL`, defaulting to those
three loopback URLs. The caller's path is not forwarded: each route builds its own upstream URL
from values it has validated. Anything outside this list is refused, and the upstream body and
status are copied unchanged:

| Gateway path | Upstream |
|---|---|
| `GET /v1/chain/status` | CometBFT `GET /status` |
| `GET /v1/chain/block?height=` | CometBFT `GET /block?height=` |
| `GET /v1/chain/blocks?min_height=&max_height=` | CometBFT `GET /blockchain?minHeight=&maxHeight=` (at most 20 blocks) |
| `GET /v1/chain/tx?hash=` | CometBFT `GET /tx?hash=` (32-byte hex, `0x` optional on the gateway path) |
| `GET /v1/chain/validators` | CometBFT `GET /validators` (`page` and `per_page` optional; default 1 and 100, capped at 100) |
| `GET /v1/chain/supply/norama` | REST `GET /cosmos/bank/v1beta1/supply/by_denom?denom=norama` |
| `GET /v1/chain/staking/pool` | REST `GET /cosmos/staking/v1beta1/pool` |
| `GET /v1/chain/index/status` | indexer `GET /index/v1/status` |
| `GET /v1/chain/index/blocks/{height}` | indexer `GET /index/v1/blocks/{height}` |
| `GET /v1/chain/index/txs/{hash}` | indexer `GET /index/v1/txs/{hash}` (32-byte hex, `0x` optional on the gateway path, sent lowercase) |
| `GET /v1/chain/index/accounts/{address}/txs` | indexer `GET /index/v1/accounts/{address}/txs` (`page` 1–1000, `limit` 1–100, both optional) |
| `GET /v1/chain/index/cnft/assets/{id}` | indexer `GET /index/v1/cnft/assets/{id}` (32-byte hex, sent lowercase) |
| `GET /v1/chain/index/cnft/owners/{address}/assets` | indexer `GET /index/v1/cnft/owners/{address}/assets` (`page`, `limit` as above) |

On the index routes the gateway checks an address's shape (lowercase `orama1` plus bech32
characters) and the indexer checks its checksum. A route that takes no query refuses one.
What the indexer holds, and what it does not, is under "Chain indexer" above.

`x/emission`, `x/fees`, and `x/power` are not on this list: they speak gRPC and have no REST
annotations. Neither are per-account bank balances. The explorer does not invent rows for a
query this proxy does not serve.

## `x/shielded`: proof verification

Code: `chain/x/shielded/verify` (the `Verifier` interface and `Check`),
`chain/x/shielded/verify/orchard` (the cgo verifier and the sighash),
`chain/x/shielded/orchardffi` (the Rust crate). This is the verify hook only. There is no
`MsgShieldedTransfer` and no keeper that calls it yet, so the chain accepts no shielded bundle.

**What verifies a bundle.** Upstream `orchard` (Zcash) with feature `circuit`, called from Go
through a tiny C ABI (`orama_orchard_verify`, header `orchardffi/include/orama_orchard.h`).
Nodes only verify. The crate has no prover, and no circuit of ours. The Halo 2 verifying key is
built once, lazily, for `OrchardCircuitVersion::PostNu6_3` only (`InsecurePreNu6_2` is never
built), and shared by every call. The first verification in a process pays the key build (about
1 s on the Apple M3 below), so warm it before the chain depends on it. Verification checks, in
this order: the canonical encoding, the proof length, every spend-authorization signature, the
binding signature, then the Halo 2 proof. It refuses trailing bytes. A panic in Rust is caught
(`catch_unwind`) and returned as `ErrVerifierFault`; it never unwinds into Go.

**Pinned versions** (exact `=` pins in `orchardffi/Cargo.toml`, resolved set in the committed
`orchardffi/Cargo.lock`):

| crate | version |
|---|---|
| `orchard` | 0.15.5 (latest 0.15.x when written; Ironwood pool, `BundleVersion::ironwood_v3`) |
| `zcash_primitives` | 0.30.1 (wire encoding only) |
| `zcash_protocol` | 0.10.6 |
| `halo2_proofs` | 0.3.5 |
| `reddsa` | 0.5.2 |

Multi-asset (ZSA) is off: no QEDIT fork is used and `orchard` 0.15.5 has no asset type.

**Wire format.** The canonical Zcash transaction-v6 encoding of the Ironwood bundle, read by
`zcash_primitives::transaction::components::orchard::read_v6_bundle` with `BranchId::Nu6_3` and
`ValuePool::Ironwood`: CompactSize action count `n`, `n` actions of 820 bytes (cv, nf, rk, cmx,
epk, 580-byte enc ciphertext, 80-byte out ciphertext), flags (1), value balance (8, i64 LE),
anchor (32), proof (CompactSize length, then bytes), `n` spend-authorization signatures (64
each), binding signature (64). The proof length must equal `Proof::expected_proof_size(n)`
= 2720 + 2272 x n bytes; a different length is `ErrProofLength` and is refused before any
proof work. A 1-action bundle is 5985 bytes and a 2-action bundle 9141 bytes.

**Sighash.** The chain has no Zcash transaction, so the sighash is ours, and it is computed in
Go (`orchard.Sighash`), not in Rust. Every signature signs

```
SHA-256( "orama-shielded-ironwood-sighash-v1" || u16be(len(chainID)) || chainID || effectingData )
```

`effectingData` is the contiguous prefix of the bundle before the proof: the action count, all
actions (including epk and both ciphertexts), flags, value balance and anchor. The proof and the
signatures are excluded; the proof is bound by the circuit's public inputs. The chain ID stops
replay across Orama networks. The verifier is built per chain ID (`orchard.New(chainID)`; the app
passes its own). Because the ciphertexts and epk are not proof inputs, only the sighash binds
them: a flipped ciphertext byte fails as `ErrSignatureRejected`.

**Errors.** `ErrMalformed`, `ErrProofLength`, `ErrProofRejected` and `ErrSignatureRejected` all
wrap `ErrTampered`. `ErrVerifierFault` is an internal fault, not a verdict on the bundle.

**Fail-closed behaviour.**
- A build without cgo or without the `orchardffi` build tag compiles, and every bundle gets
  `ErrVerifierNotLinked`. `CGO_ENABLED=0 make build` binaries never accept a bundle.
- `verify.Check` needs `verify.MinVerifiers` (2) independent verifiers, and all must accept. Only
  the Orchard verifier exists (`OramaApp.ShieldedVerifiers` holds it), so `Check` still returns
  `ErrVerifierNotLinked` for every bundle. This is deliberate: it stays that way until a second
  independent verifier is linked. No second verifier is faked.
- Input over `orchard.MaxBundleBytes` (1 MiB) is refused before parsing. The per-block and
  per-action limits belong to C12a.

**Building.** Rust (1.88 or newer, `rustup`) and cgo are needed only for the linked build.

```sh
cd chain
make orchard-lib          # static library for this host into x/shielded/verify/orchard/lib/
make orchard-test         # cargo test, then go test -tags "nowasm orchardffi" ./x/shielded/... ./app/...
# release: static linux/amd64 oramad with the verifier linked (musl, C parts through zig)
rustup target add x86_64-unknown-linux-musl
ORAMA_ZIG=/opt/homebrew/opt/zig@0.15/bin/zig make build-linux-amd64-orchard
```

`make build-linux-amd64-orchard` writes `build/oramad-linux-amd64-orchard`. `orama build` does not
build `oramad`; this target is the release path for the chain binary, and `make build` is
unchanged. `scripts/zigcc.sh` exists because cc-rs passes `--target=<rust triple>`, which `zig cc`
rejects.

**Test vectors.** `orchardffi/testdata/` holds a 1-action and a 2-action Ironwood bundle with
their sighash and chain ID. `cargo run --release --example gen_vectors -- testdata` regenerates
them (that tool proves; the node does not). Both are shielding bundles (spends disabled). The
2-action one is padded with a dummy spend, so its spend-authorization signatures are real but no
note is spent from a tree. A full-spend vector waits on the wallet builder (F7). The Go tests
recompute the sighash and require it to equal the generator's, and check each vector, a flipped
byte in the proof, an action, the anchor, the value balance, a spend-authorization signature and
the binding signature, a different chain ID (a different sighash), a short and a padded proof,
empty, truncated and oversize input, and 16 goroutines verifying at once (also under `-race`).

**Local measurements** (Apple M3, darwin/arm64, 8 cores, `go test -bench Verify -benchtime 20x`
after the key is built; these are local numbers only, not linux and not amd64, and they set no
gas price):

| bundle | 8 threads (default) | `RAYON_NUM_THREADS=1` |
|---|---|---|
| 1 action | 5.5-5.7 ms | 22.8-23.2 ms |
| 2 actions | 6.9-7.1 ms | 26.6 ms |

The linux/amd64 and linux/arm64 numbers, and the check that both accept the same bytes, are
not measured (C0-4). The linux/amd64 binary was built and linked, not run.

## Building

```sh
cd chain
make build   # linux/amd64, darwin/amd64, darwin/arm64, all CGO_ENABLED=0 (no cgo dependency;
             # the shielded verifier is not linked, see x/shielded above)
make test    # go test ./...    (chain-test is an alias)
make lint    # go vet ./...
```

`make build` passes `-trimpath` and version `ldflags` (from `git describe` and the short commit
hash), so `oramad version --long` reports something meaningful and binaries don't embed local
filesystem paths.

## Running `oramad` under cosmovisor

The global-role unit that `core/pkg/install` renders (`RenderGlobalChainUnit`,
`orama-global-chain.service`) runs `/usr/lib/orama-global/bin/cosmovisor run start --home
/var/lib/orama-global/chain ...` with `DAEMON_NAME=oramad`, `DAEMON_HOME=/var/lib/orama-global/chain`
(`constants.ChainHome`), `DAEMON_ALLOW_DOWNLOAD_BINARIES=false` and
`DAEMON_RESTART_AFTER_UPGRADE=true`, and mounts `cosmovisor/genesis` and `cosmovisor/upgrades`
read-only (`ReadOnlyPaths=`). Cosmovisor runs `DAEMON_HOME/cosmovisor/current/bin/oramad`
and, when the chain halts at an upgrade plan's height, points `current` at
`cosmovisor/upgrades/<name>` and restarts. It never downloads a binary; a plan with no staged
binary halts the chain until one is staged. Nothing installs this unit or the cosmovisor binary:
no cosmovisor release is pinned. `orama global install` writes a different chain unit
(`RenderGlobalChainDirectUnit`) that runs `/usr/lib/orama-global/bin/oramad start` with the same
home and listeners and no cosmovisor, so binaries staged by `stage-oramad` below are not run by
the installed unit; a chain binary is changed by staging a new `oramad` for `orama global
install` and restarting the chain. [RUN_A_GLOBAL_NODE.md](RUN_A_GLOBAL_NODE.md) covers the
install, the ordered lifecycle and the validator key operations. The stagenet deploy script
below still writes its own unit that runs `oramad` directly.

Binaries enter the layout only through `orama global stage-oramad` (run as root):

- `--upgrade <name>` places `cosmovisor/upgrades/<name>/bin/oramad`. `<name>` must be lowercase
  letters, digits, `.`, `-` or `_` (cosmovisor lowercases and URI-escapes plan names, so these are
  the names that map to the same directory on both sides).
  It also places `cosmovisor/upgrades/<name>/upgrade-info.json` as a symlink to
  `../../../cosmovisor-upgrade-info-<name>.json`, a file in the chain home: cosmovisor's
  `SetCurrentUpgrade` creates `upgrade-info.json` in the upgrade's directory, and the link sends
  that write into the home so the upgrade directory can stay root's and read-only.
- `--genesis` places `cosmovisor/genesis/bin/oramad`, creates `cosmovisor/upgrades/` (the unit's
  read-only mount needs it to exist), and creates `current -> genesis`, owned by `orama-chain`,
  when `current` does not exist; an existing `current` is never repointed.
- Every directory from `/` down is opened with `O_NOFOLLOW` relative to the one before it and
  checked on its descriptor: the home's ancestors must be root's and not writable by others (a
  sticky directory such as `/tmp` excepted), the home must be owned by root or `orama-chain`, and
  `cosmovisor/`, `genesis/`, `upgrades/`, `upgrades/<name>/` and each `bin/` must be root's and
  not group- or world-writable. A symlink or a foreign or writable component is refused.
- The binary is copied into a fresh 0700 root-only staging directory under `cosmovisor/`,
  synced, set to 0755 through its descriptor, and verified **through that descriptor** with
  `--release-metadata <dir> --release-target <name>` against the TUF release root adopted at
  `/etc/orama/release-root.json` (the same check `orama node stage-archive` makes). Only then is
  it hard-linked into `bin/`; the link fails if a binary is already there, so nothing is
  replaced. A failure leaves nothing behind.
- Ownership: `cosmovisor/` is root-owned, group `orama-chain`, mode 1775. The group may create
  and replace its own entries there (`current`, which cosmovisor removes and re-creates at the
  upgrade height); the sticky bit stops it removing or renaming root's `genesis/`, `upgrades/`
  and staging directories. Everything below `genesis/` and `upgrades/` is root-owned 0755.

Nothing stages automatically: a validator's `autoupdate` role refuses `auto`, and its operator
runs `stage-oramad` for every upgrade.

## The stagenet deploy script

`chain/scripts/stagenet/deploy.sh up|status|invariants|reset` is the deployment path for the
project's own stagenet nodes, which run the chain over their WireGuard mesh with a genesis the
script builds. It drives its own systemd unit directly. A global-role node is installed with
`orama global install` instead ([RUN_A_GLOBAL_NODE.md](RUN_A_GLOBAL_NODE.md)), which has no
WireGuard dependency and does not build a genesis. It refuses to run unless `CHAIN_ID` contains `-stagenet-` or
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
absent. `invariants` runs `oramad query <module> invariants` for emission, fees, storage,
nodes, relay, houses and token on every node and fails if a query fails or any check is false.
Like the localnet script, its genesis flow uses `keyring-backend test` (an unencrypted,
on-disk keyring): appropriate for a devnet/stagenet operator key that holds no real value, never
for anything that does.

## Deviations from the task spec, and why

- **`x/houses` does not govern stock modules.** It is registered. Stock `x/gov` is absent.
  Authority-gated SDK messages
  stay unreachable. Structural decisions other than a development spend are stored in `x/houses`
  and are not applied to `x/emission`, `x/power` or the upgrade module. Private delegator ballots
  are not implemented (they wait on shielded delegation, C12).
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
  `chain/x/shielded`. The Orchard proof verifier links only in the cgo `orchardffi` build, and two
  independent verifiers are required, so a shielded bundle is not accepted either.
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
- **The fee-free registration quota (C2, "bootstrap only")** is not implemented. `x/nodes`
  has `MsgRegisterOperator` and `MsgRegisterNode` and is registered, but the quota is an
  ante rule and nothing in the ante does it. `MsgShieldEarnings` is not implemented.
  `x/storage` `MsgCreateDeal` still pulls its deal fee and escrow from the payer's bank balance
  only; C2 also lets a signer's earnings fund the signer's own deal escrow, and that path is not
  built.
- **`x/fees`' state-deposit ledger is called by `x/token` and `x/nodes`.** Both are registered.
  `x/cnft` calls the same interface for a tree deposit and is registered.
  `x/storage` is registered and keeps deal escrow in its own module account.
  The per-byte contract deposit meter is not hooked into wasmd's store.
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
