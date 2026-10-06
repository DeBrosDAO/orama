package app

import (
	"context"

	"cosmossdk.io/math"

	"github.com/cosmos/gogoproto/grpc"
	googlegrpc "google.golang.org/grpc"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// earningsFunder is the x/fees call the bond-funding message handlers make: it tops up an
// address's bank balance from that address's own earnings, up to a needed amount.
type earningsFunder interface {
	FundSpendFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, needed math.Int) error
}

// earningsFundedStaking funds a signer's own bond shortfall from their earnings while
// MsgCreateValidator or MsgDelegate executes (security review B8: outsiders can never bond
// otherwise, since this chain starts every account at zero and pays only into earnings).
//
// It runs inside the message handler, not in the ante chain, on purpose: BaseApp writes ante state
// even when the message then fails, so an ante top-up would turn earnings into a spendable bank
// balance for a message that never bonds anything (a delegation to a missing validator, say). A
// handler runs in the message's own cache branch, which BaseApp discards when the message fails, so
// the top-up is reversed with everything else the message did and needs no acceptance predicate of
// its own: the staking handler decides. Several bond messages in one transaction are topped up one
// after another, each against the balance the earlier ones left.
type earningsFundedStaking struct {
	stakingtypes.MsgServer
	funder earningsFunder
}

// fund tops addr up from its earnings for amount. Earnings are norama only, so a bond in any other
// denom is passed to the staking handler untouched, which refuses it.
func (s earningsFundedStaking) fund(ctx context.Context, addr sdk.AccAddress, amount sdk.Coin) error {
	if amount.Denom != params.BaseDenom {
		return nil
	}
	return s.funder.FundSpendFromEarnings(ctx, addr, amount.Denom, amount.Amount)
}

func (s earningsFundedStaking) CreateValidator(ctx context.Context, msg *stakingtypes.MsgCreateValidator) (*stakingtypes.MsgCreateValidatorResponse, error) {
	// A malformed address is left for the staking handler to reject.
	if valAddr, err := sdk.ValAddressFromBech32(msg.ValidatorAddress); err == nil {
		if err := s.fund(ctx, sdk.AccAddress(valAddr), msg.Value); err != nil {
			return nil, err
		}
	}
	return s.MsgServer.CreateValidator(ctx, msg)
}

func (s earningsFundedStaking) Delegate(ctx context.Context, msg *stakingtypes.MsgDelegate) (*stakingtypes.MsgDelegateResponse, error) {
	if delegator, err := sdk.AccAddressFromBech32(msg.DelegatorAddress); err == nil {
		if err := s.fund(ctx, delegator, msg.Amount); err != nil {
			return nil, err
		}
	}
	return s.MsgServer.Delegate(ctx, msg)
}

// earningsFundedConfigurator hands a module a Msg registrar that wraps x/staking's Msg server.
type earningsFundedConfigurator struct {
	module.Configurator
	funder earningsFunder
}

func (c earningsFundedConfigurator) MsgServer() grpc.Server {
	return earningsFundedRegistrar{Server: c.Configurator.MsgServer(), funder: c.funder}
}

type earningsFundedRegistrar struct {
	grpc.Server
	funder earningsFunder
}

func (r earningsFundedRegistrar) RegisterService(sd *googlegrpc.ServiceDesc, ss any) {
	if staking, ok := ss.(stakingtypes.MsgServer); ok {
		ss = earningsFundedStaking{MsgServer: staking, funder: r.funder}
	}
	r.Server.RegisterService(sd, ss)
}
