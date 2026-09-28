package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// ValidateTreeShape checks the depth, changelog buffer and canopy of a tree.
// The canopy is strictly shallower than the tree, so leaves themselves are not
// cached. The buffer may be any size from 1 to MaxBuffer; it is not required
// to be a power of two.
func ValidateTreeShape(depth, buffer, canopy uint32) error {
	if depth < MinDepth || depth > MaxDepth {
		return fmt.Errorf("tree depth must be in [%d, %d], got %d", MinDepth, MaxDepth, depth)
	}
	if buffer < 1 || buffer > MaxBuffer {
		return fmt.Errorf("tree buffer must be in [1, %d], got %d", MaxBuffer, buffer)
	}
	if canopy > MaxCanopy || canopy >= depth {
		return fmt.Errorf("tree canopy must be less than depth %d and at most %d, got %d", depth, MaxCanopy, canopy)
	}
	return nil
}

// CanopyNodes is the number of cached nodes for a canopy of the given depth:
// every node in the top `canopy` levels below the root, which is 2^(canopy+1)-2.
func CanopyNodes(canopy uint32) uint64 {
	if canopy == 0 {
		return 0
	}
	return (uint64(1) << (canopy + 1)) - 2
}

// TreeStateBytes is the byte model TreeDeposit prices: a fixed header, one
// changelog slot per buffer entry, the rightmost path, and the canopy. It is
// not a measurement of the protobuf encoding.
func TreeStateBytes(depth, buffer, canopy uint32) (uint64, error) {
	if err := ValidateTreeShape(depth, buffer, canopy); err != nil {
		return 0, err
	}
	entry := uint64(nodeBytes) + uint64(depth)*uint64(nodeBytes) + uint64(indexBytes)
	rightmost := uint64(nodeBytes) + uint64(depth)*uint64(nodeBytes) + uint64(indexBytes)
	return uint64(treeHeaderBytes) + uint64(buffer)*entry + rightmost + CanopyNodes(canopy)*uint64(nodeBytes), nil
}

// TreeDeposit is the norama locked for a tree. One norama is locked per byte of
// TreeStateBytes, so the deposit is a pure function of depth, buffer and canopy.
func TreeDeposit(depth, buffer, canopy uint32) (math.Int, error) {
	n, err := TreeStateBytes(depth, buffer, canopy)
	if err != nil {
		return math.Int{}, err
	}
	return math.NewIntFromUint64(n), nil
}

// TreeDepositID is the x/fees deposit id for a tree.
func TreeDepositID(treeID uint64) string {
	return fmt.Sprintf("cnft/tree/%d", treeID)
}
