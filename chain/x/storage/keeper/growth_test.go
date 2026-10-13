package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// emptyBlockGasAfterFinishedDeals creates n PRIVATE deals that no node can hold (so each is refunded and
// finished), then returns the store gas one empty block (BeginBlock + EndBlock) consumes.
func emptyBlockGasAfterFinishedDeals(t *testing.T, n int) uint64 {
	f := newFixture(t)
	f.init(t, nil)
	client := acc(9)
	f.fund(client, 1_000_000_000)
	data := [][]byte{payload(1), payload(2), payload(3)}
	pieces := []types.PieceCommitment{commit(t, data[0]), commit(t, data[1]), commit(t, data[2])}
	for created := 0; created < n; {
		f.begin(t)
		for i := 0; i < 50 && created < n; i++ {
			f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1, 1, pieces)
			created++
		}
		f.end(t)
	}
	f.begin(t) // every deal is assigned (here: refunded) in this block
	f.end(t)
	f.Ctx = f.Ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	f.begin(t)
	f.end(t)
	return f.Ctx.GasMeter().GasConsumed()
}

// Finished (refunded or expired) deals and their slots are deleted once settled, and the expiry
// indexes let BeginBlock and EndBlock touch only what is due. An empty block must cost the same
// after 300 finished deals as after none.
func TestEmptyBlock_costDoesNotGrowWithFinishedDeals(t *testing.T) {
	empty := emptyBlockGasAfterFinishedDeals(t, 0)
	grown := emptyBlockGasAfterFinishedDeals(t, 300)
	t.Logf("store gas of one empty block: %d with no deals, %d after 300 finished deals", empty, grown)
	require.LessOrEqual(t, grown, empty+empty/10, "per-block storage work must not depend on how many deals are already finished")
}
