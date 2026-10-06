package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func (k Keeper) resyncCapacity(ctx sdk.Context, node *types.Node) error {
	if node.CapacityIndexed {
		key := collections.Join3(node.CapacityClass, node.Operator, node.NodeId)
		if err := k.FreeCapacity.Remove(ctx, key); err != nil {
			return fmt.Errorf("remove capacity index for node %s: %w", node.NodeId, err)
		}
		node.CapacityIndexed = false
		node.CapacityClass = 0
	}
	if node.ReservedCapacityBytes > node.DeclaredCapacityBytes {
		return fmt.Errorf("node %s reserved %d exceeds declared %d", node.NodeId, node.ReservedCapacityBytes, node.DeclaredCapacityBytes)
	}
	if !indexCapacity(node) {
		return nil
	}
	free := node.DeclaredCapacityBytes - node.ReservedCapacityBytes
	class := types.CapacityClass(free)
	key := collections.Join3(class, node.Operator, node.NodeId)
	if err := k.FreeCapacity.Set(ctx, key, free); err != nil {
		return fmt.Errorf("index capacity for node %s: %w", node.NodeId, err)
	}
	node.CapacityIndexed = true
	node.CapacityClass = class
	return nil
}

func indexCapacity(node *types.Node) bool {
	if !types.HasRole(node.Roles, types.RoleStorage) {
		return false
	}
	if node.DeclaredCapacityBytes <= node.ReservedCapacityBytes {
		return false
	}
	switch node.Status {
	case types.NodeStatusRegistered, types.NodeStatusActive:
		return true
	default:
		return false
	}
}

// ReserveCapacity reduces a STORAGE node's free capacity. The node must be
// registered or active. Later modules (x/storage) call this when a slot is assigned.
func (k Keeper) ReserveCapacity(ctx sdk.Context, nodeID string, bytes uint64) error {
	if bytes == 0 {
		return fmt.Errorf("reserve capacity of zero on node %s", nodeID)
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		node, err := k.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if node.Status != types.NodeStatusRegistered && node.Status != types.NodeStatusActive {
			return fmt.Errorf("node %s is %s and cannot reserve capacity", nodeID, node.Status)
		}
		if !types.HasRole(node.Roles, types.RoleStorage) {
			return fmt.Errorf("node %s does not have the STORAGE role", nodeID)
		}
		free := node.DeclaredCapacityBytes - node.ReservedCapacityBytes
		if bytes > free {
			return fmt.Errorf("reserve %d exceeds free capacity %d on node %s", bytes, free, nodeID)
		}
		node.ReservedCapacityBytes += bytes
		return k.saveNode(ctx, node)
	})
}

// ReleaseCapacity returns reserved bytes to the free-capacity index.
func (k Keeper) ReleaseCapacity(ctx sdk.Context, nodeID string, bytes uint64) error {
	if bytes == 0 {
		return fmt.Errorf("release capacity of zero on node %s", nodeID)
	}
	return k.transact(ctx, func(ctx sdk.Context) error {
		node, err := k.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if bytes > node.ReservedCapacityBytes {
			return fmt.Errorf("release %d exceeds reserved %d on node %s", bytes, node.ReservedCapacityBytes, nodeID)
		}
		node.ReservedCapacityBytes -= bytes
		return k.saveNode(ctx, node)
	})
}

// IterateStorageFreeCapacity walks one free-capacity class in operator, then
// node-id, order.
func (k Keeper) IterateStorageFreeCapacity(ctx sdk.Context, class uint32, fn func(operator, nodeID string, free uint64) error) error {
	err := k.FreeCapacity.Walk(ctx, collections.NewPrefixedTripleRange[uint32, string, string](class), func(key collections.Triple[uint32, string, string], free uint64) (bool, error) {
		if err := fn(key.K2(), key.K3(), free); err != nil {
			return true, err
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("walk capacity class %d: %w", class, err)
	}
	return nil
}

// IterateOperatorFreeCapacity walks one operator's nodes inside one class.
func (k Keeper) IterateOperatorFreeCapacity(ctx sdk.Context, class uint32, operator string, fn func(nodeID string, free uint64) error) error {
	addr, err := types.CanonicalAddress(operator)
	if err != nil {
		return err
	}
	err = k.FreeCapacity.Walk(ctx, collections.NewSuperPrefixedTripleRange[uint32, string, string](class, addr), func(key collections.Triple[uint32, string, string], free uint64) (bool, error) {
		if err := fn(key.K3(), free); err != nil {
			return true, err
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("walk capacity class %d operator %s: %w", class, addr, err)
	}
	return nil
}
