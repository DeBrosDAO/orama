# Chain security playbooks

This page covers what an operator of the Orama L1 (`oramad`) does when a chain-level
fault is found. It describes the code as it is today. No external audit has been done.
There is no bounty programme. Nothing here is an admin key: no module has a pause, a
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
| `x/market` | Market balance equals the open bids | `Keeper.CheckInvariants` only; there is no query yet |

Only `x/emission` can mint norama. `x/storage` and `x/relay` payments are minted by
`x/emission` and moved to them. `x/token` holds the Minter permission for the denoms it
creates, and its bank keeper refuses a norama mint (`chain/app/mint_policy.go`,
`TestGetMaccPerms_onlyEmissionMintsNorama`).

`x/emission`'s two checks also run on every `InitGenesis`. A chain refuses to start from a
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
   <name> ...` (cosmovisor layout, `docs/CHAIN.md`), or replaces the binary when it runs
   without cosmovisor. Validators stay in notify mode; nothing installs by itself.
7. **Restart after `H`.** Blocks resume once validators holding more than two thirds of
   voting power run the fix. Before the λ hand-over that means the bootstrap committee.
8. **Check** every invariant query above on every validator.

This procedure has not been rehearsed on a live chain yet.

## Not written yet

These G3 playbooks do not exist yet: bootstrap committee failure, dirauth compromise,
release-key compromise (TUF root rotation), mass provider failure, settlement or archive
backlog, and unshield run. The last one is moot until a shielded verifier is linked:
`x/shielded` refuses every bundle today.
