package keeper_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

const probationDepositKeyPrefix = "storage/probation/"

// probationFixture returns a fixture with three probation nodes holding one protocol PUBLIC_PIN
// deal at launch-default prices: probation_deposit 1000 and a per-epoch price of 1000, of which
// the provider share is 900 after the 5% burn and 5% archive cut.
func probationFixture(t *testing.T) (*fixture, []*nodeInfo, uint64, []byte) {
	t.Helper()
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.ProbationExpiryEpochs = 50
		gs.Params.SMinProviders = 1
		gs.Params.SFullProviders = 1
	})
	require.Equal(t, int64(1000), types.DefaultProbationDeposit)
	require.Equal(t, int64(1000), types.DefaultProtocolPrice)
	for e := uint64(1); e <= 10; e++ {
		f.Emission.ceiling[e] = math.NewInt(1_000_000)
	}
	nodes := f.threeProbationNodes()
	data := payload(9)
	id, err := f.Keeper.CreateProtocolDeal(f.Ctx, types.DealClass_DEAL_CLASS_PUBLIC_PIN, data, math.NewInt(types.DefaultProtocolPrice), 8)
	require.NoError(t, err)
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)
	return f, nodes, id, data
}

// nextEpoch advances one epoch: the previous epoch closes, its payouts settle, and this epoch's
// challenges open.
func (f *fixture) nextEpoch(t *testing.T) {
	t.Helper()
	f.Emission.epoch++
	f.begin(t)
	f.end(t)
}

func (f *fixture) proveAll(t *testing.T, id uint64, data []byte) {
	t.Helper()
	for i := uint32(0); i < 3; i++ {
		f.proveSlot(t, id, i, data)
	}
}

// A probation node's first credited payout must never require money the node has not earned: the
// deposit fills from what each credit brings, min(remaining, credited), instead of demanding the
// whole probation_deposit at once. With launch defaults the first credit is 900 against a 1000
// deposit; demanding 1000 there failed EndBlock and halted the chain.
func TestProbationDeposit_fillsProgressivelyFromFirstEarnings(t *testing.T) {
	f, nodes, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	f.nextEpoch(t) // epoch 2 closes and settles.

	held := f.slot(t, id, 0)
	depositID := probationDepositKeyPrefix + held.NodeId
	require.Equal(t, math.NewInt(900), f.Deposits.locked[depositID], "the first credit is 900, all of it is locked")
	require.True(t, f.Earnings.get(f.nodeOperator(nodes, held.NodeId)).IsZero(), "the whole first credit went into the deposit")
	state, err := f.Keeper.Nodes.Get(f.Ctx, held.NodeId)
	require.NoError(t, err)
	require.True(t, state.DepositLocked)
	require.True(t, state.EverProved)

	f.proveAll(t, id, data)
	f.nextEpoch(t)
	require.Equal(t, math.NewInt(1000), f.Deposits.locked[depositID], "the second credit tops the deposit up to probation_deposit and no further")
	require.Equal(t, math.NewInt(800), f.Earnings.get(f.nodeOperator(nodes, held.NodeId)), "the rest of the second credit stays earnings")

	f.proveAll(t, id, data)
	f.nextEpoch(t)
	require.Equal(t, math.NewInt(1000), f.Deposits.locked[depositID], "a full deposit is not topped up again")
	require.Equal(t, math.NewInt(1700), f.Earnings.get(f.nodeOperator(nodes, held.NodeId)))
	f.requireInvariants(t)
}

func (f *fixture) nodeOperator(nodes []*nodeInfo, nodeID string) sdk.AccAddress {
	for _, n := range nodes {
		if n.id == nodeID {
			return n.operator
		}
	}
	return nil
}

// A deposit that cannot be locked costs the node that deposit, never the payout and never the
// block: the payout is credited, the failure is counted against the node, and the deposit is
// retried from the next credit.
func TestProbationDeposit_aFailedLockNeverHaltsTheBlockOrLosesThePayout(t *testing.T) {
	f, nodes, id, data := probationFixture(t)
	f.Deposits.failLock = true
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	f.nextEpoch(t)

	held := f.slot(t, id, 0)
	require.Equal(t, math.NewInt(900), f.Earnings.get(f.nodeOperator(nodes, held.NodeId)), "the payout is credited in full")
	require.Empty(t, f.Deposits.locked)
	n, err := f.Keeper.FailureCount(f.Ctx, held.NodeId, keeper.FailureKindDeposit)
	require.NoError(t, err)
	require.Equal(t, uint64(1), n)

	f.Deposits.failLock = false
	f.proveAll(t, id, data)
	f.nextEpoch(t)
	require.Equal(t, math.NewInt(900), f.Deposits.locked[probationDepositKeyPrefix+held.NodeId], "the deposit is taken from the next credit")
	n, err = f.Keeper.FailureCount(f.Ctx, held.NodeId, keeper.FailureKindDeposit)
	require.NoError(t, err)
	require.Zero(t, n, "a success clears the failure count")
}

// One operator whose earnings account cannot be credited must not stop the queue: everyone else is
// paid, and its own row is retried behind the queue MaxSettlementAttempts times. A row that fails
// every time is then dropped, the reserved mint is burned so the storage account keeps holding
// exactly what is still queued, and the failures are counted against that node.
func TestSettlement_oneUnpayableRowIsRetriedThenDroppedAndTheQueueKeepsDraining(t *testing.T) {
	f, nodes, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	bad := f.slot(t, id, 0).NodeId
	f.Earnings.failCredit = f.nodeOperator(nodes, bad).String()

	f.nextEpoch(t)

	queued, err := f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(1), queued.Pending, "only the unpayable row is left, queued again")
	require.Contains(t, eventTypes(f.Ctx), "storage_settlement_requeued")
	require.Contains(t, eventTypes(f.Ctx), "storage_item_failed")
	for _, other := range nodes {
		if other.id == bad {
			continue
		}
		require.Equal(t, math.NewInt(900), f.Deposits.locked[probationDepositKeyPrefix+other.id], "the other nodes are paid and lock their deposit")
	}
	f.requireInvariants(t)
	burnedBefore := f.Bank.burned

	row := f.queuedRows(t)[0]
	require.Equal(t, uint32(1), row.Attempts)
	require.Equal(t, f.Emission.epoch+1, row.NotBeforeEpoch, "the retry is due the next epoch, not the next block")
	for i := 0; i < 5; i++ {
		require.NoError(t, f.Keeper.SettleQueue(f.Ctx))
	}
	rows := f.queuedRows(t)
	require.Len(t, rows, 1)
	require.Equal(t, row.Attempts, rows[0].Attempts, "more blocks of the same epoch neither retry the row nor use up its attempts")
	require.Equal(t, row.NotBeforeEpoch, rows[0].NotBeforeEpoch)

	for attempt := uint32(2); attempt < types.MaxSettlementAttempts; attempt++ {
		f.Emission.epoch++
		require.NoError(t, f.Keeper.SettleQueue(f.Ctx))
		rows = f.queuedRows(t)
		require.Len(t, rows, 1, "the row is still retried on attempt %d", attempt)
		require.Equal(t, attempt, rows[0].Attempts)
		queued, err = f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
		require.NoError(t, err)
		require.Equal(t, uint64(1), queued.Pending)
	}
	f.Emission.epoch++
	require.NoError(t, f.Keeper.SettleQueue(f.Ctx))

	queued, err = f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
	require.NoError(t, err)
	require.Zero(t, queued.Pending, "the row is dropped on its last attempt")
	require.Contains(t, eventTypes(f.Ctx), "storage_settlement_dropped")
	require.True(t, f.Bank.burned.GT(burnedBefore), "the dropped row's reserved mint is burned")
	f.requireInvariants(t)
	res, err := f.Query.NodeFailures(f.Ctx, &types.QueryNodeFailuresRequest{NodeId: bad})
	require.NoError(t, err)
	require.Equal(t, []types.FailureCount{{NodeId: bad, Kind: keeper.FailureKindSettlement, Consecutive: uint64(types.MaxSettlementAttempts)}}, res.Failures)
}

// A failure that clears by itself costs the operator nothing: the row is applied on a later attempt
// and nothing is burned for it.
func TestSettlement_aTransientFailureIsRetriedAndPaid(t *testing.T) {
	f, nodes, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	bad := f.slot(t, id, 0).NodeId
	op := f.nodeOperator(nodes, bad)
	f.Earnings.failCredit = op.String()
	f.nextEpoch(t)
	require.True(t, f.Earnings.get(op).IsZero())

	f.Earnings.failCredit = ""
	burnedBefore := f.Bank.burned
	f.Emission.epoch++
	require.NoError(t, f.Keeper.SettleQueue(f.Ctx))

	queued, err := f.Query.Queue(f.Ctx, &types.QueryQueueRequest{})
	require.NoError(t, err)
	require.Zero(t, queued.Pending)
	require.Equal(t, math.NewInt(900), f.Deposits.locked[probationDepositKeyPrefix+bad], "the retried row pays: its credit fills the deposit")
	require.NotContains(t, eventTypes(f.Ctx), "storage_settlement_dropped")
	require.True(t, f.Bank.burned.GT(burnedBefore), "only the ordinary service burn, not a dropped mint")
	n, err := f.Keeper.FailureCount(f.Ctx, bad, keeper.FailureKindSettlement)
	require.NoError(t, err)
	require.Zero(t, n)
	f.requireInvariants(t)
}

// A row that points at a deal that is gone is data about that row, not a fault of the state.
func TestSettlement_aRowOfAMissingDealIsIsolated(t *testing.T) {
	f, _, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	f.nextEpoch(t)
	require.NoError(t, f.Keeper.Queue.Set(f.Ctx, 999, types.Settlement{Seq: 999, DealId: 12345, Slot: 0, NodeId: "n", Proved: true}))
	require.NoError(t, f.Keeper.QueueTail.Set(f.Ctx, 1000))
	require.NoError(t, f.Keeper.QueueHead.Set(f.Ctx, 999))
	require.NoError(t, f.Keeper.EndBlock(f.Ctx))
	n, err := f.Keeper.FailureCount(f.Ctx, "n", keeper.FailureKindSettlement)
	require.NoError(t, err)
	require.Equal(t, uint64(1), n)
}

// Only failures about one item are isolated. Everything else (an unexpected error, an encoding
// fault, even one reported by a collaborator) is returned so the block fails.
func TestIsolate_onlyItemFailuresAreIsolated(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	run := func(err error) (bool, error) {
		return f.Keeper.Isolate(f.Ctx, keeper.FailureKindDeal, "deal/1", func(sdk.Context) error { return err })
	}

	failed, err := run(keeper.Reject(errors.New("operator cannot be paid")))
	require.NoError(t, err)
	require.True(t, failed, "a rejection is isolated")
	failed, err = run(types.Refuse(fmt.Errorf("deal 7 does not exist")))
	require.NoError(t, err)
	require.True(t, failed, "a missing record that its call site converted to a rejection is isolated")
	failed, err = run(fmt.Errorf("load: %w", collections.ErrNotFound))
	require.Error(t, err, "a bare not-found from x/storage's own indexes is a fault, not an item's failure")
	require.True(t, failed)

	failed, err = run(errors.New("counter went negative"))
	require.Error(t, err, "an unexpected error is fatal")
	require.True(t, failed)
	_, err = run(fmt.Errorf("decode: %w", collections.ErrEncoding))
	require.Error(t, err, "an encoding fault is fatal")
	_, err = run(keeper.Reject(fmt.Errorf("collaborator: %w", collections.ErrEncoding)))
	require.Error(t, err, "an encoding fault reported by a collaborator stays fatal")
	require.NoError(t, keeper.Reject(nil))

	n, err := f.Keeper.FailureCount(f.Ctx, "deal/1", keeper.FailureKindDeal)
	require.NoError(t, err)
	require.Equal(t, uint64(2), n, "only the two isolated failures were counted")
}

func TestSyncNodes_failureCountRisesUntilTheNodeRecovers(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	for i := 0; i < 3; i++ {
		f.Nodes.touch("ghost")
		require.NoError(t, f.Keeper.BeginBlock(f.Ctx))
	}
	res, err := f.Query.NodeFailures(f.Ctx, &types.QueryNodeFailuresRequest{NodeId: "ghost"})
	require.NoError(t, err)
	require.Equal(t, []types.FailureCount{{NodeId: "ghost", Kind: keeper.FailureKindSync, Consecutive: 3}}, res.Failures)

	f.registerUntracked("ghost", "10.1.0.0/16", 2, 1<<20)
	require.NoError(t, f.Keeper.BeginBlock(f.Ctx))
	res, err = f.Query.NodeFailures(f.Ctx, &types.QueryNodeFailuresRequest{NodeId: "ghost"})
	require.NoError(t, err)
	require.Empty(t, res.Failures, "a successful reconciliation clears the count")
}

func TestSyncNodes_aNodeStuckForAHundredBlocksIsEscalated(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	for i := 0; i < 100; i++ {
		f.Nodes.touch("ghost")
		require.NoError(t, f.Keeper.BeginBlock(f.Ctx))
	}
	require.Contains(t, eventTypes(f.Ctx), "storage_item_stuck")
}

func TestNodeFailures_requiresANodeID(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	_, err := f.Query.NodeFailures(f.Ctx, &types.QueryNodeFailuresRequest{})
	require.Error(t, err)
	res, err := f.Query.NodeFailures(f.Ctx, &types.QueryNodeFailuresRequest{NodeId: "healthy"})
	require.NoError(t, err)
	require.Empty(t, res.Failures)
}

func TestChallengesQuery_isBoundedToOneNode(t *testing.T) {
	f, _, id, data := probationFixture(t)
	f.nextEpoch(t)
	_, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 2})
	require.Error(t, err, "a whole epoch's challenges are not served by one call")
	node := f.slot(t, id, 0).NodeId
	res, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 2, NodeId: node})
	require.NoError(t, err)
	require.Len(t, res.Challenges, 1)
	_ = data
}

// A deal whose record cannot be expired (here: escrow with no client to refund) is reported and
// left for the next block; the other deals' expiry and the block itself carry on.
func TestExpireDeals_oneCorruptDealDoesNotHaltTheBlock(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000_000)
	mk := func(seed byte) uint64 {
		return f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1_000, 1, []types.PieceCommitment{
			commit(t, payload(seed)), commit(t, payload(seed)), commit(t, payload(seed)),
		})
	}
	bad, good := mk(1), mk(2)
	deal := f.deal(t, bad)
	deal.Client = ""
	require.NoError(t, f.Keeper.Deals.Set(f.Ctx, bad, deal))

	f.Emission.epoch = 5
	f.begin(t)
	f.end(t)

	require.Equal(t, types.DealStatus_DEAL_STATUS_EXPIRED, f.deal(t, good).Status, "the healthy deal still expires")
	require.NotEqual(t, types.DealStatus_DEAL_STATUS_EXPIRED, f.deal(t, bad).Status)
	n, err := f.Keeper.FailureCount(f.Ctx, "deal/"+u64(bad), keeper.FailureKindDeal)
	require.NoError(t, err)
	require.NotZero(t, n)
}

func u64(v uint64) string { return fmt.Sprintf("%d", v) }

func TestFailureCounts_surviveGenesisExport(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.Nodes.touch("ghost")
	require.NoError(t, f.Keeper.BeginBlock(f.Ctx))
	gs, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, []types.FailureCount{{NodeId: "ghost", Kind: keeper.FailureKindSync, Consecutive: 1}}, gs.FailureCounts)
	require.NoError(t, gs.Validate())
	g := newFixture(t)
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *gs))
	n, err := g.Keeper.FailureCount(g.Ctx, "ghost", keeper.FailureKindSync)
	require.NoError(t, err)
	require.Equal(t, uint64(1), n)

	gs.FailureCounts = append(gs.FailureCounts, gs.FailureCounts[0])
	require.Error(t, gs.Validate(), "a duplicated count is refused")
}

// A settlement row whose node is no longer tracked is data about that row: the call site converts
// the missing record into a rejection, so the row is isolated and the block goes on.
func TestSettlement_aRowOfAnUntrackedNodeIsIsolated(t *testing.T) {
	f, _, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	gone := f.slot(t, id, 0).NodeId
	require.NoError(t, f.Keeper.Nodes.Remove(f.Ctx, gone))

	f.nextEpoch(t)

	n, err := f.Keeper.FailureCount(f.Ctx, gone, keeper.FailureKindSettlement)
	require.NoError(t, err)
	require.NotZero(t, n, "the row of the untracked node is counted as one item's failure")
}

// A collaborator error that is not a refusal about the item (a collection it cannot decode, an
// unexpected condition) is a fault of the state machine: the block fails instead of the row being
// rolled back and retried on state nobody can trust.
func TestSettlement_aCollaboratorFaultFailsTheBlock(t *testing.T) {
	f, nodes, id, data := probationFixture(t)
	f.nextEpoch(t)
	f.proveAll(t, id, data)
	bad := f.slot(t, id, 0).NodeId
	f.Earnings.faultCredit = f.nodeOperator(nodes, bad).String()

	f.Emission.epoch++
	f.begin(t)
	err := f.Keeper.EndBlock(f.Ctx)

	require.Error(t, err)
	require.Contains(t, err.Error(), "not about one item")
}

// The refusals a collaborator makes about one item are isolated whichever collaborator makes them:
// bank funds that cannot cover the write, a blocked account, or a deposit that is gone.
func TestSettlement_collaboratorRefusalsAreIsolated(t *testing.T) {
	for name, refusal := range map[string]error{
		"insufficient funds": errorsmod.Wrap(sdkerrors.ErrInsufficientFunds, "module account is short"),
		"blocked account":    errorsmod.Wrap(sdkerrors.ErrUnauthorized, "account is blocked"),
		"record not found":   errorsmod.Wrap(sdkerrors.ErrNotFound, "deposit does not exist"),
	} {
		t.Run(name, func(t *testing.T) {
			f, nodes, id, data := probationFixture(t)
			f.nextEpoch(t)
			f.proveAll(t, id, data)
			bad := f.slot(t, id, 0).NodeId
			f.Earnings.creditErr = refusal
			f.Earnings.failCredit = f.nodeOperator(nodes, bad).String()

			f.nextEpoch(t)

			n, err := f.Keeper.FailureCount(f.Ctx, bad, keeper.FailureKindSettlement)
			require.NoError(t, err)
			require.NotZero(t, n)
		})
	}
}

// queuedRows returns the settlement rows still waiting, in queue order.
func (f *fixture) queuedRows(t *testing.T) []types.Settlement {
	t.Helper()
	var rows []types.Settlement
	require.NoError(t, f.Keeper.Queue.Walk(f.Ctx, nil, func(_ uint64, s types.Settlement) (bool, error) {
		rows = append(rows, s)
		return false, nil
	}))
	return rows
}
