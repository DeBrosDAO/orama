package keeper_test

import (
	"testing"

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
	require.True(t, inv.BalanceMatches && inv.PoolsNonNegative && inv.AccumulatorMatches, inv.Detail)
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

func TestTransfer_verifierFaultRejectsTheBundle(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	require.ErrorIs(t, transfer(e, transferBundle(2, 20, emptyAnchor(t))), verify.ErrVerifierFault)
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
