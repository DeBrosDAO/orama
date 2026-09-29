package app

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// storageNodes is the storage module's view of x/nodes. IsActive is the STORAGE
// role's own activity, not the node's: a node bonded only on another role is not a
// storage provider. Network16 is derived from the node's endpoints and ASN is the
// operator's declaration; neither is verified on chain (docs/CHAIN.md, "Node
// network identity").
type storageNodes struct {
	nodes nodeskeeper.Keeper
}

// refuseStorageNodeErr marks the errors x/nodes returns to say the one node asked about cannot serve the request
// (it is gone, or it is not active for the operation) as a failure of the item x/storage was
// processing. Every other x/nodes error is a fault of x/nodes' own state and is returned as is.
func refuseStorageNodeErr(err error) error {
	if errors.Is(err, nodestypes.ErrNotFound) || errors.Is(err, nodestypes.ErrNotActive) {
		return storagetypes.Refuse(err)
	}
	return err
}

func (s storageNodes) sdk(ctx context.Context) sdk.Context {
	return sdk.UnwrapSDKContext(ctx)
}

func (s storageNodes) IsActive(ctx context.Context, nodeID string) (bool, error) {
	ok, err := s.nodes.StorageEligible(s.sdk(ctx), nodeID)
	return ok, refuseStorageNodeErr(err)
}

func (s storageNodes) IsProbation(ctx context.Context, nodeID string) (bool, error) {
	ok, err := s.nodes.StorageProbation(s.sdk(ctx), nodeID)
	return ok, refuseStorageNodeErr(err)
}

func (s storageNodes) TakeStorageChanges(ctx context.Context) ([]string, error) {
	return s.nodes.TakeStorageChanges(s.sdk(ctx))
}

func (s storageNodes) MarkStorageChanged(ctx context.Context, nodeID string) error {
	return s.nodes.MarkStorageChanged(s.sdk(ctx), nodeID)
}

func (s storageNodes) HotKey(ctx context.Context, nodeID string) (sdk.AccAddress, error) {
	hot, err := s.nodes.HotKey(s.sdk(ctx), nodeID)
	if err != nil {
		return nil, refuseStorageNodeErr(err)
	}
	return sdk.AccAddressFromBech32(hot)
}

func (s storageNodes) Operator(ctx context.Context, nodeID string) (string, error) {
	node, err := s.nodes.GetNode(s.sdk(ctx), nodeID)
	if err != nil {
		return "", refuseStorageNodeErr(err)
	}
	if node.Operator == "" {
		return "", fmt.Errorf("node %s has no operator", nodeID)
	}
	return node.Operator, nil
}

func (s storageNodes) Network16(ctx context.Context, nodeID string) (string, error) {
	network, _, err := s.nodes.NodeNetwork(s.sdk(ctx), nodeID)
	return network, refuseStorageNodeErr(err)
}

func (s storageNodes) ASN(ctx context.Context, nodeID string) (uint32, error) {
	_, asn, err := s.nodes.NodeNetwork(s.sdk(ctx), nodeID)
	return asn, refuseStorageNodeErr(err)
}

func (s storageNodes) DeclaredCapacity(ctx context.Context, nodeID string) (uint64, error) {
	node, err := s.nodes.GetNode(s.sdk(ctx), nodeID)
	if err != nil {
		return 0, refuseStorageNodeErr(err)
	}
	return node.DeclaredCapacityBytes, nil
}

func (s storageNodes) Jail(ctx context.Context, nodeID string) error {
	return refuseStorageNodeErr(s.nodes.Jail(s.sdk(ctx), nodeID))
}

// Slash turns an absolute norama amount into a fraction of the STORAGE role
// bond and slashes that role. A node with no storage bond has nothing here to slash: x/storage
// penalizes a probation node through its record deposit, so this is a no-op for it.
func (s storageNodes) Slash(ctx context.Context, nodeID string, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return nil
	}
	node, err := s.nodes.GetNode(s.sdk(ctx), nodeID)
	if err != nil {
		return refuseStorageNodeErr(err)
	}
	var bond math.Int
	for _, b := range node.Bonds {
		if b.Role == nodestypes.RoleStorage && !b.Amount.IsNil() {
			bond = b.Amount
			break
		}
	}
	if bond.IsNil() || !bond.IsPositive() {
		return nil
	}
	fraction := math.LegacyNewDecFromInt(amount).Quo(math.LegacyNewDecFromInt(bond))
	if fraction.GT(math.LegacyOneDec()) {
		fraction = math.LegacyOneDec()
	}
	_, err = s.nodes.Slash(s.sdk(ctx), nodeID, nodestypes.RoleStorage, fraction)
	return refuseStorageNodeErr(err)
}
