package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// CheckLedgers is what the chain runs inside a block: the module account against the pools, and
// nothing that reads the nullifier database.
func TestCheckLedgers_holdsAfterAShield(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	inv, err := e.Keeper.CheckLedgers(e.Ctx)
	require.NoError(t, err)
	require.True(t, inv.BalanceMatches, inv.Detail)
	require.True(t, inv.PoolsNonNegative, inv.Detail)
}

func TestCheckLedgers_emptyPoolHolds(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	inv, err := e.Keeper.CheckLedgers(e.Ctx)
	require.NoError(t, err)
	require.True(t, inv.BalanceMatches && inv.PoolsNonNegative, inv.Detail)
}

func TestCheckLedgers_detectsADriftedModuleBalance(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.Bank.Fund(types.ModuleName, 5)
	inv, err := e.Keeper.CheckLedgers(e.Ctx)
	require.NoError(t, err)
	require.False(t, inv.BalanceMatches)
}

// A drifted accumulator is the start-up check's business: CheckLedgers does not read the nullifier
// database, so it passes where CheckInvariants fails.
func TestCheckLedgers_ignoresTheNullifierAccumulator(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	require.NoError(t, e.Keeper.Accumulator.Set(e.Ctx, make([]byte, bundle.NodeLen)))

	ledgers, err := e.Keeper.CheckLedgers(e.Ctx)
	require.NoError(t, err)
	require.True(t, ledgers.AccumulatorMatches, "CheckLedgers does not look")
	full, err := e.Keeper.CheckInvariants(e.Ctx)
	require.NoError(t, err)
	require.False(t, full.AccumulatorMatches)
}
