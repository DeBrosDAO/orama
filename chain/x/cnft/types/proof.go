package types

import "fmt"

// Proof builds a full sibling path and root for index from an ordered leaf list.
// Leaves past the end of the slice, and entries that are not HashSize bytes, are
// empty. This is the off-chain reconstruction used to check the on-chain tree:
// it does not read events or stored leaf bytes.
func Proof(leaves [][]byte, index int, depth uint32) ([][]byte, []byte, error) {
	if depth < MinDepth || depth > MaxDepth {
		return nil, nil, fmt.Errorf("depth %d is out of range", depth)
	}
	size := int(uint32(1) << depth)
	if index < 0 || index >= size {
		return nil, nil, fmt.Errorf("index %d is out of range for depth %d", index, depth)
	}
	level := make([][]byte, size)
	for i := 0; i < size; i++ {
		if i < len(leaves) && len(leaves[i]) == HashSize {
			level[i] = leaves[i]
		} else {
			level[i] = emptyNode(0)
		}
	}
	siblings := make([][]byte, depth)
	idx := index
	for h := uint32(0); h < depth; h++ {
		if idx%2 == 0 {
			siblings[h] = append([]byte(nil), level[idx+1]...)
		} else {
			siblings[h] = append([]byte(nil), level[idx-1]...)
		}
		next := make([][]byte, len(level)/2)
		for i := 0; i < len(next); i++ {
			next[i] = hashPair(level[2*i], level[2*i+1])
		}
		level = next
		idx /= 2
	}
	return siblings, append([]byte(nil), level[0]...), nil
}

// FullRoot is the Merkle root of leaves in a tree of the given depth.
func FullRoot(leaves [][]byte, depth uint32) ([]byte, error) {
	_, root, err := Proof(leaves, 0, depth)
	return root, err
}
