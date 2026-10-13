package keeper_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func epochClosedEvents(ctx sdk.Context) []map[string]string {
	var out []map[string]string
	for _, e := range ctx.EventManager().Events() {
		if e.Type != types.EventTypeEpochClosed {
			continue
		}
		attrs := map[string]string{}
		for _, a := range e.Attributes {
			attrs[a.Key] = a.Value
		}
		out = append(out, attrs)
	}
	return out
}

func TestAdvanceBlock_closingBlockReportsTheEpochItClosed(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 3, true)
		gs.EpochState.EpochStartUnixNano = 1_700_000_000_000_000_000
	})

	var closing sdk.Context
	for i := range 3 {
		closing = f.Ctx.WithEventManager(sdk.NewEventManager()).WithBlockTime(time.Unix(1_700_000_031+int64(i), 0))
		require.NoError(t, f.Keeper.AdvanceBlock(closing))
		if i < 2 {
			require.Empty(t, epochClosedEvents(closing), "a block that closes nothing reports nothing")
		}
	}

	got := epochClosedEvents(closing)
	require.Len(t, got, 1)
	require.Equal(t, map[string]string{
		types.AttributeKeyEpoch:         "1",
		types.AttributeKeyBlocks:        "3",
		types.AttributeKeyStartUnixNano: strconv.FormatInt(1_700_000_000_000_000_000, 10),
	}, got[0])
	state, err := f.Keeper.EpochState.Get(closing)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch, "the event names the epoch that closed, not the one that began")
}

func TestAdvanceBlock_aParamsChangeMovesTheBoundaryTheEventReports(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
		gs.EpochState.EpochStartUnixNano = 1_700_000_000_000_000_000
	})
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, types.NewParams(300*time.Second, 1, true)))

	early := f.Ctx.WithEventManager(sdk.NewEventManager()).WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(early))
	require.Empty(t, epochClosedEvents(early), "the longer epoch has not run its duration")

	late := f.Ctx.WithEventManager(sdk.NewEventManager()).WithBlockTime(time.Unix(1_700_000_301, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(late))
	got := epochClosedEvents(late)
	require.Len(t, got, 1)
	require.Equal(t, "2", got[0][types.AttributeKeyBlocks])
}
