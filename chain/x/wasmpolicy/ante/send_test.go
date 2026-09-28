package ante_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/ante"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func init() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
}

func acc(b byte) sdk.AccAddress {
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = b
	}
	return raw
}

func norama(n int64) sdk.Coins {
	return sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, n))
}

func TestContractSendDecorator(t *testing.T) {
	contract := acc(1)
	user := acc(2)
	otherContract := acc(3)
	fees := authtypes.NewModuleAddress(types.ModuleName)
	deposits := authtypes.NewModuleAddress(types.DepositsModuleName)

	decorator := ante.NewFeeEarningsDecorator(func(_ context.Context, addr sdk.AccAddress) bool {
		return addr.Equals(contract) || addr.Equals(otherContract)
	})
	ctx := sdk.Context{}

	cases := []struct {
		name    string
		from    sdk.AccAddress
		to      sdk.AccAddress
		amt     sdk.Coins
		wantErr bool
	}{
		{name: "contract to user norama", from: contract, to: user, amt: norama(1), wantErr: true},
		{name: "contract to fees module norama", from: contract, to: fees, amt: norama(1)},
		{name: "contract to deposits module norama", from: contract, to: deposits, amt: norama(5)},
		{name: "contract to contract norama", from: contract, to: otherContract, amt: norama(1)},
		{name: "contract to user other denom", from: contract, to: user, amt: sdk.NewCoins(sdk.NewInt64Coin("uusd", 1))},
		{name: "user to user norama", from: user, to: acc(4), amt: norama(1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			tx := txOf{msgs: []sdk.Msg{&banktypes.MsgSend{
				FromAddress: tc.from.String(),
				ToAddress:   tc.to.String(),
				Amount:      tc.amt,
			}}}
			_, err := decorator.AnteHandle(ctx, tx, false, func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
				called = true
				return ctx, nil
			})
			if tc.wantErr {
				require.ErrorIs(t, err, policytypes.ErrContractNorama)
				require.False(t, called)
				_, rerr := decorator.Restrict(context.Background(), tc.from, tc.to, tc.amt)
				require.ErrorIs(t, rerr, policytypes.ErrContractNorama)
				return
			}
			require.NoError(t, err)
			require.True(t, called)
			redirected, rerr := decorator.Restrict(context.Background(), tc.from, tc.to, tc.amt)
			require.NoError(t, rerr)
			require.True(t, redirected.Equals(tc.to), "the restriction must not reroute the recipient")
		})
	}
}
