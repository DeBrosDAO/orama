package app_test

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"cosmossdk.io/math"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

const (
	// benchDelegatorShares is the shares each benchmark delegation holds.
	benchDelegatorShares = int64(1_000_000)
	// benchEpochMint is the validator share of one epoch's mint the benchmark pays out, in norama.
	benchEpochMint = int64(1_000_000_000_000)
)

// BenchmarkEpochClosePayout pays one epoch's validator share to a single validator that has n
// delegators, through x/power's DistributeEpochRewards on the real x/staking, x/bank and x/fees
// stores: the work the epoch-close block does in BeginBlock. It reports the store gas and the
// wall-clock time of the payout; the chain's block interval is 5 s. One validator holds every
// delegator, which is the worst case for the O(delegations) loop of one validator.
func BenchmarkEpochClosePayout(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 50_000} {
		b.Run(fmt.Sprintf("delegators=%d", n), func(b *testing.B) {
			oramaApp := buildTestApp(b)
			genesisTime := time.Unix(1_700_000_000, 0)
			genState, keys := committeeGenesis(b, oramaApp, 1, 365)
			initChain(b, oramaApp, genState, 0, genesisTime)
			finalize(b, oramaApp, 1, genesisTime.Add(2*time.Second))

			ctx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
			valAddr := sdk.ValAddress(keys[0].account.PubKey().Address())
			validator, err := oramaApp.StakingKeeper.GetValidator(ctx, valAddr)
			require.NoError(b, err)
			added := math.LegacyNewDec(benchDelegatorShares).MulInt64(int64(n))
			for i := 0; i < n; i++ {
				raw := make([]byte, 20)
				binary.BigEndian.PutUint64(raw, uint64(i)+1)
				require.NoError(b, oramaApp.StakingKeeper.SetDelegation(ctx, stakingtypes.NewDelegation(
					sdk.AccAddress(raw).String(), valAddr.String(), math.LegacyNewDec(benchDelegatorShares))))
			}
			validator.DelegatorShares = validator.DelegatorShares.Add(added)
			validator.Tokens = validator.Tokens.Add(added.TruncateInt()) // an exchange rate of one share per token
			require.NoError(b, oramaApp.StakingKeeper.SetValidator(ctx, validator))

			b.ResetTimer()
			var gas storetypes.Gas
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cache, _ := ctx.CacheContext()
				cache = cache.WithGasMeter(storetypes.NewInfiniteGasMeter())
				require.NoError(b, oramaApp.BankKeeper.MintCoins(cache, emissiontypes.ModuleName,
					sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, benchEpochMint))))
				b.StartTimer()
				start := cache.GasMeter().GasConsumed()
				paid, err := oramaApp.PowerKeeper.DistributeEpochRewards(cache, oramaApp.EmissionKeeper, emissiontypes.ModuleName, math.NewInt(benchEpochMint))
				if err != nil {
					b.Fatal(err)
				}
				gas = cache.GasMeter().GasConsumed() - start
				if !paid.Equal(math.NewInt(benchEpochMint)) {
					b.Fatalf("paid %s of %d", paid, benchEpochMint)
				}
			}
			b.ReportMetric(float64(gas), "store-gas")
		})
	}
}
