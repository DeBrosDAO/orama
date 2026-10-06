package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
)

const powerModuleName = "power"

func TestCheckInvariants_emptyModuleAccountHolds(t *testing.T) {
	f := newTestFixture(t)

	detail, ok := f.Keeper.CheckInvariants(f.Ctx)
	require.True(t, ok, detail)
}

func TestCheckInvariants_strandedBalanceIsReported(t *testing.T) {
	f := newTestFixture(t)
	f.Bank.fund(powerModuleName, math.NewInt(7))

	detail, ok := f.Keeper.CheckInvariants(f.Ctx)
	require.False(t, ok)
	require.Contains(t, detail, "balance=7")
}
