package keeper_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

const perByte = 10

// fakeFees records deposits the way x/fees does, with the 99/1 split, and can be told to refuse a lock.
type fakeFees struct {
	deposits  map[string]feestypes.Deposit
	refunded  map[string]math.Int
	burned    math.Int
	failLocks bool
}

func newFakeFees() *fakeFees {
	return &fakeFees{deposits: map[string]feestypes.Deposit{}, refunded: map[string]math.Int{}, burned: math.ZeroInt()}
}

func (f *fakeFees) LockDeposit(_ context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	if f.failLocks {
		return fmt.Errorf("insufficient funds")
	}
	if _, ok := f.deposits[id]; ok {
		return fmt.Errorf("deposit id %q already exists", id)
	}
	f.deposits[id] = feestypes.Deposit{Id: id, Owner: owner.String(), Amount: amount}
	return nil
}

func (f *fakeFees) TopUpDeposit(_ context.Context, id string, extra math.Int) error {
	d, ok := f.deposits[id]
	if !ok {
		return fmt.Errorf("deposit id %q does not exist", id)
	}
	if f.failLocks {
		return fmt.Errorf("insufficient funds")
	}
	d.Amount = d.Amount.Add(extra)
	f.deposits[id] = d
	return nil
}

func (f *fakeFees) release(id string, part math.Int) {
	d := f.deposits[id]
	refund := part.MulRaw(99).QuoRaw(100)
	f.refunded[d.Owner] = f.refundedOf(d.Owner).Add(refund)
	f.burned = f.burned.Add(part.Sub(refund))
}

func (f *fakeFees) refundedOf(owner string) math.Int {
	if v, ok := f.refunded[owner]; ok {
		return v
	}
	return math.ZeroInt()
}

func (f *fakeFees) ReleaseDeposit(_ context.Context, id string) (math.Int, math.Int, error) {
	d, ok := f.deposits[id]
	if !ok {
		return math.Int{}, math.Int{}, fmt.Errorf("deposit id %q does not exist", id)
	}
	f.release(id, d.Amount)
	delete(f.deposits, id)
	return math.ZeroInt(), math.ZeroInt(), nil
}

func (f *fakeFees) ReleaseDepositPart(_ context.Context, id string, part math.Int) (math.Int, math.Int, error) {
	d, ok := f.deposits[id]
	if !ok || part.GTE(d.Amount) {
		return math.Int{}, math.Int{}, fmt.Errorf("bad partial release of %q", id)
	}
	f.release(id, part)
	d.Amount = d.Amount.Sub(part)
	f.deposits[id] = d
	return math.ZeroInt(), math.ZeroInt(), nil
}

func (f *fakeFees) GetDeposit(_ context.Context, id string) (feestypes.Deposit, error) {
	d, ok := f.deposits[id]
	if !ok {
		return feestypes.Deposit{}, fmt.Errorf("deposit id %q does not exist", id)
	}
	return d, nil
}

type fixture struct {
	ctx  sdk.Context
	k    keeper.Keeper
	fees *fakeFees
}

func newFixture(t *testing.T) fixture { return newFixtureWith(t, 0, 32) }

// newFixtureWith builds a keeper whose chunks carry overhead extra bytes and whose contracts hold at
// most maxChunks payers.
func newFixtureWith(t *testing.T, overhead, maxChunks uint64) fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t_wasmpolicy")).Ctx
	fees := newFakeFees()
	k := keeper.NewKeeper(runtime.NewKVStoreService(key), fees)
	gs := types.DefaultGenesisState()
	gs.DepositPerByte = math.NewInt(perByte)
	gs.ChunkOverheadBytes = overhead
	gs.MaxDepositChunks = maxChunks
	require.NoError(t, k.InitGenesis(ctx, gs))
	return fixture{ctx: ctx, k: k, fees: fees}
}

var (
	contractA = sdk.AccAddress("contract_a___________")
	contractB = sdk.AccAddress("contract_b___________")
	alice     = sdk.AccAddress("alice________________")
	bob       = sdk.AccAddress("bob__________________")
	carol     = sdk.AccAddress("carol________________")
)

func TestApplyStateDelta_growthLocksBytesTimesPrice(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))

	held, err := f.k.ChargedBytes(f.ctx, contractA)
	require.NoError(t, err)
	require.Equal(t, uint64(100), held)
	dep, err := f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 0))
	require.NoError(t, err)
	require.Equal(t, alice.String(), dep.Owner)
	require.True(t, dep.Amount.Equal(math.NewInt(100*perByte)))
	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.True(t, inv.BytesMatch && inv.DepositsMatch, inv.Detail)
}

func TestApplyStateDelta_netZeroAndNoChangeDoNothing(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 50, 50))
	require.Empty(t, f.fees.deposits)
}

func TestApplyStateDelta_shrinkReleasesNewestChunkFirstToItsPayer(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, bob, 40, 0))

	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 40))

	require.True(t, f.fees.refundedOf(bob.String()).Equal(math.NewInt(40*perByte*99/100)))
	require.True(t, f.fees.refundedOf(alice.String()).IsZero(), "alice's older chunk is untouched")
	held, err := f.k.ChargedBytes(f.ctx, contractA)
	require.NoError(t, err)
	require.Equal(t, uint64(100), held)
	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.True(t, inv.BytesMatch && inv.DepositsMatch, inv.Detail)
}

func TestApplyStateDelta_partialShrinkKeepsTheRemainderOfAChunk(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 30))

	dep, err := f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 0))
	require.NoError(t, err)
	require.True(t, dep.Amount.Equal(math.NewInt(70*perByte)))
	held, err := f.k.ChargedBytes(f.ctx, contractA)
	require.NoError(t, err)
	require.Equal(t, uint64(70), held)
	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.True(t, inv.BytesMatch && inv.DepositsMatch, inv.Detail)
}

func TestApplyStateDelta_shrinkAcrossChunksAndToZero(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, bob, 40, 0))

	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 60))
	require.Len(t, f.fees.deposits, 1, "bob's chunk is gone and alice's is trimmed by 20")
	dep, err := f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 0))
	require.NoError(t, err)
	require.True(t, dep.Amount.Equal(math.NewInt(80*perByte)))

	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 80))
	require.Empty(t, f.fees.deposits)
	held, err := f.k.ChargedBytes(f.ctx, contractA)
	require.NoError(t, err)
	require.Zero(t, held)
}

func TestApplyStateDelta_shrinkPastTheChargedTotalReleasesOnlyWhatWasCharged(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 10, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 500))
	require.Empty(t, f.fees.deposits)

	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractB, alice, 0, 500), "a contract with no deposits has nothing to refund")
}

func TestApplyStateDelta_growthWithoutAPayerOrFundsFailsAndChargesNothing(t *testing.T) {
	f := newFixture(t)
	err := f.k.ApplyStateDelta(f.ctx, contractA, nil, 10, 0)
	require.ErrorIs(t, err, types.ErrDepositPayer)

	f.fees.failLocks = true
	err = f.k.ApplyStateDelta(f.ctx, contractA, alice, 10, 0)
	require.ErrorContains(t, err, "insufficient funds")
	held, err := f.k.ChargedBytes(f.ctx, contractA)
	require.NoError(t, err)
	require.Zero(t, held)
}

func TestCheckInvariants_detectsADepositThatDrifted(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	id := types.DepositID(contractA.String(), 0)
	dep := f.fees.deposits[id]
	dep.Amount = dep.Amount.SubRaw(1)
	f.fees.deposits[id] = dep

	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.False(t, inv.DepositsMatch)
	require.Contains(t, inv.Detail, id)

	delete(f.fees.deposits, id)
	inv, err = f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.False(t, inv.DepositsMatch)
}

func TestGenesis_ledgerRoundTripsAndNextSequenceContinues(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractB, bob, 7, 0))

	exported, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.DepositChunks, 2)

	fresh := newFixture2(t, f.fees)
	require.NoError(t, fresh.InitGenesis(fresh.ctx, exported))
	held, err := fresh.k.ChargedBytes(fresh.ctx, contractA)
	require.NoError(t, err)
	require.Equal(t, uint64(100), held)
	require.NoError(t, fresh.k.ApplyStateDelta(fresh.ctx, contractA, carol, 5, 0))
	_, err = f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 2))
	require.NoError(t, err, "the imported ledger continues the chunk sequence after 1")
}

type importFixture struct {
	fixture
}

func (i importFixture) InitGenesis(ctx sdk.Context, gs types.GenesisState) error {
	return i.k.InitGenesis(ctx, gs)
}

func newFixture2(t *testing.T, fees *fakeFees) importFixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t_wasmpolicy2")).Ctx
	return importFixture{fixture{ctx: ctx, k: keeper.NewKeeper(runtime.NewKVStoreService(key), fees), fees: fees}}
}

func TestGenesisValidate_rejectsBadDepositState(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.DepositPerByte = math.ZeroInt()
	require.Error(t, gs.Validate())

	gs = types.DefaultGenesisState()
	gs.DepositChunks = []types.DepositChunk{{Contract: contractA.String(), Seq: 0, Payer: alice.String(), Bytes: 0, PerByte: math.NewInt(1), DepositID: types.DepositID(contractA.String(), 0)}}
	require.Error(t, gs.Validate(), "a chunk of zero bytes")

	gs = types.DefaultGenesisState()
	c := types.DepositChunk{Contract: contractA.String(), Seq: 1, Payer: alice.String(), Bytes: 4, PerByte: math.NewInt(1), DepositID: "wrong"}
	gs.DepositChunks = []types.DepositChunk{c}
	require.Error(t, gs.Validate(), "a chunk whose deposit id does not match")

	c.DepositID = types.DepositID(contractA.String(), 1)
	gs.DepositChunks = []types.DepositChunk{c, c}
	require.Error(t, gs.Validate(), "a duplicated chunk")
}

func TestApplyStateDelta_samePayerGrowthTopsUpOneChunk(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 50; i++ {
		require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 1, 0))
	}
	require.Len(t, f.fees.deposits, 1, "fifty one-byte growths by one payer are one deposit row")
	dep, err := f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 0))
	require.NoError(t, err)
	require.True(t, dep.Amount.Equal(math.NewInt(50*perByte)))
	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.True(t, inv.BytesMatch && inv.DepositsMatch, inv.Detail)
}

func TestApplyStateDelta_aNewPayerLocksTheChunkOverhead(t *testing.T) {
	const overhead = 100
	f := newFixtureWith(t, overhead, 32)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 10, 0))
	dep, err := f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 0))
	require.NoError(t, err)
	require.True(t, dep.Amount.Equal(math.NewInt((10+overhead)*perByte)), "bytes plus the ledger overhead")

	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 5, 0))
	dep, _ = f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 0))
	require.True(t, dep.Amount.Equal(math.NewInt((15+overhead)*perByte)), "a top-up adds no second overhead")

	// A partial shrink keeps the overhead; releasing every byte releases it too.
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 5))
	dep, _ = f.fees.GetDeposit(f.ctx, types.DepositID(contractA.String(), 0))
	require.True(t, dep.Amount.Equal(math.NewInt((10+overhead)*perByte)))
	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.True(t, inv.BytesMatch && inv.DepositsMatch, inv.Detail)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 10))
	require.Empty(t, f.fees.deposits)
}

func TestApplyStateDelta_aContractHoldsAtMostMaxChunksPayers(t *testing.T) {
	f := newFixtureWith(t, 0, 2)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 1, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, bob, 1, 0))

	err := f.k.ApplyStateDelta(f.ctx, contractA, carol, 1, 0)
	require.ErrorIs(t, err, types.ErrDepositLedgerFull)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 9, 0), "an existing payer can still grow")
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractB, carol, 1, 0), "another contract has its own limit")
}

func TestApplyStateDelta_theTransactionBudgetCapsWhatIsLocked(t *testing.T) {
	f := newFixture(t)
	limits, err := f.k.Limits.Get(f.ctx)
	require.NoError(t, err)
	limits.MaxDepositPerTx = math.NewInt(100 * perByte)
	require.NoError(t, f.k.Limits.Set(f.ctx, limits))

	ctx := types.WithDepositBudget(f.ctx, types.NewDepositBudget())
	require.NoError(t, f.k.ApplyStateDelta(ctx, contractA, alice, 60, 0))
	err = f.k.ApplyStateDelta(ctx, contractB, alice, 60, 0)
	require.ErrorIs(t, err, types.ErrDepositCap, "the second call in the same transaction passes the cap")
	require.NoError(t, f.k.ApplyStateDelta(ctx, contractB, alice, 40, 0), "up to the cap is allowed")

	fresh := types.WithDepositBudget(f.ctx, types.NewDepositBudget())
	require.NoError(t, f.k.ApplyStateDelta(fresh, contractA, alice, 40, 0), "a new transaction has a new budget")
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 1000, 0), "no budget, no cap: genesis and direct calls")
}

func TestGenesisValidate_rejectsBadDepositLimits(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.MaxDepositPerTx = math.ZeroInt()
	require.Error(t, gs.Validate())
	gs = types.DefaultGenesisState()
	gs.MaxDepositChunks = 0
	require.Error(t, gs.Validate())
}
