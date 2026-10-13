package keeper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

func withBlockGas(ctx sdk.Context, maxGas int64) sdk.Context {
	return ctx.WithConsensusParams(cmtproto.ConsensusParams{Block: &cmtproto.BlockParams{MaxGas: maxGas}})
}

func check(raw []byte) []keeper.ProofCheck { return []keeper.ProofCheck{{Bundle: raw}} }

func newChecker(t *testing.T, e *testutil.Env, ctx sdk.Context, budget time.Duration) *keeper.ProposalChecker {
	t.Helper()
	c, err := e.Keeper.NewProposalChecker(ctx, budget)
	require.NoError(t, err)
	return c
}

// A proposal's shape is judged from the proposal and shared state alone, before any proof is
// verified: an unparsable bundle, one with too many actions, a proposal over the block's gas, a
// bundle twice and a nullifier twice are all refused without the verifier being asked.
func TestProposalChecker_theShapeIsRefusedBeforeAnythingIsVerified(t *testing.T) {
	e := testutil.NewEnv(t, nil) // action gas 10, max actions per bundle from the default params
	anchor := emptyAnchor(t)
	good := testutil.Bundle{Seed: 1, ValueBalance: 20, Anchor: anchor}.Encode(1)

	t.Run("an unparsable bundle", func(t *testing.T) {
		c := newChecker(t, e, withBlockGas(e.Ctx, 1_000_000), time.Minute)
		require.ErrorIs(t, c.Admit(check(good[:len(good)-1])), keeper.ErrProposalShape)
		require.Zero(t, e.V2.Calls, "nothing was verified for a bundle the shape refused")
	})
	t.Run("too many actions", func(t *testing.T) {
		c := newChecker(t, e, withBlockGas(e.Ctx, 1_000_000), time.Minute)
		tooBig := testutil.Bundle{Seed: 2, ValueBalance: 20, Anchor: anchor}.Encode(200)
		require.ErrorIs(t, c.Admit(check(tooBig)), keeper.ErrProposalShape)
		require.Zero(t, e.V2.Calls, "nothing was verified for a bundle the shape refused")
	})
	t.Run("over the block's gas", func(t *testing.T) {
		c := newChecker(t, e, withBlockGas(e.Ctx, 25), time.Minute)                                              // 10 per action
		require.NoError(t, c.Admit(check(testutil.Bundle{Seed: 3, ValueBalance: 20, Anchor: anchor}.Encode(2)))) // 20
		require.ErrorIs(t, c.Admit(check(testutil.Bundle{Seed: 4, ValueBalance: 20, Anchor: anchor}.Encode(1))), keeper.ErrProposalShape, "30 > 25")
	})
	t.Run("no block gas limit means none", func(t *testing.T) {
		c := newChecker(t, e, withBlockGas(e.Ctx, -1), time.Minute)
		require.NoError(t, c.Admit(check(testutil.Bundle{Seed: 5, ValueBalance: 20, Anchor: anchor}.Encode(2))))
	})
	t.Run("a bundle twice", func(t *testing.T) {
		c := newChecker(t, e, withBlockGas(e.Ctx, 1_000_000), time.Minute)
		before := e.V2.Calls
		require.NoError(t, c.Admit(check(good)))
		require.ErrorIs(t, c.Admit(check(good)), keeper.ErrProposalShape)
		require.Equal(t, before+1, e.V2.Calls, "only the first was verified")
	})
	t.Run("a nullifier twice", func(t *testing.T) {
		c := newChecker(t, e, withBlockGas(e.Ctx, 1_000_000), time.Minute)
		require.NoError(t, c.Admit(check(good)))
		sameNullifier := testutil.Bundle{Seed: 1, ValueBalance: 21, Anchor: anchor}.Encode(1) // another bundle, the same note spent
		require.ErrorIs(t, c.Admit(check(sameNullifier)), keeper.ErrProposalShape)
	})
}

// A transaction the checker refuses leaves no trace in it: a proposer who drops it can still use the
// room and the nullifiers it would have taken.
func TestProposalChecker_aRefusedTransactionLeavesNoTrace(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	anchor := emptyAnchor(t)
	c := newChecker(t, e, withBlockGas(e.Ctx, 1_000_000), time.Minute)
	bad := testutil.Bundle{Seed: 7, ValueBalance: 20, Anchor: anchor}.Encode(1)
	e.V2.Reject = func([]byte) error { return verify.ErrProofRejected }
	require.ErrorIs(t, c.Admit(check(bad)), verify.ErrProofRejected)
	e.V2.Reject = nil
	require.NoError(t, c.Admit(check(bad)), "the same bundle, now valid, is not a duplicate of the refused one")
}

// The proof checks of one proposal share a wall-clock budget; an overrun is a refusal of this node's
// alone and never a halt.
func TestProposalChecker_anOverrunBudgetIsALocalRefusalNotAHalt(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	anchor := emptyAnchor(t)
	c := newChecker(t, e, withBlockGas(e.Ctx, 1_000_000), time.Second)
	now := time.Unix(1_700_000_000, 0)
	keeper.SetCheckerClockForTest(c, func() time.Time { return now }, now.Add(time.Second))

	require.NoError(t, c.Admit(check(testutil.Bundle{Seed: 8, ValueBalance: 20, Anchor: anchor}.Encode(1))))
	now = now.Add(2 * time.Second) // the first check took longer than the budget
	err := c.Admit(check(testutil.Bundle{Seed: 9, ValueBalance: 20, Anchor: anchor}.Encode(1)))
	require.ErrorIs(t, err, keeper.ErrProposalBudget)
	require.NotErrorIs(t, err, keeper.ErrProposalShape, "an overrun says nothing about the proposal")
}

// A bundle this node's mempool already verified is not verified again by a proposal; a bundle it has
// not is. A block never reads this memory.
func TestProposalChecker_aBundleTheMempoolVerifiedIsNotVerifiedAgain(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	anchor := emptyAnchor(t)
	raw := testutil.Bundle{Seed: 10, ValueBalance: 20, Anchor: anchor}.Encode(1)

	ctx := e.CheckCtx()
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	require.NoError(t, e.Keeper.Verify(ctx, raw, nil, adm))
	calls := e.V2.Calls

	require.NoError(t, e.Keeper.CheckProposalProof(raw, nil, time.Second))
	require.Equal(t, calls, e.V2.Calls, "the verdict of CheckTx is reused")

	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	haltOf(t, func() { _ = e.Keeper.Verify(e.Ctx.WithExecMode(sdk.ExecModeFinalize), raw, nil, adm) })
	require.Greater(t, e.V2.Calls, calls, "a block verifies whatever the mempool remembers")

	other := testutil.Bundle{Seed: 11, ValueBalance: 20, Anchor: anchor}.Encode(1)
	require.ErrorIs(t, e.Keeper.CheckProposalProof(other, nil, time.Second), verify.ErrVerifierFault, "a bundle nobody verified is verified")
}

// Only valid verdicts are remembered: a rejected bundle is asked again, and a fault is no verdict.
func TestProposalChecker_onlyValidVerdictsAreReused(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	raw := testutil.Bundle{Seed: 12, ValueBalance: 20, Anchor: emptyAnchor(t)}.Encode(1)
	e.V2.Reject = func([]byte) error { return verify.ErrProofRejected }
	require.ErrorIs(t, e.Keeper.CheckProposalProof(raw, nil, time.Second), verify.ErrProofRejected)
	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	require.ErrorIs(t, e.Keeper.CheckProposalProof(raw, nil, time.Second), verify.ErrVerifierFault)
	e.V2.Reject = nil
	require.NoError(t, e.Keeper.CheckProposalProof(raw, nil, time.Second))
}
