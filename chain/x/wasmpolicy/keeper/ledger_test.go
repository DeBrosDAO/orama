package keeper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func stranger(i int) sdk.AccAddress { return sdk.AccAddress(fmt.Sprintf("stranger_%011d", i)) }

// gasOf runs fn on a context with its own gas meter and returns the gas it consumed.
func gasOf(f fixture, fn func(ctx sdk.Context) error) (uint64, error) {
	ctx := f.ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	err := fn(ctx)
	return ctx.GasMeter().GasConsumed(), err
}

func requireSimilarGas(t *testing.T, small, large uint64, what string) {
	t.Helper()
	require.LessOrEqual(t, large, small+small/10, "%s: %d gas with few payers, %d with many", what, small, large)
}

func requireBothInvariants(t *testing.T, f fixture) {
	t.Helper()
	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.True(t, inv.BytesMatch && inv.DepositsMatch, "running totals: %s", inv.Detail)
	inv, err = f.k.CheckInvariantsExact(f.ctx)
	require.NoError(t, err)
	require.True(t, inv.BytesMatch && inv.DepositsMatch, "chunks: %s", inv.Detail)
}

func TestApplyStateDelta_chargeGasDoesNotGrowWithOtherPayersChunks(t *testing.T) {
	gas := map[int]uint64{}
	for _, others := range []int{3, 300} {
		f := newFixture(t)
		require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 10, 0))
		for i := 0; i < others; i++ {
			require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, stranger(i), 1, 0))
		}
		var err error
		gas[others], err = gasOf(f, func(ctx sdk.Context) error { return f.k.ApplyStateDelta(ctx, contractA, alice, 1, 0) })
		require.NoError(t, err)
	}
	requireSimilarGas(t, gas[3], gas[300], "topping up a payer's own chunk")
}

func TestApplyStateDelta_newPayerGasDoesNotGrowWithOtherPayersChunks(t *testing.T) {
	gas := map[int]uint64{}
	for _, others := range []int{3, 300} {
		f := newFixture(t)
		for i := 0; i < others; i++ {
			require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, stranger(i), 1, 0))
		}
		var err error
		gas[others], err = gasOf(f, func(ctx sdk.Context) error { return f.k.ApplyStateDelta(ctx, contractA, alice, 1, 0) })
		require.NoError(t, err)
	}
	requireSimilarGas(t, gas[3], gas[300], "a new payer's chunk")
}

func TestApplyStateDelta_aPayersShrinkReleasesItsOwnChunkWhateverOthersAddedAfterIt(t *testing.T) {
	f := newFixtureWith(t, 0, 2)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 10, 0))
	for i := 0; i < 40; i++ {
		require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, stranger(i), 1, 0))
	}
	before := f.fees.refundedOf(alice.String())

	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 0, 10))

	refunded := f.fees.refundedOf(alice.String()).Sub(before)
	require.True(t, refunded.Equal(math.NewInt(10*perByte*99/100)), "alice got her whole deposit back in the one call, got %s", refunded)
	_, err := f.k.ReleaseCarry.Get(f.ctx, contractA.Bytes())
	require.ErrorIs(t, err, collections.ErrNotFound, "nothing is carried: the 40 newer rows were never walked")
	held, err := f.k.ChargedBytes(f.ctx, contractA)
	require.NoError(t, err)
	require.Equal(t, uint64(40), held)
	requireBothInvariants(t, f)
}

func TestApplyStateDelta_shrinkGasDoesNotGrowWithOtherPayersChunks(t *testing.T) {
	gas := map[int]uint64{}
	for _, others := range []int{3, 300} {
		f := newFixtureWith(t, 0, 2)
		require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 10, 0))
		for i := 0; i < others; i++ {
			require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, stranger(i), 1, 0))
		}
		var err error
		gas[others], err = gasOf(f, func(ctx sdk.Context) error { return f.k.ApplyStateDelta(ctx, contractA, alice, 0, 4) })
		require.NoError(t, err)
	}
	requireSimilarGas(t, gas[3], gas[300], "a payer shrinking its own chunk")
}

func TestApplyStateDelta_aShrinkByAnAccountWithNoChunkStillReleasesNewestFirst(t *testing.T) {
	f := newFixtureWith(t, 0, 2)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 10, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, bob, 10, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, frank, 0, 10))
	require.True(t, f.fees.refundedOf(bob.String()).Equal(math.NewInt(10*perByte*99/100)))
	require.True(t, f.fees.refundedOf(alice.String()).IsZero())
	requireBothInvariants(t, f)
}

func TestCheckInvariants_runningTotalsFollowEveryWrite(t *testing.T) {
	f := newFixtureWith(t, 7, 3)
	payers := []sdk.AccAddress{alice, bob, carol, frank}
	steps := []struct {
		contract sdk.AccAddress
		payer    int
		grew     uint64
		shrank   uint64
	}{
		{contractA, 0, 30, 0}, {contractA, 1, 12, 0}, {contractB, 2, 5, 0}, {contractA, 0, 4, 0},
		{contractA, 3, 0, 9}, {contractA, 1, 0, 12}, {contractB, 2, 0, 2}, {contractA, 2, 1, 0},
		{contractA, 0, 0, 100}, {contractB, 2, 0, 100}, {contractA, 3, 0, 100},
	}
	for i, s := range steps {
		require.NoError(t, f.k.ApplyStateDelta(f.ctx, s.contract, payers[s.payer], s.grew, s.shrank), "step %d", i)
		requireBothInvariants(t, f)
	}
}

func TestCheckInvariants_detectsTotalsThatDriftedFromTheirParts(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractB, bob, 7, 0))

	g, err := f.k.Totals.Get(f.ctx)
	require.NoError(t, err)
	g.Bytes++
	require.NoError(t, f.k.Totals.Set(f.ctx, g))
	inv, err := f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.False(t, inv.BytesMatch, "the global byte total no longer sums the contracts")
	g.Bytes--
	g.Locked = g.Locked.AddRaw(1)
	require.NoError(t, f.k.Totals.Set(f.ctx, g))
	inv, err = f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.False(t, inv.DepositsMatch, "the global locked total no longer sums the contracts")
	g.Locked = g.Locked.SubRaw(1)
	require.NoError(t, f.k.Totals.Set(f.ctx, g))
	requireBothInvariants(t, f)

	ct, err := f.k.ContractTotals.Get(f.ctx, contractB.Bytes())
	require.NoError(t, err)
	require.NoError(t, f.k.ContractTotals.Remove(f.ctx, contractB.Bytes()))
	inv, err = f.k.CheckInvariants(f.ctx)
	require.NoError(t, err)
	require.False(t, inv.BytesMatch, "a contract with bytes and no rows")

	ct.Locked = ct.Locked.AddRaw(3)
	require.NoError(t, f.k.ContractTotals.Set(f.ctx, contractB.Bytes(), ct))
	inv, err = f.k.CheckInvariantsExact(f.ctx)
	require.NoError(t, err)
	require.False(t, inv.DepositsMatch, "the exact check compares a contract's totals with its chunks")
}

func TestCheckInvariants_epochCheckCostDoesNotGrowWithChunks(t *testing.T) {
	gas := map[int]uint64{}
	for _, payers := range []int{3, 300} {
		f := newFixture(t)
		for i := 0; i < payers; i++ {
			require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, stranger(i), 1, 0))
		}
		var err error
		gas[payers], err = gasOf(f, func(ctx sdk.Context) error {
			inv, err := f.k.CheckInvariants(ctx)
			require.True(t, inv.BytesMatch && inv.DepositsMatch, inv.Detail)
			return err
		})
		require.NoError(t, err)
	}
	requireSimilarGas(t, gas[3], gas[300], "the epoch invariant")
}

func TestGenesis_importRebuildsThePayerIndexAndTotals(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, alice, 100, 0))
	require.NoError(t, f.k.ApplyStateDelta(f.ctx, contractA, bob, 5, 0))
	exported, err := f.k.ExportGenesis(f.ctx)
	require.NoError(t, err)

	fresh := newFixture2(t, f.fees)
	require.NoError(t, fresh.InitGenesis(fresh.ctx, exported))
	requireBothInvariants(t, fresh.fixture)
	require.NoError(t, fresh.k.ApplyStateDelta(fresh.ctx, contractA, alice, 1, 0))
	exported2, err := fresh.k.ExportGenesis(fresh.ctx)
	require.NoError(t, err)
	require.Len(t, exported2.DepositChunks, 2, "alice's growth topped up her imported chunk instead of opening a second")
	requireBothInvariants(t, fresh.fixture)
}

func TestGenesisValidate_refusesTwoChunksOfOnePayerInOneContract(t *testing.T) {
	gs := types.DefaultGenesisState()
	mk := func(seq uint64) types.DepositChunk {
		return types.DepositChunk{Contract: contractA.String(), Seq: seq, Payer: alice.String(), Bytes: 4, PerByte: math.NewInt(1), DepositID: types.DepositID(contractA.String(), seq)}
	}
	gs.DepositChunks = []types.DepositChunk{mk(0), mk(1)}
	require.ErrorContains(t, gs.Validate(), "one payer is one chunk")
}
