package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServer returns x/storage's message server.
func NewMsgServer(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) CreateDeal(goCtx context.Context, msg *types.MsgCreateDeal) (*types.MsgCreateDealResponse, error) {
	id, err := m.Keeper.CreateDeal(sdk.UnwrapSDKContext(goCtx), msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgCreateDealResponse{DealId: id}, nil
}

func (m msgServer) ExtendDeal(goCtx context.Context, msg *types.MsgExtendDeal) (*types.MsgExtendDealResponse, error) {
	if err := m.Keeper.ExtendDeal(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgExtendDealResponse{}, nil
}

func (m msgServer) GrantDealAuthorization(goCtx context.Context, msg *types.MsgGrantDealAuthorization) (*types.MsgGrantDealAuthorizationResponse, error) {
	if err := m.Keeper.GrantDealAuthorization(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgGrantDealAuthorizationResponse{}, nil
}

func (m msgServer) RevokeDealAuthorization(goCtx context.Context, msg *types.MsgRevokeDealAuthorization) (*types.MsgRevokeDealAuthorizationResponse, error) {
	if err := m.Keeper.RevokeDealAuthorization(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgRevokeDealAuthorizationResponse{}, nil
}

func (m msgServer) AcceptDeal(goCtx context.Context, msg *types.MsgAcceptDeal) (*types.MsgAcceptDealResponse, error) {
	if err := m.Keeper.AcceptDeal(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgAcceptDealResponse{}, nil
}

func (m msgServer) DeclineDeal(goCtx context.Context, msg *types.MsgDeclineDeal) (*types.MsgDeclineDealResponse, error) {
	if err := m.Keeper.DeclineDeal(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgDeclineDealResponse{}, nil
}

func (m msgServer) SubmitProofs(goCtx context.Context, msg *types.MsgSubmitProofs) (*types.MsgSubmitProofsResponse, error) {
	if err := m.Keeper.SubmitProofs(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgSubmitProofsResponse{}, nil
}

func (m msgServer) ReleaseReplica(goCtx context.Context, msg *types.MsgReleaseReplica) (*types.MsgReleaseReplicaResponse, error) {
	if err := m.Keeper.ReleaseReplica(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgReleaseReplicaResponse{}, nil
}
