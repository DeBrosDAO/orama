package keeper

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// ProofCheck is a bundle a transaction carries and the binding its signatures must commit to.
type ProofCheck struct{ Bundle, Binding []byte }

// MinProposalRequest is the least time left in a proposal's budget for which a bundle is still put
// to a verifier. Below it the verifier is not asked: the node votes nil on its own and nothing is
// started that a healthy verifier would have to be killed for.
const MinProposalRequest = 250 * time.Millisecond

// ErrProposalShape means a proposal's bundles break a rule that depends only on the proposal and
// on state every validator shares (a bundle too big, the proposal over the block's gas, a bundle or
// a nullifier twice). Every honest validator reaches the same verdict.
var ErrProposalShape = errors.New("shielded bundles of the proposal break a limit")

// ErrProposalBudget means checking the proposal's proofs took longer than this node's budget. It is
// a fact about this node's speed, not about the proposal: the node votes nil on its own.
var ErrProposalBudget = errors.New("checking the proposal's shielded proofs took longer than this node allows")

// ProposalChecker checks the shielded bundles of one block proposal (ProcessProposal) or of the
// transactions a proposer is choosing (PrepareProposal). It first bounds the work from the proposal
// alone, then verifies proofs under one wall-clock budget. It changes no state and never halts.
type ProposalChecker struct {
	k          Keeper
	p          types.Params
	maxGas     uint64
	gas        uint64
	hashes     map[[sha256.Size]byte]struct{}
	nullifiers map[[bundle.NodeLen]byte]struct{}
	deadline   time.Time
	now        func() time.Time
}

// NewProposalChecker reads the limits from the state the proposal is judged against, which every
// validator shares: the shielded params and the consensus block gas limit (zero or negative means
// none). budget is the time the whole proposal's proof checks may take.
func (k Keeper) NewProposalChecker(ctx sdk.Context, budget time.Duration) (*ProposalChecker, error) {
	p, err := k.params(ctx)
	if err != nil {
		return nil, err
	}
	var maxGas uint64
	if cp := ctx.ConsensusParams(); cp.Block != nil && cp.Block.MaxGas > 0 {
		maxGas = uint64(cp.Block.MaxGas)
	}
	c := &ProposalChecker{
		k: k, p: p, maxGas: maxGas, now: time.Now,
		hashes:     map[[sha256.Size]byte]struct{}{},
		nullifiers: map[[bundle.NodeLen]byte]struct{}{},
	}
	c.deadline = c.now().Add(budget)
	return c, nil
}

// AbortsChecks reports whether err says this node cannot go on checking the proposal (the budget is
// spent, the verifier is starting or busy, a fault, a verifier that is not linked), as opposed to a
// verdict on one transaction. A proposer stops checking, and drops what is left, on such an error.
func AbortsChecks(err error) bool {
	return errors.Is(err, ErrProposalBudget) || localFault(err) || errors.Is(err, verify.ErrVerifierStarting)
}

// Admit checks the bundles of one transaction against everything admitted before it. Nothing is
// recorded unless every bundle passes, so a transaction a proposer drops leaves no trace.
func (c *ProposalChecker) Admit(checks []ProofCheck) error {
	hashes := make(map[[sha256.Size]byte]struct{}, len(checks))
	nullifiers := map[[bundle.NodeLen]byte]struct{}{}
	gas := c.gas
	for _, ch := range checks {
		b, err := bundle.Parse(ch.Bundle)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrProposalShape, err)
		}
		if b.Actions > int(c.p.MaxActionsPerBundle) {
			return fmt.Errorf("%w: a bundle of %d actions, the limit is %d", ErrProposalShape, b.Actions, c.p.MaxActionsPerBundle)
		}
		if gas += c.p.TxGas(b.Actions); c.maxGas > 0 && gas > c.maxGas {
			return fmt.Errorf("%w: the bundles need %d gas, the block holds %d", ErrProposalShape, gas, c.maxGas)
		}
		h := admissionKey(ch.Bundle, ch.Binding)
		if _, dup := c.hashes[h]; dup {
			return fmt.Errorf("%w: a bundle appears twice", ErrProposalShape)
		}
		if _, dup := hashes[h]; dup {
			return fmt.Errorf("%w: a bundle appears twice", ErrProposalShape)
		}
		hashes[h] = struct{}{}
		for _, nf := range b.Nullifiers {
			if _, dup := c.nullifiers[nf]; dup {
				return fmt.Errorf("%w: a nullifier appears twice", ErrProposalShape)
			}
			if _, dup := nullifiers[nf]; dup {
				return fmt.Errorf("%w: a nullifier appears twice", ErrProposalShape)
			}
			nullifiers[nf] = struct{}{}
		}
	}
	for _, ch := range checks {
		left := c.deadline.Sub(c.now())
		if left < MinProposalRequest {
			return ErrProposalBudget
		}
		if err := c.k.CheckProposalProof(ch.Bundle, ch.Binding, left); err != nil {
			return err
		}
	}
	c.gas = gas
	for h := range hashes {
		c.hashes[h] = struct{}{}
	}
	for nf := range nullifiers {
		c.nullifiers[nf] = struct{}{}
	}
	return nil
}
