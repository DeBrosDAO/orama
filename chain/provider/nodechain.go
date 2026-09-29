package provider

import (
	"context"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/client/tx"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// NodeChain is Chain over one oramad RPC, signing with the node's hot key.
type NodeChain struct {
	*node.Client
	hot tx.Account
}

// NewNodeChain pairs an RPC client with the hot key that signs for the node.
func NewNodeChain(client *node.Client, hot tx.Account) (*NodeChain, error) {
	if client == nil {
		return nil, errors.New("chain client is nil")
	}
	if hot.Address == "" {
		return nil, errors.New("hot key account is empty")
	}
	return &NodeChain{Client: client, hot: hot}, nil
}

// Params returns x/storage's parameters.
func (c *NodeChain) Params(ctx context.Context) (types.Params, error) {
	var resp types.QueryParamsResponse
	if err := c.Query(ctx, "/orama.storage.v1.Query/Params", &types.QueryParamsRequest{}, &resp); err != nil {
		return types.Params{}, err
	}
	return resp.Params, nil
}

// Slot returns one deal slot.
func (c *NodeChain) Slot(ctx context.Context, dealID uint64, slot uint32) (types.Slot, error) {
	var resp types.QuerySlotResponse
	if err := c.Query(ctx, "/orama.storage.v1.Query/Slot", &types.QuerySlotRequest{DealId: dealID, Slot: slot}, &resp); err != nil {
		return types.Slot{}, err
	}
	return resp.Slot, nil
}

// CurrentEpoch is x/emission's epoch, the one x/storage challenges in.
func (c *NodeChain) CurrentEpoch(ctx context.Context) (uint64, error) {
	var resp emissiontypes.QueryCurrentEpochResponse
	if err := c.Query(ctx, "/orama.emission.v1.Query/CurrentEpoch", &emissiontypes.QueryCurrentEpochRequest{}, &resp); err != nil {
		return 0, err
	}
	return resp.EpochState.CurrentEpoch, nil
}

// Challenges lists this node's challenges for epoch.
func (c *NodeChain) Challenges(ctx context.Context, epoch uint64, nodeID string) ([]types.Challenge, error) {
	var resp types.QueryChallengesResponse
	req := &types.QueryChallengesRequest{Epoch: epoch, NodeId: nodeID}
	if err := c.Query(ctx, "/orama.storage.v1.Query/Challenges", req, &resp); err != nil {
		return nil, err
	}
	return resp.Challenges, nil
}

// Submit signs msgs with the hot key and waits for inclusion.
func (c *NodeChain) Submit(ctx context.Context, msgs ...sdk.Msg) error {
	_, err := c.Client.Submit(ctx, c.hot, msgs...)
	return err
}
