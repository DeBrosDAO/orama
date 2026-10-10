# The chain

> **At a glance.**
>
> - **What:** `oramad` is the Orama L1, a Cosmos SDK v0.54.4 application on CometBFT v0.39.4, wired by hand in one Go module (`chain/`) that `core/` never imports. Four things set it apart from a stock chain: norama is public by default, with a shielded pool for private payments; the validator set comes from `x/power`, a blend of an equal-share bootstrap committee and capped stake, not from `x/staking`; ordinary transactions pay a burned base fee through `x/fees`; and a block must contain the transactions a 2/3 quorum of validators listed in their vote extensions. No governance module can change a stock module. A change needs a new binary on a new genesis.
> - **Key numbers:** 21 stores; ports 31000 to 31004; blocks every 5 s; 19 ante decorators; voting power scale 10^9; power cap 5% per operator (3% above 60 active operators); 30-epoch stake ramp; at most one third of power moves per block; bootstrap committee of at least 30 on a production chain; vote-extension list 32 KiB; query gas limit 2,000,000.
> - **Code:** `chain/app/`, `chain/x/power/`, `chain/x/inclusion/`, `chain/client/`, `core/pkg/chainread/`.

![The chain node, the clients that reach it and the paths between them](../technical-reference/diagrams/ch39-overview.svg)

## Why a chain, and why this shape

Private clusters carry tenant data. They do not carry value that strangers must agree about: who runs a global node, who owes whom for a storage deal, who may relay Tor traffic, how many norama exist. That needs a ledger no single operator controls, and five constraints shaped it.

First, the chain starts with no money and no stake, and proof of stake needs bonded value to pick validators. The answer is a bootstrap committee fixed in genesis, each seat with an equal share of power and no self-bond. Power then shifts to stake-weighted validators under a handover factor that only rises.

Second, stake-weighted sets concentrate. The code caps one validator's share, rate-limits how fast power moves and ramps new stake in over 30 epochs. Each rule exists because a review found a way to take the set over without it.

Third, payments between users are not public. The bank module is kept, norama moves publicly between users, and every protocol payout lands in an earnings ledger. The only way to pay a person is the shielded pool.

Fourth, a validator must not censor for free. Vote extensions carry each validator's list of long-waiting transactions, and a proposal that omits a valid listed transaction is rejected.

Fifth, there is no admin. The authority address of every stock module is the hash of a name no module has, so every authority-gated message is unreachable (`chain/app/app.go:UnreachableAuthority`).

## The application

`OramaApp` is a `baseapp.BaseApp` built by hand, without depinject, in one process with CometBFT. Storage is pebbledb, because the goleveldb backend of the pinned store library could not answer a versioned query at any height. A node refuses to start with a query gas limit of 0 on any chain that is not a localnet, since the public query route would otherwise let one query scan all state. The block gas meter is switched on (v0.54 disables it by default), because without it the base fee would read zero usage and never rise.

Besides the stock modules, the Orama modules are `emission`, `fees` and `token` (supply and fees), `power` (voting power), `nodes` (the registry in [Global nodes](ch14-global-nodes.md)), `storage`, `archive` and `relay` (services), `houses` (governance), `cnft` and `market`, `shielded` (the shielded pool) and `wasmpolicy` beside wasmd's `wasm` when libwasmvm is linked. Not wired: `x/gov`, `x/mint`, `x/crisis`, `x/authz`, IBC and an EVM. Every module account is a blocked address, so no user can send to one.

The same source builds in several variants. The default is pure Go with no CosmWasm and no Orchard verifier; a full static build links one archive that carries libwasmvm and the Orchard verifier together, because two Rust static libraries in one binary each carry their own standard library and collide. A binary without the library refuses a genesis that claims wasm, and refuses to start a node of a public network (stagenet, testnet, mainnet), which could not execute shielded transactions.

### One block

![One block: proposal, pre-blockers, begin-blockers, transactions, end-blockers, commit](../technical-reference/diagrams/ch39-block-lifecycle.svg)

Module orders are set in one place and each has a reason beside it. `emission` runs first among begin-blockers, so closing an epoch mints the validator share and hands it to `x/power` before anything reads balances. `staking` runs before `power` in the end-blockers, so power sees the bonded set after this block's messages. `fees` advances the base fee once the block's gas is final, and `emission` runs last so that its burn reconciliation sees every burn of the block.

CometBFT accepts validator updates from exactly one module per block. A wrapper lets `x/staking` still mature its unbonding queues but return no updates, so `x/power`'s end-blocker is the only source of them. `Commit` is overridden to lower CometBFT's retain height to the one `x/archive` allows, so a block no archived range covers is never pruned.

### The ante chain

An ordinary transaction passes 19 decorators in stock order, with additions. A shielded message must be alone in its transaction, checked before any fee is taken. Contract code upload is closed until a sunset height. `x/fees` replaces the stock deduct-fee decorator. `x/power` adds a guard against withdrawing force-bonded committee stake and a minimum delegation of 1 ORAMA, so the reward walk cannot be filled with dust. Signatures are verified before shielded proofs are checked, so unsigned garbage costs no proof work. A shielded transfer, which has no signer and no declared fee, takes a shorter chain that checks the bundle and the fee inside its value balance.

One restriction sits on the bank send path: it applies each factory token's pause, freeze, fee and hook. Norama itself moves publicly between users and contracts, and the chain blocks every module account as a receiver of a bank message. The private way to pay is the shielded pool.

## Who holds voting power


`x/power` computes the voting power CometBFT sees, every block, in deterministic fixed-point arithmetic with every walk in sorted order. The candidates are the bootstrap committee seats that are bonded, unjailed and untombstoned, plus every bonded staking validator.

A validator's power is a blend:

```
P_i = (1 - lambda) * B_i + lambda * C_i
```

`B_i` is the committee's equal share, 1/n. `C_i` is the stake-based share, capped and ramped. Lambda starts at 0, so only the committee has power, and once per epoch it becomes the largest of its previous value, bonded stake over a 271,000 ORAMA exit threshold, and epochs elapsed over a 365-epoch deadline, capped at 1. It reaches 1 at the deadline whatever the stake, and it cannot be set by any message.

Three rules slow it. A handover gate holds lambda at 0.95 until the count of active operators has once reached 40 (68 at the reduced cap), so a handful of validators cannot take all power the day the deadline passes. A step limit scales lambda back until the total variation of normalised power is at most one third, checked on both the published power and the latent full-stake power, so a bond added just before a step cannot be held back by the ramp and released together. And the cap itself moves with hysteresis: down to 3% when more than 60 operators are active, back up only after 30 epochs below 50.

The cap binds per operator, not per validator: an operator is the account that registered the node binding a validator's consensus key, the chain sums the stake of its validators, caps the sum, and splits the capped share back over them in proportion to stake, so running twenty validators earns one share. Validators no node binds share one operator and one cap. The chain cannot tell two operator accounts apart, so a party that registers several is not stopped. The capped share is a water-filling allocation over operators. An operator above its ceiling is pinned, and the excess is shared among the rest in proportion to raw stake, but no validator receives more than twice its own raw proportion, since otherwise ten honest validators at the cap and ten attackers with one unit each would hand the attackers half the power. New stake counts in proportion `elapsed / 30` epochs; stake that leaves reduces the admitted amount first and at once. Published power may increase by at most one third of the previous total between consecutive blocks, which is the light-client bound; decreases always apply in full, because keeping a jailed validator in the set would leave it voting.

Committee members have nothing at stake, so half of each member's reward is force-bonded into its own validator, up to 2,000 ORAMA, and cannot be withdrawn until lambda reaches 1.

Slashing needed a fix. Stock `x/staking` takes `TokensFromConsensusPower(power)` from the power CometBFT reports, which assumes power is proportional to bonded tokens at 10^6 norama per unit. Here power is capped, ramped, blended and scaled by 10^9, and the review that found this measured 83% to 100% of a stake burned for one infraction instead of 5%. `slashBaseFix` substitutes the power equivalent to the validator's current bonded tokens plus unmatured unbonding stake. Genesis sets the double-sign slash at 5% with tombstoning, and downtime at 0.01% over a 10,000-block window.

## Inclusion lists


A proposer could otherwise keep a transaction out of every block it proposes. ABCI++ vote extensions remove that option (`chain/x/inclusion/`).

Each node keeps a bounded memory of transactions that passed `CheckTx` and when it first saw them. A transaction becomes eligible after 10 seconds. In `ExtendVote`, a validator picks eligible transactions that pass the full ante chain on a scratch branch, sorts them by bytes, and returns up to 32 KiB. `VerifyVoteExtension` checks only stateless shape.

The proposer injects the previous height's extended commit as the first transaction of the block, after validating it. It accepts extensions only if the valid ones hold at least 2/3 of power, then walks the union in byte order, and every transaction that decodes, pays the fee, has the next sequence for its sender and fits the budgets becomes a required prefix. `ProcessProposal` re-runs the same walk, and every validator rejects a block in which a valid, fitting listed transaction is missing. Invalid ones, or ones that did not fit, may be absent.

The walk is budgeted so junk cannot cost every validator unbounded work: 1,024 ante runs and 4,096 signature checks per block, divided among the extensions that list anything, and signatures verified before the sender is charged. A validator that lists junk spends its own share. The switch is a genesis-only consensus parameter.

## Genesis and change

`InitChainer` runs a locked-genesis check: each locked module's parameters must equal the compiled defaults, key by key. A production chain id may differ in nothing. A test network may differ only in epoch timing, faucet parameters and committee minimum. A test fails when a module gains a parameter without a row in the check, so a parameter cannot silently become tunable.

`orama setup --create-network` builds that genesis. Each machine makes its keys first, with a placeholder genesis. The first machine then runs `oramad`'s genesis commands with every machine as a committee seat, and the chains start one at a time. The result is written to `networks/<name>/`, and a published chain id keeps its genesis for good. `core/cmd/orama/internal/setup/create_genesis.go:GenesisSteps`, `core/pkg/netclass/netclass.go:CheckCommittee`.

`x/houses` is the governance module but not the authority. It reaches the rest of the chain through narrow adapters: the emission split it enacted, the relay reporter set, an upgrade plan stored in `x/upgrade`, and the code-upload allow list. No module sets a migration and no upgrade handler is registered. A scheduled plan halts a node at its height, but the restarted binary has nothing to apply, and releases that change stored types restart from a new genesis. `oramad export` exists for a coordinated hard fork.

CosmWasm is wired with a narrow set of capabilities: no `stargate`, no IBC, and every channel keeper a refusal. A wrapper meters every call that changes stored bytes and charges the growth as a state deposit, so contract storage is priced like any other state.

## How core reads and writes the chain

`core/` links no chain code. `core/pkg/chainread` is the reader behind `orama chain`. Orama module queries use embedded protobuf descriptors, so the CLI encodes JSON and decodes answers without generated types. `core/pkg/clusterreg` hand-writes the messages the CLI sends and builds `SIGN_MODE_DIRECT` sign documents, so an agent such as RootWallet can sign without the CLI holding a key.

The gateway serves a public `/v1/chain/` proxy, with no credential, since a wallet has none and the chain charges the sender. It forwards nothing it did not build: each route is an allowlist entry that constructs one upstream URL. Module queries are an explicit allowlist, with a test that fails for any embedded method that is neither served nor deliberately withheld, so a new query cannot become public by being embedded. A request is at most 4 KiB, a height must be within the last 100 blocks, and 16 queries, 8 simulations and 16 broadcasts run at once. The refusal text of a rejected transaction has stack traces, paths and addresses removed.

`chain/client` is the Go library the global services link. It signs, simulates, scales gas by 150%, pays exactly the current base fee with no tip, and polls for inclusion for up to a minute. The Go and TypeScript transaction builders are held to the same bytes by shared fixtures.

## Trust and limits

Validators trust the compiled rules. There is no admin key, no pause and no parameter-change message, so the cost of a mistake in a stock module is a new genesis. Two consequences follow. The invariants of every module are queries, not block-time assertions, since no crisis module is wired. And a coordinated upgrade is a restart, not a migration.

The single most important limit is that last one: with no upgrade handler or migration, a state-breaking release cannot be applied to a running chain, so every such release restarts from a new genesis. Confidential nodes, the TEE-attestation boundary in `chain/x/confidential/`, accept no attestation and are imported by nothing.
