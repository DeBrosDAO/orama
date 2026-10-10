package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

var _ types.MsgServer = msgServer{}

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of x/relay's MsgServer.
func NewMsgServerImpl(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) RegisterRelay(goCtx context.Context, msg *types.MsgRegisterRelay) (*types.MsgRegisterRelayResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("register relay: nil message")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := m.Keeper.registerRelay(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgRegisterRelayResponse{}, nil
}

func (m msgServer) ReportEpoch(goCtx context.Context, msg *types.MsgReportEpoch) (*types.MsgReportEpochResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("report epoch: nil message")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	complete, err := m.Keeper.reportEpoch(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgReportEpochResponse{Complete: complete}, nil
}

func (m msgServer) UpdateReporters(goCtx context.Context, msg *types.MsgUpdateReporters) (*types.MsgUpdateReportersResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("update reporters: nil message")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := m.Keeper.updateReporters(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgUpdateReportersResponse{}, nil
}
