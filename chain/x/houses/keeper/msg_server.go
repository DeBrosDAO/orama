package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServer returns x/houses' Msg server.
func NewMsgServer(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) SubmitProposal(goCtx context.Context, msg *types.MsgSubmitProposal) (*types.MsgSubmitProposalResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty submit proposal")
	}
	proposer, err := sdk.AccAddressFromBech32(msg.Proposer)
	if err != nil {
		return nil, fmt.Errorf("proposer: %w", err)
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	id, err := m.Keeper.SubmitProposal(ctx, proposer, msg.Content)
	if err != nil {
		return nil, err
	}
	return &types.MsgSubmitProposalResponse{ProposalId: id}, nil
}

func (m msgServer) VoteToken(goCtx context.Context, msg *types.MsgVoteToken) (*types.MsgVoteTokenResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty token vote")
	}
	voter, err := sdk.AccAddressFromBech32(msg.Voter)
	if err != nil {
		return nil, fmt.Errorf("voter: %w", err)
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := m.Keeper.VoteToken(ctx, voter, msg.ProposalId, msg.Option); err != nil {
		return nil, err
	}
	return &types.MsgVoteTokenResponse{}, nil
}

func (m msgServer) VoteOperator(goCtx context.Context, msg *types.MsgVoteOperator) (*types.MsgVoteOperatorResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty operator vote")
	}
	voter, err := sdk.AccAddressFromBech32(msg.Voter)
	if err != nil {
		return nil, fmt.Errorf("voter: %w", err)
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	slashed, err := m.Keeper.VoteOperator(ctx, voter, msg.ProposalId, msg.Option)
	if err != nil {
		return nil, err
	}
	return &types.MsgVoteOperatorResponse{Slashed: slashed}, nil
}

func (m msgServer) LockHouseBond(goCtx context.Context, msg *types.MsgLockHouseBond) (*types.MsgLockHouseBondResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty lock")
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := m.Keeper.LockHouseBond(ctx, signer, msg.Amount); err != nil {
		return nil, err
	}
	return &types.MsgLockHouseBondResponse{}, nil
}

func (m msgServer) UnlockHouseBond(goCtx context.Context, msg *types.MsgUnlockHouseBond) (*types.MsgUnlockHouseBondResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty unlock")
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := m.Keeper.UnlockHouseBond(ctx, signer); err != nil {
		return nil, err
	}
	return &types.MsgUnlockHouseBondResponse{}, nil
}

func (m msgServer) ExecuteProposal(goCtx context.Context, msg *types.MsgExecuteProposal) (*types.MsgExecuteProposalResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty execute")
	}
	if _, err := sdk.AccAddressFromBech32(msg.Signer); err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := m.Keeper.ExecuteProposal(ctx, msg.ProposalId); err != nil {
		return nil, err
	}
	return &types.MsgExecuteProposalResponse{}, nil
}
