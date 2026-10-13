package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/storage/keeper"
)

func TestSampleIsLinearInKAtOneMillion(t *testing.T) {
	const replicas = 1_000_000
	const kC = 8
	idxs, steps := keeper.SampleReplicaIndexes(replicas, kC, []byte("orama-sample"))
	require.Len(t, idxs, kC)
	require.Less(t, steps, 64, "sampling 8 of 1e6 replicas must not scan the set")
	seen := map[uint64]struct{}{}
	for _, idx := range idxs {
		require.Less(t, idx, uint64(replicas))
		_, dup := seen[idx]
		require.False(t, dup)
		seen[idx] = struct{}{}
	}
	again, _ := keeper.SampleReplicaIndexes(replicas, kC, []byte("orama-sample"))
	require.Equal(t, idxs, again)
}

func BenchmarkSample1M(b *testing.B) {
	seed := []byte("orama-sample")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idxs, steps := keeper.SampleReplicaIndexes(1_000_000, 8, seed)
		if len(idxs) != 8 || steps > 64 {
			b.Fatalf("idxs %d steps %d", len(idxs), steps)
		}
	}
}
