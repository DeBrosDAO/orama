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

// relayNodes is x/relay's view of a node. It returns the ed25519 binding whose
// service is "relay", or the first ed25519 binding if that name is absent.
// The third value is the node's effective network from x/nodes (the /16 derived
// from its literal-IP endpoints; "" when it has none or its identity is still
// inside network_identity_lock_seconds), which x/relay buckets for its per-/16 cap.
type relayNodes struct {
	nodes nodeskeeper.Keeper
}

func (r relayNodes) RelayBinding(ctx context.Context, nodeID string) ([]byte, sdk.AccAddress, string, error) {
	node, err := r.nodes.GetNode(sdk.UnwrapSDKContext(ctx), nodeID)
	if err != nil {
		return nil, nil, "", err
	}
	operator, err := sdk.AccAddressFromBech32(node.Operator)
	if err != nil {
		return nil, nil, "", err
	}
	network, _, err := r.nodes.EffectiveNetwork(sdk.UnwrapSDKContext(ctx), node)
	if err != nil {
		return nil, nil, "", fmt.Errorf("node %s network: %w", nodeID, err)
	}
	var pub []byte
	for _, b := range node.Bindings {
		if b.KeyType != nodestypes.KeyTypeEd25519 || len(b.Pubkey) != 32 {
			continue
		}
		if b.Service == "relay" {
			return append([]byte(nil), b.Pubkey...), operator, network, nil
		}
		if pub == nil {
			pub = b.Pubkey
		}
	}
	if pub == nil {
		return nil, nil, "", fmt.Errorf("node %s has no ed25519 relay binding", nodeID)
	}
	return append([]byte(nil), pub...), operator, network, nil
}
