package keeper

import (
	"context"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// SetCheckerClockForTest replaces a proposal checker's clock and deadline.
func SetCheckerClockForTest(c *ProposalChecker, now func() time.Time, deadline time.Time) {
	c.now, c.deadline = now, deadline
}

// WithProposalVerifiersForTest returns k with another set of proposal verifiers.
func WithProposalVerifiersForTest(k Keeper, set []verify.Verifier) Keeper {
	k.deps.ProposalVerifiers = set
	return k
}

// AdvanceFaultMemoryForTest moves the mempool's clock forward by d, to expire remembered faults.
func AdvanceFaultMemoryForTest(k Keeper, d time.Duration) {
	base := k.admission.now
	k.admission.now = func() time.Time { return base().Add(d) }
}

// EmitSubtreeForTest runs the part of an append that stores and reports a filled subtree.
func EmitSubtreeForTest(ctx context.Context, k Keeper, frontier []byte, index uint64) error {
	return k.emitSubtree(ctx, frontier, index)
}
