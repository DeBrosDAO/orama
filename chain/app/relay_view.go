package app

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"

	"github.com/DeBrosOfficial/network/chain/app/params"
	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// relayBindingService is the service name of the node binding that holds a relay's Tor ed25519 identity.
const relayBindingService = "relay"

// relayService is x/relay's view of the C2 service split: x/storage's own split function and
// archive fund, and a burn out of the relay module account.
type relayService struct {
	storage storagekeeper.Keeper
	bank    bankkeeper.Keeper
}

func (r relayService) SplitServicePayment(amount math.Int) (math.Int, math.Int, math.Int) {
	return storagetypes.SplitServicePayment(amount)
}

func (r relayService) BurnService(ctx context.Context, senderModule string, amt math.Int) error {
	return r.bank.BurnCoins(ctx, senderModule, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amt)))
}

func (r relayService) FundArchive(ctx context.Context, senderModule string, amt math.Int) error {
	return r.storage.FundArchive(ctx, senderModule, amt)
}

// relayNodes is x/relay's view of a node. RelayBinding returns the node's ed25519 binding whose
// service is "relay": the Tor relay's ed25519 master identity, the key a directory authority's vote
// names the relay by. A node that does not hold the RELAY role, or that binds no such key, has no
// relay binding: there is no fallback to another ed25519 key of the node, because the validator's
// consensus key would never match what the votes carry and the relay would silently go unpaid.
// The third value is the node's effective network from x/nodes (the /16 derived
// from its literal-IP endpoints; "" when it has none or its identity is still
// inside network_identity_lock_seconds), which x/relay buckets for its per-/16 cap.
type relayNodes struct {
	nodes nodeskeeper.Keeper
}

// NodeLive reports whether a relay's node can still earn: a node that retired, was tombstoned or is
// jailed is paid nothing, so a relay stops earning the epoch its node leaves.
func (r relayNodes) NodeLive(ctx context.Context, nodeID string) (bool, error) {
	node, err := r.nodes.GetNode(sdk.UnwrapSDKContext(ctx), nodeID)
	if err != nil {
		return false, err
	}
	return node.Status == nodestypes.NodeStatusRegistered || node.Status == nodestypes.NodeStatusActive, nil
}

func (r relayNodes) RelayBinding(ctx context.Context, nodeID string) ([]byte, sdk.AccAddress, string, error) {
	node, err := r.nodes.GetNode(sdk.UnwrapSDKContext(ctx), nodeID)
	if err != nil {
		return nil, nil, "", err
	}
	if !nodestypes.HasRole(node.Roles, nodestypes.RoleRelay) {
		return nil, nil, "", fmt.Errorf("node %s does not have role ROLE_RELAY: a node's roles are fixed at registration", nodeID)
	}
	operator, err := sdk.AccAddressFromBech32(node.Operator)
	if err != nil {
		return nil, nil, "", err
	}
	network, _, err := r.nodes.EffectiveNetwork(sdk.UnwrapSDKContext(ctx), node)
	if err != nil {
		return nil, nil, "", fmt.Errorf("node %s network: %w", nodeID, err)
	}
	for _, b := range node.Bindings {
		if b.Service == relayBindingService && b.KeyType == nodestypes.KeyTypeEd25519 && len(b.Pubkey) == 32 {
			return append([]byte(nil), b.Pubkey...), operator, network, nil
		}
	}
	return nil, nil, "", fmt.Errorf("node %s binds no ed25519 key for service %q: bind the Tor relay's ed25519 identity with MsgUpdateNode first", nodeID, relayBindingService)
}
