package types_test

import (
	"bytes"
	"os"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

func TestMain(m *testing.M) {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.Seal()
	os.Exit(m.Run())
}

func creator() sdk.AccAddress {
	return sdk.AccAddress(bytes.Repeat([]byte{7}, 20))
}

func TestDenom_roundTripAndFeeMath(t *testing.T) {
	denom := types.Denom(creator().String(), "cash")
	require.NoError(t, sdk.ValidateDenom(denom))
	gotCreator, sub, err := types.ParseDenom(denom)
	require.NoError(t, err)
	require.Equal(t, creator().String(), gotCreator)
	require.Equal(t, "cash", sub)

	_, _, err = types.ParseDenom("norama")
	require.Error(t, err)
	_, _, err = types.ParseDenom("factory/" + creator().String() + "/CASH")
	require.Error(t, err)

	fee, err := types.TransferFeeAmount(math.NewInt(10_000), 100)
	require.NoError(t, err)
	require.True(t, fee.Equal(math.NewInt(100)))
	fee, err = types.TransferFeeAmount(math.NewInt(50), 1)
	require.NoError(t, err)
	require.True(t, fee.IsZero())
	_, err = types.TransferFeeAmount(math.NewInt(1), types.MaxTransferFeeBps+1)
	require.Error(t, err)
}

func TestParamsAndGenesis(t *testing.T) {
	require.Equal(t, int64(10)*int64(params.NoramaPerOrama), types.CreationFee)
	require.Equal(t, int64(10_000_000_000), types.CreationFee)
	require.Equal(t, int64(68359), types.DepositPerByte)
	require.NoError(t, types.DefaultGenesisState().Validate())

	bad := types.DefaultParams()
	bad.CreationFee = math.ZeroInt()
	require.Error(t, bad.Validate())

	creatorAddr := creator()
	sub, name, symbol := "cash", "Name", "SYM"
	token := types.Token{
		Denom:         types.Denom(creatorAddr.String(), sub),
		Creator:       creatorAddr.String(),
		Name:          name,
		Symbol:        symbol,
		Issued:        math.ZeroInt(),
		DepositAmount: types.DepositFor(math.NewInt(types.DepositPerByte), sub, name, symbol, ""),
	}
	gs := types.DefaultGenesisState()
	gs.Tokens = []types.Token{token, token}
	require.Error(t, gs.Validate())

	token.Shieldable = true
	token.Extensions.Freeze = true
	gs.Tokens = []types.Token{token}
	require.ErrorIs(t, gs.Validate(), types.ErrShieldPowers)

	token.Extensions.Freeze = false
	token.Shieldable = false
	gs.Tokens = []types.Token{token}
	gs.FrozenAccounts = []types.FrozenAccount{{Denom: token.Denom, Account: creatorAddr.String()}}
	require.NoError(t, gs.Validate())
}

func TestExtensions_renounceIsOneWay(t *testing.T) {
	ext := types.Extensions{Mint: true, Freeze: true, TransferFeeBps: 25, Pause: true, TransferHook: true}
	require.True(t, ext.BlocksShield())
	next, err := ext.Renounce(types.EXTENSION_FREEZE)
	require.NoError(t, err)
	require.False(t, next.Freeze)
	require.True(t, next.BlocksShield(), "pause still blocks shielding")
	next, err = next.Renounce(types.EXTENSION_PAUSE)
	require.NoError(t, err)
	require.False(t, next.BlocksShield())
	_, err = next.Renounce(types.EXTENSION_FREEZE)
	require.Error(t, err)
	_, err = ext.Renounce(types.EXTENSION_UNSPECIFIED)
	require.Error(t, err)
}
