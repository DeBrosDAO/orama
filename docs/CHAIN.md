# The Orama L1 chain

**Status: first code, devnet/localnet only.** Nothing here has run on a public network. This
document describes only what `chain/` actually does today. The design that is still
unbuilt is in `plans/open-network.md` and `plans/open-network/track-c-chain.md`.

`chain/` is its own Go module (`github.com/DeBrosOfficial/network/chain`). `core/go.mod` does not
require it, and nothing in `chain/` imports `core/`.

**This release is state-breaking; there is no in-place upgrade.** It changes stored types and their
semantics: `x/archive` `RangeRecord` and `Params`, `x/storage` `Settlement.attempts` (and the settlement
retry and miss/penalty split), `x/houses` `Proposal.advance_failures`, and slash and settlement behaviour.
No module's `ConsensusVersion` was bumped and no migration was written, so a chain running an earlier
build cannot load this state and a new binary cannot be staged as an upgrade plan over it. A running chain
restarts from a new genesis: on stagenet, `chain/scripts/stagenet/deploy.sh reset` and then `deploy.sh up`. Do not
try to start the new `oramad` on an existing chain home.

Every node also needs `query-gas-limit = "2000000"` in `<home>/config/app.toml`: `oramad start` refuses a
limit of 0 on any chain id that does not contain `-localnet-` (see "Explorer"). `oramad init`, and so
`orama global install --init-chain`, writes it into a new `app.toml`. Nothing rewrites an existing one, so
a node whose home was created by an earlier build must have the line set by hand before the new binary
starts.

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
| `shielded` (custom, `chain/x/shielded`) | the Ironwood shielded pool: signer-less transfers, shield and unshield, turnstiles, the 24 h unshield cap and queue, the nullifier set (C12) |

**Wired** in addition to the table: `x/token` (factory denoms), `x/archive` (history registry),
and `x/nodes` (operator and global-node registry). Its end block pays matured role-bond
unbondings.
`x/storage` and `x/relay` are registered. A node with no literal-IP endpoint or no declared ASN
cannot take a protocol-deal slot, and an operator without both cannot be eligible for the operator
house (see "Node network identity" under `x/nodes`). A relay payout cannot exceed that epoch's relay ceiling
minus what was already minted. `chain/x/inclusion` is wired into BaseApp as the C13
inclusion lists: see "Inclusion lists (C13)" below. They are off until a genesis sets
`vote_extensions_enable_height`.
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
The genesis ships five standard contracts, the Orama bindings are linked, and contract state
carries a deposit: see "`x/wasm`: contracts" below.

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
registered contract (a contract is any address wasmd holds a `ContractInfo` for, or is instantiating: it
moves the attached funds before it registers the contract, so `x/wasmpolicy.WithFundedContract` marks
that one recipient). The private path between users is a `MsgShieldedTransfer` in `x/shielded`,
which a node accepts only with both proof verifiers present (see "`x/shielded`" below); on any other
node a user cannot pay another user at all.

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

## Genesis parameters (G1)

Every genesis parameter is locked at the value the plan signs off (`plans/open-network.md` P1-P10 and
D12-D17, `track-c-chain.md`, `track-g-token-legal-security.md` G1). The check is
`app.ValidateLockedGenesis` (`chain/app/locked_genesis.go`). It compares each locked module's
`params` in a genesis, key by key, with the app's own default genesis, and reports every difference
by name. It runs in two places, so a genesis that passes one starts:

- `oramad genesis validate [file]` runs genutil's own module validation, then the locked check
  on the file's chain-id;
- `InitChain` runs it on the request's chain-id before any module's `InitGenesis`, and refuses to
  start on a difference.

What is locked depends on the chain-id:

| Chain-id | Locked |
|---|---|
| contains `-localnet-` | nothing: local tests and `scripts/localnet` need short epochs and one-seat committees |
| contains `-devnet-` or `-stagenet-` | everything except `emission.epoch_duration_seconds`, `emission.min_blocks_per_epoch`, `emission.allow_bootstrap_stake` and `power.min_committee_size`, which `scripts/stagenet/deploy.sh` shortens on purpose. `x/emission` and `x/power` keep their own production floors on the same split |
| anything else | every row below, plus `wasmpolicy.upload_sunset_height` |

The rows below list each locked parameter, its plan value, and where the plan states it.
`TestLockedGenesis_defaultsEqualPlanValues` pins the module default to each row's literal,
`TestLockedGenesis_everyGenesisParameterHasARow` fails when a module gains a parameter that has no
row (so a new parameter cannot ship unlocked), and `TestLockedGenesis_productionChainRejectsEveryChangedParameter`
changes each row in turn and requires a refusal that names it. A value in a genesis may be written
`"0.05"`, `0.05` or `"0.050000000000000000"`; numbers compare as decimals.

**Plan values that are not chain parameters.** P5 (the 2% per 24 hours unshield cap) is an ossified
constant in `x/shielded/pool` (`UnshieldNumerator`, `UnshieldDenominator`) and P6 is the wasmpolicy
genesis `upload_sunset_height` (locked here). P8 (network and app names), P9 (public IPFS DHT
policy) and P10 (VPN pricing) are off-chain and have no genesis field.

**Launch values the plan does not number.** The plan names `min_self_bond`, the role bonds,
`bond_per_gib`, the `x/storage` and `x/relay` numbers, the probation numbers and `house_bond`
but gives no figure (`track-g` G1: "sized ... see C7/C8", pending the G1 spreadsheet model). The
launch value for those is the code default at this commit, marked "G1 launch default" below, and the
locked check holds a genesis to it. Changing one is a change to `DefaultParams` and to its row in
the test, so the number and its citation move together. Two plan values are placeholders that
need a testnet price: P2 (`fees.min_base_fee`, 1 norama/gas, not yet a dollar figure) and P4
(`token.creation_fee`, 10 ORAMA). P7 (the active-set size, 60 to 100) is not chosen: the C0 benchmark
that sets it has not been run (`plans/open-network/decisions/C0-spikes.md`). The locked
`staking.max_validators` is the stock 100 and does not decide P7.

**Owner decisions, recorded as values.** O-A: the 5% development ceiling stays. It is
`DevelopmentSharePercent = 5` in `x/emission/types/split.go`, and it is minted only when both houses
pass a spend (`x/houses` `applySpend` calls `MintDevelopmentSpend`) after governance opens. O-B: a
contract may hold ORAMA and issue a public IOU for it, and the genesis token wrapper may not:
`wasmpolicy.RefuseNoramaWrapper` refuses a wrapper that creates or holds norama.


`emission`

| Parameter | Locked value | Source |
|---|---|---|
| `epoch_duration_seconds` | `86400` | track-c C3: an epoch is 24 hours of BFT time |
| `min_blocks_per_epoch` | `14400` | track-c C3: minimum blocks per epoch (code default, production floor) |
| `allow_bootstrap_stake` | `false` | plan D12: zero premine |

`fees`

| Parameter | Locked value | Source |
|---|---|---|
| `target_block_gas_fraction` | `0.5` | track-c C2: base fee targets 50% fullness |
| `max_base_fee_change_fraction` | `0.125` | track-c C2: at most 12.5% per block |
| `min_base_fee` | `1` | P2 fee floor: 1 norama/gas placeholder; the dollar peg needs a testnet price (G1) |
| `initial_base_fee` | `1` | P2 fee floor (equal to min_base_fee at genesis) |
| `deposit_refund_fraction` | `0.99` | plan D14: 99% of a state deposit is refunded |
| `deposit_burn_fraction` | `0.01` | plan D14: 1% of a state deposit is burned |

`power`

| Parameter | Locked value | Source |
|---|---|---|
| `min_committee_size` | `30` | plan D16: at least 30 bootstrap validators |
| `bootstrap_exit_stake` | `271000000000000 (271000 ORAMA)` | P1: 5% of year-1 emission, 271,000 ORAMA |
| `bootstrap_deadline_epochs` | `365` | P1, D16: about 12 months of daily epochs |
| `cap_fraction_normal` | `0.05` | plan D15: 5% cap |
| `cap_fraction_reduced` | `0.03` | plan D15: 3% cap above 60 validators |
| `cap_step_down_validator_count` | `60` | plan D15: step down past 60 validators |
| `cap_step_up_validator_count` | `50` | track-c C4: hysteresis returns to 5% below 50 |
| `cap_hysteresis_epochs` | `30` | track-c C4: for 30 days |
| `ramp_epochs` | `30` | plan D15: 30-day ramp |
| `force_bond_fraction` | `0.5` | plan D16: 50% of committee rewards force-bonded |
| `self_bond_cap_multiplier` | `2` | track-c C4: self-bond target 2x the minimum |
| `min_self_bond` | `1000000000000 (1000 ORAMA)` | 1,000 ORAMA; G1 launch default (see note below) |
| `useful_work_multiplier` | `1` | plan D15: M coded at 1.0 |
| `useful_work_multiplier_activated` | `false` | plan D15: M off until a two-house vote |
| `comet_power_scale` | `1000000000` | chain security review (code default) |
| `max_redistribution_multiplier` | `2` | chain security review (code default) H3(b) |
| `pre_gate_lambda_cap` | `0.95` | chain security review (code default) H3(a) |
| `min_delegation_for_rewards` | `1000000000 (1 ORAMA)` | chain security review (code default) M2: 1 ORAMA |

`nodes`

| Parameter | Locked value | Source |
|---|---|---|
| `min_bond` | `1000000000 (1 ORAMA) for each of the six roles` | track-c C6, track-g G1 min_bond[role]: 1 ORAMA per role; G1 launch default (see note below) |
| `bond_per_gib` | `1000000000 (1 ORAMA)` | track-g G1 bond_per_gib: 1 ORAMA; G1 launch default (see note below) |
| `unbonding_seconds` | `1814400` | plan D16: 21 days |
| `deposit_per_byte` | `68359` | P3: about 0.07 ORAMA/KiB |
| `probation_capacity_bytes` | `1073741824` | track-c C2 probation capacity (1 GiB); G1 launch default (see note below) |
| `min_service_volume_bytes` | `1` | track-c C5 proven service volume (1 byte); G1 launch default (see note below) |
| `max_endpoints` | `8` | track-c C6 record bound (code default) |
| `max_bindings` | `8` | track-c C6 record bound (code default) |

`storage`

| Parameter | Locked value | Source |
|---|---|---|
| `min_deal_bytes` | `1024` | track-c C7 (structure only); G1 launch default (see note below) |
| `deal_fee` | `1000` | track-c C7 (structure only); G1 launch default (see note below) |
| `max_deals_per_block` | `100` | track-c C7 (structure only); G1 launch default (see note below) |
| `k_c` | `8` | track-c C7 (structure only); G1 launch default (see note below) |
| `miss_threshold` | `4` | track-c C7 (structure only); G1 launch default (see note below) |
| `max_settlements_per_block` | `100` | track-c C7 (structure only); G1 launch default (see note below) |
| `s_min_providers` | `8` | track-c C7 (structure only); G1 launch default (see note below) |
| `s_full_providers` | `32` | track-c C7 (structure only); G1 launch default (see note below) |
| `accept_window_blocks` | `50` | track-c C7 (structure only); G1 launch default (see note below) |
| `probation_slots` | `1` | track-c C7 (structure only); G1 launch default (see note below) |
| `probation_operator_cap` | `2` | track-c C7 (structure only); G1 launch default (see note below) |
| `probation_network16_cap` | `2` | track-c C7 (structure only); G1 launch default (see note below) |
| `probation_asn_cap` | `2` | track-c C7 (structure only); G1 launch default (see note below) |
| `probation_expiry_epochs` | `4` | track-c C7 (structure only); G1 launch default (see note below) |
| `probation_deposit` | `1000` | track-c C7 (structure only); G1 launch default (see note below) |
| `max_releases_per_epoch` | `2` | track-c C7 (structure only); G1 launch default (see note below) |
| `protocol_every_epochs` | `0` | track-c C7 (structure only); G1 launch default (see note below) |
| `slash_fraction` | `0.1` | track-c C7 (structure only); G1 launch default (see note below) |
| `protocol_piece_bytes` | `1024` | track-c C7 (structure only); G1 launch default (see note below) |
| `protocol_price_per_epoch` | `1000` | track-c C7 (structure only); G1 launch default (see note below) |
| `protocol_duration_epochs` | `1` | track-c C7 (structure only); G1 launch default (see note below) |

`relay`

| Parameter | Locked value | Source |
|---|---|---|
| `min_reporters_quorum` | `3` | owner decision on track-c C8: all 3 initial dirauths, so one lying reporter cannot move a relay's pay |
| `min_uptime_fraction` | `0.9` | track-c C8 (structure only); G1 launch default (see note below) |
| `exit_multiplier` | `2` | track-c C8 (structure only); G1 launch default (see note below) |
| `per_relay_cap` | `100000000000 (100 ORAMA)` | track-c C8 (structure only); G1 launch default (see note below) |
| `per_operator_cap` | `200000000000 (200 ORAMA)` | track-c C8 (structure only); G1 launch default (see note below) |
| `per_prefix16_cap` | `200000000000 (200 ORAMA)` | track-c C8 (structure only); G1 launch default (see note below) |

The `per_prefix16_cap` bucket of a relay is its node's effective network from `x/nodes` (the /16 derived from
its literal-IP endpoints, see the identity section below), read when `MsgRegisterRelay` runs and stored as the
relay's `prefix16` (`A.B.0.0/16`). A node with no literal-IP endpoint, or whose identity is still inside
`network_identity_lock_seconds`, has no identified network: every such relay goes into one shared bucket,
stored as `unidentified`, so all of them together share a single `per_prefix16_cap`. Genesis accepts
`unidentified` as canonical, so export and import round-trip. The bucket is a snapshot taken at registration;
a later endpoint change does not move an existing relay. A node identified by a literal IPv6 endpoint (x/nodes
reports a `/32` network) has no IPv4 /16, so its relay also goes into the `unidentified` bucket.

`token`

| Parameter | Locked value | Source |
|---|---|---|
| `creation_fee` | `10000000000 (10 ORAMA)` | P4: about $10-25 in ORAMA, burned; 10 ORAMA at genesis (no price peg before a testnet price exists) |
| `deposit_per_byte` | `68359` | P3: about 0.07 ORAMA/KiB |

`archive`

| Parameter | Locked value | Source |
|---|---|---|
| `retention_window_blocks` | `201600` | plan D22: validators keep 14 days of blocks (6-second blocks) |
| `max_piece_bytes` | `4294967296` | track-c C14 (structure only): 4 GiB caps the bundle file an attestation commits to; G1 launch default |
| `max_candidates_per_range` | `4` | track-c C14 (structure only): bounds the conflicting tuples one undecided range keeps, an honest one, wrong ones and a fork; G1 launch default |
| `range_blocks` | `1000` | track-c C14 (structure only): every archived range is exactly this many blocks and starts at a multiple of it plus 1, so ranges never overlap; G1 launch default |

`houses`

| Parameter | Locked value | Source |
|---|---|---|
| `bootstrap_exit_stake` | `271000000000000 (271000 ORAMA)` | P1: 271,000 ORAMA |
| `token_quorum` | `0.4` | track-c C5, track-g G4 |
| `token_pass_threshold` | `0.5` | track-c C5, track-g G4 |
| `voting_period_seconds` | `604800` | track-c C5, track-g G4: 7 days |
| `house_bond` | `1000000000000 (1000 ORAMA)` | track-c C5, track-g G4: 1,000 ORAMA; G1 launch default (see note below) |
| `max_eligible_per_prefix16` | `3` | track-c C5, track-g G4 |
| `max_eligible_per_asn` | `5` | track-c C5, track-g G4 |
| `min_house_size` | `21` | plan D17, track-g G1: min_house_size 21 |
| `veto_window_seconds` | `604800` | plan D17: the operator house vetoes within 7 days |
| `parameter_timelock_seconds` | `1209600` | plan D17: 14 days for parameters |
| `upgrade_timelock_seconds` | `5184000` | plan D17: 60 days for upgrades |
| `spend_timelock_seconds` | `604800` | plan D17: 7 days for fund spending |

`shielded`

| Parameter | Locked value | Source |
|---|---|---|
| `anchor_window_blocks` | `14400` | track-c C12a names the anchor window without a number; 24 hours of 6-second blocks (launch default) |
| `nullifier_fee` | `1000000` | track-c C12: a burned per-nullifier fee with no number; 0.001 ORAMA; G1 launch default (see note below) |
| `action_gas` | `250000` | track-c C12: the per-action verify gas is unmeasured (C0-4); G1 launch default (see note below) |
| `max_actions_per_bundle` | `16` | track-c C12 (structure only): bounds proof work per bundle (launch default) |
| `unshield_floor` | `1000000000 (1 ORAMA)` | P5: the cap is max(2% of the pool, a floor); 1 ORAMA; G1 launch default (see note below) |
| `max_fee_topup` | `10000000` | track-c C12: max_fee_topup per unshield to the fee balance, no number; 0.01 ORAMA; G1 launch default (see note below) |
| `queue_per_address_cap` | `100000000000 (100 ORAMA)` | track-c C12: the per-address limit of the unshield queue, no number; 100 ORAMA; G1 launch default (see note below) |
| `max_signerless_per_block` | `64` | track-c C12 (structure only): bounds signer-less proof work per block (launch default) |

`staking`

| Parameter | Locked value | Source |
|---|---|---|
| `bond_denom` | `"norama"` | plan D12: norama is the bond denom |
| `unbonding_time` | `"1814400s"` | plan D16, track-c C4 (staking unbonding, slashing) |
| `max_validators` | `100` | P7 is not chosen (track-c C0-2 pending); the stock value 100 is the top of the plan's 60-100 range |
| `max_entries` | `7` | stock x/staking default |
| `historical_entries` | `10000` | stock x/staking default |
| `min_commission_rate` | `0` | stock x/staking default |

`slashing`

| Parameter | Locked value | Source |
|---|---|---|
| `slash_fraction_double_sign` | `0.05` | plan D16: double-sign costs 5% |
| `slash_fraction_downtime` | `0.0001` | plan D16: downtime costs 0.01% |
| `signed_blocks_window` | `10000` | chain security review B3 (code default) |
| `min_signed_per_window` | `0.5` | stock x/slashing default |
| `downtime_jail_duration` | `"600s"` | stock x/slashing default |

`distribution`

| Parameter | Locked value | Source |
|---|---|---|
| `community_tax` | `0` | track-c C2: community_tax = 0 |
| `base_proposer_reward` | `0` | stock x/distribution (deprecated field) |
| `bonus_proposer_reward` | `0` | stock x/distribution (deprecated field) |
| `withdraw_addr_enabled` | `true` | stock x/distribution default |

`wasmpolicy`

| Parameter | Locked value | Source |
|---|---|---|
| `upload_sunset_height` | `3162240` | P6: 183 days of 5-second blocks; D11 |

### Relay reports and settlement (C8)

Epoch `e` is reported on while the chain is in epoch `e+1`
(`ReportWindowEpochs = 1`, a constant, not a parameter): `MsgReportEpoch` for the epoch in progress, for a
future epoch, for an epoch whose window has passed, or for an epoch `x/emission` holds no relay ceiling for is
refused (`epoch is not open for reports`), so every stored report has a relay ceiling to be paid from. A
consensus weight above 2^62 is refused, which keeps the median and the pro rata sums far from `math.Int`
overflow. Settlement is not a message. `x/relay`'s end block (which
runs before `x/emission`'s) settles the oldest epoch that has a stored report or report chunk once the chain is
in epoch `e+2` or later, one epoch per block; an epoch nobody reported on is never written. Settling takes the
per-relay median over the reporters (at least `min_reporters_quorum`), applies the per-relay cap, the uptime
minimum, the exit multiplier on the median Exit flag and the per-operator and per-/16 caps, mints the total
pro rata against the epoch's relay ceiling and pays each operator (below) into its earnings account (not the bank
balance). The first epoch with quorum only activates rewards and mints nothing; payment starts with the
next. An unfinished chunked report is deleted when its epoch settles, and so is any report an imported genesis
left behind for an epoch that already has a result. A genesis that carries a report for an epoch older than
`x/emission`'s ceiling window would stop the chain at settlement; a genesis exported by this binary cannot.

Each operator's total for the epoch is paid through the C2 service split, the same function
(`storagetypes.SplitServicePayment`) and archive fund `x/storage` uses: 90% to the operator's earnings
account, 5% burned out of the `relay` module account (which holds the burner permission for this) and 5%
to the `storage_archive` module account, with `x/storage`'s `archive_fund` counter raised by the same
amount (`Keeper.FundArchive`). The operator receives the rounding remainder, so the three parts sum to the
operator's total; the payout rows and the epoch's `minted` stay gross. The default quorum is 3, so a
relay is paid on the median of at least three reports and one lying reporter cannot move it. The operator of a registered relay cannot
be a reporter: `MsgRegisterRelay` refuses a reporter's address, `MsgUpdateReporters` (and so a reporter
proposal) refuses a set naming a relay operator, and genesis validation rejects both together. The relay
bond is not escrowed by `x/relay`; it belongs to `x/nodes`.

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
`genesis_supply + cumulative_minted + cumulative_development_minted + cumulative_service_minted + cumulative_faucet_minted - cumulative_burned`.
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

**The split can move, but only through `x/houses`.** Each closed epoch reads the split in force from
`x/houses` (`types.SplitSource`, wired in `chain/app/enactment.go`): the canonical 60/25/10/5 until
a structural proposal has passed its 60-day timelock, then the enacted percentages. Each share may
sit within 10 points of its canonical value and the four must sum to 100
(`SplitPercents.Validate`; `x/houses` checks the same bounds when the proposal is submitted).
The split an epoch closed under is written on its `CeilingRecord` (`validator_percent`,
`storage_percent`, `relay_percent`, `development_percent`; all zero means canonical), so a
later storage, relay or development mint uses that epoch's own ceiling. `EpochState.validator_split_delta`
keeps the running difference between what was minted to validators and what the canonical schedule
would have minted, so invariant 1 below still holds exactly. The halving schedule and the tail
(the maximum per epoch) are ossified and no proposal can touch them. An epoch closes under the split in
force at the block that closes it: a split enacted mid-epoch applies to that epoch's close.

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

### Test-network faucet

`x/emission` carries one Msg, `MsgFaucet` (`/orama.emission.v1.MsgFaucet`, `signer`, `recipient`,
`amount` in norama), so a devnet, stagenet or localnet can fund accounts without a premine. It
mints nothing on a production chain.

**Gates**, each a typed refusal (`x/emission/types/errors.go`) returned before anything is minted:

- The chain-id must contain `-devnet-`, `-stagenet-` or `-localnet-` (`ErrFaucetProduction`). This
  is enforced twice: `InitGenesis` rejects `faucet_enabled = true` on any other chain-id, so a
  production genesis cannot switch the faucet on, and `Msg.Faucet` checks `ctx.ChainID()` again at
  execution.
- `faucet_enabled` must be true (`ErrFaucetDisabled`). It defaults to false.
- `0 < amount <= faucet_max_drip` (`ErrFaucetAmount`).
- The recipient must be a valid address that is not a module or otherwise blocked account
  (`ErrFaucetRecipient`). It need not exist: the drip creates the account.
- The recipient must not have drawn within `faucet_recipient_cooldown_seconds` of BFT time
  (`ErrFaucetCooldown`). The last drip time per recipient is kept in `x/emission` state and is not
  carried by a genesis export, so a re-imported chain starts with no cooldowns.
- The faucet's mint in the current epoch plus the drip must not exceed `faucet_epoch_cap`
  (`ErrFaucetEpochCap`). The per-epoch counter (`faucet_epoch_minted`) resets when the epoch closes.

Any existing account may sign. The signer pays its own transaction fee through `x/fees`, for example
a validator operator spending earnings, so the faucet needs no funded account of its own.

**Parameters** (genesis only, in `x/emission` params; `genesis set-emission-params` takes
`--faucet-enabled`, `--faucet-max-drip`, `--faucet-epoch-cap` and `--faucet-cooldown`, and keeps the
values already in genesis for any flag it is not given):

| Parameter | Default |
|---|---|
| `faucet_enabled` | `false` |
| `faucet_max_drip` | 1,000 ORAMA (`1000000000000` norama; the denom has 9 decimals) |
| `faucet_epoch_cap` | 100 max drips, 100,000 ORAMA per epoch |
| `faucet_recipient_cooldown_seconds` | 86,400 (24 hours; 0 disables the cooldown) |

When enabled, `faucet_max_drip` must be positive and `faucet_epoch_cap` at least `faucet_max_drip`.
G1's locked-genesis check lets a devnet or stagenet chain-id change these four (`testnetRelaxedParams`
in `app/locked_genesis.go`); a production chain-id must keep the defaults, so `faucet_enabled`
stays false there.

**Supply accounting.** A drip mints into the `x/emission` module account and sends the coins to the
recipient. `x/emission` adds the amount to `cumulative_faucet_minted` (and `faucet_epoch_minted`) in
the same transaction, and the supply identity includes it:
`bank_supply == genesis_supply + cumulative_minted + cumulative_development_minted + cumulative_service_minted + cumulative_faucet_minted - cumulative_burned`.
`ReconcileBurns` only reconciles shortfalls, and a faucet mint is never one. A genesis export carries
both counters; a nonzero `cumulative_faucet_minted` also stops `InitGenesis` from treating an epoch-1
export as a fresh genesis, so the genesis supply is not recomputed and double counted. The module
emits a `faucet` event with `recipient`, `amount` (for example `1000000000000norama`) and `signer`.

**Limits of the bounds.** The cooldown is per recipient, so a signer sending to fresh addresses is
bounded only by `faucet_epoch_cap` per epoch. The per-recipient cooldown records are kept in state,
never pruned, and not exported: a re-imported genesis starts with no cooldowns (the cap counters are
exported). Both are acceptable for a test network and the reason the faucet exists only there.

**A running chain does not gain it.** The message service and the faucet parameters are new state
and a new message type; a chain started from an earlier binary has neither, and there is no upgrade
handler for them. A stagenet or devnet gets the faucet from a new genesis (`deploy.sh reset` then
`deploy.sh up`, which enables it by default; see [DEV_DEPLOY.md](DEV_DEPLOY.md)).

### Tracking burns from elsewhere in the chain

`x/emission` has no burn path of its own, but other modules do - `x/slashing` burns a validator's
bonded or not-bonded stake on a double-sign or downtime slash, and `x/fees`' ante decorator burns
100% of every transaction's base fee (see "`x/fees`" below). Since `x/emission`'s own `BeginBlock`
has already minted the block's validator share (and updated `cumulative_minted` to match) by the
time `EndBlock` runs, `x/emission`'s `EndBlock` (`Keeper.ReconcileBurns`, run last in `app.go`'s
end-blocker order) compares live bank supply against
`genesis_supply + cumulative_minted + cumulative_development_minted + cumulative_service_minted + cumulative_faucet_minted - cumulative_burned`: any shortfall it finds must be a burn
that happened elsewhere this block, and gets added to `cumulative_burned`. This keeps the supply
invariant holding without `x/emission` needing a direct dependency on `x/slashing` or `x/fees`.

### Queries

`oramad query emission ...`:

| Command | Returns |
|---|---|
| `params` | the genesis-only `epoch_duration`/`min_blocks_per_epoch`, `allow_bootstrap_stake` and the `faucet_*` parameters |
| `current-epoch` | the live `EpochState`: current epoch, when it started, cumulative minted/burned/genesis supply |
| `schedule-at [epoch]` | that epoch's maximum mint and its four-way split |
| `cumulative-minted` | all-time cumulative minted and burned |
| `supply-cap-so-far [epoch]` | the schedule's maximum possible cumulative supply through that epoch |
| `invariants` | runs the two supply invariant checks below and reports both |

### Invariants

1. **Minted matches the schedule exactly:** `cumulative_minted` must equal exactly
   `CumulativeValidatorMinted` for the epochs already completed - not merely "no more than", since
   `CloseEpoch` mints that exact amount unconditionally every time an epoch closes. When
   `x/houses` has enacted a split, the target is the canonical cumulative plus
   `validator_split_delta`. Every `CeilingRecord` is checked the same way: its four amounts must
   match `SplitEpochMintAt` for its epoch and the split recorded on it exactly.
2. **Supply matches minted:** `bank_supply == genesis_supply + cumulative_minted + cumulative_development_minted + cumulative_service_minted + cumulative_faucet_minted - cumulative_burned`,
   with `cumulative_burned` kept current by `ReconcileBurns` (above). `cumulative_development_minted`
   is zero until `MintDevelopmentSpend` runs; `cumulative_service_minted` is zero until a storage
   or relay payment is minted; `cumulative_faucet_minted` is zero unless the test-network faucet
   (below) has run. In one line: `supply == emitted - burned`, where emitted is the genesis
   supply plus every epoch, development, service and faucet mint.

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
balance to self-bond a validator or delegate at all. x/staking's Msg service is therefore wrapped
(`chain/app/staking_topup.go`): while `MsgCreateValidator` or `MsgDelegate` executes, and before
the staking handler spends anything, the signer's own shortfall (the declared bond amount minus
their current spendable bank balance) is moved from that same signer's earnings into their bank
balance (`x/fees` `FundSpendFromEarnings`). `x/nodes` `MsgBondNode` does the same inside its handler,
after its own checks. The top-up runs **inside the message**, not in the ante chain, on purpose: BaseApp
writes ante state even when the message then fails, and a PostHandler cannot undo it either (its
writes to a failed message's branch are dropped), so an ante top-up would turn earnings into a
spendable bank balance for a delegation to a missing validator or a bond the handler rejects. A message
handler runs in the message's own cache branch, which BaseApp discards when the message fails, so a
failed message (or a later failed message in the same tx) reverses its top-up with everything else,
and no acceptance predicate has to be duplicated: the staking or nodes handler decides. Bond
messages in one tx are topped up one after another, each against the balance the earlier ones left.
The top-up happens only when the signer's earnings cover the whole shortfall; otherwise nothing is
debited and the message fails with the ordinary insufficient-funds error. It only ever moves an
address's own earnings into its own bank balance; a tx naming someone else's address fails
signature verification.

The same decorator funds a signer's **own storage deals and tokens** (C2 item 4). It is built with
`OwnFundsQuoter`s, and a module that pulls the signer's money from the bank balance prices its
messages: `x/storage` prices `MsgCreateDeal` (the per-deal fee plus price x replicas x duration escrow)
and `MsgExtendDeal` (the escrow of the extra epochs, only for the deal's own client and never a
protocol deal); `x/token` prices `MsgCreateToken` (creation fee plus the metadata deposit).
A `MsgCreateDeal` with a `granter` is not priced: the grantor's money pays, never the signer's earnings.
The top-up is the shortfall against the quote, all or nothing, exactly as for bonds. Tree and node
deposits need nothing here, because `LockDeposit` already falls back to earnings. `x/market` bid escrow
and `x/houses` bonds are not covered.

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
hysteresis streak), `validator-power [valoper-address]`, `invariants`. `invariants` reports whether
`x/power`'s module account is empty: it only passes an epoch's validator share through inside one
call, so a balance left there between blocks is a stranded amount. `validator-power` returns the last
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
bank balance is topped up from that same signer's earnings while the message executes (security
review B8; see "Outsiders can bond from earnings" under "`x/power`" above), so the fee is settled
first and a failed message reverses the top-up.

Feegrant sponsorship still works exactly as it does with the stock decorator (a fee granter, if one
is set and authorizes it, pays instead of the signer) - except that, as above, a granter-sponsored
tx's base fee may only be paid from bank, never from anyone's earnings.

**Simulation** (`--gas auto` and similar): before either the local minimum-gas-price policy or the
base-fee check above (both of which require a real, already-known fee/gas that a simulation is
trying to discover), `FeeDecorator` runs the same settlement logic on a branched, discarded context
(`ctx.CacheContext()`) purely so its gas consumption is reflected in the estimate, swallowing any
error (insufficient funds, an unresolvable payer/proposer) since the guessed fee/gas is not final
yet - mirroring stock `x/auth/ante`'s own `!simulate` guard around its equivalent fee check. While the
simulated transaction declares no gas limit (gas 0, the usual `--gas auto` request), its whole declared
fee, and at least one norama, is settled as base fee, as the delivered transaction pays it: counted as a
tip, which only a bank balance pays, or left empty, it would skip the gas of paying the base fee from
earnings for a payer with no bank balance.

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

Earnings today pay **tx fees** (the ante decorator), fund the signer's own **bond** and **storage deal and token fees** (inside the
message handlers: staking, `x/nodes` `MsgBondNode`, `x/storage` `MsgCreateDeal`/`MsgExtendDeal` for
the signer's own funds, never a grantor's, and `x/token` `MsgCreateToken`; each calls `FundSpendFromEarnings`
after its own checks, so a failed message reverses it) and fund the signer's own **state
deposits** (`LockDeposit` takes the bank balance first and the shortfall from that same owner's
earnings). `x/shielded`'s `MsgShieldEarnings` moves the signer's own earnings into the shielded pool (never fee-only balances). The only
message that moves earnings to another address is `x/nodes` `MsgFundHotKey`, and what it moves is
not earnings any more: `Keeper.FundFeeBalance` debits the operator's earnings and credits a separate
**fee-only balance** (`FeeBalances`) of the hot key registered on the operator's own node. The first
funding also creates the hot key's account, since an address with no account cannot sign; a later
funding leaves the account and its sequence as they are. A fee-only
balance can pay a transaction's base fee (`SettleFee` draws it after the bank balance and before
the payer's earnings) and nothing else: it is never bonded, shielded, put into a deposit, used for
a tip, drawn through a fee granter, or moved on. It is backed by the same `fees` module account, and
the invariant is now "earnings + fee-only balances == the fees module balance". Genesis carries
`fee_balances`; `oramad query`-side gRPC `FeeBalance` reads one. The hot key must also have proved
possession of itself (see `x/nodes`), so the address is not one the operator merely named.

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
registered and does not lock a listing deposit. A sale credits earnings. `oramad query market
invariants` checks that the market module account holds exactly the open bids.
`x/storage` is registered. Its deal escrow is its own module account,
not this deposit ledger. Contract storage is metered by `x/wasmpolicy` and locks its deposits here
(`LockDeposit`, `ReleaseDepositPart`): see "`x/wasm`: contracts".

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
- transfer hook names a CosmWasm contract (`MsgCreateToken.transfer_hook`, a bech32 address that must
  already be a contract; a build without the contract VM refuses a hook). On every `MsgTransfer` of the
  token the chain calls the contract's `sudo` entry point with
  `{"transfer_hook":{"denom","from","to","amount"}}` under a gas meter capped at 100_000
  (`types.TransferHookGasCap`). The contract returning an error refuses the transfer; asking for more gas
  fails it with "transfer hook exceeded gas cap"; either way nothing moves, and what the contract wrote is
  dropped. Its state writes are kept only when the transfer succeeds. The address is fixed at creation and
  can only be renounced.

Renouncing freeze or pause does not clear an existing freeze or the paused flag. Only the
permanent delegate can renounce that capability. `MsgSetShieldable` may be signed by anyone. It
sets a one-way flag and is refused while freeze, permanent delegate, or pause is still held.
There is no shielded transfer of a token here, and no on-chain verified registry: `x/shielded`
shields only ORAMA (multi-asset is off). A shieldable token still moves by the public
`MsgTransfer` bank send.

`MsgTransfer` does that send after the pause, non-transferable, signer, and freeze checks.
`from` must be the signer unless the signer is the current permanent delegate.

**The bank send restriction.** The same powers hold on every other path that moves a token, because
`app.go` appends `Keeper.SendRestriction` to the bank keeper: `x/bank` `MsgSend` and `MsgMultiSend`, a
contract's `BankMsg` and attached funds, and any module send. Such a send of a factory denom is refused
when the token is paused or non-transferable, when it has a transfer fee or a transfer hook (those run
only in `MsgTransfer`), or when the sender or the recipient is frozen. The restriction lets through a
send with the `token` module account on either side (its own mint, burn and fee burn) and the bank send
inside `MsgTransfer`, which has made the checks itself. A factory-looking denom with no token record
is not governed. The permanent-delegate move exists only in `MsgTransfer`.

State is one record per token plus one key per frozen account. A transfer is a single bank send,
plus a burn when a fee is due. The invariant walks every token, the same shape as x/fees'
deposit walk.

## `x/cnft` and `x/market`: compressed NFTs and the royalty market

`chain/x/cnft` and `chain/x/market` implement plans/open-network/track-c-chain.md C11. Both are
registered and neither has an admin key.

**Trees.** `MsgCreateCollection{name, royalty_bps}` makes a collection (royalty at most 10,000 bps).
`MsgCreateTree{collection_id, depth, buffer, canopy}` is signed by the collection's creator. Depth is
1 to 30, the changelog buffer 1 to 2,048 roots, the canopy 0 to 14 and below the depth. It locks a
state deposit through `x/fees`, one norama per byte of a stated state model (header, one changelog
slot per buffer entry, the rightmost path, the canopy), so the deposit is a pure function of the
three numbers (`types.TreeDeposit`). A tree keeps its root, the changelog ring, the rightmost path and
the canopy. A proof built against any root still in the ring is fast-forwarded through the changes
since, so several updates to one tree in one block all land; a proof against a root that has left the
ring, or one the update of another leaf invalidated, fails (`TestStaleProofAndRootOlderThanBuffer`).
Many trees per collection are the intended shape, because writes to one tree serialise.

**Leaves.** A leaf is the SHA-256 of `types.EncodeLeaf`: asset id, owner, delegate, metadata CID and
creator hash, each as a 4-byte length and the bytes, then the nonce (8 bytes) and the `hash_id` (4 bytes,
1 for SHA-256), all big-endian. The creator hash is the SHA-256 of the collection creator's address
bytes. Every owner, delegate and nonce change rewrites the leaf with the nonce plus one.

**Messages.** `MsgMint` (the tree's creator, up to 64 leaves, nonce 0) appends. `MsgTransfer`,
`MsgBurn` and `MsgUpdateMetadata` carry the current leaf and a Merkle proof and are signed by the leaf's
owner or its delegate. `MsgDecompress` (the owner) removes the leaf and writes a
`DecompressedAsset` record, which is the on-chain handle a contract can hold; `MsgCompress` appends it
back with the nonce plus one. `MsgRecordSnapshot` (the tree's creator) records a CID and the tree's
sequence number. A Merkle proof is a root, a leaf index and the siblings.

**Data availability.** The chain does not keep leaves, and ABCI events are not in consensus (the C0-6
record). Every leaf is rebuilt from the committed transaction bytes: `TestRebuildTreeFromTxBodies`
replays only message bodies and gets the on-chain root. The index (`chain/indexer`) does the same.
`x/cnft` and `x/market` emit no events.

**Market.** `x/market` has listings and bids. `MsgList` (the owner, with a proof) records the seller,
the price and the collection's royalty creator and rate at the time of listing; an asset has at most
one listing. `MsgBid` escrows the bid in the `market` module account; `MsgCancelBid` and
`MsgCancelListing` refund. `MsgSettle` either buys at the listed price (the signer is the buyer and
pays from the bank balance) or, signed by the seller, accepts a bid (`bid_id` set). It proves the
seller still owns the leaf, moves the leaf to the buyer (the delegate is cleared), refunds every other
bid, and credits the royalty (`price * royalty_bps / 10,000`, rounded down) to the collection creator's
**earnings account** and the rest to the seller's earnings account. Nothing is paid to a bank balance, so
the market cannot be a public payment rail. A transfer outside the market pays no royalty. `oramad query
market invariants` checks that the module account holds exactly the open bids.

**Not built.**
- Tree snapshots are recorded as CIDs by the tree's creator. The chain does not open protocol
  `PUBLIC_PIN` deals for them every N epochs and does not check that a CID is pinned: a protocol deal
  needs a piece commitment, which only the party holding the snapshot bytes can compute, and the
  chain cannot derive one from its rightmost path.
- Metadata is a CID. No `PUBLIC_PIN` deal is opened or prepaid at mint.
- Shielding a cNFT waits for multi-asset shielding.

## `x/houses`: two-house governance

`chain/x/houses` implements plans/open-network/track-c-chain.md C5 and decisions D17 and D18.
It is registered. `EndBlock` closes elapsed votes and executes elapsed timelocks. An operator is eligible only with a /16 network
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
mint refuses the amount, the proposal is marked failed and nothing is credited. Execution runs on a
cache context, so a refused action of any kind leaves no partial write and the proposal is
marked `FAILED` with its reason.

### Governance enactment

A passed structural proposal changes a module's behaviour only once its timelock ends and someone
executes it (`EndBlock` does, and so can any account with `MsgExecuteProposal`). Each outcome is
consumed like this:

| Outcome | Consumer | What changes |
|---|---|---|
| Emission split | `x/emission` reads `x/houses`' enacted split when it closes an epoch (`emissionSplitSource`) | The next closed epoch mints and records ceilings at the new percentages. See "The split, and what actually gets minted" |
| Relay reporters | `x/relay`, through its own `MsgUpdateReporters` handler | `relayReporterEnactor` computes the new set from the current one plus `add` and `remove`, then calls the handler with `relaykeeper.WithAllowReporterChange`, the only way that message is accepted. A change that would empty the set, or name a bad address, fails the proposal and leaves the set as it was |
| Code-upload allow-list | `x/wasmpolicy` reads `Keeper.CodeUploadAllowed` on every `MsgStoreCode` before `upload_sunset_height` | A code blob whose SHA-256 (of the uncompressed wasm; a gzip upload is decompressed, capped at 4 MiB) is on the list may be stored before the sunset. Any other hash stays closed. Removing a hash closes it again. After the sunset every store is allowed anyway, and nothing can move the sunset |
| Software upgrade | The SDK `x/upgrade` module | `upgradeScheduler` calls `UpgradeKeeper.ScheduleUpgrade` with the plan name and height when the timelock ends. A height already past fails the proposal. At the plan height `x/upgrade`'s PreBlocker runs a registered handler, or halts the node with "UPGRADE NEEDED" so its operator (cosmovisor, see "Running `oramad` under cosmovisor") swaps in the new binary. `x/upgrade` is wired; only its `MsgSoftwareUpgrade` stays unreachable |
| Development spend | `x/emission` and `x/fees` | Mints against the epoch's development ceiling and credits the recipient's earnings |
| M activation and `m_max` | none | Recorded on `Enacted` only: `x/power` computes no useful-work multiplier yet (`useful_work_multiplier` is a coded 1.0), so there is nothing to read it |
| Adapter allow-list | none | Recorded on `Enacted` and readable with `Keeper.AdapterAllowed`. The shielded adapter path that would consult it is not built |

A software upgrade or reporter proposal fails, and records why, when its consumer is not wired
(`WithEnactors`), so an outcome that nothing reads is never reported as enacted.

### Governance parameters are genesis parameters (G4)

Everything that shapes governance is a `Params` field with a coded bound checked by
`Params.Validate` (at `InitGenesis`, in `genesis validate`, and on a parameter proposal):

| Parameter | Default | Bounds |
|---|---|---|
| `token_quorum` | 0.4 | 0.334 to 0.667 |
| `token_pass_threshold` | 0.5 | 0.5 to 0.667 |
| `voting_period_seconds` | 7 days | 1 day to 28 days |
| `house_bond` | 1000 ORAMA | 1 ORAMA to 1,000,000 ORAMA |
| `max_eligible_per_prefix16` | 3 | 1 to 21 |
| `max_eligible_per_asn` | 5 | 1 to 21 |
| `min_house_size` | 21 | 21 to 101 |
| `veto_window_seconds` | 7 days | 7 days to 28 days |
| `parameter_timelock_seconds` | 14 days | 14 days to 60 days |
| `upgrade_timelock_seconds` | 60 days | 60 days to 180 days |
| `spend_timelock_seconds` | 7 days | 7 days to 30 days |

The last five are fixed at genesis: a parameter proposal cannot move them (nor `bootstrap_exit_stake`),
and each floor is the plan value, so a genesis may lengthen a delay but never shorten it or open
governance to a smaller house. The other rows move only inside their bounds by a parameter proposal.
These fields are new: houses `Params` stored by an older binary decode them as 0 and fail `Params.Validate`, and `x/houses` has no migration (`ConsensusVersion` 1), so a chain that ran the older binary must restart from a new genesis. No production chain exists, and stagenet and devnet are reset.
`TestParamsValidate_bounds` tests every bound at the minimum, the maximum, and one past each. Still
coded, and not parameters: the 3% delegated-vote cap, the 30% veto share, the 90 service days, the 7
/16 networks and 5 ASNs the structural tier needs, and the 64 active proposals.

### Ossified rules and invariants

Ossified rules have no message and no field a message can set: the emission schedule and tail,
the burn rule, privacy-by-default, and the absence of freeze, blacklist, halt, pause, circuit
breaker, multisig or authority. `bootstrap_exit_stake` is genesis-only. `TestNoMessageReachesAnOssifiedField`
walks every `sdk.Msg` and rejects a field that is not on the allow-list.

The bond invariant is: sum of locked house bonds equals the `houses` module account balance.
Queries are `oramad query houses params|proposal|tiers|invariants`.

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
  registration); a hot key that must differ from the operator, is not any operator and not another
  live node's hot key, and proves itself with a `hot-key` binding (below); service-key bindings; public
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
operator's own earnings fund any shortfall first, inside the handler and after its checks (see
"Outsiders can bond from earnings"), so an operator whose payouts sit in earnings can bond a node
with a zero bank balance, and a bond the handler rejects (unknown or foreign node, a role the node
lacks, a retired node) reverses the top-up with the message.
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
down to the new backing and then clamps reserved bytes down to the clamped declaration: a slash
is a penalty and never fails because the node is busy (x/storage, which keeps the reservations of
its own replicas, releases the replicas the smaller declaration cannot hold). `Jail` / `Unjail` are keeper methods: a jailed node is not active and leaves the
capacity index. `ReserveCapacity` / `ReleaseCapacity` move free capacity for a later storage
module. `CreditRoleBond` increases a role bond only when the module account already holds the
coins; it does not mint.

**The hot key proves possession.** The node's bindings must contain exactly one `hot-key` binding: a
secp256k1 key, signed over the ordinary binding statement
(`orama-global-bind-v1|chain-id|operator|hot-key|hex(pubkey)`), whose account address is the node's
`hot_key`. `MsgRegisterNode` and `MsgUpdateNode` enforce it (an update that changes the hot key must
carry bindings with the new key's own binding, and an update that replaces bindings must keep the
current hot key's), and genesis validation enforces it for live nodes. So an operator cannot set
`hot_key` to an address it does not control. A hot key also cannot be an operator, another live
node's hot key (the `HotKeys` index, and the service pubkey is already unique network-wide), and an
account that is a live node's hot key cannot register as an operator. Retiring or tombstoning a node
releases its hot key from the index; its pubkey stays revoked. `orama global bind --service hot-key`
with the hot key's secret produces the binding.

`MsgFundHotKey{operator, node_id, amount}` (C2 item 5) moves `amount` from the operator's own
earnings account to the **fee-only balance** of `node.hot_key` (see "`x/fees`"). The message has no
destination field: the target is always the hot key registered on the operator's own node, and that
key proved it holds itself, so earnings cannot be aimed at a third party. It fails for a node the
signer does not operate, an unknown, retired or tombstoned node, a zero amount, or an amount above
the operator's earnings. It follows a rotated hot key (`MsgUpdateNode`). `oramad tx nodes
fund-hot-key [node-id] [amount-norama]` builds it. The bank balance is not involved, and the hot
key can spend the balance on base fees only.

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
- **One host classifier** (`chain/netclass`) decides what is public and what is a literal IP, and
  x/nodes validation, `LiteralIPs`, `NetworkOf` and the repair fetcher all use it, so a spelling one of
  them reads as a name cannot be read as an address by another. It parses with `netip` (no zone ids,
  no leading zeros) and refuses any host whose last label is a number (`2130706433`, `0x7f.1`, `127.1`,
  `010.0.0.1`, `01.2.3.4`), because a resolver reads those as IPv4 addresses. Every trailing dot is
  dropped before a name is classified (`127.1.` is `127.1`), a host with an empty label (a leading
  dot, `..`, more than one trailing dot) is refused, and so is any host that is not printable ASCII
  (an internationalized name is given in its `xn--` form). Names of the local machine or a private
  network are refused: `localhost`, `localhost.localdomain` and anything under `.localhost`,
  `.localdomain`, `.local`, `.internal`, `.lan` or `.home.arpa`, and so is any single-label name with no
  dot (`intranet`), because a resolver may complete it with a search domain into a private host. A host with a zone id, or
  a schemeless endpoint carrying a path, query or fragment (`10.0.0.1/x`), is refused, and the address in
  an `/ip4/` or `/ip6/` multiaddr part must be a literal of that family. An IPv4-mapped IPv6 address
  is its IPv4 form. Refused ranges: `0.0.0.0/8`, `10/8`, `100.64/10`, `127/8`, `169.254/16`, `172.16/12`,
  `192.0.0/24`, `192.0.2/24`, `192.88.99/24`, `192.168/16`, `198.18/15`, `198.51.100/24`, `203.0.113/24`,
  `224/4` and `240/4` (with the limited broadcast); and for IPv6 everything outside `2000::/3`, plus
  NAT64 (`64:ff9b::/96`, `64:ff9b:1::/48`), `100::/64`, `2001::/23`, `2001:db8::/32`, `2002::/16`,
  `3fff::/20`, `5f00::/16`, `fc00::/7`, `fe80::/10`, `fec0::/10` and `ff00::/8`. Documentation
  addresses are therefore not valid endpoints on this chain; tests use routable ones.
- **ASN.** `MsgRegisterNode.asn`, and `MsgUpdateNode` with `set_asn` (0 clears it). The value is
  stored on the node. Zero, `AS_TRANS` (23456), documentation ranges (64496-64511, 65536-65551),
  private-use ranges (64512-65535, 4200000000-4294967295) are refused at the boundary and in
  genesis validation. The spec gives no on-chain source for an ASN (an oracle would be a new trust
  point) and this module implements no challenge or dispute for a wrong one.

**What the chain enforces, and what it does not.**

- *A literal-IP endpoint belongs to one live node.* `LiveIPs` maps each literal IP (normalised, so
  `https://203.0.113.10:443` and `/ip4/203.0.113.10/tcp/4001` are one address) to its node;
  registering or updating onto another live node's address fails with `ErrEndpointTaken`, and
  retiring a node releases its addresses. This stops two nodes of one or many operators from claiming
  the same address. It does **not** prove the node controls the address, and hostnames are not
  indexed.
- *Identity takes effect after a lock.* Each node records `identity_since_unix`: when it registered, or
  when its ASN or derived /16 last changed (changing region, or moving within the same /16, does not
  restart it). `NodeNetwork` and the operator-house view report a node's identity only once
  `network_identity_lock_seconds` have passed since (default 14 days, the parameter-change timelock;
  0 turns it off; genesis nodes keep the value the genesis gives them). Inside the lock the node counts as
  unidentified: it cannot fill a protocol-deal slot and does not count for a house's /16 and ASN
  caps. House eligibility therefore counts identity as of a rolling snapshot, an identity that was
  already in place a lock ago, so identity cannot be moved to fit a slot draw or a vote.
- *ASN is still unverified.* It is whatever the operator declared, checked only against reserved
  ranges. IP control is not proven on chain, so an operator can declare an address it does not run,
  or a real ASN that is not its own.
- What the caps guarantee: one operator's declared identities cannot be swapped in and out quickly,
  each address is one node's, and distinct operators are still required. What they do not
  guarantee: that the declared networks are real or distinct. Economics (a bond and a deposit per
  node) and the lock are the bound; a wrong declaration is not detected or disputed.

The keeper reads identity through `NodeNetwork(node id)`; `chain/app/storage_view.go` gives x/storage
the same values, and `chain/app/houses_view.go` gives x/houses an operator's identity. A slot records
the /16 and ASN at assignment time, so a later endpoint change does not move an existing slot.

### Feeding x/storage

x/storage assigns slots only to nodes in its own tracked set (`x/storage` `Nodes`). `x/nodes`
keeps that set current without a hook or a per-block scan: every write to a node with the STORAGE
role (`saveNode`: bond, unbond, jail, unjail, retire, slash, capacity, endpoints, hot key) queues
its id in `StorageDirty`, and imported genesis nodes are queued the same way. At the start of its
`BeginBlock`, x/storage drains the queue (`NodeView.TakeStorageChanges`) and reconciles each id:

- **Tracked** while the STORAGE role is bonded at `min_bond` and the node is not jailed, retired
  or tombstoned (`Keeper.StorageEligible`, which is `IsRoleActive(STORAGE)`). A node bonded only on
  another role is not tracked and is never picked.
- **One broken node does not halt the chain.** Each id is reconciled in its own cache branch. If its
  record cannot be read or reconciled, that node's work is rolled back, the id is re-queued for the
  next block, and a `storage_node_sync_failed` event carries the reason. Only a failure to read or
  re-write the queue itself fails `BeginBlock`. See "One item's failure never halts a block" below.
- **Untracked** when it stops qualifying and holds no replicas. A node that still holds replicas
  stays tracked, because challenge sampling and settlement read its state, and is re-queued each
  block until its last replica is released, evicted or expired. Assignment already skips it
  because it no longer qualifies. A probation record deposit is released on untracking.
- Tracking never resets the storage state of an already tracked node.

The tracked set is derived from x/nodes, so genesis export/import round-trips it: x/storage exports
its `NodeState` rows, and x/nodes re-queues every imported STORAGE node so the first block
reconciles them.

**Probation nodes.** A fee-free registration has the STORAGE role and no bond, so it is never
`StorageEligible`. `Keeper.StorageProbation` (`NodeView.IsProbation`) holds for a node that is
Registered (not jailed, retired or tombstoned), has the role and a zero STORAGE bond. x/storage tracks
such a node with `Probation` set and its `registered_epoch`; a node with a partial bond is not one. A
probation node:
- takes only protocol-deal slots, at most `probation_slots` each and under the per-operator, /16 and
  ASN caps; it never takes a PRIVATE or PUBLIC_PIN user deal, because it has no bond to slash;
- is not counted as an active operator, so it does not open the subsidy ramp;
- locks the `probation_deposit` record deposit from its first credited earnings, progressively: each
  credit adds `min(probation_deposit - held, credited)` to the deposit (the deposit id
  `storage/probation/<node id>`), so a first payout smaller than the deposit is never asked for money
  the node has not earned. At the launch defaults the first credit is 900 (a protocol price of 1000
  less the 5% burn and 5% archive share) and the second tops the deposit up to 1000. `NodeState.deposit_locked`
  means a deposit row is open, whether or not it is full. A deposit that cannot be locked or topped up
  is rolled back on its own: the payout that triggered it stands, and the next credit tries again;
- is jailed at `probation_expiry_epochs` if it proved nothing, and graduates if it proved storage
  (the deposit is recovered), after which it holds no slots until it bonds;
- graduates at once when it bonds, becoming an ordinary provider.

The caps count slots at assignment and release them at detach for a node still on probation, so a node
that leaves probation (graduates, bonds or is jailed) releases the counts of the slots it holds at
that moment. A probation node that graduated without a bond stays tracked, without slots, and does not
start a second probation.

A protocol-deal slot additionally needs a node with a known /16 and a declared ASN; a node without
either can still hold PRIVATE and PUBLIC_PIN user deals, where only distinct operators are required.

**One item's failure never halts a block.** Returning an error out of `BeginBlock` or `EndBlock`
fails `FinalizeBlock` on every validator, so a per-node, per-deal or per-user record that cannot be
processed must not do it. The per-item work of x/storage (node reconciliation, each settlement row,
deal assignment, accept windows, challenge opening, deal expiry, probation expiry, the scheduled
protocol deals, scoring a closed challenge, counting active operators, the penalty of a miss) runs on
its own cache branch: a failure rolls that item back, emits `storage_item_failed` (`kind`, `subject`,
`consecutive`, `error`) and the block goes on.

Only failures that are about the one item are isolated: an error marked `ErrItemRejected`. x/storage
marks its own consistency failures (an item that cannot be paid, a malformed record, a deal or slot
the item points to that is gone) and, at its collaborator boundary, only the refusals a collaborator
makes about the one item: funds that cannot cover the write (`ErrInsufficientFunds`), a blocked or
unauthorized account (`ErrUnauthorized`) or a record that is not there (`ErrNotFound`, for example a
deposit that does not exist), which bank and x/fees return, and a node that is gone or not active for
the operation (`x/nodes` `ErrNotFound`, `ErrNotActive`), which the app's x/nodes adapter marks. Every
other error, a collaborator's included, and every error that wraps a collections encoding fault even
when a collaborator reports it, is a fault of the state machine and fails the block: a bare
`collections.ErrNotFound` from x/storage's own indexes (`Reserved`, `ReplicaCount`, `ReplicaAt`,
`Params`, `QueueTail`, `Nodes`), reading or writing the module's own counters, or decoding what they
hold, means the state can no longer be trusted. What happens to the failed item depends on it:
- a settlement payout row is **queued again** behind the rows already waiting, with its `attempts`
  raised and `not_before_epoch` set to the next epoch (`storage_settlement_requeued`), so its retries
  are spaced by epochs and a failure that clears by itself costs the operator nothing. A row that is
  not due yet is moved to the back of the queue unchanged, at the cost of one of the block's
  `max_settlements_per_block`. On the `MaxSettlementAttempts`-th failure (5, a constant: it only
  bounds how long a broken row can occupy the queue) the row is **dropped**
  (`storage_settlement_dropped`): it pays nothing, the deal keeps the escrow the row would have
  moved (returned to the client when the deal expires), and the mint reserved for it is burned so
  the storage account keeps holding exactly the mint payments still queued and x/emission's supply
  invariant keeps holding. The burn must succeed; if it fails the block fails, because a mint left
  behind breaks the storage invariant. A **miss** row is never queued again or dropped: its miss
  counter and eviction read and write only x/storage's own state and are applied the first time the
  row is processed (if the deal or slot it names is gone, the row is counted as one item's failure
  and finished, since it carries no payment). Only its penalty can be retried and dropped, in a
  row of its own (`penalty_only`) on the same schedule;
- node reconciliation, deal assignment, accept windows, challenge opening, deal expiry and probation
  expiry are **retried** in the next block;
- a scheduled protocol deal that cannot be created still counts as scheduled for its epoch.

**A miss is always recorded and always evicts at the threshold.** A missed challenge raises the
slot's consecutive misses, a slot at `miss_threshold` is evicted and repaired, and from the second
consecutive miss on the node is penalized. The miss is recorded and the slot evicted without any
collaborator, and the penalty runs afterwards in its own isolated branch (`slash` failures): one
that a collaborator refuses becomes a `penalty_only` settlement row retried once per epoch and dropped
after `MaxSettlementAttempts` (`storage_settlement_dropped` with `penalty_only=true`), so a node whose
penalty fails still loses the slot it does not serve. The penalty is `slash_fraction` of one epoch's price of the missed deal:
- a bonded node is slashed through x/nodes, which clamps its declared capacity to what the smaller
  bond backs; x/storage then evicts the node's replicas, highest replica index first (the index is swap-removed, so this is not assignment order), until its reserved
  bytes fit the clamped declaration, so reserved never exceeds declared;
- a probation node has no bond: its record deposit is its stake and is burned (`SlashDeposit`, at
  most what it holds; a deposit burned in full is closed and the node earns a new one from its next
  credit, `storage_probation_slashed`). A probation node that has not earned a deposit yet has
  nothing to slash: its miss still counts and it is still evicted.

x/storage counts the consecutive failures per subject (a node id, or `deal/<id>`) and kind (`sync`,
`settlement`, `deposit`, `challenge`, `scoring`, `operators`, `probation`, `deal`, `slash`): the
`NodeFailures` query returns a node's counts (empty when it applies cleanly), the counts are in
genesis (`failure_counts`), and every 100th consecutive failure of a subject also emits
`storage_item_stuck` and an error-level log line, so a node that never recovers is visible to a
monitor. A success clears the count.

The same rule holds in the other modules' block hooks, each with its own marker for what is about
one item and with every other failure fatal: x/nodes skips a matured unbonding it cannot pay
(`nodes_unbonding_failed`, retried every block until it can be paid), x/power returns a validator's
reward it cannot pay to the emission module (`power_reward_failed`) so its own account still ends the
block empty, and x/houses retries a proposal it cannot advance in the next block, counting
`advance_failures` on the proposal and closing it as FAILED with the reason only after
`MaxAdvanceAttempts` (5) consecutive failures (`houses_proposal_failed` on each). A proposal whose
enactment is refused when its timelock ends is FAILED at once and is not retried: C5 records a
refused spend as failed, an enactment runs once against the state the vote was cast on, and a
proposer who wants the action again submits a new proposal.

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
RPC (`--rpc`, default `tcp://127.0.0.1:31001`; a co-located unit passes `tcp://198.18.0.2:31001`) via `chain/client/node`. That
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
- `monitor.json`: hot-key balance, unanswered challenges, bytes stored, and
  the deal slots (`held_slots`: bound to a stored piece; `pending_slots`:
  assigned and still waiting for the piece), in the fields
  `core/pkg/telemetry/report` reads.

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
per-address rate limit.

Public Kubo (`--ipfs-api`, `--ipfs-token-file`; the unit passes
`http://127.0.0.1:31011`, or `http://198.18.0.2:31011` on a co-located machine, and `/var/lib/orama-global/ipfs/api-token`). The client
(`chain/provider/kubo.go`) sends the bearer token only to a loopback `http` URL or the co-located namespace address `198.18.0.2`
and follows no redirect. The token allows only `add`, `cat`, `pin/add`,
`pin/rm`, `repo/gc` and the read-only `repo/stat` (`installers.PublicAPIAllowedPaths`). When it is
configured:
- Before it accepts a slot of a **PUBLIC_PIN or ARCHIVE** deal, the runner reads
  the deal's class from x/storage and pins the stored piece in the public Kubo.
  The bytes were already checked against the slot's piece root when they were
  stored. Each CID the piece was fetched by through `POST /pins` is pinned (at
  most 4 per piece). A piece with none is added with `ipfs add --pin
  --cid-version=1 --raw-leaves` (default chunker), so a client can compute that
  CID from the bytes. CIDs are recorded in the piece's metadata before any later
  step can fail.
- A **PRIVATE** deal's ciphertext never reaches the public Kubo. Roots are
  public on chain, so another deal could name a private piece's root as a
  PUBLIC_PIN. Before a public slot pins, the runner reads the class of every
  other slot on this node that holds or waits for the same root; if one is not
  public the slot is declined with `root held for a private deal`. A private slot
  that arrives after a public one unpins the piece and forgets its CIDs.
  `POST /pins` serves only roots whose every waiting slot is public.
- If the pin fails, the slot is retried every step and, `DeclineMarginBlocks`
  before the accept window closes, declined with the reason `public pin failed`.
  A CID on `<home>/denylist` is declined with `denylist`; the denylist compares
  the multihash, so a CIDv0 and the CIDv1 of the same hash are one entry (a
  different chunking is a different CID and is not matched). The piece is
  unpinned and discarded first (unless another waiting slot needs the root). If
  the unpin fails, nothing is declined and the next step retries, so with the
  public Kubo down the slot lapses with its accept window and the chain
  reassigns it.
- `POST /pins/<hex piece root>` with `X-Piece-CID: <CID>` (no body) fetches the
  piece through the public Kubo instead of taking an upload. The CID must be a
  CIDv0 or a base32 CIDv1 that is not on the denylist; the bytes must hash to the
  assigned root (otherwise `409`); a fetch failure is `502`. The fetch is bounded
  by `--max-piece-bytes`, by 30 s, by 2 fetches at once (separate from the 2
  upload slots) and by one fetch per root at a time (`429`). A CID already
  recorded for the piece is not fetched again. The endpoint is not
  authenticated, so a caller who names an assigned public root can occupy the 2
  fetch slots or that root's slot with CIDs that never resolve, and can record
  up to 4 valid CIDs of the piece's bytes before the client's own. Blocks Kubo
  fetched for a piece that failed the root check stay in its repo, unpinned,
  until the next GC run (every 6 hours): the disk that can be filled in one
  window is bounded by the per-address rate limit times `--max-piece-bytes`.
- Releasing the last slot that binds a piece unpins its CIDs before the bytes are
  removed; if the unpin fails the binding stays and the next sweep retries.
- Without `--ipfs-api` the provider does not pin, and nothing above happens.

ARCHIVE deals opened by `MsgCreateArchiveDeal` commit to the bundle file's `piece/`
root, so the slot's bytes are the bundle file. The archiver uploads them to each
assigned provider by `POST /pieces/<root>` (see "History archiver"); a provider
can also fetch them by `POST /pins/<root>` when the bundle is fetchable from IPFS. An ARCHIVE bundle's x/archive `bundle_cid` is CIDv1 raw sha2-256 of the whole
file. It is a hash of the file, not the root of a UnixFS DAG, so Kubo can serve
it by that CID only when the bundle fits in one block. A bundle above that is
fetchable by the CID that `ipfs add` returns for it, which is not the
`bundle_cid`.

### Repair delegate (`orama-global repair`)

`<home>/operator` holds the address that deals name as `repair_delegate`.
x/storage never assigns a slot of such a deal to that operator, and the
delegate refuses a deal where it finds one. It dials only public addresses
and follows no redirects, since provider endpoints are whatever a node
registered in x/nodes. "Public" is `netclass.IsPublic`, the same list x/nodes validates against, and
the provider root is rebuilt from the endpoint's scheme, host and path only (a query, fragment or
userinfo it carries is dropped). `<home>/deals/<id>.json`
(mode 0600) holds `{"deal_id": N, "repair_seed": "<hex>"}`. The delegate's
operator installs these files; no network path hands a seed to the delegate.
A file that is readable by others, malformed, short or named for another deal is logged as an
error every pass and its deal is not repaired; the other deals are repaired in the same pass.

Each pass, for every such deal:
- A slot that is assigned but not accepted, while another slot is active, is
  rebuilt: fetch the surviving replica, check its root, and apply
  `chain/storagekey.Rewrap` (strip one slot layer, apply the other).
- The result must hash to the new slot's root. If it doesn't, the repair seed
  is wrong and nothing is uploaded.
- The result is uploaded to the new provider.
- A new deal with no accepted replica is left alone.
- The delegate never recovers plaintext.
- Each restored slot is logged (`replica restored`) with `blocks_since_assigned`: the blocks between
  the chain assigning the replacement slot (the eviction) and the delegate's upload. That is the
  delegate's part of the time to restore the full replica count; the new provider's acceptance
  follows in a later block. The height is read after the upload: if it cannot be read, the restore
  stands, the failure is logged as an error and `blocks_since_assigned` is left out of the line.
  There is no metrics endpoint and no SLO threshold is enforced.

`TestRepairChaos_killedProviderIsEvictedAndTheDelegateRestoresTheReplica`
runs the whole path against the x/storage keeper: a provider stops, misses
evict its slot, the delegate restores it, and the new provider proves it.

### History archiver (`orama-global archiver`, `orama-global history get`)

The archiver reads each finalised range of the chain's `archive` param `range_blocks` (1000 at
launch, locked in G1) blocks over RPC. Ranges start at 1, 1+w, and so on. x/archive accepts only these
canonical ranges (`ErrNotCanonicalRange`): a range must start at k*w+1 and be exactly w blocks long, so
ranges never overlap and one operator cannot squat an arbitrary span or a slice of a neighbouring
range. The archiver reads w from the chain at start; `--range-blocks` defaults to 0 (use the chain's
value) and any other value must equal it, or the archiver refuses to start.

For each range, it writes `<home>/bundles/<start>-<end>.orbh`. This is not a
CAR file. The layout is magic `ORBH`, version 1, the start height and the
count. Each block follows as its hash, a length, and the `tendermint.types.Block`
protobuf.

It then submits `MsgAttest`, signed by `<home>/hot-key` for the node named
in `<home>/node-id`, with:
- the bundle CID: CIDv1, raw codec, sha2-256 of the file;
- the file's SHA-256;
- the block-hash Merkle root;
- the file's `piece/` commitment: root, real and padded leaf counts and byte size. The chain checks
  the shape (the leaf counts must be the ones the byte size implies) and refuses a file over the
  `archive` param `max_piece_bytes` (4 GiB at launch, locked in G1).

`<home>/cursor` is the last attested height, and a restart resumes after it.

x/archive tallies attestations per tuple (bundle CID, content hash, Merkle root and piece commitment),
so a wrong tuple on chain does not stop the archiver. When it finds the range already attested with a
different tuple, the archiver reads the range's blocks a second time and checks that they pack into the
same bundle (blocks that changed between two reads are not attested, and the pass errors), then keeps its
own tuple and attests it: the tuple that three operators agree on wins. Only a range that another tuple
has already won cannot be attested; the archiver writes `<home>/conflicts/<start>-<end>.json` with both
sets of values, reports `ErrRootConflict` once, and moves on to the next range. An archiver whose own
earlier attestation differs from what it reads now is a conflict too, and so is a range another tuple wins
after this archiver attested it (it stops following that range). When another key of the same operator
already attested a different tuple, the chain refuses this archiver's attestation for as long as that
stands (`ErrConflictingAttestation`, one tuple per operator per range): the archiver records the
conflict once the same way, moves its cursor past the range and does not retry it.

`history get --height H --from <home or http base>` loads the range's
bundle. It checks the file hash, each block's bytes against its header hash,
and the Merkle root against the x/archive record, and only then writes the
block.

**Archive deals.** After it attests a range, the archiver follows it (one file
`<home>/deals/<start>-<end>.json` holds the deal ids it opened that the range does not record
yet) until x/archive marks it archived. Each pass, for each such range:
1. Once a tuple has won the range (three distinct operators attested it), and if the range holds
   fewer than three live deals, counting recorded ones and its own pending ones, it computes the `piece/`
   commitment of the bundle file, checks it against the winning tuple's, and submits
   `MsgCreateArchiveDeal` for each missing one. Before a tuple has won it opens nothing and waits. The chain answers with the deal id in an `archive_create_deal` event. When the chain refuses one as over
   the range's allowance (`ErrDealsFull`, which happens when another archiver of the range was faster),
   the range has enough deals and the archiver moves on.
2. For every slot of its deals that x/storage has assigned to a node and the node has not accepted, it
   uploads the bundle file to that node's first http(s) endpoint in x/nodes, `POST <endpoint>/pieces/<hex root>`
   with `X-Piece-Root`. A provider cannot prove bytes it never received, so a slot nobody uploads to is evicted and the
   deal fails. Before any byte leaves the machine the slot's assigned piece root must equal the bundle's root, otherwise
   nothing is sent and the pass reports the mismatch. The client dials only public addresses (loopback, private,
   link-local and CGNAT ranges are refused after DNS) and follows no redirects. A provider answers 403 until its runner
   has read the assignment, so each slot gets four tries a pass (1, 2 and 4 seconds apart) and is tried again on the
   next pass for as long as the chain still assigns it to that node. A slot it uploaded is remembered in the deal
   file as `<deal>/<slot>/<node>` and is not sent again after a restart; a slot the chain moves to another node is a
   new key and gets the bundle too.
3. When x/storage has assigned a provider to one of its deals (a new deal is OPEN until the next block), it submits
   `MsgAttachReplicas` for it.
4. A deal that ended without a provider is dropped and replaced.

**`MsgCreateArchiveDeal`** (`archiver`, `node_id`, the range, and the bundle's piece commitment: root, real
and padded leaf counts, bytes) opens one protocol ARCHIVE deal in x/storage, through
`Keeper.CreateArchiveDeal`. Users cannot: `MsgCreateDeal` still refuses the ARCHIVE class. The chain
sets the price (`protocol_price_per_epoch`, per replica) and the duration
(`archive.ArchiveDealEpochs`, 3650 epochs, so ten years at one epoch a day); the archiver
chooses neither, and it does not choose the content either: the message's commitment must equal the
winning tuple's (`ErrWrongPiece`), the range must be decided, that is, one tuple attested by at least
three operators (`ErrQuorumPending`, the threshold that archives it), and the deal opens with the
winning commitment. One archiver therefore cannot fill a range's three deal slots with content nobody
attested, and a first attester that lost the range gets no deal for its bytes. The signer must be the hot
key of an active ARCHIVER node whose operator attested the winning tuple, and a range may hold at most
`MaxLiveDealsPerRange` (3) live deals. The deal is recorded as
pending for the range (a collection of (start, deal id)) and reserved for it in the `AttachedDeals`
index, so it cannot back another range. Pending deals that x/storage no longer runs are forgotten when
the next one is asked for, which frees their place. When the deal ends, `MsgCreateArchiveDeal`
drops it from the range and a new one can be opened, so history is renewed by the archivers rather than
lapsing. Pending state is not part of genesis export; an export drops the reservation of a deal
that was not yet recorded.

**Attestation tally.** A range is undecided until one tuple has been attested by three distinct
operators. Until then every tuple attested for it is a candidate, with its own archivers and operators;
the `RangeRecord` keeps them in `candidates` (at most `max_candidates_per_range` of them, 4 at launch,
locked in G1; a tuple beyond that is refused with `ErrCandidatesFull`) and its winning-tuple fields are
empty. The first tuple to reach three operators wins: the range is `decided`, the winner's fields,
archivers and operators become the range's, and the other candidates are dropped. A decided range takes
only the winning tuple (a different one is refused with `ErrWrongRoot`, `ErrWrongBundle` or
`ErrWrongPiece`, naming the field that differs) and more operators attesting it as extra evidence up to
64. An operator counts toward one tuple per range: a second attestation of a different tuple by the same
operator, or by another of its nodes, is refused (`ErrConflictingAttestation`), so an operator cannot
fill the candidate set. A candidate none of whose attesting nodes (`Candidate.node_ids`, parallel to its
archivers) still has an active ARCHIVER role bond (lost the role, jailed, retired or gone) is freed when
the next attestation of the range arrives: x/archive reads the nodes at that moment, so nothing runs
per block, and the operators of a freed candidate may attest another tuple. Residual: a coalition of as
many distinct bonded operators as the candidate cap can still keep an honest tuple out of an undecided
range while they stay bonded if they attest first.

**Trust model of the root, and why it is not checked on chain.** x/archive does not verify the
attested block-hash Merkle root; what makes a root trustworthy is that three distinct bonded
operators computed the same one from their own node's blocks, and that anyone can recompute it
(`orama-global history get`, `VerifyBundle`) from a bundle and the chain's block hashes. The app
cannot check it itself, for three reasons. The app's state holds no block hashes: `BaseApp` builds
the header in the context from the fields `FinalizeBlock` carries, which lack the ones the CometBFT
block hash commits to (the last commit hash, the data hash, the evidence hash and others), so the
headers `x/staking` keeps in `HistoricalInfo` cannot be hashed back into block hashes; the block
hash of the block being executed is available in `HeaderInfo` and is not stored anywhere. Only the
last `historical_entries` (10000) of them exist in any case, a fraction of the 14-day window. A
per-block hash index written by the app would be a new consensus-state write on every block, and
verifying an attestation would read and hash a whole range (1000 hashes at launch) inside one
transaction. It is not built. Nothing slashes or jails an operator whose tuple loses a decided
range: C14 defines no penalty for it, so none is invented here; the loser is only counted toward a
tuple that did not win, and its archiver records the conflict locally. `MsgAttachReplicas` and
`MsgCreateArchiveDeal` need a decided range (`ErrQuorumPending`). Genesis export writes `decided` and the
candidates, refuses state that breaks these rules (a decided range with candidates or under three
operators, an undecided one with an operator in two candidates or over the cap), and `ExportGenesis` runs
the same validation as `InitGenesis`. The `archive_attest` event carries `decided` and the attested
`bundle_cid`.

x/archive accepts `MsgAttest`, `MsgAttachReplicas` and `MsgCreateArchiveDeal` only from the hot key of
the x/nodes node the message names, and only while that node is active with an
ARCHIVER role bond. Each operator counts once toward a range: a second node of
an operator, or the same operator under a rotated key, is accepted for the same tuple and
counts nothing, and a key that already attested stays idempotent after its
node loses the role. Every id in `MsgAttachReplicas` must be a decimal x/storage
deal id of an active ARCHIVE deal (`ErrNotArchiveDeal`, so an OPEN deal with no provider yet is refused) that backs no other
range (`ErrDealAttached`), and only an operator that attested the range may
attach deals to it (`ErrNotAttester`). When a tuple has won and the three-deal
threshold is reached, the deals are read again: ended ones are dropped from the
range and freed, and the range is archived only if three live deals remain.
After that `archived` is permanent. `MsgAttachReplicas` does not require the deal to have been
made by `MsgCreateArchiveDeal`: the scheduled ARCHIVE protocol deals, which carry a synthetic
payload, can still be attached, and the deal's stored bytes are not checked against the bundle, so the
attesting operators and the winning piece commitment are what vouch for it. Nothing slashes a wrong root
(C14 defines no penalty); the root is checkable by anyone against the chain's own block hashes.

**Retention.** The app enforces C14's retain height in `OramaApp.Commit`. `BaseApp` returns the height
its own rules give (`min-retain-blocks`, the evidence age and the snapshot interval; 0, prune
nothing, when `min-retain-blocks` is 0). The app lowers it to
`min(tip - retention_window_blocks, last_archived_height)`, where the last archived height is the
contiguous archived prefix (`x/archive` `RetainHeight`), and returns 0 while nothing is archived.
A block that no archived range covers is never pruned, however far the tip runs ahead of a stalled
archive, and the block store then grows past the validator budget until archiving resumes. A node prunes only
when its operator sets `min-retain-blocks` in `app.toml`; the stagenet deploy script sets it to
14 days of blocks. The archiver does not hold CometBFT's retain height itself: the app's gate is what
holds it, for every node, so an archiver that is down or behind costs storage but never history.

`<home>/monitor.json` reports the archiver's progress:

| Field | Meaning |
|---|---|
| `attested_height` | The archiver's cursor: the last height it attested. |
| `last_archived_height` | x/archive's contiguous archived prefix, the height the chain never prunes above. |
| `tip_height` | The chain tip the archiver saw. |
| `retain_lag_blocks` | `tip_height - last_archived_height`. It grows while archiving stalls; alert on it. |
| `unarchived_ranges` | Attested ranges x/archive has not marked archived. |
| `deals_opened` | ARCHIVE deals this process has opened since it started. |
| `pieces_uploaded` | Bundle uploads to assigned providers this process completed since it started. |
| `upload_failures` | Uploads that failed all their tries in a pass (or were refused for a wrong root); a slot nobody uploads to is evicted. |

core's telemetry does not read this file yet; its `MonitorFile` covers the provider, the relay and the directory authority.

**Budget.** `TestArchiveBudget_aYearOfRangesFitsTheStorageCeiling` sizes the deals from the defaults: at
1000 blocks a range and 6-second blocks the chain archives about 14 ranges a day, each with three deals of
three slots, paid the protocol price per epoch for 3650 epochs. It asserts that the bill never passes 1% of
any epoch's storage ceiling over the first year and over the first ten. The price does not depend on the bundle's
size, and the bill keeps growing with history until the first deals end, so the price and the range width
are the levers if that stops holding.

### Chain indexer (`orama-global indexer`)

`chain/indexer` follows oramad over the loopback RPC and keeps an index in
Pebble (pure Go, no cgo) under `<home>/index`. The unit
(`RenderGlobalIndexerUnit`, user `orama-indexer`, home
`/var/lib/orama-global/indexer`) runs it with `--listen 127.0.0.1:31015`
(`constants.GlobalIndexerPort`); co-located, `--listen 198.18.0.2:31015` and `--rpc tcp://198.18.0.2:31001`.
`--listen` must be a loopback IP or the co-located namespace address; anything
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
  transaction hashes, the gas its transactions used, and the base fee they
  burned (the `base_fee` attribute of each `tx` event; burns in the
  finalize-block phase are not counted).
- **Transactions** (failed ones too): hash (SHA-256 of the bytes, lowercase
  hex), height, index in the block, the block's time, code, codespace, log, gas
  wanted and used, the type URL of each message, the events of the
  `ExecTxResult`, the signer (the account of the first signature's public key;
  empty for a transaction with none, such as a shielded one), the memo, and
  `body`: each message as JSON with its `@type`. A message whose type the
  indexer's codec does not know (CosmWasm messages) is `{"@type": url}` alone;
  a message of a known type that cannot be decoded stops the pass. A
  transaction whose bytes are not a Cosmos transaction (the chain refused it)
  is kept with no message type URLs and an empty body.
- **Newest transactions:** every transaction in block order, so the newest can
  be listed without walking blocks.
- **Hourly statistics:** per UTC hour, the transactions included, how many
  failed, and the base fee they burned. They count from `--start-height`.
- **Account summary:** per address in the address index, how many transactions
  named it and the time of the first and of the last.
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
- `orama global install --services chain,indexer` creates the `orama-indexer`
  user and writes and enables the unit; without it `/v1/chain/index/…` answers
  `502`. The indexer opens no firewall port. On a state-synced node, set `--start-height` to a height
  the node keeps.
- Shielded activity beyond its public transaction bytes and events.

Read API on the loopback listener. Everything is `GET` and JSON; any other
method, path, or query parameter is refused (`404`, `405`, `400`):

| Path | Answer |
|---|---|
| `/index/v1/status` | `start_height`, `cursor`, and the node's `earliest` and `tip` (`502` if the node is unreachable) |
| `/index/v1/blocks/{height}` | the block, or `404` `not indexed` |
| `/index/v1/txs/{hash}` | the transaction (64 hex, either case, no `0x`) |
| `/index/v1/txs?limit=` | the newest transactions, newest first (`limit` 1–100, default 20; no page) |
| `/index/v1/stats` | `start_height` and `hours`: 48 hourly buckets ending with the hour of the newest indexed block, oldest first, each `{hour, txs, failed, burned}`; an hour with no transaction is zeros |
| `/index/v1/accounts/{address}` | `{address, tx_count, first_seen, last_active}`, or `404` `not indexed` for an address no transaction named |
| `/index/v1/accounts/{address}/txs?page=&limit=` | the address's transactions, newest first |
| `/index/v1/cnft/assets/{id}` | `{"id", "records": [...]}`, each record a DAS-style asset (`ownership`, `compression`, `grouping` by collection, `burnt`, `content.metadata_cid`, `last_update`) |
| `/index/v1/cnft/owners/{address}/assets?page=&limit=` | the compressed and decompressed assets the address owns, by asset id |

`page` is 1–1000 (default 1) and `limit` 1–100 (default 20). An address must
be a lowercase bech32 `orama1…` account with a valid checksum.

An index carries a layout version, and an index written by another version is
refused. This version (2) added `time`, `signer`, `memo`, `body`, the block's `gas_used` and
`burned`, and the three indexes above. The indexer exits naming the problem;
the fix is to index again into a new `--home`. The gateway
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
- `orama storage repair --deal-id N --repair-seed-file F --rpc <oramad RPC>` is the owner's own repair,
  for a deal that names no repair delegate. For every slot the chain assigned to a new provider that
  has not accepted, it fetches an accepted replica from another provider (checked against that
  slot's root), rewraps it with the repair seed, checks the result against the new slot's root and
  uploads it. A repair seed that is not the deal's misses the root and uploads nothing. A deal with no
  accepted replica is refused (`orama storage put` makes the first upload). A deal that names a
  delegate is repaired by the delegate while the owner is away; without one, the deal runs with
  fewer replicas until the owner runs this command.
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

- `chain/x/confidential` refuses every attestation. It is the software boundary for track H
  and is not imported by the app or any module (a test enforces that):
  - `AttestationVerifier.Verify(quote, expectedMeasurement)` returns a `VerifiedReport` or an
    error. A `VerifiedReport` has an unexported `verified` field, so only a verifier inside the package
    can produce one, and none does.
  - `DefaultVerifier()` checks the quote's structure (`ParseQuote`: a 1184-byte SEV-SNP report of
    version 2, 3 or 5 with the ECDSA P-384 algorithm, or a TDX v4 quote with the P-256 attestation
    key) and then needs an accepted vendor root for that TEE. The vendor root registry
    (`RootRegistry`) is empty by default and a root can enter only through `NewRootRegistry` (a
    known kind, a self-signed CA certificate, no duplicates). Even with a root present, the
    signature and certificate-chain check is not implemented, so the verifier returns
    `ErrVerifierNotLinked`. Nothing parses a quote into trust.
  - `RegisterNode` (a confidential-node registration) and `NewListing` (a marketplace listing) both
    require a verified report, a measurement equal to the expected one, and report data that binds
    the operator, node id and node key (`ReportDataFor`). With the default verifier every quote
    (nil, empty, garbage, an unsigned SEV-SNP report, a TDX quote with a bogus signature) is
    refused.
  - `Listing.Lease` always returns `ErrMarketplaceNotLive`. There is no message, module or path that
    trades, and `MarketplaceLive` is `false`.
  - The tests use a stub verifier that returns a verified report so the checks after verification
    can be tested. It exists only in `_test.go`, and it is not a quote.
- `core/pkg/tornet` is no longer one of these: the Orama Tor network is built
  ([TOR_NETWORK.md](TOR_NETWORK.md)). Its network file is refused with fewer than
  three authorities, and an exit is installed only on a network file that says
  `allow_exit`.
- `chain/x/vpnlaunch` is the public-VPN launch gate: every threshold on each of the last 30 days and a launch
  switch that is off in every build. Nothing links it into `oramad`
  ([TOR_NETWORK.md](TOR_NETWORK.md#the-public-launch-gate)).

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

## Memory: the IAVL cache and GOMEMLIMIT

`oramad init` writes `iavl-cache-size = 100000` into `app.toml`, not the SDK's 781250. The SDK's
figure is sized for a host that runs a validator and nothing else; on a 4 GB stagenet node that also
runs IPFS and every namespace's services, the cache grew past half a gigabyte of live heap in a day.
The chain unit also sets `GOMEMLIMIT=1GiB`, a soft limit: without it Go's collector let the heap reach
twice what was live, and the node reached 98% memory. Over the limit the collector runs more often; it
does not kill. `deploy.sh` sets `iavl-cache-size` on nodes whose `app.toml` predates it; elsewhere an
existing `app.toml` keeps whatever value it has until it is edited.

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
zero), patches a finite consensus block `max_gas` (`BLOCK_MAX_GAS`, default 100,000,000) into the generated genesis, and `VOTE_EXTENSIONS_ENABLE_HEIGHT` (default 0, off; a positive value turns on inclusion lists, see "Inclusion lists (C13)"), distributes it, and
starts every node in the background on distinct localhost ports in the 31000-31099 range
(P2P/RPC/gRPC/API/Prometheus/pprof, ten ports per node so up to ten validators fit). That packing
is localnet only. A production global node uses 31000–31004 for the chain (p2p public, RPC, gRPC,
REST and Prometheus on loopback), 31010–31013 for public storage (swarm public, Kubo RPC and
gateway on loopback (the RPC on the namespace address when co-located), provider HTTP public), 31014 for relay metrics on loopback, 31015 for the
chain indexer's read API on loopback, 31020–31021
for a Tor relay and a dirauth, and 31022 for the validator tx gate on loopback. The public Kubo on a global node has no swarm.key,
announces only pinned content (`Provide.Strategy=pinned`, which Kubo has read since v0.38), and
does not dial or announce private ranges, so it cannot join a cluster's mesh.
Every setup
command's output goes to `scripts/localnet/.localnet/setup.log` rather than being discarded, so a
failure can actually be diagnosed. `WITH_WASM=1` builds `oramad` with cgo and libwasmvm and stores the
standard contracts in genesis (`oramad genesis add-standard-contracts`); the default build has no CosmWasm.

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

## Inclusion lists (C13)

Inclusion lists make a proposer unable to censor a transaction that at least 2/3 of voting power
has seen. Code: `chain/x/inclusion` (the rule), `chain/app/inclusion_*.go` (the CometBFT wiring).

**Switch.** Vote extensions are a consensus parameter, `abci.vote_extensions_enable_height`, set in
the genesis `consensus.params`. `0` means off, and every binary and genesis before C13 has `0`.
The chain cannot change it later: every consensus-param authority is `UnreachableAuthority`. With
height `E` set, validators extend their precommit from height `E`, and the block at height `E+1`
and every later one carries the extended commit. At heights `<= E` (and everywhere when `E` is
`0`) the four handlers do nothing and `PrepareProposal` / `ProcessProposal` are the SDK default
handlers, unchanged; `ProcessProposal` only refuses a block that contains an injected-commit
transaction. `scripts/stagenet/deploy.sh` sets `E = 2` (`VOTE_EXTENSIONS_ENABLE_HEIGHT`);
`scripts/localnet/localnet.sh` leaves it `0` unless `VOTE_EXTENSIONS_ENABLE_HEIGHT` is set.

**Upgrade implication for a live chain.** A running chain without extensions cannot turn them on
through governance or a parameter message. It takes a coordinated hard fork that installs the new
binary and rewrites `consensus.params.abci.vote_extensions_enable_height` in a fresh genesis
(export, edit, restart with a new chain id or an agreed halt height). Nodes that run the new
binary against an old genesis keep `0` and behave as before. Never mix binaries with different
handlers on one chain: extended-commit blocks fail `ProcessProposal` on a node without them.

**ExtendVote.** Each validator keeps a local set of transactions that passed `CheckTx`
(`inclusion_pool.go`, bounded to 16 MiB and expired after an hour; it is not consensus state,
and the wrapper `OramaApp.CheckTx` fills it). The extension lists those first seen at least 10 s
ago (`inclusionIncludeAfter`) and not already in the block being voted on. Each candidate must
decode, pay at least 1 norama, and pass the full ante chain against the last committed state,
judged one after another in byte order. The list holds at most `list_max_bytes` (32 KiB) and a
sender at most 32 KiB. A validator with nothing to list sends an empty extension, and that counts
as an empty list. Only ordered transactions with exactly one signer can be listed.

**VerifyVoteExtension.** Rejects, with no state read, an extension that does not decode as a list,
is over 32 KiB, is not strictly sorted and unique, has a transaction that does not decode, pays
under 1 norama, or breaks the per-sender cap. An empty extension is accepted. The size cap is the
app's, not CometBFT's 1 MiB.

**PrepareProposal.** From height `E+1` the proposer:
1. checks its `LocalLastCommit` with `baseapp.ValidateVoteExtensions`;
2. puts the `ExtendedCommitInfo` first in the block as one injected transaction:
   `ORAMA-INCLUSION-EXTENDED-COMMIT-V1:` followed by the protobuf bytes. The prefix starts with a
   byte that is an illegal protobuf wire type, so it can never decode as a chain transaction;
3. computes the required transactions with `x/inclusion` and puts them next, in byte order:
   every listed transaction from the commit's valid extensions, walked in byte order against
   account sequences read from state and the ante chain run on a scratch branch. A transaction
   that fails the ante chain, repeats a sender's sequence already taken by an earlier one, or does
   not fit the block budget is skipped, and skipped ones change no state for the next;
4. hands the remaining space, and the request's transactions that are not already in the block,
   to the SDK default handler.

The block budget for step 3 is the consensus `block.max_bytes` minus 2 MiB
(`blockOverheadReserve`, for the header, last commit, evidence and framing) minus the injected
transaction. If the commit cannot be turned into a valid block (`ValidateVoteExtensions` fails, or
under 2/3 of power holds valid extensions) the handler errors, BaseApp falls back to the raw
request transactions, and every honest `ProcessProposal` rejects that proposal.

**ProcessProposal.** From height `E+1` it rejects a block with no injected commit first; a commit
that is not the canonical encoding or fails `ValidateVoteExtensions` (signatures, order, 2/3
power); a second injected commit; or a block that does not start, after the commit, with exactly
the required transactions in order (`inclusion.Process`). Anything else goes to the SDK default
handler with the commit removed. Every input is in the block or in state, so all validators reach
the same verdict. It never looks at a mempool or a clock.

**Extensions that did not pass VerifyVoteExtension.** A late precommit can reach a commit without
`VerifyVoteExtension` having run on it. Both handlers therefore re-validate every extension in
the commit with the same stateless rules and drop, deterministically, one that fails; its power
still counts in the total. Quorum (2/3 of total power) is judged on every valid extension in the commit, **before**
trimming. If the deduplicated listed bytes then exceed `max_embedded_list_bytes` (4 MiB),
extensions are kept highest power first (ties by validator address) while they fit, and the rest
stop contributing required transactions; their power still counts. Trimming never removes quorum, so
a valid commit cannot leave every proposer unable to build a block.

**Extension budget.** The injected commit takes at most half of the block budget (`block.max_bytes`
minus 2 MiB). The per-extension cap is `list_max_bytes` (32 KiB) cut so that `max_validators`
(staking) extensions, each with 2 KiB of vote overhead, fit that half; at the defaults and 150
validators the cap stays 32 KiB. `VerifyVoteExtension` uses the cut cap. `PrepareProposal` refuses to
inject a commit over the budget or over the request's `MaxTxBytes`, and `ProcessProposal` rejects one
over the budget, so an oversized commit has one deterministic outcome.

**CPU limits on listed transactions.** The walk checks each candidate's signature against its
sender's account (`inclusionTxRules.verifier`) before charging it. A transaction that only names a
sender it cannot sign for is skipped without costing that sender's budget or an ante attempt, but
each such check is a signature verification, so at most 4096 (`max_verify_attempts`) run per block:
without the bound, junk that names a real sender at its next sequence (roughly 20,000 of the
smallest fit in the 4 MiB embedded cap) would cost every node that many signature checks per
proposal. Every other candidate that reaches the ante chain is charged to its sender's 32 KiB byte
budget whether or not it passes, and at most 1024 (`max_ante_attempts`) run the ante chain per
block.

Both caps are the block's totals and are budgeted **per listing extension**, so one validator's junk
cannot use up what the others' transactions need. The totals are divided evenly among the vote
extensions that list anything (at least one run each, so total work is at most the larger of the
total and the number of listing validators). The walk still visits the deduplicated transactions in
lexicographic order, and each verification or ante run is charged to the extension that lists the
transaction and has the most budget left (the first in public-key order on a tie), so a transaction
that many validators list costs one run, not one per validator. A transaction that every validator
listing it can no longer afford is skipped and the walk goes on. A validator listing only junk runs
out of its own share and its remaining junk is skipped; a valid transaction another validator lists
is still verified and required. What the bound still costs: a validator (or f of N validators) can
starve the transactions that only it lists, and no more than f/N of the total runs. A skipped
transaction is not *required*, but a proposer can still include it and it stays in the mempool.
`VerifyVoteExtension` cannot check signatures, which need the account number.

**State the rule is judged on.** `ProcessProposal` judges listed transactions on the last committed
state, but they execute after this block's `BeginBlock`. The judgement can drift (the base fee moves by
at most 12.5% a block; `BeginBlock` pays rewards), and a required transaction can then fail at
execution. That was chosen over running a different state view because `BeginBlock` cannot run in
`ProcessProposal`. The free space it allows is bounded: required transactions come only from the
deduplicated embedded set, so they never exceed `max_embedded_list_bytes` (4 MiB of a 21 MiB block).

**FinalizeBlock.** `OramaApp.FinalizeBlock` removes the injected transaction before BaseApp sees
the block, so it never reaches the ante handler or a message handler, and puts a code-0 result
with log `inclusion-list extended commit` in its place (CometBFT wants one result per block
transaction). It also drops every included transaction from the seen-set. BaseApp's optimistic
execution calls its finalize path directly and would skip this, so it must stay off.

**Limits, stated plainly.**
- The block carries the SDK's `ExtendedCommitInfo`, which holds each validator's raw extension,
  and then the required transactions again, so listed bytes are on the block twice. The
  deduplicated set is bounded at 4 MiB, the raw commit at validators x 32 KiB.
- "Fits" is judged on bytes only. Block gas is not part of it, so a required transaction can run
  out of block gas at execution and fail; it is still in the block, so the rule is met.
- Listed transactions are walked in byte order, not nonce order. Two transactions of one sender
  in one list keep only the one that sorts first if it has the sender's current sequence.
- Liveness needs fewer than 1/3 of power to publish extensions that fail the rules above; a
  timely bad extension is refused by `VerifyVoteExtension` and never enters a commit.
- The walk runs the ante chain, signature checks included, over up to 4 MiB of listed bytes in
  every proposal and every `ProcessProposal`.
- No CometBFT crash e2e has been run; the fleet suite owns that.

## Explorer

The website explorer (`website/src/explorer`, mounted at `/explorer` by `website/src/pages/explorer.tsx`)
reads the chain through the gateway's chain proxy (below) on the origin it is served from. It has no
demo data and no fixture accounts: when the proxy or the indexer is down, a page shows an error box
and nothing else. It needs the chain indexer ("Chain indexer" above) on the node the gateway
reads; without it the transaction, block and wallet pages fail with "The chain could not be read".

Layout of `website/src/explorer`:

| Folder | Holds |
|---|---|
| `model/` | domain types (`types.ts`), ORAMA/norama formatting (`units.ts`), what a pasted string is (`search.ts`, with a bech32 checksum check), the investigation trail (`trail.ts`), and how a transaction reads as a sentence (`describe.ts`) |
| `data/` | the `ExplorerDataSource` interface (`source.ts`), the React provider and the `useQuery` / `useLiveQuery` hooks |
| `data/chain/` | the one implementation of that interface (`source.ts`): the HTTP client (`client.ts`), the response readers (`wire.ts`), the validator directory (`directory.ts`), and the mapping of transactions, blocks, wallets, the network and validators |
| `ui/` | shared building blocks (amounts, wallet links, sentences, transaction rows, help tips) |
| `shell/` | the header, the ⌘K search palette, the investigation trail bar, and the transaction preview drawer |
| `pages/` | Home, Transaction, Block, Wallet, and Validators (the λ hand-over is a card on that page) |

Pages only ever call `ExplorerDataSource`. Its contract is written per method in `data/source.ts`:
a lookup for something that does not exist resolves to `null` (never an invented empty record); a
real failure rejects with a readable `Error`; ordering is stated per method; pagination is
cursor-based and a bad cursor is rejected; limits are clamped. A figure the chain cannot give is `null`
or absent in the model, and the page leaves it out.

What each page reads, and from where:

| Shown | Read from |
|---|---|
| Head, chain id, sync state | `GET /v1/chain/status` |
| Supply, base fee, epoch, fees burned, transaction counts | `/supply/norama`, `orama.fees.v1.Query/BaseFee`, `orama.emission.v1.Query/CurrentEpoch` and `Params`, and the indexer's `/stats` (transactions, failures and burned base fee per hour; the 24 hours and the 24 before) |
| Validators, voting power, committee seats, λ, the Nakamoto coefficient, total bonded | the node's staking validators, CometBFT's `/validators`, `orama.power.v1.Query/BootstrapCommittee` and `Lambda`, `/staking/pool`. A validator's consensus address is the first 20 bytes of the SHA-256 of its ed25519 key, computed in the browser |
| Blocks | CometBFT's `/blocks` and `/block`, and the indexer's block (gas, burned base fee, transaction hashes). A block's signatures are the next block's last commit, so the head has none yet |
| A transaction | the indexer's record: signer, memo, the body as JSON, events, code and log. The fee is the `base_fee` and `tip` of the `tx` event the fee handler emits. "Balances before → after" is built from the `coin_spent`, `coin_received` and `burn` events, so it has the change and not the before and after, and it leaves out what moved through an earnings account |
| A wallet | the bank `AllBalances`, staking `DelegatorDelegations` and `DelegatorUnbondingDelegations` queries of the wallet-query route (norama only), and the indexer's account summary (count, first and last time). A wallet with no transaction and no funds is not found |
| A wallet's activity | the indexer's per-address transactions, 100 a page, filtered in the browser; the cursor is `page:position` |
| Who a wallet deals with | its 100 newest transactions only: successful transfers, by volume |

Messages the explorer has a sentence for are bank sends of norama, staking delegate and undelegate of
norama, and `x/storage` `MsgCreateDeal` (the amount is the escrow: price per epoch times replicas
times epochs; the providers are assigned later, so none is named). Any other message, and a send of
another token, is shown by its type URL. A transaction with no signature (a shielded one) has no
signer.

Not shown, because nothing on the chain gives it: a wallet's balance over time, a validator's
uptime history, the number of delegators, claimable rewards, how large a transfer is against the
week's, and how often a signer has paid a receiver before.

### What the chain adapter must do

The UI trusts its data source; these are the adapter's duties, because chain data is written by
anyone:

- Validate every amount as a base-unit integer string of bounded length before returning it; a
  malformed record must reject the query (an error box), not reach a page.
- Strip control and bidirectional-override characters from labels, monikers, memos and failure
  reasons. `verified` on a wallet must come from a curated registry shipped with the site, never
  from a chain-writable field.
- Percent-encode every path segment and query value when building indexer requests, and return
  reader-safe error messages (no internal hostnames or raw upstream errors).
- Clamp `limit` and search-query length, and keep the indexer's search rate-limited: the palette
  sends a debounced query while the reader types.
- Serve only finalized blocks as the head.

### The gateway's chain proxy

`core/pkg/gateway/routes.go` mounts the `core/pkg/gateway/handlers/chainread` proxy at `/v1/chain/`
(an open route in `route_policy.go`): reads for the explorer and for wallets, plus two POST routes that
take a signed transaction (below). The website explorer reads only this proxy. The upstream bases are
`ORAMA_CHAIN_RPC_URL`, `ORAMA_CHAIN_REST_URL` and `ORAMA_CHAIN_INDEX_URL`, defaulting to those
three loopback URLs, or, on a co-located machine (the `orama-global` namespace layout is installed), to the
same ports on the namespace address `198.18.0.2`, where the chain and indexer listen there and only the
host may connect (`constants.GlobalNetnsAddr`; [RUN_A_GLOBAL_NODE.md](RUN_A_GLOBAL_NODE.md)). The caller's path is not forwarded: each route builds its own upstream URL
from values it has validated. Anything outside this list is refused. A healthy upstream body is
copied unchanged (at most 8 MiB, the largest being one block on `/block`), except on `/v1/chain/query/`
below, which decodes the answer. An upstream failure is never copied: a 404 or a CometBFT "not found"
error is answered `404 not found on chain`, and any other error status, or a JSON-RPC error object under a
200, is answered `502 chain request failed`, because the node's message can carry paths and store details.
CometBFT answers an error from a GET route, a transaction that is not in its index included, with HTTP
500 and the JSON-RPC error object in the body, so the body is read whatever the status: `tx (HASH) not
found` is a 404, never a 502. `GET /v1/chain/tx?hash=` answers that 404 with `Retry-After: 2`, because a
transaction a wallet has just broadcast is not found until it is in a block and the node it asks (any
gateway answers; each asks its own node) has indexed it:

| Gateway path | Upstream |
|---|---|
| `GET /v1/chain/status` | CometBFT `GET /status` |
| `GET /v1/chain/block?height=` | CometBFT `GET /block?height=` |
| `GET /v1/chain/blocks?min_height=&max_height=` | CometBFT `GET /blockchain?minHeight=&maxHeight=` (at most 20 blocks) |
| `GET /v1/chain/tx?hash=` | CometBFT `GET /tx?hash=` (32-byte hex, `0x` optional on the gateway path) |
| `GET /v1/chain/validators` | CometBFT `GET /validators` (`page` and `per_page` optional; default 1 and 100, capped at 100) |
| `GET /v1/chain/supply/norama` | REST `GET /cosmos/bank/v1beta1/supply/by_denom?denom=norama` |
| `GET /v1/chain/staking/pool` | REST `GET /cosmos/staking/v1beta1/pool` |
| `GET /v1/chain/staking/validators` | REST `GET /cosmos/staking/v1beta1/validators` (page size fixed at 200, no query; the wallet queries below serve one validator, not the list) |
| `GET /v1/chain/index/status` | indexer `GET /index/v1/status` |
| `GET /v1/chain/index/blocks/{height}` | indexer `GET /index/v1/blocks/{height}` |
| `GET /v1/chain/index/txs/{hash}` | indexer `GET /index/v1/txs/{hash}` (32-byte hex, `0x` optional on the gateway path, sent lowercase) |
| `GET /v1/chain/index/txs` | indexer `GET /index/v1/txs` (`limit` 1–100, optional) |
| `GET /v1/chain/index/stats` | indexer `GET /index/v1/stats` |
| `GET /v1/chain/index/accounts/{address}` | indexer `GET /index/v1/accounts/{address}` |
| `GET /v1/chain/index/accounts/{address}/txs` | indexer `GET /index/v1/accounts/{address}/txs` (`page` 1–1000, `limit` 1–100, both optional) |
| `GET /v1/chain/index/cnft/assets/{id}` | indexer `GET /index/v1/cnft/assets/{id}` (32-byte hex, sent lowercase) |
| `GET /v1/chain/index/cnft/owners/{address}/assets` | indexer `GET /index/v1/cnft/owners/{address}/assets` (`page`, `limit` as above) |
| `GET /v1/chain/query/{package.Service}/{Method}` | CometBFT `GET /abci_query?path="/{package.Service}/{Method}"&prove=false` (see "Module queries") |
| `POST /v1/chain/simulate` | CometBFT JSON-RPC `abci_query` of `/cosmos.tx.v1beta1.Service/Simulate`, and x/fees `BaseFee` (see "Simulate and broadcast") |
| `POST /v1/chain/broadcast` | CometBFT JSON-RPC `broadcast_tx_sync` (see "Simulate and broadcast") |

The validator list takes no query (`400`). On the index routes the gateway checks an address's shape (lowercase `orama1` plus bech32
characters) and the indexer checks its checksum. A route that takes no query refuses one.
What the indexer holds, and what it does not, is under "Chain indexer" above.

**Module queries.** The gateway reaches the Orama modules through `abci_query`, its one HTTP route to
them (they are also on the node's own REST API, see "Module queries over REST" below). `GET /v1/chain/query/<package.Service>/<Method>`, for example
`/v1/chain/query/orama.nodes.v1.Query/Node`, runs one of them on the local node's RPC and answers the
decoded response as JSON with the proto field names (uint64 fields are decimal strings). The route serves
only the `Query` services embedded in `core/pkg/chainread/queries.binpb`: a Msg service, a transaction
path or any name outside that set is a 404, and the gateway never asks for a proof or writes.
The request is `data=<base64 protobuf>` (standard or URL-safe alphabet, padding optional) or
`json=<JSON request>` (proto field names), not both, or neither for the empty request; each is at most 4 KiB
and is checked against the method's request type before the node sees it. `height=<n>` reads at that
height (a positive integer; 0 or absent is the latest), and must be within the last 100 blocks
(`queryMaxHeightAge`; the gateway reads the latest height from `/status`, cached for a second, and answers
400 for an older one): an old height makes the node open an old state version. Any other query key, a
repeated key, a non-GET method and a response over 4 MiB are refused. A key the chain does not have is a
404; a request the chain judges malformed (an address that is not one, ABCI code 18 of codespace `sdk`) is a
400 `the chain refused the request`; any other chain error is a 502 without the node's message. The gateway decodes with the same
`dynamicpb`/`protojson` code `orama chain` uses (`core/pkg/chainread`), which links no chain code.

The public route serves an explicit list of methods (`publicQuery` in `handlers/chainread/query.go`), not
every embedded one. Every module's `Invariants` query walks the module's whole state and is withheld, as
are `orama.houses.v1.Query/Tiers` (reads every operator and its service days) and
`orama.shielded.v1.Query/Pools` (every pool). The served queries are point lookups, constant
computations, or walks the module caps on the server: `Challenges` requires a `node_id` and reads that
node's key range, and `Snapshots` and `NodeUnbondings` return at most 1000 entries. A test fails for an
embedded Query method on neither list, so a new module query is not public until someone decides it
should be. `orama chain query --rpc` and the node's own gRPC serve all of them.

Three more limits keep a public caller from putting unbounded work on the node. The route has its own
per-address rate-limit bucket (120 a minute, burst 30) apart from the gateway's general one; at most 16
module queries run at once, and the rest get `503` with `Retry-After`; and the node's `app.toml`
carries `query-gas-limit = "2000000"` (`oramad init` writes it, so `orama global install --init-chain` and
the stagenet deploy script's fresh install get it, and the script asserts it after the install; nothing
patches an `app.toml` that already exists), which stops a query that scans state after about two thousand
store reads. A limit of 0 means unbounded in the SDK, so `oramad start` refuses to start with one on
any chain id that does not contain `-localnet-`, and its error names the setting to fix. The x/storage
`Invariants` query is the one exception: it sums every deal and its slots, so its cost grows with the
chain's history (it ran past the limit at a few thousand deals), and it runs on its own gas meter. It is
withheld from the public route above, so only a caller that can reach the node's own RPC or gRPC runs it.

The route's rate-limit bucket is per client address, except that an IPv6 client is limited by its /64
(a subscriber is routinely handed a whole /64 and can source a request from any address in it). The
Reporters query is capped at the size of the reporter set (`MaxReporters`, 128) on the server.

**Wallet queries.** A wallet with no node of its own reads one address through the same route. The
cosmos-sdk and wasmd `Query` services of bank, auth, staking, distribution and `cosmwasm.wasm.v1` are embedded
in `queries.binpb` whole (`gen.sh` builds them from the versions `chain/go.mod` pins), and `walletQuery`
(`handlers/chainread/query_wallet.go`) names the 15 methods served, the rest being a 404 (so bank's
`TotalSupply`, staking's `Validators` and wasm's `AllContractState` are not reachable):

| Service | Methods |
|---|---|
| `cosmos.bank.v1beta1.Query` | `Balance`, `AllBalances`, `SpendableBalances` |
| `cosmos.auth.v1beta1.Query` | `Account`, `AccountInfo` (an address the chain has never seen is a 404) |
| `cosmos.staking.v1beta1.Query` | `Delegation`, `DelegatorDelegations`, `UnbondingDelegation`, `DelegatorUnbondingDelegations`, `Validator`, `Pool`, `Params` |
| `cosmos.distribution.v1beta1.Query` | `DelegationRewards`, `DelegationTotalRewards` |
| `cosmwasm.wasm.v1.Query` | `ContractInfo` (an address that is not a contract is a `404 not found on chain`) |

A request for `AllBalances`, `SpendableBalances`, `DelegatorDelegations` or `DelegatorUnbondingDelegations`
may set `pagination.limit` up to 100 (`queryMaxPageLimit`; unset is the SDK's default of 100) and `pagination.key`
to continue; a larger limit is a 400 before the node is asked. wasmd returns an address that is no contract as
a plain error, which baseapp reports as code 6 of codespace `sdk` with the message `no such contract`; the wasm
code is lost on the way out, so `chainread` recognises that code, codespace and message for `ContractInfo`
alone (`core/pkg/chainread/grpc.go`) and nowhere else. The existing routes a wallet already reads,
`/v1/chain/index/accounts/{address}/txs` and the staking `Params` query, are unchanged.

**Simulate and broadcast.** A wallet with no tunnel to a node prices and submits a transaction through two POST
routes (`handlers/chainread/tx.go`). Both take `Content-Type: application/json` and the body
`{"tx_bytes":"<base64 TxRaw>"}` (a signed `cosmos.tx.v1beta1.TxRaw`; unknown members, a second object, an empty
or non-base64 value are 400; at most 1 MiB of transaction, CometBFT's default mempool `max_tx_bytes`, which the
chain does not change, and a larger one is 413). They take no query and any other method is 405 with
`Allow: POST`. Answers are `Cache-Control: no-store`.

- `POST /v1/chain/simulate` runs the transaction through the ante handlers and messages without keeping
  anything. Success is 200 `{"gas_wanted":N,"gas_used":N,"fee":{"denom":"norama","amount":"…"},"base_fee":"…"}`:
  `fee` is x/fees' current base fee times `gas_used`, and `base_fee` (norama per unit of gas) lets a caller that pads the
  gas limit price the padded limit; the on-chain rule is `fee >= base_fee * gas_limit`
  ([x/fees](#xfees-base-fee-earnings-accounts-and-state-deposits)). A transaction the chain refuses is 422 `{"code":N,"codespace":"…","log":"…"}`.
- `POST /v1/chain/broadcast` submits the transaction with CometBFT `broadcast_tx_sync`, which answers after
  `CheckTx` and never waits for a block (`broadcast_tx_commit` is never called). It answers
  `{"code":N,"codespace":"…","log":"…","tx_hash":"<64 hex, upper case>"}`: 200 when `code` is 0, and 422 when the
  mempool refused it. Sending bytes the mempool has already seen is 422 with `code` 19, codespace `sdk`, log
  `tx already in mempool cache` and the hash, so a client that retried after a timeout has the hash to read. A full
  mempool is 503 with `Retry-After`. The caller then reads `GET /v1/chain/tx?hash=` until the transaction is in a
  block (404 until it is).

The `log` of a refusal is sanitised (`sanitize.go`): a stack trace, source locations, filesystem paths and
IP addresses are replaced with `[redacted]`, control characters dropped, and it is cut to 512 characters; the
codespace must look like one. What is left is the SDK's reason ("insufficient fees; got … required …"). Any
other failure is a fixed text body, 502 `chain unreachable` or `chain request failed`, never the node's message.

The chain charges a spam transaction's sender its fee, so the gateway bounds only the load. Each route has
its own limits, apart from the general bucket and from the module-query bucket: at most 8 simulates and 16
broadcasts in flight (the rest are 503 with `Retry-After`), and two rate-limit buckets in the gateway
(`chain_tx_limit.go`), one per client network (an IPv6 client is its /64) and one for the whole route on that gateway,
answered 429 with the retryable `RATE_LIMITED` envelope and `Retry-After: 10`:

| Route | Per client network | Whole route, per gateway |
|---|---|---|
| `simulate` | 30 a minute, burst 10 | 1200 a minute, burst 200 |
| `broadcast` | 12 a minute, burst 4 | 600 a minute, burst 100 |

The SDK calls are `simulateTx`, `broadcastTx` and the wallet reads of `OramaChainClient`
([TS_SDK.md](TS_SDK.md#the-chain-module)); the Go client is `chainread.Reader.Simulate` and `Broadcast`
(a refused transaction is a `*TxRefusedError`). Fleet e2e: `e2e/features/chain-wallet-routes`.

The explorer shows what these routes serve and nothing else: where the chain has no source for a figure
(a wallet's balance over time, a validator's uptime history, the number of delegators), the page leaves
it out.

### Module queries over REST

Every rpc of every Orama module's `Query` service (x/archive, cnft, emission, fees, houses, market,
nodes, power, relay, shielded, storage, token) carries a `google.api.http` GET annotation in
`chain/proto/orama/<module>/v1/query.proto`, and each module's `RegisterGRPCGatewayRoutes` serves it
on the node's REST API (`api.enable`, port 31003 on a node), next to the SDK's own routes. The path is
`/orama/<module>/v1/<method-in-kebab-case>`; a numeric key, an address or a node id is a path segment
(`/orama/emission/v1/schedule-at/3`, `/orama/fees/v1/earnings/{address}`), and a field that can hold a
slash (a token denom, a deposit id) or is bytes is a query parameter
(`/orama/token/v1/token?denom=factory/…`). The node's REST API serves every query including each
`Invariants`; what the public gateway serves is still only the list under "Module queries" above.
`chain/app/rest_gateway_test.go` serves the routes over a real gRPC connection to the app. The
generated code is `query.pb.go` and `query.pb.gw.go` (`protoc` with `protoc-gen-gocosmos` and
`protoc-gen-grpc-gateway` v1.16); after a change to a `query.proto`, regenerate them and
`core/pkg/chainread/queries.binpb` (`core/pkg/chainread/gen.sh`).

## Wallet clients: transactions, reads and onion submission

**Transaction builder.** `chain/client/tx` (Go) and `sdk/src/chain` (TypeScript, `@debros/orama/chain`,
see [TS_SDK.md](TS_SDK.md#the-chain-module)) both build `SIGN_MODE_DIRECT` transactions. A fixture
the Go side writes (`chain/client/tx/testdata/tx_vectors.json`) pins the bytes of 20 messages
across every module; the TypeScript tests rebuild each one and must match the body, auth info,
`SignDoc`, signature and transaction bytes. `wallet_msgs.json` beside it lists every Orama message the
chain registers and the SDK messages a wallet signs; the TypeScript message registry, which also
carries the human-readable decoders, must hold all of them. Regenerate both with
`go test ./client/tx -run TestVectors -update-tx-vectors`.

**Wallet-flow tests.** `chain/app/wallet_flow_test.go` drives the builder against a real app through
`FinalizeBlock`, with secp256k1 accounts, so each transaction crosses the ante chain and the message
router. What the chain lets a wallet do today: the fee comes from the bank balance and falls back to
earnings when the bank is short; `x/bank` refuses public user-to-user norama sends; the four
shielded messages are registered and signed by the same builder, and refused here because this build
links neither verifier (the accepted proofs run in `shielded_real_test.go` and
`shielded_wallet_test.go` under the `orchardffi` build); a wallet delegates and undelegates from earnings; votes in the token house; registers
a node, bonds it from earnings and funds its hot key from earnings; creates a token and enforces its
powers (mint, freeze, pause, permanent delegate, non-transferable, renounce); mints, transfers, lists
and buys a compressed NFT with the royalty paid to earnings. The parameter tier of `x/houses` is closed
at bootstrap, so the test seeds a voting proposal directly and a `MsgSubmitProposal` is asserted refused.

**`orama chain`** reads the chain over HTTP JSON and links no chain code. Each command uses one read
path: the gateway proxy above (`--gateway`, default the active environment), a node's REST API
(`--node`), or a node's CometBFT RPC (`--rpc`). The module reads (`earnings`, `node`, `deal`, `query`) go
through the gateway's `/v1/chain/query/` route unless `--rpc` is set.

| Command | Path | Reads |
|---|---|---|
| `orama chain status` | gateway `/v1/chain/status`, or `--rpc` `/status` | height, network, sync state |
| `orama chain validator [oramavaloper1...]` | gateway `/v1/chain/validators` or `--rpc`; with an address, `--node` staking REST | validator set, or one validator |
| `orama chain balance <address>` | `--node` `/cosmos/bank/v1beta1/balances/{address}` | bank balances |
| `orama chain earnings <address>` | gateway `/v1/chain/query/orama.fees.v1.Query/Earnings`, or `--rpc` `abci_query` | earnings balance |
| `orama chain node <id>` | gateway `/v1/chain/query/orama.nodes.v1.Query/Node`, or `--rpc` | a registered node |
| `orama chain deal <id>` | gateway `/v1/chain/query/orama.storage.v1.Query/Deal`, or `--rpc` | a storage deal |
| `orama chain query <Service/Method> [json]` | gateway `/v1/chain/query/…`, or `--rpc` `abci_query` | any Orama module query; `--list` names them |

With `--rpc` (a node's CometBFT RPC, `http://127.0.0.1:31001` on the node) the CLI runs `abci_query` itself;
without it the gateway does, and the CLI sends the request as `data=` after checking it. The request and
response are protobuf, encoded and decoded from the query descriptors embedded in
`core/pkg/chainread/queries.binpb`, generated from `chain/proto` by `core/pkg/chainread/gen.sh`; a test
fails when the file is stale.

**Onion submission.** `--onion <addr.onion[:port]>` on every transaction command (`orama global register`,
`bond`, `unbond`, `capacity`, `retire`, `unjail` and the other validator commands, `orama storage create`,
`grant`, `revoke` and `prove`, `orama cluster register-onchain` and `retire-onchain`), or
`ORAMA_CHAIN_ONION`, sends the account read and the broadcast to a validator's onion service through the
Tor SOCKS5 proxy at `--onion-socks` (default `127.0.0.1:9050`, or `ORAMA_ONION_SOCKS`). `--node` and
`--onion` together are a usage error. The client has one route, the SOCKS proxy: no direct dialer, no
environment proxy, no redirects. Each command run uses one new SOCKS credential, which Tor maps to its own
circuit, so two transactions never share one. A failed onion path returns the error ("the transaction was not
sent, and nothing was tried outside Tor") and never falls back to the clearnet. Reads (`orama chain`) do not
go through Tor yet. A wallet that cannot use Tor or reach a validator submits through the gateway instead
(`POST /v1/chain/broadcast`, above).

A validator's onion service is installed by `orama global install --services onion`
([TOR_NETWORK.md](TOR_NETWORK.md#the-validator-onion-service)). It serves port 80 and forwards to
`orama global txgate`, which passes only the three calls this client makes (`GET
/cosmos/auth/v1beta1/accounts/{address}`, `POST /cosmos/tx/v1beta1/txs`, `GET /cosmos/tx/v1beta1/txs/{hash}`)
to the node's REST API and answers 404 to the rest. The service lives on the **Orama Tor network**, so
`--onion-socks` must point at a client of that network (`tornet.ClientTorrc`, SOCKS port
`constants.TorNetSOCKSPort`, 9052 by convention); the default `127.0.0.1:9050` is the node's client of the public
Tor network, which cannot resolve it. No unit on a node runs a client of the Orama network.

`--onion-network <tor-network.json>` (or `ORAMA_ONION_NETWORK`) makes the command start its own Tor client on an
Orama Tor network, with that network's directory authorities and no others, and submit through it; without
`--onion` it picks a validator onion service from the file at random for the transaction. The network file, the
client and the failure behaviour are in [TOR_NETWORK.md](TOR_NETWORK.md#onion-transaction-submission).

## `x/wasm`: contracts

Code: `chain/x/wasmpolicy` (policy and the deposit ledger), `chain/x/wasmbindings` (the Orama
bindings), `chain/contracts/standard` (the genesis contracts), `chain/app/wasm_vm.go` and
`chain/app/wasm_deposit.go` (the wiring, cgo only). This is plans/open-network/track-c-chain.md C9.
The VM is wasmd v0.70.3 on wasmvm v3.0.7; the binary needs cgo and libwasmvm (`-tags nowasm`
builds have no contracts and refuse a genesis that has any). There is no IBC, no admin key and no
`x/gov`; advertised capabilities omit `stargate` and `ibc2` (`WasmCapabilities`).

### Genesis standard contracts

Five contracts are stored in genesis as wasm codes 1 to 5, so they exist from height 1 although
upload is closed. Nothing is instantiated; anyone can instantiate a code at any time. The code's
recorded creator is the `wasmpolicy` module account, which no key controls.

| Code | Contract | Upstream source, pinned | cosmwasm-std |
|---|---|---|---|
| 1 | CW20 base (`cw20-base` 2.0.0) | CosmWasm/cw-plus tag v2.0.0, `d91c70ea53acf2ac694efa343f3697e7cd165534` | 2.2.2 |
| 2 | CW721 base (`cw721-base` 0.22.0) | CosmWasm/cw-nfts tag v0.22.0, `b11876a65890cf9ee2201f768e81b0a00ae395e9` | 2.2.1 |
| 3 | escrow (`cw20-escrow` 0.14.2) | CosmWasm/cw-tokens tag v0.14.2, `1db4b7387953538d7a0123d3732385981d18db57` | 1.5.4 |
| 4 | CW3 fixed multisig (`cw3-fixed-multisig` 2.0.0) | CosmWasm/cw-plus tag v2.0.0, `d91c70ea53acf2ac694efa343f3697e7cd165534` | 2.2.2 |
| 5 | vesting (`cw-vesting` 2.7.1) | DA0-DA0/dao-contracts tag v2.7.1, `92c44e593e6a0677a437e028514ce207efbd4d66` | 1.5.4 |

`chain/contracts/standard/manifest.json` records each contract's source tag and commit, its patch,
the toolchain and the sha256 of its wasm; the wasm files sit in `chain/contracts/standard/wasm/`.
`Load` refuses a file whose sha256 differs from the manifest. The set is a starting point, and the
CW20 base is a user token base only: it never wraps norama.

**Reproducible build.** `chain/contracts/standard/build.sh verify` (`make contracts-verify`) fetches each source at its pinned
commit (and checks the commit), applies its patch, builds with Rust 1.81.0 for `wasm32-unknown-unknown`
with `--locked`, runs `wasm-opt -Os --signext-lowering` (the official cosmwasm/optimizer's post-step)
and compares the sha256 with the manifest. `build.sh update` rewrites `wasm/` and the hashes. No prebuilt
wasm is downloaded. Two builds in different work directories produce the same hashes; that needs the
same wasm-opt (the manifest pins `wasm-opt version 132`; `ALLOW_TOOL_DRIFT=1` overrides and the hashes
will then differ).

Why the patches: (1) wasmvm v3.0.7 aborts (a dlmalloc assertion inside the contract) in contracts built
with cosmwasm-std 2.0.x or 1.1 to 1.3, so the pinned upstream `Cargo.lock` files are updated to
cosmwasm-std 2.2.2 (cw-plus) and 1.5.4 (cw-tokens); only lock files change. (2) cw-vesting's workspace
turns on cosmwasm-std's `ibc3` feature, which makes the contract require the `stargate` capability this
chain does not advertise; the patch removes that one feature. Rust 1.81 is used because rustc 1.87 and later
emit bulk-memory from the precompiled standard library, which wasmvm rejects.

**Adding them to a genesis.** `oramad genesis add-standard-contracts` adds the five codes to the `wasm`
genesis, sets wasmd's code sequence to 6, and lists 1..5 in `wasmpolicy`'s `genesis_code_ids`. It refuses
a genesis that already has wasm codes or a code set, and a binary without libwasmvm (its default genesis
has no `wasm` module). `chain/scripts/stagenet/deploy.sh` runs it after the bootstrap committee is added
and builds the static binary with `make build-linux-amd64-full`. `WITH_WASM=1 make localnet` does the same on a
localnet with a host cgo build.

**What the standard contracts cannot do with ORAMA.** They run as contracts, so the send restriction
applies to them. A CW3 multisig proposal that bank-sends ORAMA to a user fails when executed. The escrow
releases and refunds by bank send, so ORAMA escrowed for a user recipient cannot leave it (it stays in the
escrow; token and CW20 escrows work). The vesting contract's instantiate for the native denom sends a
distribution `SetWithdrawAddress`, which is refused, so ORAMA cannot be vested at all; it can vest a user
token. A contract pays a user ORAMA through the `earnings` binding below.

### Native library

The static linux/amd64 binary links one Rust archive, `chain/native` (`make native-lib-linux-amd64`,
`native/build.sh build|verify`): libwasmvm at the version `go.mod` pins (copied from the module cache, so its
source is the one `go.sum` covers, not downloaded prebuilt) and the Orchard verifier as rlibs of one
staticlib crate, so there is one copy of the Rust std. It is built with Rust 1.98.1 for
`x86_64-unknown-linux-musl`, C parts through zig (`scripts/zigcc.sh`), with `native/Cargo.lock`. wasmer_vm calls
`__rust_probestack`, which compiler_builtins now exports only under a mangled name, so
`native/src/probestack.rs` carries its x86_64 routine unmangled. The sha256 of the archive is recorded in
`native/libwasmvm_muslc.x86_64.a.sha256` with the wasmvm, rustc and zig versions it holds for (zig 0.15.2:
`ORAMA_ZIG=/opt/homebrew/opt/zig@0.15/bin/zig` on macOS). wasmvm stays on v3.0.7 (wasmd v0.70.3): v3.0.8, which
wasmd v0.70.4 requires, moves to wasmer 7, whose `sha2 ^0.11` cannot share one crate graph with the
`bip32 =0.6.0-pre.1` (`sha2 =0.11.0-pre.4`) that `zcash_primitives 0.30.1` pulls in for the Orchard verifier. The build is reproducible across checkouts: cargo hashes
a path dependency's location into crate metadata and symbol names, so `build.sh` copies the crate and its
`x/shielded/orchardffi` path dependency into one fixed directory (`/tmp/orama-native-build`, override with
`ORAMA_NATIVE_STAGE`) and remaps it, the cargo home and the Rust sysroot out of the archive; two checkouts at
different paths produce the same sha256, and `build.sh verify` checks it. The result was
linked, not run: `file` reports `ELF 64-bit LSB executable, x86-64, statically linked` for
`build/oramad-linux-amd64-full` (about 166 MB). `make build-linux-amd64-full` passes
`-tags "muslc orchardffi netgo osusergo"`.

### Upload

`x/wasmpolicy` holds `upload_sunset_height` (P6: 183 days of 5 s blocks, 3,162,240) in genesis and no
message can change it. Before that height `MsgStoreCode`, `MsgStoreAndInstantiateContract` and
`MsgStoreAndMigrateContract` are refused by the ante chain (`UploadSunsetDecorator`, matched by type
URL: wasmd's generated types carry no `XXX_MessageName`); from that height on they are allowed. Uploaded code
names no code id, so even the exact bytes of a genesis contract are refused before the sunset height.
Every other message is unaffected: anyone can instantiate a stored code at any time. A contract cannot
upload code: `CosmosMsg` has no such variant.

### The send restriction and contract messages

`norama` moves between accounts only when one side is a module account or the recipient is a contract
(`x/shielded/policy.NoramaSendRestriction`, with `isContract` bound to wasmd's `HasContractInfo`, and
`x/wasmpolicy/ante.ContractSendDecorator`, which allows every module account). So a user can pay a
contract, a contract can pay a contract or a module (the token, market and storage bindings pull
fees and escrow that way) and a contract cannot pay a user. `BankMsg::Send` to a module account is refused
by bank's blocked-address rule. wasmd's instantiate moves the attached funds before it registers the
contract, so the coin transferrer marks that recipient (`WithFundedContract`) for the one transfer.

The message handler (`wasmbindings.Messenger`) also refuses `CosmosMsg::Staking` (the delegation rules in `x/power` are ante-only, so a contract could otherwise fill the epoch reward walk with dust delegations), `CosmosMsg::Any`, which is how the stargate
form arrives, and distribution `SetWithdrawAddress`. IBC messages are refused as before. A contract reaches
a module only through the bindings.

### Orama bindings

A contract sends `CosmosMsg::Custom` with a JSON object of exactly one variant (unknown fields are
refused) and queries with `QueryRequest::Custom`. The contract's own address is the signer of every message,
there is no sender field, and the message goes through the module's own Msg server: its validation, its
fee, its deposit and its authority checks all apply.

| Message | Effect |
|---|---|
| `{"token":{"create":{...}}}` | `x/token` `MsgCreateToken` with the contract as creator; the creation fee and metadata deposit come from the contract's balance. Refused when the denom is named `norama` |
| `{"token":{"mint":{"denom","recipient","amount"}}}`, `{"token":{"burn":{"denom","amount"}}}` | mint of a token the contract created and still holds mint authority for; burn from the contract's balance. Refused for `norama` and norama-named tokens |
| `{"cnft":{"create_collection"\|"create_tree"\|"mint":{...}}}` | `x/cnft`: collections, trees and mints the contract owns (only a tree's creator mints) |
| `{"market":{"list"\|"cancel_listing"\|"bid"\|"cancel_bid"\|"settle":{...}}}` | `x/market` with the contract as seller, bidder or settler. Bid escrow comes from the contract's balance; sale proceeds are credited to the contract's earnings |
| `{"storage":{"create_deal":{...}}}` | `x/storage` `MsgCreateDeal` paid from the contract's own funds (never a granter's allowance); protocol classes are refused |
| `{"earnings":{"pay":{"recipient","amount"}}}` | moves norama from the contract into the recipient's earnings account (`x/fees.PayEarnings`): the only way a contract pays a user in ORAMA |
| `{"shielded":{...}}` | `NOT_LINKED`. The shielded adapter is the audited unshield, call, reshield flow of C12, not a trivial binding |

Queries: `{"token":{"info":{"denom"}}}`, `{"cnft":{"tree":{"tree_id"}}}`,
`{"cnft":{"verify_proof":{"tree_id","leaf","proof"}}}` (answers `{"valid":bool,"reason"}`, charged
20,000 gas plus 3,000 per proof sibling), `{"market":{"listing":{"id"}}}`. wasmd redacts a query error to its
codespace and code before the contract sees it.

Two consequences to know. Sale proceeds and earnings credited to a contract address cannot be spent by the
contract: earnings move only for the account's own signer, and a contract has none. And the `stargate`
and `grpc` query paths stay rejected (wasmd's default), so a contract cannot read any other module.

### State deposits

Contract storage is priced at `deposit_per_byte` (genesis default 68,359 norama per byte, the P3
price `x/token` and `x/nodes` also use). `x/wasmpolicy` genesis holds four locked parameters (G1,
`app/locked_genesis.go`): `deposit_per_byte`, `max_deposit_per_tx` (10 ORAMA), `max_deposit_chunks`
(32) and `chunk_overhead_bytes` (512). `depositEngine` wraps the VM
engine: each `Instantiate`, `Execute`, `Migrate`, `Sudo` and `Reply` runs against a metered store that counts
the bytes the call adds to and removes from the contract's storage (an entry weighs key plus value), and on
success the net change is settled in the ledger:

- **One chunk per payer.** Growth locks `bytes x deposit_per_byte` in `x/fees` in the payer's chunk of that
  contract (`wasm/{contract}/{seq}`): the first growth by a payer opens a chunk and also locks
  `chunk_overhead_bytes x deposit_per_byte`, which prices the ledger row and the `x/fees` deposit row; later
  growth by the same payer tops the same deposit up (`x/fees.TopUpDeposit`). A contract holds at most
  `max_deposit_chunks` chunks, so a shrink walks a bounded list and always fits in a block. A new payer
  growing a contract that is full is refused (`ErrDepositLedgerFull`); existing payers still grow.
- **Shrink** releases chunks newest created first, each refunded to the account that locked it (99% to its
  earnings, 1% burned, as for every deposit; a chunk may be released in part with `ReleaseDepositPart`, and
  its overhead is released only with its last byte). Only charged bytes are refunded: a contract's state that
  was never charged, such as any that arrives in a genesis import, refunds nothing.
- **Cap per transaction.** The ante chain gives each transaction a budget; every chunk opened or topped up
  adds to it, and a call that would take the total past `max_deposit_per_tx` fails
  (`ErrDepositCap`). A caller cannot raise it. It bounds what a contract can lock on the signer of a
  transaction that calls it, nested calls included; a submessage that later reverts still counts.
- **Who pays.** The message sender when it is a plain account; otherwise the transaction's first signer,
  which `DepositPayerDecorator` records in the context. A contract never pays. A call with no payer and
  growth fails.
- **A call that cannot pay** returns a contract error (`state deposit: ...`) and wasmd reverts it; a failed
  or reverted call charges nothing.
- **Gas.** The metered store reads the old value of a key before every write and delete, so each write costs
  one more store read than an unmetered call. That read and the ledger writes are charged to the
  transaction like any other state access.

The ledger (`ContractBytes`, `Chunks`, `Limits`) lives in `x/wasmpolicy`'s store and in its genesis
(`deposit_chunks`); the deposits themselves are `x/fees` deposits, so `x/fees`'s "deposit module balance
equals open deposits" invariant covers them (deposits are separate from earnings and fee balances, so the
earnings and fee-balance invariants are unaffected). `Keeper.CheckInvariants` (wasmpolicy) checks that each
contract's charged bytes equal the sum of its chunks and that each chunk's `x/fees` deposit exists, has the
same owner and holds exactly `(bytes + overhead) x per_byte`. It is a keeper method the tests call; the module has no
query service. Not metered: code storage (upload is closed or priced by gas), contract metadata, and IBC entry
points, which cannot run.

### Not built here

- A shielded binding (see above).
- A caller-chosen deposit cap: the per-transaction cap is a locked genesis parameter, not something a signer can set.
- Any way for a contract to spend the earnings credited to its own address.
- A `wasmpolicy` query service or CLI for the ledger and its invariants.
- The stagenet deploy has not been run against the nodes: `deploy.sh` builds the combined binary (linked, not
  executed here), installs it through `orama global install --colocated`, and was checked with `bash -n` and
  shellcheck, its Go helpers with unit tests; the same genesis was run on a localnet with the host build.

## `x/shielded`: the Ironwood shielded pool

Code: `chain/x/shielded` (`keeper`, `ante`, `types`, `bundle`, `pool`, `policy`, `nullifier`,
`snapshot`), `chain/x/shielded/verify` (the `Verifier` interface and `Check`),
`verify/orchard` (the cgo verifier, the tree and the sighash), `verify/orchardproc` (the
out-of-process verifier), `orchardffi` (the Rust library) and `orchardverifier-bin` (the Rust
verifier binary). Protos are `proto/orama/shielded/v1`; `make proto-shielded` regenerates
`x/shielded/types/*.pb.go` (needs `protoc` and `protoc-gen-gocosmos`).

The module is wired: it has a store, a transient store, a burner module account named `shielded`
and an end blocker that runs before x/staking's. It accepts a bundle only on a node that has
**both** verifiers, the orchard library linked through cgo and the verifier binary. Every other
build or node refuses every bundle (see "Fail-closed" below). There is no pause, no kill switch,
no admin key and no message that changes a parameter; parameters are set at genesis.

### Messages and how they reach the chain

| Message | Signed by | What it does | Bundle value balance |
|---|---|---|---|
| `MsgShieldedTransfer` | nobody | moves value inside the pool | `+fee` |
| `MsgShield` | the signer | signer's bank balance into the pool | `-amount` |
| `MsgShieldEarnings` | the signer | signer's own earnings into the pool | `-amount` |
| `MsgUnshield` | the target's owner | pool value to a target the signer owns | `+amount` |

The value balance is the bundle's own (Orchard's `valueBalance`): negative shields, positive takes
value out. It is in norama.

**Signer-less transfer.** Cosmos requires every message to name a signer, so a
`MsgShieldedTransfer` names the protocol's fixed address, `authtypes.NewModuleAddress("shielded")`
(`types.SignerlessAddress()`): a constant, not an account. The tx is routed to its own short ante
chain (`ante.Route`: extension options, timeout height, then its own decorator) and must be the
**only message in its tx**, with no signature, no fee, no fee granter and no memo, and it must declare **exactly** `action_gas x actions` as its gas limit. Its
fee is the bundle's value balance. A tx with a `MsgShieldedTransfer` among other messages, or a
shielded message beside another, is refused by `ante.ShapeDecorator` before any fee is taken.

A signer-less tx may carry at most `ante.MaxSignerlessOverhead` (512) bytes around its bundle: it
pays no size gas, so padding is refused. Gas simulation does not verify in the ante handler, so a
wallet computes a transfer's gas as `action_gas x actions`.

**Order of checks** (`keeper.Admit`), cheap first, in the ante handler and again in the message:
size and action count, the value balance's sign and the fee, each nullifier (duplicate within the
bundle, spent, pending), the anchor, and only then, in the mempool's `CheckTx`, the proofs. In a
block the ante handler does the cheap checks and the message server verifies, once. `ReCheckTx`
repeats the cheap checks and marks nullifiers pending again but does not re-verify. The message
server is the authority: it runs every check whatever the ante handler did.

**Pending nullifiers.** A nullifier is marked in a transient store (`transient_shielded`). In the
mempool's check state the mark is what refuses a second bundle with the same nullifier, and
`Commit` clears it. In a block the mark is written by the message, so a tx that fails after
marking rolls it back with everything else, and a tx that fails in the ante handler marks nothing.
A simulation (`Simulate`, a wallet's gas estimate) runs on the check state but also runs the message,
so the ante handler neither verifies nor marks there: the message does both, as in a block. Marked in
the ante handler, the message would refuse its own nullifiers as pending.

### Fees and gas

Nothing here is measured: the C0-4 spike measured no verify cost, and
`decisions/C12a-shielded-spec.md` does not exist yet, so the defaults are named placeholders until
the G1 sign-off, like P2.

| Parameter | Default | Meaning |
|---|---|---|
| `anchor_window_blocks` | 14,400 | how far back an anchor may be (24 h at 6-second blocks) |
| `action_gas` | 250,000 | gas one action declares and is charged |
| `max_actions_per_bundle` | 16 | bounds proof work before any proof runs |
| `max_signerless_per_block` | 64 | most signer-less transfers one block may hold; a later one fails and gives its slot back |
| `nullifier_fee` | 1,000,000 norama | burned for every nullifier a bundle inserts |
| `unshield_floor` | 1,000,000,000 norama | floor of the 24 h unshield cap |
| `max_fee_topup` | 10,000,000 norama | most one unshield to the signer's own earnings may move |
| `queue_per_address_cap` | 100,000,000,000 norama | most one address is paid from the queue per window |

* **Transfer.** The fee (the value balance) must be at least `base_fee x action_gas x actions +
  nullifier_fee x actions`. The base part and the nullifier fees are **burned**; the rest is the
  tip, credited to the proposer's earnings (`x/fees` `CreditEarnings`). If the proposer does not
  resolve, the whole fee is burned, as in the ante fee decorator. The fee leaves the pool. The
  **tip counts against the pool's 24 h cap** and a tip over what the cap has left fails the
  transfer: it becomes a proposer's spendable earnings, so an uncapped tip would let a proposer
  drain the pool through fees. The burned part is exempt.
* **Shield.** The source pays `amount + nullifier_fee x nullifiers`; the pool is credited `amount`
  and the nullifier fees are burned, so the pool holds exactly what the notes are worth.
* **Unshield.** The pool is debited `amount`, the nullifier fees are burned out of it, and the
  target receives `amount - nullifier fees`. Burned fees do not count against the unshield cap.
* **Gas.** A signer-less transfer is charged the fixed `action_gas x actions` and its own state
  accesses run on an unmetered context (its declared gas is exactly the verify schedule, so the
  store reads and writes could not be paid from it). The cost is bounded by
  `max_actions_per_bundle`. A signed message declares its own gas and pays the normal store costs
  plus `action_gas x actions` for verification. The per-block action limit is the block gas limit.
  The verify cost has not been measured on linux (C0-4); `action_gas` is not a measured price.

### Pools, turnstile, cap and queue

State is per vintage and per asset (`Pools`, keyed by vintage and a 32-byte asset id). Only
vintage 1 (orchard 0.15.5, `PostNu6_3`) and the native asset exist. **Multi-asset is off**: no
message names an asset, genesis refuses any other asset, and `pool.AllowAsset` refuses one until
the structural vote. There is no vintage-migration message yet because there is no second circuit;
`pool.Pools.Move` is the tested turnstile logic waiting for one.

* **Turnstile.** A pool never pays out more than went in: a debit over the balance fails the tx
  (`pool.ErrUnderflow`). `TestShieldedReal_shieldTransferUnshieldThroughFinalizeBlock` drains a
  pool to exactly zero through real proofs.
* **Cap.** Net outflow per pool per rolling 24 h is at most `max(2% of the pool, unshield_floor)`
  (`pool.Limiter`, persisted). The window opens with the first outflow and rolls after 24 h of
  block time. The cap's base is the pool balance before the outflow; queued amounts have already
  left the pool, so they shrink the cap slightly.
* **Targets** (`MsgUnshield.target`), each owned by the signer, who is the only party the message
  can name (there is no beneficiary field):

| Target | Behaviour |
|---|---|
| `BOND` | the signer's own delegation to `validator`, through x/staking's `MsgDelegate`, held to x/power's `min_delegation_for_rewards` (the rule its ante decorator applies to a signed `MsgDelegate`, which a delegation made from this module would skip): a payment that would leave the delegation strictly between zero and the minimum is refused. Over the cap, or behind a non-empty queue, it **queues**. |
| `NODE_BOND` | the signer's own x/nodes role bond on `node_id` (`MsgBondNode` with the signer as operator, so x/nodes refuses a node the signer does not own). Queues like `BOND`. |
| `FEE_TOPUP` | credits the signer's own **fee-only balance** (`x/fees` `CreditFeeBalance`, the `FeeBalances` ledger `MsgFundHotKey` also credits: it pays base fees and cannot be bonded, sent, shielded or deposited), at most `max_fee_topup` per tx. Counts against the cap and **fails the whole tx** when it does not fit; it is not queued. |
| `DEPOSIT` | **not linked** (refused in `ValidateBasic`, before any proof work): no module has a path that tops up an existing deposit. |
| `CONTRACT` | **not linked** (refused in `ValidateBasic`): the audited unshield-call-reshield adapter is not built. It would fail atomically over the cap, as specified. |

* **Queue.** A queued unshield has already spent its notes and left the pool (the turnstile); its
  coins stay in the module account and the module balance equals the pools plus the queue. The
  block's end blocker serves the queue once per 24 h window: capacity is what the cap has left,
  split pro rata by amount over one request per address (`pool.Serve`) and capped per address at
  `queue_per_address_cap`, so no request at the head holds the queue. A grant is paid FIFO across
  that address's requests. Capacity left after the per-address cap is not redistributed. The
  block that queues a request also ends by serving its window, so part of it can be paid at once.
  A window makes at most `keeper.MaxServedPerWindow` (1000) payments, oldest first, so the end
  blocker's work does not grow with the queue; the queue is not otherwise bounded, and a
  request that is queued while the queue is non-empty takes no cap until it is paid. A bond is
  refused up front when its validator does not exist or the amount alone is below the minimum;
  a queued grant that would leave a delegation below the minimum is not paid.
  A target that refuses a payment (a validator that no longer exists, a retired node, a delegation
  that would be dust) keeps its request queued and the block emits `shielded_queue_payment_failed`; the other requests are paid.
  There is no cancel message, so a request whose target never accepts stays queued.

### The note-commitment tree and anchors

The chain keeps the tree's **frontier** (the right edge of the depth-32 Sinsemilla tree), the
current root and the size in IAVL. Sinsemilla exists only in Rust, so the frontier is appended
through `orama_orchard_tree_append` (a stateless function over bytes, tested against the empty
root in the vectors' anchors and against real spends). Frontier encoding: empty is zero bytes;
otherwise `position (u64 LE) || leaf (32) || one ommer (32) per set bit of the position`. Every
accepted bundle appends its action commitments in order; the chain never creates notes.

At the end of every block the current root is recorded as an anchor with that height, and the
anchor that just left the window (`anchor_window_blocks`) is dropped, so an idle tree's root never
expires. A bundle's anchor is valid when it is the **empty tree's root** (only fake spends can use
it, and shielding bundles do) or a recorded root within the window. An anchor produced by a block
is usable from the next block. A build without the Rust library cannot append, so it cannot take
a bundle either.

### Nullifiers: outside IAVL, committed by an accumulator

The nullifier set lives in a **dedicated append-only database** under the node home, not in IAVL
(`<home>/data/shielded_nullifiers.db`, same backend as `application.db`, so `pebbledb` by
default; `oramad tendermint unsafe-reset-all` removes it with the rest of `data/`).
`nullifier.Store` keeps two key spaces: `n || nullifier -> height` (the index) and
`h || height || seq -> nullifier` (the log, in insertion order).

The app hash commits to it through a **running accumulator kept in module state (IAVL)**:
`acc = SHA-256(acc || nullifier)` folded over every nullifier in insertion order, starting from 32
zero bytes, plus a count. The set itself is not hashed; the accumulator is what two nodes must
agree on, and `CheckInvariants` recomputes it from the database.

* **Writing.** A message folds its nullifiers into the accumulator (IAVL, so a failed tx rolls
  back) and marks them pending. The end blocker writes the block's pending nullifiers to the
  database, in order, before `Commit`.
* **Reading.** A record written at height `h` is visible to a reader at height `asOf` when
  `h < asOf`. A block reads at its own height and the mempool's check state at the last committed
  height plus one. So a block never sees its own records, or those of a block it replaces.
* **Crash between FinalizeBlock and Commit.** The database holds the block's records and IAVL does
  not. On restart the block runs again: its records are invisible to it, and its end blocker first
  deletes every record at or above its height and then writes its own. The replay gives the same
  app hash (`TestShieldedReal_aBlockReplayedAfterACrashIsIdentical`).
* **Rollback** (`oramad rollback`, or a restart at an older height): records above the height are
  invisible and are deleted when the first block at that height commits.
* **Genesis export/import.** Export lists the first `nullifier_count` records (records past that
  belong to a block that never committed) in insertion order, and refuses if the database and the
  state disagree. Import writes them at height 0, so they are visible to a chain starting at any
  initial height, requires an **empty** database, and `Validate` refuses a list that does not fold
  to `nullifier_accumulator`. The genesis is as large as the set (about 32 bytes per nullifier
  plus JSON).
* **State sync.** The IAVL snapshot carries only the accumulator and the count. The
  `snapshot.Extension` (`shielded_nullifiers`, format 1) adds the records to the snapshot stream
  and, on restore, imports them into an empty database and **fails the restore** unless they fold
  to the accumulator and count the restored state committed to. This is unit tested; it has not
  been run through a real state-sync between two nodes.
* **Disk.** About one 32-byte index entry and one log entry per nullifier plus key overhead. The
  real per-nullifier number is C0-2/C0-7's to measure.

### Sighash and the unshield binding

The chain has no Zcash transaction, so the sighash is ours and is computed in Go
(`orchard.Sighash`); both verifiers are given it, never compute it:

```
SHA-256( "orama-shielded-ironwood-sighash-v1" || u16be(len(chainID)) || chainID || effectingData )
```

`effectingData` is the bundle before the proof (count, actions, flags, value balance, anchor). An
**unshield** also commits to its signer and target, under its own domain:

```
SHA-256( "orama-shielded-ironwood-sighash-bound-v1" || u16be(len(chainID)) || chainID
         || u32be(len(binding)) || binding || effectingData )
binding = u8(len(signer)) || signer || u8(target) || u8(len(validator)) || validator
          || u16be(len(node_id)) || node_id || u8(role)        (address bytes, not bech32)
```

Without it, anyone who saw an unshield in the mempool could submit the same bundle as their own
unshield and take what it pays out. A bundle a wallet built for one signer and one target fails its
signatures under any other (`TestShieldedReal_anUnshieldCannotBeRedirected`). Transfers and
shields are not bound: a copied transfer pays its own fee once, and a copied shield spends the
copier's own funds. The chain ID stops replay across Orama networks.

### Two verifiers, and the C12a decision

`verify.Check` needs `verify.MinVerifiers` (2) verifiers and **all** must accept. The app wires:

1. **The library.** `orama_orchard_verify` (the C ABI in `orchardffi`) linked through cgo in an
   `orchardffi` build. Upstream `orchard` 0.15.5 (feature `circuit`), `PostNu6_3` verifying key
   only, `InsecurePreNu6_2` never built.
2. **The binary.** `orama-orchard-verifier`, a **separately built and separately pinned Rust
   program** (`orchardverifier-bin`: its own crate and its own `Cargo.lock`, sharing no source
   with the library) that the node runs **out of process**.

**Decision (C12a): the second verifier is the separately built and pinned Rust binary, not a Go
verifier.** No Go Halo 2 verifier exists, and writing one for a circuit that has a soundness-bug
history (`InsecurePreNu6_2`) is a project of its own that this change does not attempt.

*What the independence gives.* A memory-corruption bug, a linking or ABI problem, an allocator or
threading bug in the cgo path, a hang, or a crash in one verifier cannot silently accept a bundle
in the other. The binary is built, pinned and updated on its own, so a bad dependency bump in one
lock file does not move the other. The two also run different checks of framing: the library's is
Rust called from Go, the binary's is a separate copy of the same canonical order (framing and
proof length, spend-authorization signatures, binding signature, then the Halo 2 proof).

*What it does not give.* Both call the same upstream crate, so **a logic bug in `orchard` itself
(circuit, verifying key, signature verification) is shared by both and accepts the same bad
bundle in both.** This is not the "two independent implementations" the spec describes. **A
genuinely independent implementation (a Go verifier, or a Rust one that does not use `orchard`) is
an open item**, and the G3 audit budget for it stands.

**Protocol** (`verify/orchardproc`; frames are length-prefixed, `u32` big-endian):

```
request  = u64 BE id || sighash (32) || bundle
response = u64 BE id || result code (1)        codes as the library: 0 ok, 1 malformed, 2 proof
                                               length, 3 proof rejected, 4 signature rejected,
                                               5 panic
ready    = a response with id 2^64-1 and code 0, sent once the verifying key is built
```

The process is started on first use and kept; requests are serialized (one mutex); a request over
1 MiB, a short read or a closed stdin ends the process. A request that exceeds its hard timeout
(30 s; 60 s to become ready), whose process dies, or that is answered out of protocol kills the
process and **is rejected** with `verify.ErrVerifierFault`; it is never retried and never turned
into an accept. The next request starts a fresh process. A process that died while idle is
restarted before the request, not counted against it. Tested with a fake process (hang, die,
wrong id, bad frame, bad code, killed from outside, concurrent) and with the real binary (stopped
process times out then recovers, killed mid-request, killed idle, and agreement with the library on
every mutation of every vector).

**Verdicts are deterministic; faults are not.** `ErrTampered` (proof, signature, framing) is a
verdict every node reaches. `ErrVerifierFault` (timeout, crash, missing binary) is a fact about
one node. In `FinalizeBlock` a fault fails the tx on that node only, so that node's app hash
diverges from the others and it halts: fail-stop, not a fork. It is the behaviour the spec asks
for (a crashed or timed-out verifier rejects), and operators must treat a fault in a block as an
incident. The timeout is generous for that reason.

### Fail-closed

| Situation | Result |
|---|---|
| `CGO_ENABLED=0`, or no `orchardffi` tag | the library verifier refuses every bundle (`ErrVerifierNotLinked`); nothing is accepted |
| verifier binary missing or `--shielded-verifier` unset with nothing at the default path | the second verifier refuses every bundle; the node logs a warning at start |
| chain ID empty | no verifiers are built (the CLI's throwaway app instance), so `verify.Check` refuses; a verifier is never built for the empty chain (`ErrEmptyChainID`) |
| verifier binary not the pinned one | the process verifier refuses to start it (`ErrBinaryPin`); a node that configured it will not start |
| fewer than 2 verifiers, or a nil one | `verify.Check` refuses |
| one verifier rejects or faults | the bundle is refused |

`TestShielded_buildWithoutTheVerifierAcceptsNoBundle` runs a real vector through `FinalizeBlock` in
the default build. `orama-orchard-verifier` is found by, in order of precedence, the
`--shielded-verifier <path>` flag of `oramad start`, then `<home>/bin/orama-orchard-verifier`.

**Start-up.** A node with a chain ID does all of this before it serves a block, and stops with a
message that names the cause and the flag when any step fails:

* the orchard library's verifying key is built (`orchard.Warm`, an `orchardffi` build);
* the verifier binary is read and its **SHA-256 must equal the pin**, `--shielded-verifier-sha256`
  or the release's link-time `app.ShieldedVerifierSHA256` (`make orchard-verifier` prints it,
  `make build-linux-amd64-orchard` and `-full` write `build/orama-orchard-verifier-linux-amd64.sha256`); no pin
  refuses too. The file is hashed and then executed, so its directory must be writable only by
  the node's user;
* a configured verifier binary (the flag, or a file at the default path) is started and warmed;
* **the nullifier database must fold to the committed accumulator and count**
  (`Keeper.CheckNullifierStore`, first `count` records; records past that are an uncommitted
  block's). A missing, short or foreign database stops the node: restore
  `data/shielded_nullifiers.db` together with `application.db`, or reset the node's data and sync
  again. The fold reads the whole set, so it grows with the chain.

### Admission control in the mempool

A signer-less transfer pays nothing before its proof is checked, so `CheckTx` limits what a stream
of bad bundles can cost (`keeper.Verify`, `admission.go`; all of it local to a node, none of it in
a block):

* cheap checks first: size, value balance and fee, nullifiers (spent or pending), anchor, the
  signer-less shape and gas, and only then a proof;
* a bounded memory (4096) of bundles that failed a proof or signature check, keyed by the hash of
  their exact bytes, so a repeat costs nothing; a verifier fault is never remembered, and one
  changed byte is another bundle;
* at most `MaxCheckVerificationsPerBlock` (256) proofs verified per node between two blocks; more
  are refused with `ErrMempoolBusy` (not invalid, send again next block);
* in a block, `max_signerless_per_block` bounds the signer-less transfers a block may hold.

These bound the cost, they do not remove it: an attacker who mutates proof bytes still costs each
node one verification per distinct bundle up to the per-block budget. A per-peer rate limit is E3's.

### Genesis, export and invariants

Genesis carries the parameters, pool balances, limiters, the queue, the frontier, the size, the
current root, the anchors, the accumulator, the count and the nullifier list. `Validate` refuses
another asset, a negative or duplicate pool, a queued fee top-up, a frontier without a size, and a
nullifier list that does not fold to the accumulator. `InitGenesis` needs an empty nullifier
database. **Invariants** (`CheckInvariants`, query `Invariants`): the module account balance equals
the sum of pool balances plus the queued unshields; no pool or queued amount is negative; the
nullifier database folds to the accumulator in state. The tests also check the fee module's own
invariants after every scenario.

### Building and the verifier binary

```sh
cd chain
make orchard-lib            # the static library for this host (x/shielded/verify/orchard/lib/)
make orchard-verifier       # the verifier binary for this host (orchardverifier-bin/target/release/)
make orchard-test           # cargo test for both crates, then go test -tags "nowasm orchardffi"
rustup target add x86_64-unknown-linux-musl
ORAMA_ZIG=/opt/homebrew/opt/zig@0.15/bin/zig make build-linux-amd64-orchard
```

`make build-linux-amd64-orchard` (nowasm) and `make build-linux-amd64-full` (CosmWasm and the
library, one static native library so one copy of the Rust std, `chain/native`) each write **two
files** into `build/`, both static linux/amd64 (musl, C parts through zig): `oramad-linux-amd64-orchard`
or `oramad-linux-amd64-full`, and `orama-orchard-verifier-linux-amd64` with its
`orama-orchard-verifier-linux-amd64.sha256`. The same make run **links that sha256 into oramad** (the
pin), so a release's oramad runs exactly the verifier built with it and no other file. `native/`
carries the orchard library (with the note-commitment tree function) as an rlib of the one static
library; the verifier binary is a separate crate and is not part of it.
`scripts/stagenet/deploy.sh` installs both: the verifier goes in the chain home's `bin/`, root-owned, which is where
oramad looks by default (`--shielded-verifier` names another path); `orama global install` does not stage it. `orama build`
does not build `oramad`; these targets are the release path, and `make build` is unchanged and
produces nodes that accept no shielded bundle. All were built and linked for linux/amd64 on
macOS/arm64; they were not run on linux.

### Test vectors and tests

`orchardffi/testdata/` holds real Ironwood bundles for chain ID `orama-localnet-orchard-vector-1` (a localnet ID, so the locked genesis parameters do not apply and tests set small fees and caps; a production ID needs every shielded parameter at its locked value), each
with its sighash: `ironwood-1-action` and `ironwood-2-action` (shielding; spends disabled, dummy
spends), then a flow that continues the 1-action bundle's note: `ironwood-transfer` (spends the
5000 note, pays a fee of 100, value balance +100), and two alternatives that spend the 4900 change
note and unshield all of it: `ironwood-unshield` (bound to alice, `FEE_TOPUP`) and
`ironwood-unshield-bond` (bound to alice and committee member 0's validator address, `BOND`).
`cargo run --release --example gen_vectors -- testdata` and `... --example gen_flow_vectors --
testdata <signer hex> <validator hex>` regenerate them (these prove; nodes never do). The flow is
proven against the tree the chain builds when it appends each earlier bundle's commitments, so a
chain test replaying it checks the FFI tree and the anchor window against real proofs.

* Keeper, ante, module and store tests use `x/shielded/testutil`: a verifier that accepts, a tree
  that is not the Orchard tree, in-memory bank and fee keepers, and a bundle builder. **Test only**:
  `TestNoProductionImport` fails if any non-test file imports it.
* `app/shielded_real_test.go` (`orchardffi` tag, both real verifiers, `FinalizeBlock`): shield,
  transfer, unshield with the pool drained to zero; replayed and tampered bundles; the same
  bundle twice in one block; a stale anchor; a fee below the floor; wrong gas, fee or signature on
  a signer-less tx; an unshield under another signer or target; cap exhausted (fee top-up fails
  atomically, bond queues and is paid through x/staking); shielding earnings with the 2-action
  vector; a block replayed after a crash with an identical app hash; a killed verifier process.
* Rust: `cargo test` in both crates (vectors, tree, the protocol over real pipes).

### The library verifier in detail

**What verifies a bundle.** Upstream `orchard` (Zcash) with feature `circuit`, called from Go
through a tiny C ABI (`orama_orchard_verify`, header `orchardffi/include/orama_orchard.h`).
Nodes only verify. The crate has no prover, and no circuit of ours. The Halo 2 verifying key is
built once, lazily, for `OrchardCircuitVersion::PostNu6_3` only (`InsecurePreNu6_2` is never
built), and shared by every call. The first verification in a process pays the key build (about
1 s on the Apple M3 below), so the first bundle a node verifies waits for it; the verifier binary
builds its key before it says it is ready. Verification checks, in
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

**Errors.** `ErrMalformed`, `ErrProofLength`, `ErrProofRejected` and `ErrSignatureRejected` all
wrap `ErrTampered`. `ErrVerifierFault` is an internal fault, not a verdict on the bundle.

**Local measurements** (Apple M3, darwin/arm64, 8 cores, `go test -bench Verify -benchtime 20x`
after the key is built; these are local numbers only, not linux and not amd64, and they set no
gas price):

| bundle | 8 threads (default) | `RAYON_NUM_THREADS=1` |
|---|---|---|
| 1 action | 5.5-5.7 ms | 22.8-23.2 ms |
| 2 actions | 6.9-7.1 ms | 26.6 ms |

The linux/amd64 and linux/arm64 numbers are not measured (C0-4). Both verifiers were run against
every vector and mutation on this host and agree; the linux/amd64 binaries were built and
linked, not run.


### Not built here

* A Go or otherwise independent second implementation (open item above).
* The contract path (`CONTRACT`) and the audited adapter, and `DEPOSIT` unshields.
* Vintage migration (`Move` is logic only), multi-asset, shielded delegation tokens, cNFT
  burn-leaf notes, and private delegator ballots.
* Mempool DoS limits per peer and per onion service (E3), and the gas price: `action_gas` and
  `nullifier_fee` are placeholders.
* A real two-node state-sync of the nullifier database, and a verifier run on linux.
* Per-peer admission control for `CheckTx` (E3); the per-node budget above bounds the cost, it does not remove it.
* The start-up check does not pin the verifier binary by version, only by hash, and the library's
  own build is not pinned at all; both fail-stop if they differ between nodes.
* The `Invariants` and `Pools` queries read the whole nullifier database and queue, unpaginated;
  keep them off public RPC nodes.
* `oramad query shielded ...` and `oramad tx shielded ...` are the
  autocli commands generated from the services (`params`, `pools`, `tree-state`,
  `nullifier-spent`, `invariants`); there is no hand-written CLI.
* A store upgrade: the module has its own store, so a chain that already produced blocks needs an
  upgrade plan with `StoreUpgrades{Added: ["shielded"]}` to gain it. Nothing here writes one.


### Shielded keys (F7)

Code: `chain/x/shielded/wallet` (Rust crate `orama-shielded-wallet`, library and the `gen_scenario` example). It is wallet-side: it proves, it is not a dependency of `oramad`, and
no node links it. It pins the same `orchard` 0.15.5 as `orchardffi` (exact `=` pins; `zip32` and
`zcash_spec` are the versions orchard resolves to) and its `Cargo.lock` is committed.

**Key tree.** ZIP-32 hardened-only Orchard derivation, with Orama's master personalization:

```
BIP-39 seed (64 bytes)
  -> HKDF-SHA256(salt = "orama-shielded-v1", info = "", L = 32)        RootWallet branch convention
  -> master = BLAKE2b-512(personal = "OramaIP32Orchard", seed32)       (sk, chain code)
  -> m / 32' / 1329811789' / account'                                  ZIP-32 child derivation
  -> SpendingKey -> FullViewingKey -> IncomingViewingKey / OutgoingViewingKey (external, internal)
  -> address at diversifier index j (external scope)
```

| constant | value |
|---|---|
| HKDF branch (salt) | `orama-shielded-v1` (a new RootWallet branch; RootWallet must add it to `HKDFDerivation.ts`) |
| master personalization | `OramaIP32Orchard` (Zcash uses `ZcashIP32Orchard`) |
| child derivation | ZIP-32 Orchard, `PRF^expand` domain `0x81`, unchanged |
| purpose | `32'` |
| coin type | `0x4F52414D` = 1329811789 (ASCII "ORAM"), hardened; not a SLIP-44 registration, unrelated to Cosmos coin type 118 |
| account | hardened index, below 2^31 |

Because the master personalization differs, no Orama key equals a Zcash Orchard key from the same
seed, and the HKDF step keeps the tree disjoint from every other RootWallet branch of that seed. An
address is the raw 43-byte Orchard address (11-byte diversifier, 32-byte `pk_d`); no text encoding
is defined yet.

**Vectors.** `chain/x/shielded/wallet/testdata/keys/keys.json`: the all-"abandon ... about" seed
(`5eb00bbd...ce9e38e4`) and two BIP-39 test phrases; for accounts 0 and 1 each: chain code, spending
key, full viewing key (96 bytes), external and internal IVK (64 bytes) and OVK (32 bytes), and the
first three external addresses. `cargo test` recomputes them and also checks the framework against
orchard's own `SpendingKey::from_zip32_seed` under Zcash's personalization.

**Builder and bundle vectors.** `cargo run --release --example gen_scenario > testdata/bundles/scenario.json`
builds real Ironwood v6 bundles from an in-memory note tree: two shields, a transfer that spends a
real note (one real spend, change back, **and a fee of 100 left in its value balance**, which the
chain requires of a transfer), and an unshield that spends a real note and is **bound to its signer
and target** (`sighash::unshield_binding`; the scenario's signer is the app tests' alice and the
target is `FEE_TOPUP`). A shield needs nothing extra in its bundle: the chain takes the nullifier
fee on top of the amount from the funds that pay for it. Each step carries the canonical bundle bytes, the effecting data, the Orama sighash (for chain
ID `orama-localnet-shielded-wallet-1`, the binding when it has one), the anchor, the nullifiers, the commitments and the value
balance. Generation uses a seeded ChaCha20 RNG, so a regeneration reproduces the file byte for byte
(about 20 s of proving).

**Chain path.** `TestShieldedReal_theWalletBuilderScenarioRunsThroughTheChain` (`orchardffi` build,
both real verifiers, `FinalizeBlock`) runs the scenario in order: shield from a bank balance,
shield from earnings, the signer-less transfer with its fee, the bound unshield to a fee-only
balance. Before each step the chain's tree root equals the anchor the wallet used, the wallet's
binding equals `MsgUnshield.Binding`, the tip and fee balance are the expected numbers, every
nullifier is spent and the invariants hold; the same unshield at another target is refused.

**Verify path.** Every committed bundle is checked three ways: Rust `cargo test` passes it through
`orama-orchard-ffi` (the chain's verifier crate); the Go test `TestSighash_matchesWalletBuilder`
recomputes the sighash with `orchard.Sighash` and requires equality; and, under the `orchardffi`
build, `TestVerify_walletBuilderBundlesAccept` runs each bundle through `orchard.New(chainID).Verify`.
Tampered proofs, ciphertexts, nullifiers, anchors, value balances, signatures and chain IDs are
rejected. `make orchard-test` runs all of it.

**Not done.** Scanning is trial decryption of one bundle at a time; the note tree recomputes roots
and paths from every leaf and is for tests and vectors, not a wallet's incremental witness store.
There is no RootWallet integration, no address text format, and no fee or delegation logic here.

## Building

```sh
cd chain
make build   # linux/amd64, darwin/amd64, darwin/arm64, all CGO_ENABLED=0 (no cgo dependency;
             # the shielded library verifier is not linked, so these accept no shielded bundle,
             # see x/shielded above)
make test    # go test ./...    (chain-test is an alias)
make lint    # go vet ./...
```

`make build` passes `-trimpath` and version `ldflags` (from `git describe` and the short commit
hash), so `oramad version --long` reports something meaningful and binaries don't embed local
filesystem paths.

## Running `oramad` under cosmovisor

The global-role unit that `core/pkg/install` renders (`RenderGlobalChainUnit`,
`orama-global-chain.service`, the unit `orama global install` writes) runs `/usr/lib/orama-global/bin/cosmovisor run start --home
/var/lib/orama-global/chain ...` with `DAEMON_NAME=oramad`, `DAEMON_HOME=/var/lib/orama-global/chain`
(`constants.ChainHome`), `DAEMON_ALLOW_DOWNLOAD_BINARIES=false` and
`DAEMON_RESTART_AFTER_UPGRADE=true`, and mounts `cosmovisor/genesis` and `cosmovisor/upgrades`
read-only (`ReadOnlyPaths=`). Cosmovisor runs `DAEMON_HOME/cosmovisor/current/bin/oramad`
and, when the chain halts at an upgrade plan's height, points `current` at
`cosmovisor/upgrades/<name>` and restarts. It never downloads a binary; a plan with no staged
binary halts the chain until one is staged. Before every start it also runs the
double-sign guard (`orama global validator check-sign-floor`) as root.

`orama global install` installs the pinned cosmovisor: **cosmovisor/v1.7.3** of
`cosmos/cosmos-sdk` (tag object `6dee2e6f`). The operator stages the official
`cosmovisor-v1.7.3-linux-<amd64|arm64>.tar.gz` beside `oramad`; the installer
checks its SHA-256 against the digest pinned in `constants.CosmovisorTarballSHA256`
(amd64 `3df6ef38…332e`, arm64 `ff27992e…95cd`; both equal the release's published
`SHA256SUMS-cosmovisor-v1.7.3.txt` and the digest the GitHub release API reports)
and installs only the tarball's `cosmovisor` file, root-owned 0755. The installer
then places the staged `oramad` as `cosmovisor/genesis/bin/oramad` (the layout
below) and points `current` at it. That step needs the chain home to have a
genesis; a second run with the same `oramad` bytes is a no-op, and different
bytes are refused: a consensus-breaking binary goes in with `stage-oramad
--upgrade <plan>`. There is no installed path for a patch release that changes no
consensus behaviour (the B6 updater is not built): stage it as an upgrade plan.
The stagenet deploy script below installs through this command.

Binaries enter the layout through `orama global install` (the genesis binary, verified only by the bytes the operator staged) and `orama global stage-oramad` (run as root, TUF-verified):

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

`chain/scripts/stagenet/deploy.sh reset|up|start|status|invariants|register|smoke|gen-shielded` deploys the
project's stagenet nodes through the product's own install path, so a stagenet deploy exercises the code
operators run: `orama global install --colocated`, `orama global start`, and `orama global bind`, `register`,
`bond` and `capacity`. Each stagenet machine already runs a private-cluster node, so the global services are
installed co-located, in the `orama-global` network namespace ([RUN_A_GLOBAL_NODE.md](RUN_A_GLOBAL_NODE.md),
"Sharing a machine with a cluster node"); the script never starts, stops or reconfigures a cluster service.
The procedure, the environment variables and the order of the commands are in
[DEV_DEPLOY.md](DEV_DEPLOY.md), "Stagenet: the chain and the global services".

What the script does that the docs of the individual commands do not say:

- **Guards.** It refuses to run unless `CHAIN_ID` contains `-stagenet-` or `-devnet-`, and validates every value
  it reads back from a node (a validator address, a node id, a consensus pubkey) and every environment value
  before it reaches a remote command line, which is built with `printf %q` quoting.
- **Binaries.** `oramad` is `make build-linux-amd64-full` (CosmWasm and the Orchard verifier in one static
  binary, see "Native library" under "`x/wasm`: contracts") beside the pinned `orama-orchard-verifier`;
  `orama-global` and the `stagenet-node` helper are `make build-linux-amd64-global`; the `orama` CLI is
  `make -C core build-linux`. Kubo v0.43.1 and cosmovisor v1.7.3 are downloaded from their official releases
  and checked against pinned digests before anything is staged: Kubo's sha512 is the release's published
  `.sha512` file and its sha256 the digest GitHub reports for the asset; the cosmovisor sha256 is the one
  `core/pkg/constants/cosmovisor.go` pins, and the script checks the two agree.
- **Two install runs.** The genesis needs each node's own consensus key, which exists only once the chain home
  does. The first run of `orama global install --init-chain` therefore uses a placeholder genesis of the right
  chain id and creates the keys; the script then builds the real genesis from the three nodes' public keys
  (in a scratch home on the first node, so no key leaves a node), replaces the placeholder, and runs the same
  install again with `--persistent-peers`. Running the command twice with the same flags is a supported no-op
  apart from the units it rewrites. The peers are the nodes' **public** addresses: the namespace cannot reach
  the WireGuard mesh.
- **Genesis.** Zero supply, a bootstrap committee of the three nodes, the five standard contracts, a 300 s
  epoch and 10 blocks per epoch, a finite `max_gas`, and `vote_extensions_enable_height` 2
  (`VOTE_EXTENSIONS_ENABLE_HEIGHT`), so stagenet runs inclusion lists.
- **The shielded verifier.** `orama global install` stages `oramad` and `orama` but not
  `orama-orchard-verifier`. oramad looks for it at `<home>/bin/orama-orchard-verifier` when `--shielded-verifier`
  is not given, and the global chain unit does not give it, so the script installs the pinned file there, root's
  and mode 0755 (the sha256 linked into oramad is what binds it). A node without it accepts no shielded bundle.
- **`register`.** After the chain has run two epochs (polled, not slept) it registers, per node: the operator
  (`MsgRegisterOperator`, which no `orama` command builds, so `stagenet-node register-operator` sends it), the
  hot-key binding (`orama global bind`), the node with STORAGE and ARCHIVER roles, the declared ASN and the
  provider endpoint (`orama global register`), both role bonds from earnings (`orama global bond`), the hot key's
  fee-only balance (`MsgFundHotKey`) and the capacity (`orama global capacity`). A node has one hot key: the
  provider's, which the script copies to the archiver's home before the archiver is pointed at a node id.
  The `orama` commands sign through the RootWallet agent protocol; on a stagenet node
  `stagenet-node agent` stands in for the agent, serving `/v1/orama/tx/sign` on a root-owned unix socket for
  the run. It is fed the operator key straight from oramad's test keyring over one pipe on the node, so the key
  is never printed, stored or copied off the node. The keyring is `keyring-backend test` (unencrypted, on disk):
  appropriate for a devnet/stagenet operator key that holds no real value, never for anything that does.
  The ASN each node declares is the true one (`ASN_<name>`, default 16276, OVH). Protocol deals, ARCHIVE
  deals included, give a slot only to a node with a declared ASN distinct from the other slots', so while every
  provider shares one ASN they stay unassigned; the archive check reports that from chain state as a SKIP.
- **`reset`.** Stops and disables every `orama-global-*` unit (through `orama global stop` first), removes the
  namespace layout and the ufw rules tagged `orama-global` as RUN_A_GLOBAL_NODE.md's removal steps say, restores
  the two lines the install added to the cluster's `preferences.yaml` (`role: both`, `global_netns`), and deletes
  the `orama-global` state and binary directories, the release directory and the helper. Each step tolerates the
  thing it removes being absent, and it also removes a legacy install that ran `oramad` under its own unit.
- **`invariants`.** Runs `oramad query <module> invariants` against the namespace address for every module in
  `INVARIANT_MODULES` (emission, fees, storage, nodes, relay, houses, token, market, power, shielded) on every
  node, and fails if a query fails or any check is false.
- **`smoke`.** `chain/scripts/stagenet/smoke` (`stagenetctl`) runs on the operator's machine and prints PASS,
  FAIL or SKIP per check: blocks advance and the injected inclusion-list commit starts block 3 and the tip;
  every module's invariants on every node; the five standard contracts are stored and a CW20 instantiates and
  transfers; a PRIVATE storage deal through `orama storage seal`, `create`, `put`, `get` and `open` with slots on
  three distinct providers and proofs accepted over an epoch; the first archive range is attested by three
  operators, its ARCHIVE deals opened, and it is archived (SKIP with the cause when the deals stay unassigned
  because the providers share an ASN, and when the chain is below height 1000, the archiver's range width);
  the shielded wallet scenario; and `/v1/chain/query` on the gateway returns each node's x/nodes record, over TLS
  with the staging CA. The chain's RPC and REST API listen on the namespace address 198.18.0.2, which each node's host
  reaches directly, so it reaches them with `ssh -L`, and it signs through the agent, forwarded
  over a unix socket, so no key reaches the operator's machine. Every transaction of a stagenet account is paid
  from earnings: a stagenet account holds no bank balance (zero supply, and users cannot send norama to each
  other), so a shield is `MsgShieldEarnings`.
- **`gen-shielded`.** Builds the wallet scenario for this chain: `gen_scenario` reads
  `ORAMA_SCENARIO_CHAIN_ID`, `ORAMA_SCENARIO_UNSHIELD_SIGNER`, `ORAMA_SCENARIO_SCALE` and `ORAMA_SCENARIO_FEE`
  (all unset gives the committed localnet scenario byte for byte), and `stagenetctl shielded-env` sizes the
  transfer fee to the chain's action gas, base fee and nullifier fee.

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
  contract, and a contract can pay a contract or a module account. The private path is `x/shielded`: a
  node accepts a shielded bundle only with the orchard library linked (a cgo `orchardffi` build) and
  the verifier binary present, so on any other node payments between users are not possible at all.
  The two verifiers share the upstream `orchard` crate; a genuinely independent implementation is open.
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
  `x/wasmpolicy` meters contract storage and locks its deposits through the same interface.
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
