package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

var _ types.MsgServer = msgServer{}

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns x/archive's Msg server.
func NewMsgServerImpl(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) Attest(goCtx context.Context, msg *types.MsgAttest) (*types.MsgAttestResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	archived, attesters, err := m.Keeper.Attest(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgAttestResponse{Archived: archived, Attesters: attesters}, nil
}

func (m msgServer) AttachReplicas(goCtx context.Context, msg *types.MsgAttachReplicas) (*types.MsgAttachReplicasResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("nil MsgAttachReplicas")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	archived, replicas, err := m.Keeper.AttachReplicas(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgAttachReplicasResponse{Archived: archived, Replicas: replicas}, nil
}
