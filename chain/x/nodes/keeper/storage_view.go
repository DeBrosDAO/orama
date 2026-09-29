package keeper

import (
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// noteStorageChange queues a STORAGE-role node for x/storage to reconcile.
// Every write to a node goes through saveNode, so a bond, unbond, jail,
// retire, slash, capacity or endpoint change is always seen. x/storage drains
// the queue at the start of its BeginBlock (TakeStorageChanges), so the queue
// holds only what changed since then and never a full node scan.
func (k Keeper) noteStorageChange(ctx sdk.Context, node types.Node) error {
	if !types.HasRole(node.Roles, types.RoleStorage) {
		return nil
	}
	if err := k.StorageDirty.Set(ctx, node.NodeId); err != nil {
		return fmt.Errorf("queue node %s for storage reconciliation: %w", node.NodeId, err)
	}
	return nil
}

// MarkStorageChanged re-queues a node id for x/storage. x/storage uses it for
// a node that no longer qualifies but still holds replicas, so the next
// reconciliation retries once they are gone.
func (k Keeper) MarkStorageChanged(ctx sdk.Context, nodeID string) error {
	if err := k.StorageDirty.Set(ctx, nodeID); err != nil {
		return fmt.Errorf("queue node %s for storage reconciliation: %w", nodeID, err)
	}
	return nil
}

// TakeStorageChanges returns, in node id order, the ids of STORAGE-role nodes
// written since the last call, and clears the queue.
func (k Keeper) TakeStorageChanges(ctx sdk.Context) ([]string, error) {
	var ids []string
	if err := k.StorageDirty.Walk(ctx, nil, func(id string) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("walk storage change queue: %w", err)
	}
	for _, id := range ids {
		if err := k.StorageDirty.Remove(ctx, id); err != nil {
			return nil, fmt.Errorf("clear storage change queue entry %s: %w", id, err)
		}
	}
	return ids, nil
}

// StorageEligible reports whether a node may hold storage slots: it has the
// STORAGE role, that role is bonded at min_bond, and the node is not jailed,
// retired or tombstoned. A node that does not exist is not eligible.
func (k Keeper) StorageEligible(ctx sdk.Context, nodeID string) (bool, error) {
	ok, err := k.IsRoleActive(ctx, nodeID, types.RoleStorage)
	if errors.Is(err, types.ErrNotFound) {
		return false, nil
	}
	return ok, err
}

// StorageProbation reports whether a node is a fee-free probation registration for the STORAGE
// role (C2, C7): it has the role, has posted no STORAGE bond, and is Registered, not jailed,
// retired or tombstoned. Such a node cannot be StorageEligible, which needs the bond; x/storage
// tracks it separately, with a small capped capacity and only protocol-deal slots. A node with a
// bond below min_bond is not a probation node: it chose to bond and has not finished. A node that
// does not exist is not one either.
func (k Keeper) StorageProbation(ctx sdk.Context, nodeID string) (bool, error) {
	node, err := k.GetNode(ctx, nodeID)
	if errors.Is(err, types.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if node.Status != types.NodeStatusRegistered || !types.HasRole(node.Roles, types.RoleStorage) {
		return false, nil
	}
	return bondOf(node, types.RoleStorage).IsZero(), nil
}

// NodeNetwork returns the node's network group (the /16 derived from its
// endpoints, "" when none carries a literal IP) and its declared ASN (0 when
// undeclared). Both are operator declarations: see docs/CHAIN.md.
func (k Keeper) NodeNetwork(ctx sdk.Context, nodeID string) (string, uint32, error) {
	node, err := k.GetNode(ctx, nodeID)
	if err != nil {
		return "", 0, err
	}
	return types.NetworkOf(node.Endpoints), node.Asn, nil
}
