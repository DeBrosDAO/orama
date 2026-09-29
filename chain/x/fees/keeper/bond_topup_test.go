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
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
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

type multiBondTx struct{ msgs []sdk.Msg }

func (t multiBondTx) GetMsgs() []sdk.Msg                    { return t.msgs }
func (t multiBondTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

func runBondTopUp(t *testing.T, f *testFixture, tx sdk.Tx, simulate bool) error {
	t.Helper()
	dec := feesante.NewBondTopUpDecorator(f.Bank, f.Keeper)
	_, err := dec.AnteHandle(f.Ctx, tx, simulate, func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		return ctx, nil
	})
	return err
}

func bondNodeMsg(op sdk.AccAddress, id string, amt int64) *nodestypes.MsgBondNode {
	return &nodestypes.MsgBondNode{Operator: op.String(), NodeId: id, Role: nodestypes.RoleStorage, Amount: math.NewInt(amt)}
}

func requireEarnings(t *testing.T, f *testFixture, addr sdk.AccAddress, want int64) {
	t.Helper()
	got, err := f.Keeper.GetEarnings(f.Ctx, addr)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(want)), "earnings = %s, want %d", got, want)
	inv, err := f.Keeper.CheckInvariants(f.Ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func fundEarnings(t *testing.T, f *testFixture, addr sdk.AccAddress, bank, earnings int64) {
	t.Helper()
	if bank > 0 {
		f.Bank.fund(addr.String(), math.NewInt(bank))
	}
	f.Bank.fund(testSourceModule, math.NewInt(earnings))
	require.NoError(t, f.Keeper.CreditEarnings(f.Ctx, testSourceModule, addr, sdk.NewCoin(params.BaseDenom, math.NewInt(earnings))))
}

func TestBondTopUpDecorator_bondNodeFromEarningsWithZeroBank(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	op := sdk.AccAddress("node_operator_zero__")
	fundEarnings(t, f, op, 0, 1500)

	require.NoError(t, runBondTopUp(t, f, bondTx{msg: bondNodeMsg(op, "n1", 1000)}, false))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(math.NewInt(1000)))
	requireEarnings(t, f, op, 500)
}

func TestBondTopUpDecorator_bondNodePartialBankPlusEarnings(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	op := sdk.AccAddress("node_operator_part__")
	fundEarnings(t, f, op, 300, 1500)

	require.NoError(t, runBondTopUp(t, f, bondTx{msg: bondNodeMsg(op, "n1", 1000)}, false))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(math.NewInt(1000)), "only the 700 shortfall moves")
	requireEarnings(t, f, op, 800)
}

func TestBondTopUpDecorator_bondNodeInsufficientEarningsDebitsNothing(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	op := sdk.AccAddress("node_operator_poor__")
	fundEarnings(t, f, op, 100, 200)

	require.NoError(t, runBondTopUp(t, f, bondTx{msg: bondNodeMsg(op, "n1", 1000)}, false))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(math.NewInt(100)), "the bond message is left to fail on the short balance")
	requireEarnings(t, f, op, 200)
}

func TestBondTopUpDecorator_twoBondNodeMsgsInOneTxAreSummed(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	op := sdk.AccAddress("node_operator_two___")
	fundEarnings(t, f, op, 0, 5000)

	tx := multiBondTx{msgs: []sdk.Msg{bondNodeMsg(op, "n1", 1000), bondNodeMsg(op, "n1", 1500)}}
	require.NoError(t, runBondTopUp(t, f, tx, false))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(math.NewInt(2500)), "both messages must be funded, not just the first")
	requireEarnings(t, f, op, 2500)
}

func TestBondTopUpDecorator_bondNodeAndDelegateInOneTxAreSummed(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	op := sdk.AccAddress("node_operator_mixed_")
	fundEarnings(t, f, op, 400, 5000)

	tx := multiBondTx{msgs: []sdk.Msg{
		bondNodeMsg(op, "n1", 1000),
		&stakingtypes.MsgDelegate{DelegatorAddress: op.String(), ValidatorAddress: sdk.ValAddress(op).String(), Amount: sdk.NewCoin(params.BaseDenom, math.NewInt(600))},
	}}
	require.NoError(t, runBondTopUp(t, f, tx, false))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(math.NewInt(1600)))
	requireEarnings(t, f, op, 3800)
}

func TestBondTopUpDecorator_bondNodeNeverPullsAnotherOperatorsEarnings(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	rich := sdk.AccAddress("rich_operator______")
	poor := sdk.AccAddress("poor_operator______")
	fundEarnings(t, f, rich, 0, 5000)

	// The message names poor as the operator; only poor's own (empty) earnings are ever used.
	require.NoError(t, runBondTopUp(t, f, bondTx{msg: bondNodeMsg(poor, "n1", 1000)}, false))
	require.True(t, f.Bank.balanceOf(poor.String()).IsZero())
	require.True(t, f.Bank.balanceOf(rich.String()).IsZero())
	requireEarnings(t, f, rich, 5000)
}

func TestBondTopUpDecorator_bondNodeMalformedOperatorAndEmptyAmountAreIgnored(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	op := sdk.AccAddress("node_operator_edge__")
	fundEarnings(t, f, op, 0, 500)

	bad := &nodestypes.MsgBondNode{Operator: "not-an-address", NodeId: "n1", Role: nodestypes.RoleStorage, Amount: math.NewInt(10)}
	require.NoError(t, runBondTopUp(t, f, bondTx{msg: bad}, false))
	require.NoError(t, runBondTopUp(t, f, bondTx{msg: bondNodeMsg(op, "n1", 0)}, false))
	require.NoError(t, runBondTopUp(t, f, bondTx{msg: &nodestypes.MsgBondNode{Operator: op.String(), NodeId: "n1", Role: nodestypes.RoleStorage}}, false))
	requireEarnings(t, f, op, 500)
}

func TestBondTopUpDecorator_bondNodeSimulateFundsLikeARealRun(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	op := sdk.AccAddress("node_operator_sim___")
	fundEarnings(t, f, op, 0, 1500)

	require.NoError(t, runBondTopUp(t, f, bondTx{msg: bondNodeMsg(op, "n1", 1000)}, true))
	require.True(t, f.Bank.balanceOf(op.String()).Equal(math.NewInt(1000)))
}
