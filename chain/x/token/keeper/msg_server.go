package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

var _ types.MsgServer = msgServer{}

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of x/token's MsgServer interface.
func NewMsgServerImpl(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) CreateToken(goCtx context.Context, msg *types.MsgCreateToken) (*types.MsgCreateTokenResponse, error) {
	token, err := m.Keeper.CreateToken(sdk.UnwrapSDKContext(goCtx), msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgCreateTokenResponse{Denom: token.Denom}, nil
}

func (m msgServer) Mint(goCtx context.Context, msg *types.MsgMint) (*types.MsgMintResponse, error) {
	if err := m.Keeper.Mint(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgMintResponse{}, nil
}

func (m msgServer) Burn(goCtx context.Context, msg *types.MsgBurn) (*types.MsgBurnResponse, error) {
	if err := m.Keeper.Burn(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgBurnResponse{}, nil
}

func (m msgServer) Transfer(goCtx context.Context, msg *types.MsgTransfer) (*types.MsgTransferResponse, error) {
	if err := m.Keeper.Transfer(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgTransferResponse{}, nil
}

func (m msgServer) SetFrozen(goCtx context.Context, msg *types.MsgSetFrozen) (*types.MsgSetFrozenResponse, error) {
	if err := m.Keeper.SetFrozen(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgSetFrozenResponse{}, nil
}

func (m msgServer) SetPaused(goCtx context.Context, msg *types.MsgSetPaused) (*types.MsgSetPausedResponse, error) {
	if err := m.Keeper.SetPaused(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgSetPausedResponse{}, nil
}

func (m msgServer) Renounce(goCtx context.Context, msg *types.MsgRenounce) (*types.MsgRenounceResponse, error) {
	if err := m.Keeper.Renounce(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgRenounceResponse{}, nil
}

func (m msgServer) SetShieldable(goCtx context.Context, msg *types.MsgSetShieldable) (*types.MsgSetShieldableResponse, error) {
	if err := m.Keeper.SetShieldable(sdk.UnwrapSDKContext(goCtx), msg); err != nil {
		return nil, err
	}
	return &types.MsgSetShieldableResponse{}, nil
}

func (m msgServer) DeleteToken(goCtx context.Context, msg *types.MsgDeleteToken) (*types.MsgDeleteTokenResponse, error) {
	refund, burned, err := m.Keeper.DeleteToken(sdk.UnwrapSDKContext(goCtx), msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgDeleteTokenResponse{Refund: refund, Burned: burned}, nil
}
