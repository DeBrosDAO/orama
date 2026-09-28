package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	feesante "github.com/DeBrosOfficial/network/chain/x/fees/ante"
)

func TestBondTopUpDecorator_fundsTheShortfallFromTheSignersEarnings(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("outsider_bonder_____")
	f.Bank.fund(addr.String(), math.NewInt(100))
	f.Bank.fund(testSourceModule, math.NewInt(900))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(900))))

	dec := feesante.NewBondTopUpDecorator(f.Bank, f.Keeper)
	msg := &stakingtypes.MsgDelegate{
		DelegatorAddress: addr.String(),
		ValidatorAddress: sdk.ValAddress(addr).String(),
		Amount:           sdk.NewCoin(params.BaseDenom, math.NewInt(400)),
	}
	_, err := dec.AnteHandle(f.Ctx, bondTx{msg: msg}, false, func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		return ctx, nil
	})
	require.NoError(t, err)
	require.True(t, f.Bank.balanceOf(addr.String()).Equal(math.NewInt(400)), "bank had 100, so 300 comes from earnings")
	left, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, left.Equal(math.NewInt(600)))

	got, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, got.EarningsMatchModule, got.Detail)
}

func TestBondTopUpDecorator_leavesACoveredBondAlone(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	addr := sdk.AccAddress("already_funded_bond_")
	f.Bank.fund(addr.String(), math.NewInt(500))
	f.Bank.fund(testSourceModule, math.NewInt(500))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(500))))

	dec := feesante.NewBondTopUpDecorator(f.Bank, f.Keeper)
	msg := &stakingtypes.MsgCreateValidator{
		ValidatorAddress: sdk.ValAddress(addr).String(),
		Value:            sdk.NewCoin(params.BaseDenom, math.NewInt(500)),
	}
	_, err := dec.AnteHandle(f.Ctx, bondTx{msg: msg}, false, func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		return ctx, nil
	})
	require.NoError(t, err)
	left, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, left.Equal(math.NewInt(500)), "earnings stay put when the bank already covers the bond")
}

type bondTx struct{ msg sdk.Msg }

func (t bondTx) GetMsgs() []sdk.Msg                    { return []sdk.Msg{t.msg} }
func (t bondTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }
