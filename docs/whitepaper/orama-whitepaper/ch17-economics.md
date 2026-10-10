# Economics

> **At a glance.**
>
> - **What:** how norama is created, moved and destroyed. `x/emission` is the only minter: it pays the validator share of a halving-with-tail schedule and records the other shares as ceilings that modules mint against only when they prove work. `x/fees` holds the base-fee market, the earnings ledger that receives every protocol payout, and state deposits. `x/token` creates factory denoms. Every parameter is fixed at genesis.
> - **Key numbers:** 1 ORAMA is 10^9 norama; an epoch closes after 24 h and 14,400 blocks, both; maximum emission per epoch 14,848 ORAMA, halving every 730 epochs, then 274 forever; split 60/25/10/5; base fee floor 1 norama per gas, moves at most 12.5% per block, burned in full; deposits refund 99% and burn 1%; service payments split 90/5/5.
> - **Code:** `chain/x/emission/`, `chain/x/fees/`, `chain/x/token/`.

![The flows of norama: minted by x/emission, paid into earnings, burned by fees, deposits and service payments](../technical-reference/diagrams/ch40-overview.svg)

## What the design has to guarantee

The currency's supply must be nobody's to change, and the price of use must follow load, not a committee. Four constraints follow.

Supply is a function of the epoch number alone. The schedule counts completed epochs, never calendar time, so a halt cannot stretch it. `x/emission` has no authority address, no parameter message, and one message, a faucet it refuses on a production chain.

Validators must not decide what the storage, relay and development shares are worth. Those shares are ceilings, minted only when a module proves work against them. What nobody claims is never minted.

A payout must not be aimed at a chosen address. Every protocol payment lands in the recipient's earnings ledger inside `x/fees`, never in a public balance chosen by the payer. Users can pay each other in the open with an ordinary bank send, or privately through the pool (see [The chain](ch16-the-chain.md)).

Fees follow the EIP-1559 shape and the base fee is burned entirely, so ordering a block enriches nobody.

## The schedule

The maximum norama that may exist for a closed epoch comes from a compiled table of five halving brackets of 730 epochs and a tail.

| Epochs | Maximum per epoch (ORAMA) |
|---|---|
| 1 to 730 | 14,848 |
| 731 to 1,460 | 7,424 |
| 1,461 to 2,190 | 3,712 |
| 2,191 to 2,920 | 1,856 |
| 2,921 to 3,650 | 928 |
| 3,651 onward | 274 |

The brackets sum to 21,000,640 ORAMA at epoch 3,650, ten years at one epoch a day, and the tail then adds at most 100,010 ORAMA a year. These are maxima. Every figure is a whole number of ORAMA, so a schedule amount splits into four shares with no remainder, and for any split the shares sum to the maximum to the norama, the validator share taking the remainder.

## Closing an epoch

![Epoch close: the two conditions, the mint, the payout and the ceiling record](../technical-reference/diagrams/ch40-epoch-close.svg)

`x/emission` runs first in every block and closes an epoch when two conditions hold together: 24 hours of BFT time have passed, and 14,400 blocks have been counted. At 5-second blocks the clock binds, at about 17,280 blocks. The block floor brakes timestamp games: a supermajority that skewed time still has to produce 14,400 real blocks. One call closes exactly one epoch, and after a long halt missed epochs are never caught up.

The close reads the split in force, mints the validator share, hands it to `x/power`, writes a ceiling record for the three unminted shares, drops the record that fell out of a 30-epoch window, and advances the epoch. The default split is 60% validators, 25% storage, 10% relay and 5% development. `x/houses` can move each share by at most 10 points from its canonical value, with the four summing to 100, and the split is re-validated at every close. The halving table and the tail are beyond any proposal.

### Paying validators

`x/power` pays the validator share itself, not through `x/distribution`. It recomputes each validator's normalised power and pays validator `i` `floor(share_i * total)`. Commission goes to the validator, the rest to its delegations pro rata, each as an earnings entry. Each validator is paid in its own cache branch: a failure about one rolls back that branch and returns its share to the `emission` account, and a rejected share is never paid to anyone.

### Service mints

Storage, relay and development shares are claimed by the modules that can prove work. Each claim mints against one epoch's ceiling and is refused above what remains. Only these claims, the validator mint and the faucet create norama.

### Accounting for burns

`x/emission` has no burn path; fees, deposits, tokens, service splits, shielded fees and slashing burn on their own. A reconciliation in the last end-blocker compares the bank supply with genesis supply plus mints minus recorded burns, and attributes any shortfall to burning. It never raises the counter on a surplus, so a mint that bypasses the counters shows up as a broken invariant.

## The base fee

`x/fees` keeps one number, norama per gas. It starts at 1 and moves once per block against the block gas meter. The target is 50% of `max_gas`, and the change is `(used - target) / target`, clamped to plus or minus 12.5%: an empty block lowers the fee by 12.5% until the floor of 1, a full block raises it by 12.5%. Integer truncation would pin a fee of 1 forever, so a positive change that truncates to the same integer is raised by one.

### Settling a fee

![The fee decorator: base fee, tip, payer, proposer and the earnings fallback](../technical-reference/diagrams/ch40-fee-settlement.svg)

The fee must be at least the base fee times the gas limit; the excess is a tip. The payer is the fee granter if there is one, otherwise the first signer. The base fee comes from the payer's bank balance, with any shortfall drawn from its fee-only balance and then its earnings, so a validator holding only rewards can still transact. It is burned. The tip is credited to the proposer's operator earnings; if the proposer cannot be resolved, the whole fee is burned. An exact invariant holds: `burned + distributed == collected`.

The tip must come from the bank balance alone. A tip is a payment to the proposer; earnings pay fees and bonds.

## Earnings

Every module that pays a protocol reward credits earnings from its own module account: `x/power`, `x/storage`, `x/relay`, `x/houses`, `x/market`. Earnings are a ledger backed by coins in the `fees` account. They can pay the base fee, bond a validator or node role, create a token, open a storage deal, fund a deposit, enter the shielded pool, or fund a node's hot key. They cannot be sent to another user directly: the owner withdraws them to the owner's own bank balance with `MsgWithdrawEarnings` and sends from there, in the open or through the shielded pool.

Where earnings convert to spendable balance matters. The top-up, by exactly the shortfall, happens inside message handlers, never in the ante chain. Ante writes survive a message that then fails, so a top-up there would turn earnings into spendable balance for free; a handler's cache branch is discarded on failure, taking the top-up with it.

A fee-only balance has a narrower use still: it pays a base fee and nothing else. It is how an operator funds a node's hot key.

## Deposits and service payments

Records that occupy state are priced by size. A deposit is locked under a unique id in a separate module account, paid from bank balance first and then earnings, and released with 99% refunded to the owner's earnings and 1% burned. The price is 68,359 norama per byte, about 0.07 ORAMA per KiB; the chain has no price oracle, so the number is a constant. Tokens, node and cluster records, storage deals and contract storage all pay it.

A payment to a storage provider or relay operator is split 90/5/5: 90% to the operator's earnings, 5% burned, 5% to the archive fund. A signer-less shielded transfer has no fee decorator; its fee is the difference between value entering and leaving the bundle, burned except for a tip to the proposer.

## Factory tokens

Anyone can create a denom `factory/creator/subdenom` in `x/bank` for 10 ORAMA plus the metadata deposit, which is refundable. Capabilities are chosen at creation and can only be dropped: mint, freeze, a permanent delegate, a transfer fee that burns tokens, non-transferable, pause, and a transfer hook. The hook is an existing CosmWasm contract, fixed at creation; every transfer calls it under a 100,000-gas cap, and an error refuses the transfer.

## Limits

The ledger invariants are not asserted after genesis, since no crisis module is wired; a break is visible only through queries. And commission is uncapped: a validator can take all of an epoch's reward from its delegators.
