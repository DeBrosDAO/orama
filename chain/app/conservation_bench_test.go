package app_test

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// BenchmarkConservationFull times the full conservation pass against a state with many funded
// accounts, to size the stall of the one block that runs it (conservation.go): the pass iterates every
// balance once, so the time is linear in the account count.
func BenchmarkConservationFull(b *testing.B) {
	for _, accounts := range []int{1_000, 10_000, 50_000} {
		b.Run(fmt.Sprintf("accounts=%d", accounts), func(b *testing.B) {
			genesis := time.Unix(1_700_000_000, 0)
			oramaApp := buildTestApp(b)
			genState, _ := committeeGenesis(b, oramaApp, 1, 365)
			initChain(b, oramaApp, genState, simMaxGas, genesis)
			finalize(b, oramaApp, 1, genesis.Add(2*time.Second))

			ctx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesis.Add(4 * time.Second), ChainID: testChainID})
			one := sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1))
			for i := 0; i < accounts; i++ {
				var addr [20]byte
				binary.BigEndian.PutUint64(addr[:], uint64(i)+1)
				require.NoError(b, mintNoramaForTest(oramaApp, ctx, one))
				require.NoError(b, oramaApp.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, sdk.AccAddress(addr[:]), one))
			}
			writeCache(b, ctx)
			_, err := oramaApp.Commit()
			require.NoError(b, err)

			check := oramaApp.NewContext(true)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				require.NoError(b, oramaApp.CheckConservationFull(check))
			}
		})
	}
}
