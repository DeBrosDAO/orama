package gen

import (
	"crypto/sha256"
	"fmt"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// proofSet computes, in one pass over the leaves, the full sibling path of every leaf of a cNFT
// tree of the given depth, and the tree's root. cnfttypes.Proof does the same for one leaf by
// rebuilding the whole tree, which is too slow for thousands of listings. Leaves past the end of the
// slice are empty.
func proofSet(leaves [][]byte, depth uint32) (siblings [][][]byte, root []byte, err error) {
	if depth < cnfttypes.MinDepth || depth > cnfttypes.MaxDepth {
		return nil, nil, fmt.Errorf("depth %d is out of range", depth)
	}
	if uint64(len(leaves)) > uint64(1)<<depth {
		return nil, nil, fmt.Errorf("%d leaves do not fit a tree of depth %d", len(leaves), depth)
	}
	empty := emptyLevels(depth)
	levels := make([][][]byte, depth+1)
	levels[0] = leaves
	for h := uint32(0); h < depth; h++ {
		cur := levels[h]
		next := make([][]byte, (len(cur)+1)/2)
		for i := range next {
			right := empty[h]
			if 2*i+1 < len(cur) {
				right = cur[2*i+1]
			}
			next[i] = hashPair(cur[2*i], right)
		}
		levels[h+1] = next
	}
	siblings = make([][][]byte, len(leaves))
	for idx := range leaves {
		siblings[idx] = pathOf(levels, empty, idx, depth)
	}
	root = empty[depth]
	if len(levels[depth]) == 1 {
		root = levels[depth][0]
	}
	return siblings, root, nil
}

func pathOf(levels [][][]byte, empty [][]byte, idx int, depth uint32) [][]byte {
	path := make([][]byte, depth)
	for h := uint32(0); h < depth; h++ {
		sib := idx ^ 1
		if sib < len(levels[h]) {
			path[h] = levels[h][sib]
		} else {
			path[h] = empty[h]
		}
		idx /= 2
	}
	return path
}

// emptyLevels[h] is the hash of an empty subtree of height h.
func emptyLevels(depth uint32) [][]byte {
	out := make([][]byte, depth+1)
	out[0] = make([]byte, cnfttypes.HashSize)
	for h := uint32(1); h <= depth; h++ {
		out[h] = hashPair(out[h-1], out[h-1])
	}
	return out
}

func hashPair(left, right []byte) []byte {
	buf := make([]byte, 0, 2*cnfttypes.HashSize)
	buf = append(append(buf, left...), right...)
	sum := sha256.Sum256(buf)
	return sum[:]
}

// depthFor is the smallest tree depth that holds n leaves, and at least the chain's minimum.
func depthFor(n int) uint32 {
	depth := cnfttypes.MinDepth
	for uint64(1)<<depth < uint64(n) && depth < cnfttypes.MaxDepth {
		depth++
	}
	return depth
}
