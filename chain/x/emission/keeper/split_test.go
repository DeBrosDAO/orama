package keeper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

type fakeSplitSource struct {
	pct types.SplitPercents
	err error
}

func (s *fakeSplitSource) EmissionSplit(context.Context) (types.SplitPercents, error) {
	return s.pct, s.err
}

// epoch1Max is epoch 1's maximum mint: 14,848 ORAMA.
var epoch1Max = math.NewInt(14_848_000_000_000)

func splitFixture(t *testing.T, src *fakeSplitSource) *testFixture {
	t.Helper()
	f := newTestFixture(t)
	f.Keeper = f.Keeper.WithSplitSource(src)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	return f
}

func closeEpochs(t *testing.T, f *testFixture, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031+int64(i)*31, 0))
		require.NoError(t, f.Keeper.AdvanceBlock(ctx))
		f.Ctx = ctx
	}
}

func TestCloseEpoch_usesTheEnactedSplit(t *testing.T) {
	src := &fakeSplitSource{pct: types.SplitPercents{Validator: 70, Storage: 15, Relay: 10, Development: 5}}
	f := splitFixture(t, src)
	closeEpochs(t, f, 1)

	record, err := f.Keeper.Ceilings.Get(f.Ctx, 1)
	require.NoError(t, err)
	require.True(t, record.ValidatorMinted.Equal(epoch1Max.MulRaw(70).QuoRaw(100)), record.ValidatorMinted)
	require.True(t, record.StorageCeiling.Equal(epoch1Max.MulRaw(15).QuoRaw(100)), record.StorageCeiling)
	require.Equal(t, uint32(70), record.ValidatorPercent)
	require.Equal(t, uint32(15), record.StoragePercent)

	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	canonical := types.SplitEpochMint(epoch1Max).Validator
	require.True(t, state.ValidatorSplitDelta.Equal(record.ValidatorMinted.Sub(canonical)))
	require.True(t, f.Power.distributed.Equal(record.ValidatorMinted))

	_, broken := f.Keeper.CheckSupplyInvariant(f.Ctx)
	require.False(t, broken, "the supply invariant must hold under an enacted split")
}

func TestCloseEpoch_splitChangeAppliesFromTheNextClosedEpoch(t *testing.T) {
	src := &fakeSplitSource{pct: types.CanonicalSplitPercents()}
	f := splitFixture(t, src)
	closeEpochs(t, f, 1)

	src.pct = types.SplitPercents{Validator: 50, Storage: 35, Relay: 10, Development: 5}
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031+31, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))
	f.Ctx = ctx

	first, err := f.Keeper.Ceilings.Get(f.Ctx, 1)
	require.NoError(t, err)
	second, err := f.Keeper.Ceilings.Get(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, first.Percents().IsCanonical())
	require.Zero(t, first.ValidatorPercent, "a canonical epoch is stored with zero percents")
	require.Equal(t, uint32(50), second.ValidatorPercent)
	require.True(t, second.ValidatorMinted.LT(first.ValidatorMinted))

	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, state.ValidatorSplitDelta.IsNegative())
	_, broken := f.Keeper.CheckSupplyInvariant(f.Ctx)
	require.False(t, broken)
}

func TestCloseEpoch_refusesAnInvalidEnactedSplit(t *testing.T) {
	cases := map[string]types.SplitPercents{
		"sum below 100":      {Validator: 60, Storage: 25, Relay: 10, Development: 4},
		"share out of bound": {Validator: 100, Storage: 0, Relay: 0, Development: 0},
	}
	for name, pct := range cases {
		t.Run(name, func(t *testing.T) {
			f := splitFixture(t, &fakeSplitSource{pct: pct})
			ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
			require.ErrorContains(t, f.Keeper.AdvanceBlock(ctx), "enacted emission split is invalid")
		})
	}
}

func TestCloseEpoch_surfacesASplitSourceError(t *testing.T) {
	f := splitFixture(t, &fakeSplitSource{err: errors.New("houses store unreadable")})
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.ErrorContains(t, f.Keeper.AdvanceBlock(ctx), "houses store unreadable")
}

func TestCloseEpoch_noSourceIsTheCanonicalSplit(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	closeEpochs(t, f, 1)
	record, err := f.Keeper.Ceilings.Get(f.Ctx, 1)
	require.NoError(t, err)
	require.True(t, record.ValidatorMinted.Equal(types.SplitEpochMint(epoch1Max).Validator))
	require.Zero(t, record.ValidatorPercent)
}

func TestMintDevelopmentSpend_usesTheDevelopmentShareOfTheEnactedSplit(t *testing.T) {
	src := &fakeSplitSource{pct: types.SplitPercents{Validator: 55, Storage: 25, Relay: 10, Development: 10}}
	f := splitFixture(t, src)
	closeEpochs(t, f, 1)

	ceiling := epoch1Max.MulRaw(10).QuoRaw(100)
	_, err := f.Keeper.MintDevelopmentSpend(f.Ctx, 1, ceiling.AddRaw(1))
	require.Error(t, err)
	_, err = f.Keeper.MintDevelopmentSpend(f.Ctx, 1, ceiling)
	require.NoError(t, err)
	_, broken := f.Keeper.CheckSupplyInvariant(f.Ctx)
	require.False(t, broken)
}

func TestSplitExportImport_keepsTheSplitInvariant(t *testing.T) {
	src := &fakeSplitSource{pct: types.SplitPercents{Validator: 70, Storage: 15, Relay: 10, Development: 5}}
	f := splitFixture(t, src)
	closeEpochs(t, f, 2)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate(), "an exported enacted-split state must validate")

	exported.EpochState.ValidatorSplitDelta = exported.EpochState.ValidatorSplitDelta.AddRaw(1)
	require.Error(t, exported.Validate(), "a wrong split delta must fail cumulative_minted")
}

func TestGenesisValidate_rejectsACeilingRecordWithAnOutOfBoundSplit(t *testing.T) {
	src := &fakeSplitSource{pct: types.SplitPercents{Validator: 70, Storage: 15, Relay: 10, Development: 5}}
	f := splitFixture(t, src)
	closeEpochs(t, f, 1)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)

	exported.Ceilings[0].ValidatorPercent = 80
	exported.Ceilings[0].StoragePercent = 5
	require.ErrorContains(t, exported.Validate(), "validator_percent")
}
