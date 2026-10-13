package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

type msgServer struct {
	Keeper
}

var _ types.MsgServer = msgServer{}

// NewMsgServerImpl returns x/fees' MsgServer.
func NewMsgServerImpl(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

// WithdrawEarnings moves the signer's own earnings to the signer's own bank balance.
func (m msgServer) WithdrawEarnings(goCtx context.Context, msg *types.MsgWithdrawEarnings) (*types.MsgWithdrawEarningsResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("nil MsgWithdrawEarnings")
	}
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("withdraw earnings: signer %q: %w", msg.Signer, err)
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := m.Keeper.WithdrawEarnings(ctx, signer, msg.Amount); err != nil {
		return nil, fmt.Errorf("withdraw earnings: %w", err)
	}
	remaining, err := m.Keeper.GetEarnings(ctx, signer)
	if err != nil {
		return nil, err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent(types.EventTypeWithdrawEarnings,
		sdk.NewAttribute(types.AttributeSigner, msg.Signer),
		sdk.NewAttribute(types.AttributeAmount, msg.Amount.String()),
		sdk.NewAttribute(types.AttributeRemaining, remaining.String()),
	))
	return &types.MsgWithdrawEarningsResponse{}, nil
}
