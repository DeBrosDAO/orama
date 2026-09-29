package keeper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// validator is a valid operator address; the bech32 prefix is set by testutil.NewEnv.
func validator() string { return sdk.ValAddress(bob).String() }

// capEnv has a 1000-norama pool, a 50-norama unshield cap (the floor: 2% of 1000 is 20) and a
// per-address queue cap of 30.
func capEnv(t *testing.T, mutate func(*types.GenesisState)) *testutil.Env {
	t.Helper()
	e := testutil.NewEnv(t, func(gs *types.GenesisState) {
		gs.Params.UnshieldFloor = math.NewInt(50)
		gs.Params.QueuePerAddressCap = math.NewInt(30)
		if mutate != nil {
			mutate(gs)
		}
	})
	shield(t, e, 1, 1000)
	e.EndBlock()
	return e
}

func unshieldMsg(seed byte, vb int64, target types.UnshieldTarget, signer sdk.AccAddress) *types.MsgUnshield {
	return &types.MsgUnshield{
		Signer: signer.String(),
		Bundle: testutil.Bundle{Seed: seed, ValueBalance: vb, Anchor: emptyRoot()}.Encode(1),
		Target: target, Validator: validator(),
	}
}

func unshield(t *testing.T, e *testutil.Env, msg *types.MsgUnshield) (*types.MsgUnshieldResponse, error) {
	t.Helper()
	var resp *types.MsgUnshieldResponse
	err := e.Tx(func(ctx sdk.Context) error {
		var err error
		resp, err = keeper.NewMsgServerImpl(e.Keeper).Unshield(ctx, msg)
		return err
	})
	return resp, err
}

func emptyRoot() [32]byte {
	root, _ := testutil.Tree{}.EmptyRoot()
	return root
}

func bond(seed byte, vb int64, signer sdk.AccAddress) *types.MsgUnshield {
	return unshieldMsg(seed, vb, types.UnshieldTargetBond, signer)
}

func topup(seed byte, vb int64, signer sdk.AccAddress) *types.MsgUnshield {
	m := bond(seed, vb, signer)
	m.Target = types.UnshieldTargetFeeTopup
	return m
}

func delegated(e *testutil.Env, who sdk.AccAddress) string {
	if v, ok := e.Bonder.Delegated[who.String()+"/"+validator()]; ok {
		return v.String()
	}
	return "0"
}

func TestUnshield_feeTopupCreditsTheSignersOwnEarnings(t *testing.T) {
	e := capEnv(t, nil)
	resp, err := unshield(t, e, topup(2, 41, alice))
	require.NoError(t, err)
	require.False(t, resp.Queued)
	require.Equal(t, "40", e.Fees.EarningsOf(alice).String(), "41 leaves the pool, 1 nullifier fee is burned")
	require.Equal(t, "959", poolBalance(t, e).String())
	requireInvariants(t, e)
}

func TestUnshield_feeTopupIsCappedPerTx(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.MaxFeeTopup = math.NewInt(30) })
	_, err := unshield(t, e, topup(2, 41, alice))
	require.ErrorIs(t, err, types.ErrFeeTopupCap)
	_, err = unshield(t, e, topup(2, 31, alice))
	require.NoError(t, err, "exactly max_fee_topup is allowed")
}

func TestUnshield_overTheCapFeeTopupFailsAtomically(t *testing.T) {
	e := capEnv(t, nil)
	_, err := unshield(t, e, topup(2, 41, alice))
	require.NoError(t, err)
	poolBefore := poolBalance(t, e)

	_, err = unshield(t, e, topup(3, 41, alice))
	require.ErrorIs(t, err, pool.ErrCapExhausted, "40 + 40 is over the 50 cap and a fee top-up is not queued")
	require.Equal(t, poolBefore.String(), poolBalance(t, e).String(), "nothing left the pool")
	require.Equal(t, "40", e.Fees.EarningsOf(alice).String())
	empty, _ := e.Keeper.Queue.Iterate(e.Ctx, nil)
	defer empty.Close()
	require.False(t, empty.Valid(), "it did not queue")

	_, err = unshield(t, e, topup(3, 11, alice))
	require.NoError(t, err, "the same nullifier is free again after the failed tx, and 10 fits under the cap")
}

func TestUnshield_theCapWindowRollsAfter24Hours(t *testing.T) {
	e := capEnv(t, nil)
	_, err := unshield(t, e, topup(2, 51, alice))
	require.NoError(t, err)
	_, err = unshield(t, e, topup(3, 11, alice))
	require.ErrorIs(t, err, pool.ErrCapExhausted)
	nextWindow(e)
	_, err = unshield(t, e, topup(3, 11, alice))
	require.NoError(t, err)
}

func TestUnshield_burnedFeesAreExemptButATipCountsAgainstTheCap(t *testing.T) {
	e := capEnv(t, nil)
	e.Fees.Proposer = bob
	// Fee 41: 11 is burned (exempt), 30 is tip and counts. Cap 50 leaves 20.
	require.NoError(t, transfer(e, transferBundle(20, 41, emptyRoot())))
	_, err := unshield(t, e, topup(2, 21, alice))
	require.NoError(t, err, "the burned 11 took nothing from the cap: 20 is still there")
	_, err = unshield(t, e, topup(3, 2, alice))
	require.ErrorIs(t, err, pool.ErrCapExhausted, "the tip and the top-up used the whole 50")
}

func TestTransfer_aTipOverTheCapFailsSoAProposerCannotDrainThePoolThroughFees(t *testing.T) {
	e := capEnv(t, nil)
	e.Fees.Proposer = bob
	err := transfer(e, transferBundle(20, 400, emptyRoot()))
	require.ErrorIs(t, err, pool.ErrCapExhausted)
	require.Equal(t, "1000", poolBalance(t, e).String())
	e.Fees.Proposer = nil
	require.NoError(t, transfer(e, transferBundle(20, 400, emptyRoot())), "with no proposer the whole fee is burned, which is exempt")
}

func TestQueue_aBondBehindAQueueTakesNoCap(t *testing.T) {
	e := capEnv(t, nil)
	queueTwo(t, e)
	nextWindow(e)
	lim, err := e.Keeper.Limiters.Get(e.Ctx, collectionsPool())
	require.NoError(t, err)
	before := lim.Counted
	resp, err := unshield(t, e, bond(20, 6, bob))
	require.NoError(t, err)
	require.True(t, resp.Queued)
	lim, err = e.Keeper.Limiters.Get(e.Ctx, collectionsPool())
	require.NoError(t, err)
	require.Equal(t, before.String(), lim.Counted.String(), "a queued bond has paid nothing and counts nothing")
}

func TestUnshield_aBondBelowTheDelegationMinimumIsRefusedUpFront(t *testing.T) {
	e := capEnv(t, nil)
	e.Bonder.Minimum = math.NewInt(100)
	_, err := unshield(t, e, bond(2, 21, alice))
	require.ErrorIs(t, err, types.ErrTarget)
	require.Equal(t, "1000", poolBalance(t, e).String(), "nothing left the pool")
}

func TestUnshield_bondWithinTheCapIsPaidAtOnce(t *testing.T) {
	e := capEnv(t, nil)
	resp, err := unshield(t, e, bond(2, 41, alice))
	require.NoError(t, err)
	require.False(t, resp.Queued)
	require.Equal(t, "40", delegated(e, alice))
	requireInvariants(t, e)
}

func TestUnshield_bondOverTheCapQueuesAndKeepsTheCoinsInTheModule(t *testing.T) {
	e := capEnv(t, nil)
	_, err := unshield(t, e, bond(2, 41, alice))
	require.NoError(t, err)

	resp, err := unshield(t, e, bond(3, 41, alice))
	require.NoError(t, err)
	require.True(t, resp.Queued)
	require.Equal(t, "40", delegated(e, alice), "nothing more was delegated yet")
	require.Equal(t, "918", poolBalance(t, e).String(), "the pool is debited when the notes are spent")
	require.Equal(t, "958", module(e).String(), "918 in the pool plus the queued 40")
	requireInvariants(t, e)
}

// queueTwo fills the 50 cap with a top-up, then queues a 40 bond for alice and a 40 bond for bob.
func queueTwo(t *testing.T, e *testutil.Env) {
	t.Helper()
	_, err := unshield(t, e, topup(2, 51, alice))
	require.NoError(t, err)
	for i, who := range []sdk.AccAddress{alice, bob} {
		resp, err := unshield(t, e, bond(byte(3+i), 41, who))
		require.NoError(t, err)
		require.True(t, resp.Queued)
	}
}

func nextWindow(e *testutil.Env) {
	e.Advance(pool.Window + time.Minute)
	e.EndBlock()
}

func queued(t *testing.T, e *testutil.Env) []types.QueuedUnshield {
	t.Helper()
	gs, err := e.Keeper.ExportGenesis(e.Ctx)
	require.NoError(t, err)
	return gs.Queue
}

func TestQueue_nextWindowServesRequestsProRataByAmount(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueTwo(t, e)
	nextWindow(e)

	// The cap is 50; alice and bob each asked for 40, so each is paid 50 x 40/80 = 25.
	require.Equal(t, "25", delegated(e, alice))
	require.Equal(t, "25", delegated(e, bob))
	rest := queued(t, e)
	require.Len(t, rest, 2)
	for _, q := range rest {
		require.Equal(t, "15", q.Amount.String(), "40 asked, 25 paid")
	}
	requireInvariants(t, e)
}

func TestQueue_isServedOncePerWindow(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueTwo(t, e)
	nextWindow(e)
	e.Blocks(5)
	require.Equal(t, "25", delegated(e, alice), "more blocks in the same window pay nothing more")
	nextWindow(e)
	require.Equal(t, "40", delegated(e, alice), "the next window pays the rest")
	require.Empty(t, queued(t, e))
	requireInvariants(t, e)
}

func TestQueue_perAddressCapBindsAWhaleWithManyRequests(t *testing.T) {
	e := capEnv(t, nil) // per-address cap 30
	_, err := unshield(t, e, topup(2, 51, alice))
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		resp, err := unshield(t, e, bond(byte(3+i), 41, alice)) // the whale queues 40 twice
		require.NoError(t, err)
		require.True(t, resp.Queued)
	}
	resp, err := unshield(t, e, bond(9, 11, bob)) // and a small holder queues 10
	require.NoError(t, err)
	require.True(t, resp.Queued)
	nextWindow(e)

	// capacity 50 over 90 asked: the whale would get 44, so it is cut to 30; bob gets 5.
	require.Equal(t, "30", delegated(e, alice), "the per-address cap binds however many requests it queued")
	require.Equal(t, "5", delegated(e, bob))
	var alices []string
	for _, q := range queued(t, e) {
		if q.Owner == alice.String() {
			alices = append(alices, q.Amount.String())
		}
	}
	require.Equal(t, []string{"10", "40"}, alices, "the grant went to the earlier request first")
}

func TestQueue_newBondsWaitBehindANonEmptyQueue(t *testing.T) {
	e := capEnv(t, nil)
	queueTwo(t, e)
	nextWindow(e) // serves some; the rest stays queued
	require.NotEmpty(t, queued(t, e))

	resp, err := unshield(t, e, bond(20, 6, bob)) // 5 net: far below the remaining cap
	require.NoError(t, err)
	require.True(t, resp.Queued, "a small bond must not jump a queue that still has entries")
}

func TestQueue_feeTopupIsNotHeldBehindTheQueue(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(10) })
	queueTwo(t, e)
	nextWindow(e) // pays 10 to each, 20 of the 50: there is room left and the queue is not empty
	require.NotEmpty(t, queued(t, e))
	_, err := unshield(t, e, topup(20, 6, alice))
	require.NoError(t, err, "a fee top-up is judged by the cap alone; only bonds wait behind the queue")
}

func TestQueue_aRefusingTargetKeepsItsRequestAndOthersStillPay(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueTwo(t, e)
	e.Bonder.Fail = testutil.ErrBoom
	e.Advance(pool.Window + time.Minute)
	require.NoError(t, e.Keeper.EndBlock(e.Ctx))
	var failed int
	for _, ev := range e.Ctx.EventManager().Events() {
		if ev.Type == keeper.EventTypeQueuePaymentFailed {
			failed++
		}
	}
	require.Equal(t, 2, failed, "each refused request is reported")
	require.Equal(t, "0", delegated(e, alice))
	require.Len(t, queued(t, e), 2, "nothing was lost")
	// The fake bank is not part of the cache context the payment runs in, so the module balance
	// is checked against the real bank in the app integration tests, not here.
}

func TestUnshield_nodeBondTargetGoesToTheSignersOwnNode(t *testing.T) {
	e := capEnv(t, nil)
	msg := unshieldMsg(2, 21, types.UnshieldTargetNodeBond, alice)
	msg.NodeId, msg.Role = "node-1", nodestypes.RoleStorage
	_, err := unshield(t, e, msg)
	require.NoError(t, err)
	require.Len(t, e.Nodes.Bonds, 1)
	bond := e.Nodes.Bonds[0]
	require.Equal(t, alice.String(), bond.Operator, "the operator is the signer, never a field of the message")
	require.Equal(t, "20", bond.Amount.String())
	require.Equal(t, "node-1", bond.NodeId)
}

func TestUnshield_depositAndContractTargetsAreNotLinked(t *testing.T) {
	e := capEnv(t, nil)
	for _, target := range []types.UnshieldTarget{types.UnshieldTargetDeposit, types.UnshieldTargetContract} {
		_, err := unshield(t, e, unshieldMsg(2, 21, target, alice))
		require.ErrorIs(t, err, types.ErrTargetNotLinked, target.String())
	}
	require.Equal(t, "1000", poolBalance(t, e).String())
}

func TestUnshield_theTurnstileRefusesMoreThanThePoolHolds(t *testing.T) {
	e := capEnv(t, nil)
	_, err := unshield(t, e, bond(2, 5000, alice))
	require.ErrorIs(t, err, pool.ErrUnderflow)
	require.Equal(t, "1000", poolBalance(t, e).String())
}

func TestUnshield_amountBelowNullifierFeeAndWrongSignAreRefused(t *testing.T) {
	e := capEnv(t, nil)
	_, err := unshield(t, e, bond(2, 1, alice))
	require.ErrorIs(t, err, types.ErrAmountTooSmall)
	_, err = unshield(t, e, bond(2, -50, alice))
	require.ErrorIs(t, err, types.ErrValueBalance)
}

func TestUnshield_theTargetIsTheSignerAndNobodyElse(t *testing.T) {
	e := capEnv(t, nil)
	_, err := unshield(t, e, bond(2, 21, bob))
	require.NoError(t, err)
	require.Equal(t, "20", delegated(e, bob))
	require.Equal(t, "0", delegated(e, alice), "a bond goes to the signer's own delegation")
	_, err = unshield(t, e, topup(3, 21, bob))
	require.NoError(t, err)
	require.Equal(t, "20", e.Fees.EarningsOf(bob).String())
	require.True(t, e.Fees.EarningsOf(alice).IsZero())
}

func TestMsg_validateBasic(t *testing.T) {
	e := capEnv(t, nil)
	_ = e
	good := bond(2, 21, alice)
	require.NoError(t, good.ValidateBasic())
	bad := *good
	bad.Validator = "not-a-validator"
	require.ErrorIs(t, bad.ValidateBasic(), types.ErrTarget)
	bad = *good
	bad.Target = types.UnshieldTargetUnspecified
	require.ErrorIs(t, bad.ValidateBasic(), types.ErrTarget)
	bad = *good
	bad.Target = types.UnshieldTargetNodeBond
	require.ErrorIs(t, bad.ValidateBasic(), types.ErrTarget, "a node bond needs a node id and a role")
	bad = *good
	bad.Bundle = nil
	require.ErrorIs(t, bad.ValidateBasic(), types.ErrBundleSize)
	bad = *good
	bad.Signer = "nobody"
	require.Error(t, bad.ValidateBasic())
}

func TestUnshield_verifiersAreGivenTheSignerAndTargetAsTheBinding(t *testing.T) {
	e := capEnv(t, nil)
	e.V1.Bindings, e.V2.Bindings = nil, nil
	msg := bond(2, 21, alice)
	_, err := unshield(t, e, msg)
	require.NoError(t, err)
	want, err := msg.Binding()
	require.NoError(t, err)
	require.Equal(t, [][]byte{want}, e.V1.Bindings)
	require.Equal(t, [][]byte{want}, e.V2.Bindings, "both verifiers check the same binding")

	// Only an unshield is bound: the other messages have nothing on the transparent side an
	// observer could redirect.
	e.V1.Bindings = nil
	shield(t, e, 9, 100)
	require.NoError(t, transfer(e, transferBundle(10, 20, emptyRoot())))
	for _, b := range e.V1.Bindings {
		require.Empty(t, b)
	}
}
