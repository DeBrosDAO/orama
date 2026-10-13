package spikes_test

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"

	"github.com/cometbft/cometbft/crypto/merkle"
	"github.com/stretchr/testify/require"
)

// leafPreimage is the C11 leaf, fixed-width so the reconstruction cost is
// the hash plus the tree and not JSON parsing:
// asset_id(32) || owner(32) || delegate(32) || metadata_cid(32) || creator_hash(32) || nonce(8) || hash_id(1).
func leafPreimage(i int) []byte {
	buf := make([]byte, 32*5+8+1)
	binary.BigEndian.PutUint64(buf[32*5:], uint64(i))
	buf[len(buf)-1] = 1 // hash_id: SHA-256
	for field := 0; field < 5; field++ {
		binary.BigEndian.PutUint64(buf[field*32:], uint64(field+1))
		binary.BigEndian.PutUint64(buf[field*32+8:], uint64(i))
	}
	sum := sha256.Sum256(buf)
	return sum[:]
}

func rebuildLeaves(n int) ([][]byte, []byte) {
	leaves := make([][]byte, n)
	for i := 0; i < n; i++ {
		leaves[i] = leafPreimage(i)
	}
	return leaves, merkle.HashFromByteSlices(leaves)
}

func TestCNFTTreeRebuildsFromLeafBytes(t *testing.T) {
	const n = 4096
	start := time.Now()
	leaves, root := rebuildLeaves(n)
	elapsed := time.Since(start)
	require.Len(t, root, 32)

	again := merkle.HashFromByteSlices(leaves)
	require.Equal(t, root, again)

	provedRoot, proofs := merkle.ProofsFromByteSlices(leaves)
	require.Equal(t, root, provedRoot)
	require.NoError(t, proofs[0].Verify(root, leaves[0]))
	require.NoError(t, proofs[n-1].Verify(root, leaves[n-1]))

	tampered := append([]byte(nil), leaves[0]...)
	tampered[0] ^= 0xff
	require.Error(t, proofs[0].Verify(root, tampered))

	t.Logf("leaves=%d leaf_bytes=%d rebuild=%s root=%x", n, len(leaves[0]), elapsed, root)
}

func BenchmarkCNFTTreeRebuild(b *testing.B) {
	const n = 1_000_000
	for i := 0; i < b.N; i++ {
		_, root := rebuildLeaves(n)
		if len(root) != 32 {
			b.Fatalf("root=%d", len(root))
		}
	}
}
