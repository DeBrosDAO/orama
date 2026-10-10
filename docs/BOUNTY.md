# Security disclosure and bounty policy

**Status: scope, severity and disclosure rules only. Payouts are the owner's to set.**

The plan calls for standing bounties on the chain ("sized to pool value and staked value", from
the incentivised testnet, `plans/open-network/track-g-token-legal-security.md` G3, and D20 in
`plans/open-network.md`) and says pre-mainnet costs, bounties included, are paid by the owner
(O-D). It does not define a programme: no amounts, no funding source, no schedule, no payer of
record. So this page does not promise a payout, sets no amount, and names no budget. It says what is
in scope, how findings are rated, and how to report one. The owner decides whether a given finding
is paid and how much, and publishes any amounts separately.

No audit of `chain/` has been done (`docs/SECURITY_PLAYBOOKS.md`). Reports are welcome before one.

## How to report

Do not open a public issue. `CONTRIBUTING.md` names `security@debros.io` for vulnerability reports;
use it. Include the commit or release, the module, what you did, what happened, and a test or
transcript that reproduces it. Test only on a chain you run yourself (`chain/scripts/localnet`) or
on a devnet or stagenet you have permission for, never against someone else's node or user data.

This page makes no legal commitment. There is no legal workstream in the plan.

## What is in scope

- `chain/`: every module (`x/emission`, `x/fees`, `x/power`, `x/houses`, `x/nodes`, `x/storage`,
  `x/relay`, `x/archive`, `x/token`, `x/cnft`, `x/market`, `x/wasmpolicy`, `x/inclusion`,
  `x/shielded`, `x/confidential`), the app wiring, ante handlers and bank send restrictions in
  `chain/app`, and `oramad`.
- The genesis parameter lock (`app.ValidateLockedGenesis`) and `oramad genesis validate`.
- `piece/`, and the global services `orama-global` (provider, repair delegate, archiver, indexer).
- Release verification: `core/pkg/releaseverify`, `core/pkg/archivetrust`, and
  `orama global stage-oramad`.
- The confidential-node boundary: any path that treats a quote, report or blob as a valid
  attestation is in scope and is rated Critical (below).

## What is out of scope

- The declared trust points in `plans/open-network.md` ("Security model and declared trust
  points"). That the bootstrap committee decides before lambda reaches 1, that release signers
  choose what validators can install, that directory authorities and relay reporters are trusted
  until a majority is independent, and similar, are known and documented. A way to exceed them
  is in scope.
- Findings that need a majority of voting power, or an operator's own root access to their own
  machine.
- Volume attacks (flooding, resource exhaustion by sheer traffic) with no protocol flaw behind them.
- Upstream bugs in Cosmos SDK, CometBFT, wasmd or wasmvm, Kubo, Tor, go-tuf or the Zcash crates
  that this repository does not change. Report those upstream. A way this repository's use of them
  is exploitable is in scope.
- Placeholder economics recorded as "G1 launch default" in `docs/CHAIN.md` ("Genesis parameters
  (G1)"), unless the number lets a fake identity earn more than it costs or breaks an invariant.
- The stagenet test release root, test keys, and any `-localnet-`, `-devnet-` or `-stagenet-`
  genesis.
- Social engineering, and physical access.

## Severity

The chain has no admin key, pause, freeze or upgrade authority to fall back on, and a state-machine
fix is a coordinated halt-height fix (`docs/SECURITY_PLAYBOOKS.md`). Severity follows what an
attacker gains and how hard it is to undo.

| Severity | Examples |
|---|---|
| Critical | Minting norama outside the emission schedule. Creating or moving funds from any module account without the module's rule. Any key, message or path that pauses, freezes, blacklists, halts or upgrades the chain, or changes an ossified rule. A shielded bundle accepted without two independent verifiers, an unshield that breaks the turnstile or the 2% cap, or double-spend of a nullifier. Treating any quote or blob as a valid TEE attestation, or any way to make a marketplace lease go through. Splitting consensus or halting the chain without more than a third of voting power. Forging a release the node's verifier accepts. |
| High | Breaking an invariant that `oramad query <module> invariants` checks (`docs/SECURITY_PLAYBOOKS.md`). Governance passing without its opening rule, its votes or its timelock, or a house-bond escape. Paying a module account with a bank message, which would unbalance the earnings or deposit ledgers. Bypassing the upload allow-list or sunset. Drawing protocol payments for work not done, or storage or relay payments beyond the ceiling. A validator-set or power computation that can be biased by a non-validator. |
| Medium | A path that lets an attacker cheaply grief one operator or deal (a slash, jail or eviction they did not earn). A leak that narrows privacy without breaking a proof. An error that halts one node's block processing but not the chain. A parameter accepted outside its coded bound. |
| Low | Hardening gaps, misleading errors or docs that could lead an operator to a wrong action, and issues with no realistic exploit. |

## Handling

1. The reporter sends the finding privately. It is acknowledged, then reproduced.
2. A finding that changes the state machine is fixed through the coordinated halt-height procedure
   in `docs/SECURITY_PLAYBOOKS.md`. Nothing is patched on one node, and validators choose whether to
   run the fix.
3. The finding is made public after validators holding two thirds of voting power have had the fix
   staged, or immediately when no state-machine change is needed. The reporter is credited unless
   they ask not to be.
4. The owner decides, per finding, whether a payout is made and how much. A reporter should not
   expect one until the owner has published a programme with amounts.
