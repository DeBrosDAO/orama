package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

func TestRefuseNoramaMint_refusesOnlyTheBaseDenom(t *testing.T) {
	ctx := context.Background()
	require.ErrorContains(t, refuseNoramaMint(ctx, sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1))), "only x/emission")
	require.ErrorContains(t, refuseNoramaMint(ctx, sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 5), sdk.NewInt64Coin("utoken", 1))), "only x/emission")
	require.NoError(t, refuseNoramaMint(ctx, sdk.NewCoins(sdk.NewInt64Coin("utoken", 1))))
	require.NoError(t, refuseNoramaMint(ctx, sdk.NewCoins()))
}
