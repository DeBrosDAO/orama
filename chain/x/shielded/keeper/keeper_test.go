package keeper_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// With the test genesis (nullifier fee 1, action gas 10) and a base fee of 1, a one-action transfer
// needs a fee of 11: 10 of base fee and 1 of nullifier fee.
const oneActionFee = 11

var (
	alice = sdk.AccAddress([]byte("alice_______________"))
	bob   = sdk.AccAddress([]byte("bob_________________"))
)

func emptyAnchor(t *testing.T) [bundle.NodeLen]byte {
	t.Helper()
	root, err := testutil.Tree{}.EmptyRoot()
	require.NoError(t, err)
	return root
}

func module(e *testutil.Env) math.Int { return e.Bank.Of(types.ModuleName) }

func poolBalance(t *testing.T, e *testutil.Env) math.Int {
	t.Helper()
	bal, err := e.Keeper.Pools.Get(e.Ctx, collectionsPool())
	if err != nil {
		return math.ZeroInt()
	}
	return bal
}

// requireInvariants ends the block first: the nullifier database is written at the end of a block,
// so it agrees with the state only then.
func requireInvariants(t *testing.T, e *testutil.Env) {
	t.Helper()
	e.EndBlockUncommitted()
	inv, err := e.Keeper.CheckInvariants(e.Ctx)
	require.NoError(t, err)
	require.True(t, inv.BalanceMatches && inv.PoolsNonNegative && inv.AccumulatorMatches && inv.QueueIndexMatches, inv.Detail)
}

// shield funds the pool through MsgShield with a bundle of the given seed. It returns the bundle.
func shield(t *testing.T, e *testutil.Env, seed byte, amount int64) []byte {
	t.Helper()
	e.Bank.Fund(alice.String(), amount+1) // the amount, and the burned nullifier fee on top
	raw := testutil.Bundle{Seed: seed, ValueBalance: -amount, Anchor: emptyAnchor(t)}.Encode(1)
	_, err := keeper.NewMsgServerImpl(e.Keeper).Shield(e.Ctx, &types.MsgShield{Signer: alice.String(), Bundle: raw})
	require.NoError(t, err)
	return raw
}

func transferBundle(seed byte, fee int64, anchor [bundle.NodeLen]byte) []byte {
	return testutil.Bundle{Seed: seed, ValueBalance: fee, Anchor: anchor}.Encode(1)
}

func transfer(e *testutil.Env, raw []byte) error {
	return e.Tx(func(ctx sdk.Context) error {
		_, err := keeper.NewMsgServerImpl(e.Keeper).ShieldedTransfer(ctx, &types.MsgShieldedTransfer{
			Signer: types.SignerlessAddress().String(), Bundle: raw,
		})
		return err
	})
}

// shieldedRoot is the test tree's root after the given seeds' first commitments.
func rootAfter(seeds ...byte) [bundle.NodeLen]byte {
	var cmx [][bundle.NodeLen]byte
	for _, s := range seeds {
		var c [bundle.NodeLen]byte
		c[0], c[1] = 0x02, s
		cmx = append(cmx, c)
	}
	return testutil.RootAfter(cmx...)
}

func TestShield_fundsThePoolAndBurnsTheNullifierFee(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)

	require.Equal(t, "1000", poolBalance(t, e).String(), "the pool holds what the notes are worth; the nullifier fee is paid on top")
	require.Equal(t, "1000", module(e).String())
	require.Equal(t, "1", e.Bank.Burned.String())
	require.Equal(t, "0", e.Bank.Of(alice.String()).String())
	acc, count, err := e.Keeper.NullifierState(e.Ctx)
	require.NoError(t, err)
	require.NotEqual(t, [bundle.NodeLen]byte{}, acc)
	require.Equal(t, uint64(1), count)
	requireInvariants(t, e)
}

func TestShield_endOfBlockRecordsNullifierAndAnchor(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	height := e.Height
	e.EndBlock()

	spent, err := e.Store.Spent(testutil.Nullifier(1, 0), height+1)
	require.NoError(t, err)
	require.True(t, spent)
	root := rootAfter(1)
	got, err := e.Keeper.Anchors.Get(e.Ctx, root[:])
	require.NoError(t, err)
	require.Equal(t, height, got)
}

func TestShield_wrongValueBalanceSignIsRefused(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.Bank.Fund(alice.String(), 1000)
	for name, vb := range map[string]int64{"positive": 1000, "zero": 0} {
		raw := testutil.Bundle{Seed: 9, ValueBalance: vb, Anchor: emptyAnchor(t)}.Encode(1)
		_, err := keeper.NewMsgServerImpl(e.Keeper).Shield(e.Ctx, &types.MsgShield{Signer: alice.String(), Bundle: raw})
		require.ErrorIs(t, err, types.ErrValueBalance, name)
	}
}

func TestShield_withoutFundsFailsAtomically(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	raw := testutil.Bundle{Seed: 9, ValueBalance: -500, Anchor: emptyAnchor(t)}.Encode(1)
	err := e.Tx(func(ctx sdk.Context) error {
		_, err := keeper.NewMsgServerImpl(e.Keeper).Shield(ctx, &types.MsgShield{Signer: alice.String(), Bundle: raw})
		return err
	})
	require.Error(t, err)
	require.True(t, poolBalance(t, e).IsZero())
	e.EndBlock()
	spent, _ := e.Store.Spent(testutil.Nullifier(9, 0), e.Height+1)
	require.False(t, spent, "a failed tx must not leave its nullifier spent")
}

func TestShieldEarnings_movesEarningsIntoThePool(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.Fees.GrantEarnings(alice, 500)
	raw := testutil.Bundle{Seed: 3, ValueBalance: -300, Anchor: emptyAnchor(t)}.Encode(1)
	_, err := keeper.NewMsgServerImpl(e.Keeper).ShieldEarnings(e.Ctx, &types.MsgShieldEarnings{Signer: alice.String(), Bundle: raw})
	require.NoError(t, err)

	require.Equal(t, "199", e.Fees.EarningsOf(alice).String(), "300 shielded and 1 of nullifier fee")
	require.Equal(t, "300", poolBalance(t, e).String())
	require.Equal(t, "300", module(e).String())
	requireInvariants(t, e)
}

func TestShieldEarnings_cannotShieldMoreThanTheSignerEarned(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.Fees.GrantEarnings(bob, 10_000) // someone else's earnings are not reachable
	e.Fees.GrantEarnings(alice, 100)
	raw := testutil.Bundle{Seed: 3, ValueBalance: -300, Anchor: emptyAnchor(t)}.Encode(1)
	_, err := keeper.NewMsgServerImpl(e.Keeper).ShieldEarnings(e.Ctx, &types.MsgShieldEarnings{Signer: alice.String(), Bundle: raw})
	require.Error(t, err)
}

func TestTransfer_paysItsFeeFromTheValueBalance(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.Fees.Proposer = bob
	shield(t, e, 1, 1000)
	e.EndBlock()

	require.NoError(t, transfer(e, transferBundle(2, 15, rootAfter(1))))

	require.Equal(t, "985", poolBalance(t, e).String(), "1000 less the 15 fee")
	require.Equal(t, "4", e.Fees.EarningsOf(bob).String(), "the tip is what the fee leaves after base and nullifier fees")
	require.Equal(t, "12", e.Bank.Burned.String(), "1 from the shield, then 10 base + 1 nullifier")
	requireInvariants(t, e)
}

func TestTransfer_withoutAResolvableProposerBurnsTheWholeFee(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	require.NoError(t, transfer(e, transferBundle(2, 15, rootAfter(1))))

	require.Equal(t, "16", e.Bank.Burned.String(), "the shield's 1 plus the whole 15")
	require.True(t, e.Fees.EarningsOf(bob).IsZero())
	requireInvariants(t, e)
}

func TestTransfer_feeTooLowIsRefused(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	err := transfer(e, transferBundle(2, oneActionFee-1, rootAfter(1)))
	require.ErrorIs(t, err, types.ErrFeeTooLow)
	require.NoError(t, transfer(e, transferBundle(2, oneActionFee, rootAfter(1))), "the exact fee is enough")
}

func TestTransfer_feeFollowsTheBaseFee(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	e.Fees.BaseFee = math.NewInt(3)
	require.ErrorIs(t, transfer(e, transferBundle(2, oneActionFee, rootAfter(1))), types.ErrFeeTooLow)
	require.NoError(t, transfer(e, transferBundle(2, 10*3+1, rootAfter(1))))
}

func TestTransfer_negativeValueBalanceIsRefused(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	err := transfer(e, transferBundle(2, -5, emptyAnchor(t)))
	require.ErrorIs(t, err, types.ErrValueBalance)
}

func TestTransfer_cannotSpendMoreThanThePoolHolds(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 20)
	e.EndBlock()
	err := transfer(e, transferBundle(2, 1000, rootAfter(1)))
	require.ErrorIs(t, err, pool.ErrUnderflow, "the turnstile refuses a fee the pool cannot cover")
	require.Equal(t, "20", poolBalance(t, e).String())
}

func TestTransfer_replayIsRefused(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	raw := transferBundle(2, 20, rootAfter(1))
	require.NoError(t, transfer(e, raw))

	require.ErrorIs(t, transfer(e, raw), types.ErrNullifierSpent, "in the same block")
	e.EndBlock()
	require.ErrorIs(t, transfer(e, raw), types.ErrNullifierSpent, "in a later block")
	e.Blocks(5)
	require.ErrorIs(t, transfer(e, raw), types.ErrNullifierSpent, "much later")
}

func TestTransfer_twoBundlesSharingANullifierInOneBlock(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	shared := testutil.Nullifier(50, 0)
	a := testutil.Bundle{Seed: 5, ValueBalance: 20, Anchor: rootAfter(1), Nullifiers: [][bundle.NodeLen]byte{shared}}.Encode(1)
	b := testutil.Bundle{Seed: 6, ValueBalance: 20, Anchor: rootAfter(1), Nullifiers: [][bundle.NodeLen]byte{shared}}.Encode(1)
	require.NoError(t, transfer(e, a))
	require.ErrorIs(t, transfer(e, b), types.ErrNullifierSpent)
}

func TestTransfer_bundleRepeatingANullifierIsRefused(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	nf := testutil.Nullifier(7, 0)
	raw := testutil.Bundle{Seed: 7, ValueBalance: 50, Anchor: emptyAnchor(t), Nullifiers: [][bundle.NodeLen]byte{nf, nf}}.Encode(2)
	require.ErrorIs(t, transfer(e, raw), types.ErrDuplicateNullifier)
}

func TestTransfer_anchorMustBeInTheWindow(t *testing.T) {
	e := testutil.NewEnv(t, func(gs *types.GenesisState) { gs.Params.AnchorWindowBlocks = 3 })
	shield(t, e, 1, 1000)
	e.EndBlock()
	old := rootAfter(1)

	var unknown [bundle.NodeLen]byte
	unknown[0] = 0xEE
	require.ErrorIs(t, transfer(e, transferBundle(2, 20, unknown)), types.ErrAnchorUnknown, "a root the tree never had")

	e.Blocks(2)
	require.NoError(t, transfer(e, transferBundle(3, 20, old)), "still inside the window")

	// A new note moves the root. The old root then ages out; the new one is current.
	shield(t, e, 4, 100)
	e.EndBlock()
	fresh := rootAfter(1, 3, 4) // the accepted transfer added its own commitment
	e.Blocks(4)
	require.ErrorIs(t, transfer(e, transferBundle(5, 20, old)), types.ErrAnchorUnknown, "aged out of the window")
	require.NoError(t, transfer(e, transferBundle(6, 20, fresh)), "an idle tree's current root never expires")
}

func TestTransfer_emptyTreeAnchorIsAlwaysValid(t *testing.T) {
	e := testutil.NewEnv(t, func(gs *types.GenesisState) { gs.Params.AnchorWindowBlocks = 2 })
	shield(t, e, 1, 1000)
	e.Blocks(10)
	require.NoError(t, transfer(e, transferBundle(2, 20, emptyAnchor(t))))
}

func TestTransfer_actionLimit(t *testing.T) {
	e := testutil.NewEnv(t, func(gs *types.GenesisState) { gs.Params.MaxActionsPerBundle = 2 })
	shield(t, e, 1, 1000)
	e.EndBlock()
	three := testutil.Bundle{Seed: 8, ValueBalance: 100, Anchor: rootAfter(1)}.Encode(3)
	require.ErrorIs(t, transfer(e, three), types.ErrBundleSize)
	two := testutil.Bundle{Seed: 8, ValueBalance: 100, Anchor: rootAfter(1)}.Encode(2)
	require.NoError(t, transfer(e, two))
}

func TestTransfer_malformedBundles(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	good := transferBundle(2, 20, emptyAnchor(t))
	cases := map[string][]byte{
		"empty":          nil,
		"truncated":      good[:len(good)-1],
		"trailing bytes": testutil.Bundle{Seed: 2, ValueBalance: 20, Anchor: emptyAnchor(t), Tail: []byte{0}}.Encode(1),
		"zero actions":   {0},
	}
	for name, raw := range cases {
		err := e.Tx(func(ctx sdk.Context) error {
			_, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, true)
			return err
		})
		require.Error(t, err, name)
	}
}

func TestTransfer_everyVerifierMustAccept(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	raw := transferBundle(2, 20, rootAfter(1))
	before := e.V2.Calls

	e.V2.Reject = func([]byte) error { return verify.ErrProofRejected }
	require.ErrorIs(t, transfer(e, raw), verify.ErrProofRejected, "the second verifier rejecting is enough")
	e.V2.Reject = nil
	e.V1.Reject = func([]byte) error { return verify.ErrSignatureRejected }
	require.ErrorIs(t, transfer(e, raw), verify.ErrSignatureRejected)
	e.V1.Reject = nil
	require.NoError(t, transfer(e, raw))
	require.Equal(t, 2, e.V2.Calls-before, "the second attempt stopped at the first verifier's rejection")
}

func TestTransfer_oneVerifierFailsClosed(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	raw := transferBundle(2, 20, emptyAnchor(t))
	for name, vs := range map[string][]verify.Verifier{
		"none":         nil,
		"one":          {e.V1},
		"a nil second": {e.V1, nil},
	} {
		k := e.KeeperWith(vs...)
		err := e.Tx(func(ctx sdk.Context) error {
			_, err := keeper.NewMsgServerImpl(k).ShieldedTransfer(ctx, &types.MsgShieldedTransfer{
				Signer: types.SignerlessAddress().String(), Bundle: raw,
			})
			return err
		})
		require.ErrorIs(t, err, verify.ErrVerifierNotLinked, name)
	}
}

// haltOf runs fn and returns the VerifierHalt it panicked with, or fails the test when it did not
// panic or panicked with anything else.
func haltOf(t *testing.T, fn func()) (halt keeper.VerifierHalt) {
	t.Helper()
	defer func() {
		r := recover()
		require.NotNil(t, r, "a verifier fault in a block must stop the node, not return")
		var ok bool
		halt, ok = r.(keeper.VerifierHalt)
		require.True(t, ok, "the panic is a VerifierHalt, got %T: %v", r, r)
	}()
	fn()
	return halt
}

// A verifier fault in a block is no verdict on the bundle, so it records no result: the node
// stops with a message that names the verifier and what to do, and nothing of the transaction is
// kept. The test verifier faults the way the out-of-process one does when its child is killed.
func TestTransfer_aVerifierFaultInABlockHaltsTheNodeAndRecordsNoResult(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error {
		return fmt.Errorf("%w: out-of-process verifier: the process died", verify.ErrVerifierFault)
	}
	raw := transferBundle(2, 20, emptyAnchor(t))
	halt := haltOf(t, func() { _ = transfer(e, raw) })
	require.ErrorIs(t, halt, verify.ErrVerifierFault)
	require.Contains(t, halt.Error(), "orama-orchard-verifier")
	require.Contains(t, halt.Error(), "--shielded-verifier")
	require.Contains(t, halt.Error(), "executes the block again")
	require.Equal(t, 1, e.V2.Calls, "a fault is not asked again")
	_, count, err := e.Keeper.NullifierState(e.Ctx)
	require.NoError(t, err)
	require.Zero(t, count, "the halted transaction recorded nothing")
}

// The property the halt exists for: a node that lost its verifier mid-block does not record a
// result its healthy peer did not. After the restart it executes the same block and reaches the
// peer's result and state exactly.
func TestTransfer_aNodeThatFaultedReachesItsPeersStateAfterTheRestart(t *testing.T) {
	healthy := testutil.NewEnv(t, nil)
	faulty := testutil.NewEnv(t, nil)
	for _, e := range []*testutil.Env{healthy, faulty} {
		shield(t, e, 1, 1000)
		e.EndBlock()
	}
	raw := transferBundle(2, 20, rootAfter(1))
	require.NoError(t, transfer(healthy, raw))

	faulty.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	haltOf(t, func() { _ = transfer(faulty, raw) })
	faulty.V2.Reject = nil // the verifier is fixed and the node restarted
	require.NoError(t, transfer(faulty, raw), "the restarted node accepts what its peer accepted")

	accH, countH, err := healthy.Keeper.NullifierState(healthy.Ctx)
	require.NoError(t, err)
	accF, countF, err := faulty.Keeper.NullifierState(faulty.Ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), countH)
	require.Equal(t, countH, countF)
	require.NotEqual(t, [bundle.NodeLen]byte{}, accH)
	require.Equal(t, accH, accF, "same block, same nullifier accumulator")
	require.Equal(t, healthy.Bank.Burned.String(), faulty.Bank.Burned.String())
}

// A bundle the verifier rejects, or panics on, is rejected the same way on every node: both are
// verdicts that depend on the bytes alone, and neither halts a node.
func TestTransfer_aVerdictIsTheSameOnEveryNodeAndNeverHalts(t *testing.T) {
	for name, verdict := range map[string]error{
		"proof rejected":     verify.ErrProofRejected,
		"signature rejected": verify.ErrSignatureRejected,
		"verifier panicked":  verify.ErrVerifierPanic,
	} {
		var results []string
		for node := 0; node < 3; node++ {
			e := testutil.NewEnv(t, nil)
			e.V2.Reject = func([]byte) error { return verdict }
			err := transfer(e, transferBundle(2, 20, emptyAnchor(t)))
			require.ErrorIs(t, err, verdict, name)
			require.ErrorIs(t, err, verify.ErrTampered, name)
			require.Equal(t, 1, e.V2.Calls, "%s: a verdict is asked once", name)
			results = append(results, err.Error())
		}
		require.Equal(t, results[0], results[1], name)
		require.Equal(t, results[0], results[2], name)
	}
}

// The mempool and a simulation are local: a fault there refuses the transaction on this node and
// is asked once.
func TestVerify_aFaultInCheckTxRefusesLocallyAndIsAskedOnce(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	raw := transferBundle(3, 20, emptyAnchor(t))
	ctx := e.CheckCtx()
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	require.ErrorIs(t, e.Keeper.Verify(ctx, raw, nil, adm), verify.ErrVerifierFault)
	require.Equal(t, 1, e.V2.Calls, "CheckTx asks once")

	sim := e.Ctx.WithIsCheckTx(true).WithExecMode(sdk.ExecModeSimulate)
	require.ErrorIs(t, e.Keeper.Verify(sim, raw, nil, adm), verify.ErrVerifierFault, "a simulation refuses, it does not halt")
}

// Only FinalizeBlock halts on a fault. The proposal modes never verify a bundle today, but if one
// ever did, halting there would stop a node on a proposal it merely votes on.
func TestVerify_onlyFinalizeBlockHaltsOnAFault(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	raw := transferBundle(2, 20, emptyAnchor(t))
	adm, err := e.Keeper.Admit(e.Ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	for _, mode := range []sdk.ExecMode{sdk.ExecModePrepareProposal, sdk.ExecModeProcessProposal, sdk.ExecModeVoteExtension} {
		require.ErrorIs(t, e.Keeper.Verify(e.Ctx.WithExecMode(mode), raw, nil, adm), verify.ErrVerifierFault, "mode %d", mode)
	}
	haltOf(t, func() { _ = e.Keeper.Verify(e.Ctx.WithExecMode(sdk.ExecModeFinalize), raw, nil, adm) })
}

func TestTransfer_signerMustBeTheProtocolAddress(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	_, err := keeper.NewMsgServerImpl(e.Keeper).ShieldedTransfer(e.Ctx, &types.MsgShieldedTransfer{
		Signer: alice.String(), Bundle: transferBundle(2, 20, emptyAnchor(t)),
	})
	require.ErrorIs(t, err, types.ErrSigner)
}

func TestTransfer_chargesTheBundleGas(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	ctx := e.Ctx.WithGasMeter(storetypesGas())
	raw := testutil.Bundle{Seed: 2, ValueBalance: 40, Anchor: emptyAnchor(t)}.Encode(2)
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	before := ctx.GasMeter().GasConsumed()
	require.NoError(t, e.Keeper.Verify(ctx, raw, nil, adm))
	require.Equal(t, uint64(20), ctx.GasMeter().GasConsumed()-before, "action gas 10 x 2 actions")
}

func TestAdmit_changesNoState(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	raw := transferBundle(2, 20, emptyAnchor(t))
	for i := 0; i < 2; i++ {
		_, err := e.Keeper.Admit(e.Ctx, raw, nil, keeper.KindTransfer, true)
		require.NoError(t, err, "admitting twice is fine: it marks nothing")
	}
	size, _ := e.Keeper.TreeSize.Get(e.Ctx)
	require.Zero(t, size)
}

func TestMempool_pendingNullifiersAreRefusedUntilTheBlockCommits(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	check := e.CheckCtx()
	first := testutil.Bundle{Seed: 2, ValueBalance: 20, Anchor: emptyAnchor(t)}.Encode(1)
	adm, err := e.Keeper.Admit(check, first, nil, keeper.KindTransfer, true)
	require.NoError(t, err)
	require.NoError(t, e.Keeper.MarkPending(check, adm.Bundle.Nullifiers))

	_, err = e.Keeper.Admit(check, first, nil, keeper.KindTransfer, false)
	require.ErrorIs(t, err, types.ErrNullifierSpent, "already pending in the mempool")

	e.CMS.Commit() // the check state is rebuilt after a commit: the mark is gone
	_, err = e.Keeper.Admit(e.Ctx.WithIsCheckTx(true), first, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
}

func TestMempool_committedNullifierIsSpentInTheNextCheckState(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock() // commits block 1; the check state now sits at height 1
	check := e.CheckCtx().WithBlockHeight(e.Height - 1)
	_, err := e.Keeper.Admit(check, testutil.Bundle{Seed: 1, ValueBalance: -100, Anchor: emptyAnchor(t)}.Encode(1), nil, keeper.KindShield, false)
	require.ErrorIs(t, err, types.ErrNullifierSpent)
}

func TestNullifiers_aReplayedBlockDoesNotSeeItsOwnRecords(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	raw := transferBundle(2, 20, rootAfter(1))

	require.NoError(t, transfer(e, raw))
	e.EndBlockUncommitted() // the database now holds this block's record
	spent, _ := e.Store.Spent(testutil.Nullifier(2, 0), e.Height+1)
	require.True(t, spent)

	e.Crash() // the node stopped before Commit: the block runs again
	require.NoError(t, transfer(e, raw), "the replayed block must accept the same bundle")
	e.EndBlock()
	var seen int
	require.NoError(t, e.Store.Walk(func(nf [bundle.NodeLen]byte, _ int64) error {
		if nf == testutil.Nullifier(2, 0) {
			seen++
		}
		return nil
	}))
	require.Equal(t, 1, seen, "the replay replaced the record, it did not duplicate it")
	requireInvariants(t, e)
}

func TestNullifiers_stateAndDatabaseAgreeAfterManyBlocks(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 100_000)
	e.EndBlock()
	for seed := byte(10); seed < 30; seed++ {
		require.NoError(t, transfer(e, transferBundle(seed, 20, rootAfter(1))))
		if seed%3 == 0 {
			e.EndBlock()
		}
	}
	e.EndBlock()
	requireInvariants(t, e)
	_, count, err := e.Keeper.NullifierState(e.Ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(21), count)
}

func TestInvariants_detectADriftedModuleBalance(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.Bank.Fund(types.ModuleName, 5)
	inv, err := e.Keeper.CheckInvariants(e.Ctx)
	require.NoError(t, err)
	require.False(t, inv.BalanceMatches)
}

func TestInvariants_detectADriftedAccumulator(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	require.NoError(t, e.Keeper.Accumulator.Set(e.Ctx, make([]byte, bundle.NodeLen)))
	inv, err := e.Keeper.CheckInvariants(e.Ctx)
	require.NoError(t, err)
	require.False(t, inv.AccumulatorMatches)
}

func TestAccumulator_isTheFoldOfEveryNullifierInOrder(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	require.NoError(t, transfer(e, testutil.Bundle{Seed: 2, ValueBalance: 40, Anchor: rootAfter(1)}.Encode(2)))
	e.EndBlock()

	var want [bundle.NodeLen]byte
	for _, nf := range [][bundle.NodeLen]byte{testutil.Nullifier(1, 0), testutil.Nullifier(2, 0), testutil.Nullifier(2, 1)} {
		want = nullifier.Fold(want, nf)
	}
	got, count, err := e.Keeper.NullifierState(e.Ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, uint64(3), count)
}

func TestMultiAsset_isInert(t *testing.T) {
	require.ErrorIs(t, pool.AllowAsset(false, [32]byte{1}), pool.ErrMultiAssetInactive)
	gs := types.DefaultGenesisState()
	gs.Pools = []types.PoolBalance{{Vintage: 1, Asset: make([]byte, 32), Balance: math.OneInt()}}
	require.Error(t, gs.Validate(), "genesis cannot name another asset")
}

// The halt names the height and the hash of the bundle that was in flight, so an operator can see
// whether every node stopped on the same one.
func TestTransfer_theHaltNamesTheBundleItStoppedOn(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	raw := transferBundle(2, 20, emptyAnchor(t))
	halt := haltOf(t, func() { _ = transfer(e, raw) })
	sum := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(sum[:]), halt.BundleHash)
	require.Contains(t, halt.Error(), halt.BundleHash)
	require.Equal(t, e.Ctx.BlockHeight(), halt.Height)
	require.Contains(t, halt.Error(), fmt.Sprint(halt.Height))
}

// A remote submitter learns nothing about the node's verifier: the mempool's refusal is generic and
// the detail (a path, a hash, the child's state) is only logged.
func TestVerify_theMempoolRefusalOfAFaultIsGeneric(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	secret := "/var/lib/orama-global/chain/cosmovisor/current/bin/orama-orchard-verifier sha256 deadbeef"
	e.V2.Reject = func([]byte) error { return fmt.Errorf("%w: start %q", verify.ErrVerifierFault, secret) }
	raw := transferBundle(3, 20, emptyAnchor(t))
	ctx := e.CheckCtx()
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	err = e.Keeper.Verify(ctx, raw, nil, adm)
	require.ErrorIs(t, err, verify.ErrVerifierFault)
	require.NotContains(t, err.Error(), "orama-orchard-verifier")
	require.NotContains(t, err.Error(), "deadbeef")
	require.Contains(t, err.Error(), "unavailable on this node")

	e.V2.Reject = func([]byte) error { return fmt.Errorf("%w: %s", verify.ErrVerifierNotLinked, secret) }
	err = e.Keeper.Verify(e.CheckCtx(), transferBundle(4, 20, emptyAnchor(t)), nil, adm)
	require.ErrorIs(t, err, verify.ErrVerifierNotLinked)
	require.NotContains(t, err.Error(), "deadbeef")
}

// On a chain that must verify, a verifier that is missing or not distinct is a fault of this node:
// in a block it halts. On a localnet or a devnet, which run without verifiers on every node, it is
// the same refusal everywhere and halts nothing.
func TestTransfer_missingVerifiersHaltOnlyAChainThatMustVerify(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	raw := transferBundle(2, 20, emptyAnchor(t))
	for name, vs := range map[string][]verify.Verifier{
		"one verifier":   {e.V1},
		"a nil verifier": {e.V1, nil},
		"the same twice": {e.V1, e.V1},
	} {
		strict := e.KeeperRequiring(vs...)
		halt := haltOf(t, func() {
			_ = e.Tx(func(ctx sdk.Context) error {
				_, err := keeper.NewMsgServerImpl(strict).ShieldedTransfer(ctx, &types.MsgShieldedTransfer{
					Signer: types.SignerlessAddress().String(), Bundle: raw,
				})
				return err
			})
		})
		require.ErrorIs(t, halt, verify.ErrVerifierNotLinked, name)

		lax := e.KeeperWith(vs...)
		err := e.Tx(func(ctx sdk.Context) error {
			_, err := keeper.NewMsgServerImpl(lax).ShieldedTransfer(ctx, &types.MsgShieldedTransfer{
				Signer: types.SignerlessAddress().String(), Bundle: raw,
			})
			return err
		})
		require.ErrorIs(t, err, verify.ErrVerifierNotLinked, name)
	}
}

// Mempool work has verifiers of its own: a bundle that holds the mempool's child does not hold the
// block's verifier. (This is the verifier's mutex, not CometBFT's ABCI lock, which the keeper does not
// see; the mempool's short request timeout bounds that stall.)
func TestVerify_theMempoolAndTheBlockDoNotShareAVerifier(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	release := make(chan struct{})
	var entered atomic.Int32
	mempoolHung := &testutil.Verifier{Name: "mempool-b", Reject: func([]byte) error { entered.Add(1); <-release; return nil }}
	k := e.KeeperWithMempool([]verify.Verifier{e.V1, e.V2}, []verify.Verifier{e.V1, mempoolHung})

	raw := transferBundle(3, 20, rootAfter(1))
	ctx := e.CheckCtx()
	adm, err := k.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	checked := make(chan error, 1)
	go func() { checked <- k.Verify(ctx, raw, nil, adm) }()
	require.Eventually(t, func() bool { return entered.Load() == 1 }, 5*time.Second, time.Millisecond)

	blockDone := make(chan error, 1)
	go func() {
		blockDone <- e.Tx(func(ctx sdk.Context) error {
			_, err := keeper.NewMsgServerImpl(k).ShieldedTransfer(ctx, &types.MsgShieldedTransfer{
				Signer: types.SignerlessAddress().String(), Bundle: transferBundle(4, 20, rootAfter(1)),
			})
			return err
		})
	}()
	select {
	case err := <-blockDone:
		require.NoError(t, err, "the block's verifiers are not the mempool's")
	case <-time.After(5 * time.Second):
		t.Fatal("block execution waited on a mempool check")
	}
	close(release)
	require.NoError(t, <-checked)
}

// A bundle whose check ended in a fault is not put to the mempool's verifier again for a while, so
// resending it cannot keep a child busy. The memory is the mempool's alone: a block asks anyway.
func TestVerify_aFaultingBundleIsNotAskedAgainByTheMempoolButAlwaysByABlock(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return fmt.Errorf("%w: hung for 30s", verify.ErrVerifierFault) }
	raw := transferBundle(3, 20, emptyAnchor(t))
	ctx := e.CheckCtx()
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)

	require.ErrorIs(t, e.Keeper.Verify(ctx, raw, nil, adm), verify.ErrVerifierFault)
	require.ErrorIs(t, e.Keeper.Verify(ctx, raw, nil, adm), verify.ErrVerifierFault)
	require.ErrorIs(t, e.Keeper.Verify(ctx, raw, nil, adm), verify.ErrVerifierFault)
	require.Equal(t, 1, e.V2.Calls, "the mempool asked once and remembered the fault")

	haltOf(t, func() { _ = e.Keeper.Verify(e.Ctx.WithExecMode(sdk.ExecModeFinalize), raw, nil, adm) })
	require.Equal(t, 2, e.V2.Calls, "a block never reads the mempool's memory of faults")

	e.V2.Reject = nil
	other := transferBundle(5, 20, emptyAnchor(t))
	adm2, err := e.Keeper.Admit(ctx, other, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	require.NoError(t, e.Keeper.Verify(ctx, other, nil, adm2), "another bundle is not refused for the first one's fault")
}

// With the verifier fixed and the memory expired, the same bundle is checked again.
func TestVerify_aRememberedFaultExpires(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	raw := transferBundle(3, 20, emptyAnchor(t))
	ctx := e.CheckCtx()
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	require.ErrorIs(t, e.Keeper.Verify(ctx, raw, nil, adm), verify.ErrVerifierFault)
	e.V2.Reject = nil
	require.ErrorIs(t, e.Keeper.Verify(ctx, raw, nil, adm), verify.ErrVerifierFault, "still remembered")
	keeper.AdvanceFaultMemoryForTest(e.Keeper, 2*keeper.FaultMemory)
	require.NoError(t, e.Keeper.Verify(ctx, raw, nil, adm), "the memory expired")
}

// On a devnet, verifiers that are not linked refuse a bundle in a block, and the transaction's log
// carries no path or pin.
func TestTransfer_notLinkedInABlockLeaksNothingToTheLog(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error {
		return fmt.Errorf("%w: binary %q has sha256 deadbeef", verify.ErrVerifierNotLinked, "/var/lib/orama-global/chain/bin/orama-orchard-verifier")
	}
	err := transfer(e, transferBundle(2, 20, emptyAnchor(t)))
	require.ErrorIs(t, err, verify.ErrVerifierNotLinked)
	require.NotContains(t, err.Error(), "deadbeef")
	require.NotContains(t, err.Error(), "/var/lib")
}

// notReady is a mempool verifier that has not started its process yet.
type notReady struct{ *testutil.Verifier }

func (notReady) Ready() bool { return false }

// A mempool verifier that is still starting is answered before the block's verification budget is
// spent and before anything is asked of it, and the answer is neither a verdict nor a fault, so
// nothing is remembered.
func TestVerify_aStartingMempoolVerifierSpendsNothingAndIsNeverRemembered(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	starting := &testutil.Verifier{Name: "mempool-b"}
	k := e.KeeperWithMempool([]verify.Verifier{e.V1, e.V2}, []verify.Verifier{notReady{starting}, e.V1})
	raw := transferBundle(3, 20, emptyAnchor(t))
	ctx := e.CheckCtx()
	adm, err := k.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	for i := 0; i < keeper.MaxCheckVerificationsPerBlock+10; i++ {
		err = k.Verify(ctx, raw, nil, adm)
		require.ErrorIs(t, err, verify.ErrVerifierStarting)
		require.NotErrorIs(t, err, verify.ErrVerifierFault)
	}
	require.Zero(t, starting.Calls, "nothing was asked of the verifier")

	// The budget was never spent, so a ready set still has all of it.
	k2 := e.KeeperWithMempool([]verify.Verifier{e.V1, e.V2}, []verify.Verifier{starting, e.V1})
	require.NoError(t, k2.Verify(ctx, raw, nil, adm))
}

// A proposal's proof check asks the mempool's verifiers, says "starting" while they are not ready,
// reports a rejection as one, and spends none of the mempool's budget.
func TestCheckProposalProof(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	mp := &testutil.Verifier{Name: "mempool-b"}
	k := e.KeeperWithMempool([]verify.Verifier{e.V1, e.V2}, []verify.Verifier{mp, e.V1})
	raw := transferBundle(3, 20, emptyAnchor(t))
	require.NoError(t, k.CheckProposalProof(raw, nil, time.Second))
	require.Equal(t, 1, mp.Calls)

	rejected, faulted := transferBundle(4, 20, emptyAnchor(t)), transferBundle(5, 20, emptyAnchor(t))
	mp.Reject = func([]byte) error { return verify.ErrProofRejected }
	require.ErrorIs(t, k.CheckProposalProof(rejected, nil, time.Second), verify.ErrProofRejected)
	mp.Reject = func([]byte) error { return verify.ErrVerifierFault }
	require.ErrorIs(t, k.CheckProposalProof(faulted, nil, time.Second), verify.ErrVerifierFault)

	starting := e.KeeperWithMempool([]verify.Verifier{e.V1, e.V2}, []verify.Verifier{notReady{mp}, e.V1})
	require.ErrorIs(t, starting.CheckProposalProof(transferBundle(6, 20, emptyAnchor(t)), nil, time.Second), verify.ErrVerifierStarting)

	mp.Reject = nil
	for i := 0; i < keeper.MaxCheckVerificationsPerBlock+10; i++ {
		_ = k.CheckProposalProof(transferBundle(byte(10+i%100), 20, emptyAnchor(t)), nil, time.Second)
	}
	other := transferBundle(200, 20, emptyAnchor(t))
	adm, err := k.Admit(e.CheckCtx(), other, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	require.NoError(t, k.Verify(e.CheckCtx(), other, nil, adm), "proposal checks spend none of the mempool's budget")
}
