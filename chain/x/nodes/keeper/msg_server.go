package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

type msgServer struct {
	Keeper
}

var _ types.MsgServer = msgServer{}

// NewMsgServerImpl returns x/nodes' MsgServer.
func NewMsgServerImpl(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) RegisterOperator(goCtx context.Context, msg *types.MsgRegisterOperator) (*types.MsgRegisterOperatorResponse, error) {
	if err := m.Keeper.RegisterOperator(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgRegisterOperatorResponse{}, nil
}

func (m msgServer) RegisterNode(goCtx context.Context, msg *types.MsgRegisterNode) (*types.MsgRegisterNodeResponse, error) {
	if err := m.Keeper.RegisterNode(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgRegisterNodeResponse{}, nil
}

func (m msgServer) UpdateNode(goCtx context.Context, msg *types.MsgUpdateNode) (*types.MsgUpdateNodeResponse, error) {
	if err := m.Keeper.UpdateNode(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgUpdateNodeResponse{}, nil
}

func (m msgServer) RetireNode(goCtx context.Context, msg *types.MsgRetireNode) (*types.MsgRetireNodeResponse, error) {
	if err := m.Keeper.RetireNode(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgRetireNodeResponse{}, nil
}

func (m msgServer) BondNode(goCtx context.Context, msg *types.MsgBondNode) (*types.MsgBondNodeResponse, error) {
	if err := m.Keeper.BondNode(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgBondNodeResponse{}, nil
}

func (m msgServer) UnbondNode(goCtx context.Context, msg *types.MsgUnbondNode) (*types.MsgUnbondNodeResponse, error) {
	if err := m.Keeper.UnbondNode(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgUnbondNodeResponse{}, nil
}

func (m msgServer) DeclareCapacity(goCtx context.Context, msg *types.MsgDeclareCapacity) (*types.MsgDeclareCapacityResponse, error) {
	if err := m.Keeper.DeclareCapacity(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgDeclareCapacityResponse{}, nil
}

func (m msgServer) RegisterCluster(goCtx context.Context, msg *types.MsgRegisterCluster) (*types.MsgRegisterClusterResponse, error) {
	if err := m.Keeper.RegisterCluster(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgRegisterClusterResponse{}, nil
}

func (m msgServer) UpdateCluster(goCtx context.Context, msg *types.MsgUpdateCluster) (*types.MsgUpdateClusterResponse, error) {
	if err := m.Keeper.UpdateCluster(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgUpdateClusterResponse{}, nil
}

func (m msgServer) RetireCluster(goCtx context.Context, msg *types.MsgRetireCluster) (*types.MsgRetireClusterResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("nil MsgRetireCluster")
	}
	if err := m.Keeper.RetireCluster(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgRetireClusterResponse{}, nil
}
