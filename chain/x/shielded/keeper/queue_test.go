package keeper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func nodeBondMsg(seed byte, vb int64, signer sdk.AccAddress, nodeID string) *types.MsgUnshield {
	msg := unshieldMsg(seed, vb, types.UnshieldTargetNodeBond, signer)
	msg.NodeId, msg.Role = nodeID, nodestypes.RoleStorage
	return msg
}

func TestUnshield_aNodeBondToABogusNodeIsRefusedWhenItIsMade(t *testing.T) {
	e := capEnv(t, nil)
	e.Nodes.Refuse = map[string]error{"ghost": testutil.ErrBoom}

	_, err := unshield(t, e, nodeBondMsg(2, 21, alice, "ghost"))
	require.ErrorIs(t, err, types.ErrTarget, "refused within the cap")
	require.Equal(t, "1000", poolBalance(t, e).String(), "nothing left the pool")

	_, err = unshield(t, e, topup(3, 51, alice)) // use up the cap, so the next one would queue
	require.NoError(t, err)
	_, err = unshield(t, e, nodeBondMsg(4, 21, alice, "ghost"))
	require.ErrorIs(t, err, types.ErrTarget, "refused over the cap too, instead of queued to be refused at payment")
	e.EndBlock()
	require.Empty(t, queued(t, e))
	requireInvariants(t, e)

	resp, err := unshield(t, e, nodeBondMsg(4, 21, alice, "node-1"))
	require.NoError(t, err)
	require.True(t, resp.Queued, "a real node still queues")
}

func TestQueue_anOwnerCannotQueueMoreThanTheLimit(t *testing.T) {
	e := capEnv(t, nil)
	_, err := unshield(t, e, topup(2, 51, alice)) // the cap is spent: bonds queue
	require.NoError(t, err)
	for i := 0; i < types.MaxQueuedPerOwner; i++ {
		resp, err := unshield(t, e, bond(byte(10+i), 2, bob))
		require.NoError(t, err)
		require.True(t, resp.Queued)
	}
	before := poolBalance(t, e)
	_, err = unshield(t, e, bond(40, 2, bob))
	require.ErrorIs(t, err, types.ErrQueueOwnerFull)
	require.Equal(t, before.String(), poolBalance(t, e).String(), "the refused unshield spent no notes and took nothing from the pool")
	e.EndBlock()
	require.Len(t, queued(t, e), types.MaxQueuedPerOwner)

	resp, err := unshield(t, e, bond(41, 2, carol))
	require.NoError(t, err)
	require.True(t, resp.Queued, "the limit is per owner")
	requireInvariants(t, e)
}

func TestQueue_theOwnerCountFallsWhenARequestIsPaidOrRefunded(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueDeadHeadThenBob(t, e) // carol's bond refuses, bob's is paid at the first window
	count := func(who sdk.AccAddress) uint64 {
		n, err := e.Keeper.QueueOwners.Get(e.Ctx, who.String())
		if err != nil {
			return 0
		}
		return n
	}
	require.EqualValues(t, 1, count(bob))
	require.EqualValues(t, 1, count(carol))
	nextWindow(e)
	require.EqualValues(t, 0, count(bob), "paid in full")
	requireInvariants(t, e)
	for i := 1; i < keeper.MaxQueueRefusals; i++ {
		nextWindow(e)
	}
	require.Empty(t, queued(t, e))
	require.EqualValues(t, 0, count(carol), "refunded")
	requireInvariants(t, e)
}

// fillRefusing puts owners*perOwner requests that their validator refuses at the head of the queue
// and an honest request of bob's behind them, as if they had been queued by earlier unshields.
func fillRefusing(t *testing.T, e *testutil.Env, owners, perOwner int) {
	t.Helper()
	e.Bonder.Refuse = map[string]error{deadValidator(): testutil.ErrBoom}
	e.Bank.Fund(types.ModuleName, int64(owners*perOwner+5)) // the coins behind the queued amounts
	var id uint64
	add := func(owner sdk.AccAddress, validator string, amount int64) {
		require.NoError(t, e.Keeper.Queue.Set(e.Ctx, id, types.QueuedUnshield{
			Id: id, Vintage: types.VintageOrchardV1, Asset: pool.NativeAsset[:], Owner: owner.String(),
			Target: types.UnshieldTargetBond, Validator: validator, Amount: math.NewInt(amount),
		}))
		n, err := e.Keeper.QueueOwners.Get(e.Ctx, owner.String())
		if err != nil {
			n = 0
		}
		require.NoError(t, e.Keeper.QueueOwners.Set(e.Ctx, owner.String(), n+1))
		id++
	}
	for o := 0; o < owners; o++ {
		owner := sdk.AccAddress([]byte(fmt.Sprintf("owner%015d", o)))
		for j := 0; j < perOwner; j++ {
			add(owner, deadValidator(), 1)
		}
	}
	add(bob, validator(), 5)
	require.NoError(t, e.Keeper.NextQueueID.Set(e.Ctx, id))
}

func TestQueue_refusingRequestsAheadOfAnHonestOneDoNotDelayItBeyondOneWindow(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	fillRefusing(t, e, keeper.MaxServedPerWindow/types.MaxQueuedPerOwner, types.MaxQueuedPerOwner)
	require.Len(t, queued(t, e), keeper.MaxServedPerWindow+1)

	nextWindow(e)
	require.Equal(t, "5", delegated(e, bob), "the 1000 refusals did not use up the window's 1000 payments")
	rest := queued(t, e)
	require.Len(t, rest, keeper.MaxServedPerWindow, "only the refusing ones are left")
	for _, q := range rest {
		require.EqualValues(t, 1, q.Refusals)
	}
	requireInvariants(t, e)
}

func TestQueue_aWindowExaminesAtMostMaxScannedRows(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	fillRefusing(t, e, keeper.MaxScannedPerWindow/types.MaxQueuedPerOwner, types.MaxQueuedPerOwner)

	nextWindow(e)
	require.Equal(t, "0", delegated(e, bob), "the honest request is past the rows one window examines")
	require.Equal(t, keeper.MaxScannedPerWindow, eventCount(e, keeper.EventTypeQueuePaymentFailed), "and the window did not look at more")

	for i := 1; i < keeper.MaxQueueRefusals; i++ {
		nextWindow(e)
	}
	require.Len(t, queued(t, e), 1, "the refusing requests were refunded and left")
	nextWindow(e)
	require.Equal(t, "5", delegated(e, bob))
	require.Empty(t, queued(t, e))
}

func TestQueue_aRequestTheWindowSkipsIsNotCountedAsRefusedInARow(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueDeadHeadThenBob(t, e)
	nextWindow(e)
	rest := queued(t, e)
	require.Len(t, rest, 1)
	require.EqualValues(t, 1, rest[0].Refusals, "refused once")

	// Next window the validator accepts again, but the per-address cap cuts the 40 to a part of 20
	// that is below the delegation minimum: carol is skipped, not refused.
	e.Bonder.Refuse = nil
	e.Bonder.Minimum = math.NewInt(100)
	p, err := e.Keeper.Params.Get(e.Ctx)
	require.NoError(t, err)
	p.QueuePerAddressCap = math.NewInt(20)
	require.NoError(t, e.Keeper.Params.Set(e.Ctx, p))
	failedBefore := eventCount(e, keeper.EventTypeQueuePaymentFailed)
	nextWindow(e)
	rest = queued(t, e)
	require.Len(t, rest, 1)
	require.EqualValues(t, 0, rest[0].Refusals, "a skip breaks the streak: refusals are counted in a row")
	require.Equal(t, failedBefore, eventCount(e, keeper.EventTypeQueuePaymentFailed), "and it is not a refusal")
}

func TestQueue_aRefundTheBankRefusesKeepsTheRowAndIsRetriedNextWindow(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueDeadHeadThenBob(t, e)
	for i := 1; i < keeper.MaxQueueRefusals; i++ {
		nextWindow(e)
	}
	e.Bank.RefuseTo = map[string]error{carol.String(): testutil.ErrBoom}
	nextWindow(e)

	rest := queued(t, e)
	require.Len(t, rest, 1, "the row stays")
	require.Equal(t, carol.String(), rest[0].Owner)
	require.Equal(t, "40", rest[0].Amount.String(), "with its whole amount")
	require.EqualValues(t, keeper.MaxQueueRefusals, rest[0].Refusals, "and its count")
	require.Equal(t, 0, eventCount(e, keeper.EventTypeQueueRefunded))
	require.Equal(t, "0", e.Bank.Of(carol.String()).String())
	requireInvariants(t, e)

	// A chain exported in this state must import again: the row waiting for its refund is valid.
	exported, err := e.Keeper.ExportGenesis(e.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate(), "an export holding a refund the bank refused is a valid genesis")

	e.Bank.RefuseTo = nil
	nextWindow(e)
	require.Empty(t, queued(t, e), "the next window refunds it")
	require.Equal(t, "40", e.Bank.Of(carol.String()).String())
	require.Equal(t, 1, eventCount(e, keeper.EventTypeQueueRefunded))
	requireInvariants(t, e)
}

func TestQueue_theRefundFailureIsReportedByAnEvent(t *testing.T) {
	e := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueDeadHeadThenBob(t, e)
	for i := 1; i < keeper.MaxQueueRefusals; i++ {
		nextWindow(e)
	}
	e.Bank.RefuseTo = map[string]error{carol.String(): testutil.ErrBoom}
	e.Advance(pool.Window + 2*60*1e9)
	require.NoError(t, e.Keeper.EndBlock(e.Ctx))
	var reasons []string
	for _, ev := range e.Ctx.EventManager().Events() {
		if ev.Type != keeper.EventTypeQueuePaymentFailed {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == "reason" {
				reasons = append(reasons, a.Value)
			}
		}
	}
	require.NotEmpty(t, reasons)
	require.Contains(t, reasons[len(reasons)-1], "refund")
}

func TestGenesis_aQueuedRequestWithRefusalsRoundTrips(t *testing.T) {
	src := capEnv(t, func(gs *types.GenesisState) { gs.Params.QueuePerAddressCap = math.NewInt(1000) })
	queueDeadHeadThenBob(t, src)
	nextWindow(src)
	exported, err := src.Keeper.ExportGenesis(src.Ctx)
	require.NoError(t, err)
	require.Len(t, exported.Queue, 1)
	require.EqualValues(t, 1, exported.Queue[0].Refusals)

	dst := testutil.NewEnv(t, func(gs *types.GenesisState) { *gs = *exported })
	again, err := dst.Keeper.ExportGenesis(dst.Ctx)
	require.NoError(t, err)
	require.Equal(t, exported, again)
	n, err := dst.Keeper.QueueOwners.Get(dst.Ctx, carol.String())
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "the owner index is rebuilt from the queue")
}

func TestGenesis_queueRulesAreValidated(t *testing.T) {
	native := pool.NativeAsset[:]
	entry := func(id uint64, owner sdk.AccAddress, refusals uint32) types.QueuedUnshield {
		return types.QueuedUnshield{
			Id: id, Vintage: types.VintageOrchardV1, Asset: native, Owner: owner.String(),
			Target: types.UnshieldTargetBond, Amount: math.OneInt(), Refusals: refusals,
		}
	}
	gs := types.DefaultGenesisState()
	gs.NextQueueId = 100
	gs.Queue = []types.QueuedUnshield{entry(0, alice, types.MaxQueueRefusals-1)}
	require.NoError(t, gs.Validate())

	// At MaxQueueRefusals a row stays only while its refund is refused and retried: valid.
	gs.Queue = []types.QueuedUnshield{entry(0, alice, types.MaxQueueRefusals)}
	require.NoError(t, gs.Validate(), "a row waiting for a refund the bank refused is stored")

	gs.Queue = []types.QueuedUnshield{entry(0, alice, types.MaxQueueRefusals+1)}
	require.ErrorContains(t, gs.Validate(), "refunded", "no path stores a request refused more than MaxQueueRefusals times")

	gs.Queue = nil
	for i := 0; i <= types.MaxQueuedPerOwner; i++ {
		gs.Queue = append(gs.Queue, entry(uint64(i), alice, 0))
	}
	require.ErrorIs(t, gs.Validate(), types.ErrQueueOwnerFull)
	gs.Queue = gs.Queue[:types.MaxQueuedPerOwner]
	require.NoError(t, gs.Validate())
}
