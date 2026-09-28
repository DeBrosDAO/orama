package app

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// storageNodes is the storage module's view of x/nodes. Network16 and ASN are
// empty: a node record does not store them, so a protocol deal that requires
// distinct networks cannot be placed.
type storageNodes struct {
	nodes nodeskeeper.Keeper
}

func (s storageNodes) sdk(ctx context.Context) sdk.Context {
	return sdk.UnwrapSDKContext(ctx)
}

func (s storageNodes) IsActive(ctx context.Context, nodeID string) (bool, error) {
	return s.nodes.IsActive(s.sdk(ctx), nodeID)
}

func (s storageNodes) HotKey(ctx context.Context, nodeID string) (sdk.AccAddress, error) {
	hot, err := s.nodes.HotKey(s.sdk(ctx), nodeID)
	if err != nil {
		return nil, err
	}
	return sdk.AccAddressFromBech32(hot)
}

func (s storageNodes) Operator(ctx context.Context, nodeID string) (string, error) {
	node, err := s.nodes.GetNode(s.sdk(ctx), nodeID)
	if err != nil {
		return "", err
	}
	if node.Operator == "" {
		return "", fmt.Errorf("node %s has no operator", nodeID)
	}
	return node.Operator, nil
}

func (s storageNodes) Network16(context.Context, string) (string, error) { return "", nil }

func (s storageNodes) ASN(context.Context, string) (uint32, error) { return 0, nil }

func (s storageNodes) DeclaredCapacity(ctx context.Context, nodeID string) (uint64, error) {
	node, err := s.nodes.GetNode(s.sdk(ctx), nodeID)
	if err != nil {
		return 0, err
	}
	return node.DeclaredCapacityBytes, nil
}

func (s storageNodes) Jail(ctx context.Context, nodeID string) error {
	return s.nodes.Jail(s.sdk(ctx), nodeID)
}

// Slash turns an absolute norama amount into a fraction of the STORAGE role
// bond and slashes that role. A node with no storage bond is not slashed.
func (s storageNodes) Slash(ctx context.Context, nodeID string, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return nil
	}
	node, err := s.nodes.GetNode(s.sdk(ctx), nodeID)
	if err != nil {
		return err
	}
	var bond math.Int
	for _, b := range node.Bonds {
		if b.Role == nodestypes.RoleStorage && !b.Amount.IsNil() {
			bond = b.Amount
			break
		}
	}
	if bond.IsNil() || !bond.IsPositive() {
		return fmt.Errorf("node %s has no storage bond to slash", nodeID)
	}
	fraction := math.LegacyNewDecFromInt(amount).Quo(math.LegacyNewDecFromInt(bond))
	if fraction.GT(math.LegacyOneDec()) {
		fraction = math.LegacyOneDec()
	}
	_, err = s.nodes.Slash(s.sdk(ctx), nodeID, nodestypes.RoleStorage, fraction)
	return err
}
