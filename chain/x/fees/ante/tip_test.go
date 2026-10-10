package ante_test

import (
	"context"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	feesante "github.com/DeBrosOfficial/network/chain/x/fees/ante"
	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

type proposerStaking struct {
	operator sdk.ValAddress
	cons     sdk.ConsAddress
}

func (p proposerStaking) GetValidatorByConsAddr(_ context.Context, cons sdk.ConsAddress) (stakingtypes.Validator, error) {
	if !p.cons.Equals(cons) {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return stakingtypes.Validator{OperatorAddress: p.operator.String()}, nil
}

func TestFeeDecorator_tipReachesTheProposer(t *testing.T) {
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	transientKey := storetypes.NewTransientStoreKey("transient_tip_test")
	testCtx := testutil.DefaultContextWithDB(t, storeKey, transientKey)
	proposer := sdk.ValAddress("proposer_operator____")
	cons := sdk.ConsAddress("proposer_cons_addr__!")
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{
		Time:            time.Unix(1_700_000_000, 0),
		Height:          10,
		ProposerAddress: cons,
	})

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)
	bank := newFakeBankKeeper()
	feesKeeper := keeper.NewKeeper(cdc, runtime.NewKVStoreService(storeKey), bank)
	require.NoError(t, feesKeeper.InitGenesis(ctx, *types.DefaultGenesisState()))

	payer := sdk.AccAddress("tip_payer_account____")
	bank.fund(payer.String(), math.NewInt(1_500))

	decorator := feesante.NewFeeDecorator(fakeAccountKeeper{}, nil, proposerStaking{operator: proposer, cons: cons}, feesKeeper)
	tx := fakeFeeTx{
		fee:      sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(1_500))),
		gas:      1_000, // base fee is 1 norama per gas, so the tip is 500
		feePayer: payer,
	}
	_, err := decorator.AnteHandle(ctx, tx, false, func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		return ctx, nil
	})
	require.NoError(t, err)

	tip, err := feesKeeper.GetEarnings(ctx, sdk.AccAddress(proposer))
	require.NoError(t, err)
	require.True(t, tip.Equal(math.NewInt(500)), "tip = %s, want 500 in the proposer's earnings", tip)
	require.True(t, bank.burned.Equal(math.NewInt(1_000)))

	got, err := feesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, got.FeesBalance, got.Detail)
	require.True(t, got.EarningsMatchModule, got.Detail)
}
