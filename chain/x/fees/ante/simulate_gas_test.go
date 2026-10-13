package ante_test

import (
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

	"github.com/DeBrosOfficial/network/chain/app/params"
	feesante "github.com/DeBrosOfficial/network/chain/x/fees/ante"
	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// simulatedGasLimit is what a `--gas auto` client declares while it simulates: the gas is what the
// simulation is for, so it is not known yet.
const simulatedGasLimit = 0

// simulationGasTolerance is how far the simulated fee gas may be from the delivered fee gas.
const simulationGasTolerance = 0.05

// An operator whose only money is earnings pays its base fee from them. A client simulates with gas
// 0 and a nominal fee, so the whole fee used to count as a tip, which only a bank balance pays:
// settlement failed inside the simulation and its gas was never counted. The delivered transaction
// then ran out of gas (stagenet orama-stagenet-4, MsgRegisterOperator: 84850 wanted, 85803 used).
func TestFeeDecorator_simulationCountsTheGasOfPayingTheBaseFeeFromEarnings(t *testing.T) {
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	testCtx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_sim_test"))
	proposer := sdk.ValAddress("proposer_operator____")
	cons := sdk.ConsAddress("proposer_cons_addr__!")
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{Time: time.Unix(1_700_000_000, 0), Height: 10, ProposerAddress: cons})

	bank := newFakeBankKeeper()
	feesKeeper := keeper.NewKeeper(codec.NewProtoCodec(codectypes.NewInterfaceRegistry()), runtime.NewKVStoreService(storeKey), bank)
	require.NoError(t, feesKeeper.InitGenesis(ctx, *types.DefaultGenesisState()))
	payer := sdk.AccAddress("earnings_only_payer__")
	const earningsSource = "earnings_source"
	bank.fund(earningsSource, math.NewInt(1_000_000))
	require.NoError(t, feesKeeper.CreditEarnings(ctx, earningsSource, payer, sdk.NewCoin(params.BaseDenom, math.NewInt(1_000_000))))
	decorator := feesante.NewFeeDecorator(fakeAccountKeeper{}, nil, proposerStaking{operator: proposer, cons: cons}, feesKeeper)
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	gasOf := func(tx fakeFeeTx, simulate bool) uint64 {
		run, _ := ctx.CacheContext()
		run = run.WithGasMeter(storetypes.NewInfiniteGasMeter())
		_, err := decorator.AnteHandle(run, tx, simulate, next)
		require.NoError(t, err)
		return run.GasMeter().GasConsumed()
	}
	delivered := gasOf(fakeFeeTx{fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 90_000)), gas: 90_000, feePayer: payer}, false)
	// A nominal fee (chain/client/node) and an empty one (a typical `--gas auto` request) alike.
	for _, fee := range []sdk.Coins{sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)), sdk.NewCoins()} {
		simulated := gasOf(fakeFeeTx{fee: fee, gas: simulatedGasLimit, feePayer: payer}, true)
		// A client scales the estimate by its own margin (chain/client/node: 1.5x), which covers a
		// small difference, never a missing settlement path.
		require.InEpsilon(t, float64(delivered), float64(simulated), simulationGasTolerance,
			"fee %q: the simulation counted %d gas for the fee, delivery uses %d", fee, simulated, delivered)
	}
}
