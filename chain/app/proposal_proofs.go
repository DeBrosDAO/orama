package app

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shieldedante "github.com/DeBrosOfficial/network/chain/x/shielded/ante"
	shieldedkeeper "github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	orchardverify "github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

// The proof check of a block proposal. A shielded bundle costs every validator a Halo 2 verification,
// and a bundle that kills or hangs the verifier would stop every validator that executes it
// (shieldedkeeper.VerifierHalt). So a proposal is checked before it is voted for, in two steps.
//
// First the work is bounded from the proposal alone, with limits every validator reads from the same
// state: each bundle must parse and stay within max_actions_per_bundle, the actions of the whole
// proposal must fit the block's gas, and no bundle hash and no nullifier may appear twice. A breach
// is a deterministic refusal. Then every bundle goes through the mempool's verifiers, the child that
// can be killed first, under one wall-clock budget for the whole proposal (proposalProofBudget); a
// bundle this node's mempool already verified is not verified again. A rejection, a fault, a verifier
// that is still starting or an overrun of the budget gets a nil vote. Nothing here ever halts the
// node: a halt is for FinalizeBlock, where the block is decided. An overrun is a fact about this
// node's speed, not about the proposal.
//
// PrepareProposal leaves out the transactions that fail, so an honest proposer whose verifier is
// ready does not build a block its peers refuse for this reason. FinalizeBlock still verifies every
// bundle with the block's own verifiers and still halts on a fault.
//
// The check applies only to the proposer's own part of the block. The transactions an inclusion list
// forces to the front of the block (inclusion.Required) are never gated here, in PrepareProposal or in
// ProcessProposal: the inclusion rules require them whatever their proof, and a proposal check that
// refused what those rules require would let one validator that lists a bad transaction stall every
// round. A forced transaction with a bad proof, or a nullifier another one spends, goes to execution,
// where FinalizeBlock verifies it, fails it with an ordinary result and halts nobody.

// proposalProofBudget is the time one proposal's proof checks may take on this node. CometBFT's
// timeout_propose is 3 s by default and a validator that has not decided by then prevotes nil; the
// budget leaves a margin under it for the rest of ProcessProposal. Measured on a development laptop
// (Apple silicon, unloaded; app/shielded_proposal_bench_test.go): 25 bundles of 16 actions cost about
// 1.55 s to check on a node that has verified none of them, about 62 ms each, and a few ms on a node
// whose mempool has. The shielded chapter's known gaps carry what that means for a full block.
const proposalProofBudget = 2500 * time.Millisecond

// canCheckProofs reports whether this node has verifiers to check a proposal with: the library is
// linked and the verifier binary is configured. A localnet or a scripted devnet runs without them,
// on every node alike, and refuses every bundle at execution; there the proposal check would refuse
// every shielded proposal instead of leaving that to execution, so it does not run. Every other
// chain refuses to start without them (CheckNodeCanVerify).
func (app *OramaApp) canCheckProofs() bool {
	if !orchardverify.Linked || len(app.ShieldedProposalVerifiers) != 2 {
		return false
	}
	m, ok := app.ShieldedProposalVerifiers[0].(*mempoolVerifier)
	return ok && m.configured
}

// proposalChecker returns the checker for one proposal, or nil when this node has nothing to check
// with or cannot read the limits (it then votes as it did before: execution decides).
func (h *inclusionHandlers) proposalChecker(ctx sdk.Context) *shieldedkeeper.ProposalChecker {
	if h.newProofChecker == nil {
		return nil
	}
	c, err := h.newProofChecker(ctx)
	if err != nil {
		h.logger.Error("failed to read the shielded limits for the proposal check", "err", err)
		return nil
	}
	return c
}

// checks lists the bundles of a transaction's bytes. Bytes that do not decode carry none: the SDK
// handlers decide what to do with them. ok is false for a transaction whose bundles cannot be
// listed at all (an unshield whose binding cannot be computed): it cannot pass.
func (h *inclusionHandlers) checks(raw []byte) (checks []shieldedkeeper.ProofCheck, ok bool) {
	tx, err := h.rules.decode(raw)
	if err != nil {
		return nil, true
	}
	checks, err = shieldedante.ProofChecks(tx)
	return checks, err == nil
}

// allProofsOK reports whether every shielded bundle of a proposal passes the proof check.
func (h *inclusionHandlers) allProofsOK(ctx sdk.Context, txs [][]byte) bool {
	c := h.proposalChecker(ctx)
	if c == nil {
		return true
	}
	for _, raw := range txs {
		checks, ok := h.checks(raw)
		if !ok {
			return false
		}
		if len(checks) == 0 {
			continue
		}
		if err := c.Admit(checks); err != nil {
			h.logger.Debug("a shielded bundle in a proposal did not pass the proof check", "module", "x/shielded", "err", err)
			return false
		}
	}
	return true
}

// dropFailingProofs returns txs, in order, without the transactions that fail the proof check. It
// is given only the proposer's own transactions: the transactions an inclusion list forces into the
// block are never passed here (see the note at the top), they go to execution as they are. When the
// node can no longer check (the budget is spent, the verifier is starting, busy or faulting) it stops
// checking and leaves out every shielded transaction that is left: a block with fewer transactions
// is better than a block its own proposer cannot vouch for.
func (h *inclusionHandlers) dropFailingProofs(ctx sdk.Context, txs [][]byte) [][]byte {
	c := h.proposalChecker(ctx)
	if c == nil {
		return txs
	}
	aborted := false
	out := make([][]byte, 0, len(txs))
	for _, raw := range txs {
		checks, ok := h.checks(raw)
		if !ok {
			continue
		}
		if len(checks) > 0 {
			if aborted {
				continue
			}
			if err := c.Admit(checks); err != nil {
				h.logger.Debug("a shielded bundle was left out of the proposal", "module", "x/shielded", "err", err)
				aborted = shieldedkeeper.AbortsChecks(err)
				continue
			}
		}
		out = append(out, raw)
	}
	return out
}

// newProofChecker builds the checker for one proposal when this node can check proofs.
func (app *OramaApp) newProofChecker(ctx sdk.Context) (*shieldedkeeper.ProposalChecker, error) {
	if !app.canCheckProofs() {
		return nil, nil
	}
	return app.ShieldedKeeper.NewProposalChecker(ctx, proposalProofBudget)
}
