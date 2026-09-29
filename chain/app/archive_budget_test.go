package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/archiver"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// archiveCostPerEpoch simulates ranges being archived one after another for years epochs, at the
// default block interval, and returns what the ARCHIVE deals of all ranges archived so far cost in
// each epoch. Every range gets archivetypes.MaxLiveDealsPerRange deals of storagetypes.MinReplicas
// slots, each slot paid the protocol price per epoch, for archivetypes.ArchiveDealEpochs epochs.
func archiveCostPerEpoch(years int) []math.Int {
	const epochsPerYear = 365
	blocksPerEpoch := int64(24*60*60) / archivetypes.DefaultBlockIntervalSeconds
	price := storagetypes.DefaultParams().ProtocolPricePerEpoch
	slotsPerRange := int64(archivetypes.MaxLiveDealsPerRange) * int64(storagetypes.MinReplicas)

	epochs := years * epochsPerYear
	// archived[e] is how many ranges the chain has archived by the end of epoch e. A range is
	// archived once the tip is a full range past its end, and its deals are opened in the next
	// block, so an epoch's blocks close the ranges they completed.
	archived := make([]int64, epochs+1)
	cost := make([]math.Int, epochs+1)
	for e := 1; e <= epochs; e++ {
		archived[e] = int64(e) * blocksPerEpoch / archiver.DefaultRangeBlocks
		// A deal runs ArchiveDealEpochs epochs from the epoch its range closed; older ranges
		// are no longer paid until they are renewed.
		oldest := max(e-int(archivetypes.ArchiveDealEpochs), 0)
		running := archived[e] - archived[oldest]
		cost[e] = price.MulRaw(slotsPerRange).MulRaw(running)
	}
	return cost
}

// TestArchiveBudget_aYearOfRangesFitsTheStorageCeiling sizes the archive deals of the first year
// (and of the first ten, the length of one deal) against each epoch's storage ceiling from x/emission.
// The archive pays the same protocol price as any protocol deal, so the check is that the history the
// chain must keep never costs more than a small share of what it may mint for storage.
func TestArchiveBudget_aYearOfRangesFitsTheStorageCeiling(t *testing.T) {
	const maxShare = 1 // percent of the epoch's storage ceiling
	for _, years := range []int{1, 10} {
		cost := archiveCostPerEpoch(years)
		var peak math.Int = math.ZeroInt()
		for e := 1; e < len(cost); e++ {
			ceiling := emissiontypes.SplitEpochMint(emissiontypes.MaxMintableForEpoch(uint64(e))).Storage
			require.Truef(t, cost[e].MulRaw(100).LTE(ceiling.MulRaw(maxShare)),
				"epoch %d: archive deals cost %s norama, over %d%% of the %s norama storage ceiling", e, cost[e], maxShare, ceiling)
			if cost[e].GT(peak) {
				peak = cost[e]
			}
		}
		t.Logf("%d year(s): the archive costs at most %s norama in one epoch", years, peak)
	}
}

func TestArchiveBudget_costGrowsWithHistoryAndStopsWhenADealEnds(t *testing.T) {
	cost := archiveCostPerEpoch(1)
	require.True(t, cost[365].GT(cost[100]), "every new range adds its deals to the bill")
	require.True(t, cost[1].IsPositive() || cost[2].IsPositive(), "the first ranges are archived in the first epochs")

	// A deal that runs ArchiveDealEpochs epochs ends: the same model over a longer horizon stops
	// counting the oldest ranges, so the bill cannot grow past the number of ranges one deal covers.
	long := archiveCostPerEpoch(int(archivetypes.ArchiveDealEpochs/365) + 2)
	last := len(long) - 1
	require.True(t, long[last].LT(long[last-1].MulRaw(2)))
	price := storagetypes.DefaultParams().ProtocolPricePerEpoch
	rangesPerEpoch := int64(24*60*60) / archivetypes.DefaultBlockIntervalSeconds / archiver.DefaultRangeBlocks
	ceiling := price.MulRaw(int64(archivetypes.MaxLiveDealsPerRange) * int64(storagetypes.MinReplicas)).
		MulRaw(rangesPerEpoch + 1).MulRaw(int64(archivetypes.ArchiveDealEpochs))
	require.True(t, long[last].LTE(ceiling), "the bill is bounded by the ranges one deal duration covers")
}
