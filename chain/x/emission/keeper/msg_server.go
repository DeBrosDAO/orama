package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServer returns x/emission's Msg implementation.
func NewMsgServer(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

// Faucet mints msg.Amount norama to msg.Recipient on a non-production chain whose genesis enabled
// the faucet. Every refusal is a typed error (types/errors.go) returned before anything is
// minted. A successful drip is accounted in EpochState.CumulativeFaucetMinted, so the supply
// invariant holds and ReconcileBurns never mistakes it for anything else.
func (m msgServer) Faucet(goCtx context.Context, msg *types.MsgFaucet) (*types.MsgFaucetResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	p, err := m.faucetParams(ctx, msg.Amount)
	if err != nil {
		return nil, err
	}
	recipient, err := sdk.AccAddressFromBech32(msg.Recipient)
	if err != nil {
		return nil, errors.Wrapf(types.ErrFaucetRecipient, "invalid recipient %q: %s", msg.Recipient, err)
	}
	if m.bankKeeper.BlockedAddr(recipient) {
		return nil, errors.Wrapf(types.ErrFaucetRecipient, "%s is a module or blocked account", msg.Recipient)
	}
	if err := m.checkCooldown(ctx, p, recipient); err != nil {
		return nil, err
	}
	if err := m.reserveEpochCap(ctx, p, msg.Amount); err != nil {
		return nil, err
	}
	if err := m.payDrip(ctx, recipient, msg.Amount); err != nil {
		return nil, err
	}
	if err := m.FaucetDrips.Set(ctx, recipient, ctx.BlockTime().Unix()); err != nil {
		return nil, fmt.Errorf("failed to record faucet drip time for %s: %w", msg.Recipient, err)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeFaucet,
		sdk.NewAttribute(types.AttributeKeyRecipient, msg.Recipient),
		sdk.NewAttribute(types.AttributeKeyAmount, sdk.NewCoin(params.BaseDenom, msg.Amount).String()),
		sdk.NewAttribute(types.AttributeKeySigner, msg.Signer),
	))
	return &types.MsgFaucetResponse{Amount: msg.Amount}, nil
}

// faucetParams loads Params and applies the gates that need no per-recipient state: the chain-id
// (checked again here, not only at genesis, as defence in depth), faucet_enabled and the drip size.
func (k Keeper) faucetParams(ctx sdk.Context, amount math.Int) (types.Params, error) {
	if !isNonProductionChainID(ctx.ChainID()) {
		return types.Params{}, errors.Wrapf(types.ErrFaucetProduction, "chain-id %q", ctx.ChainID())
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return types.Params{}, fmt.Errorf("failed to get emission params: %w", err)
	}
	if !p.FaucetEnabled {
		return types.Params{}, types.ErrFaucetDisabled
	}
	if amount.IsNil() || !amount.IsPositive() || amount.GT(nonNilInt(p.FaucetMaxDrip)) {
		return types.Params{}, errors.Wrapf(types.ErrFaucetAmount, "got %v, faucet_max_drip is %s", amount, nonNilInt(p.FaucetMaxDrip))
	}
	return p, nil
}

// checkCooldown refuses a recipient that drew less than faucet_recipient_cooldown_seconds ago.
func (k Keeper) checkCooldown(ctx sdk.Context, p types.Params, recipient sdk.AccAddress) error {
	if p.FaucetRecipientCooldownSeconds == 0 {
		return nil
	}
	last, err := k.FaucetDrips.Get(ctx, recipient)
	if err != nil {
		if errors.IsOf(err, collections.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to read faucet drip time for %s: %w", recipient, err)
	}
	next := last + int64(p.FaucetRecipientCooldownSeconds)
	if now := ctx.BlockTime().Unix(); now < next {
		return errors.Wrapf(types.ErrFaucetCooldown, "%s drew at %d, next drip allowed at %d (now %d)", recipient, last, next, now)
	}
	return nil
}

// reserveEpochCap adds amount to the epoch's faucet total, or refuses when that would pass
// faucet_epoch_cap. The epoch total resets when the epoch closes (closeEpoch).
func (k Keeper) reserveEpochCap(ctx sdk.Context, p types.Params, amount math.Int) error {
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load emission epoch state: %w", err)
	}
	used := nonNilInt(state.FaucetEpochMinted)
	if used.Add(amount).GT(nonNilInt(p.FaucetEpochCap)) {
		return errors.Wrapf(types.ErrFaucetEpochCap, "epoch %d minted %s, drip %s, cap %s", state.CurrentEpoch, used, amount, p.FaucetEpochCap)
	}
	state.FaucetEpochMinted = used.Add(amount)
	state.CumulativeFaucetMinted = nonNilInt(state.CumulativeFaucetMinted).Add(amount)
	if err := k.EpochState.Set(ctx, state); err != nil {
		return fmt.Errorf("failed to record faucet mint of %s: %w", amount, err)
	}
	return nil
}

// payDrip mints amount into x/emission's module account and sends it to the recipient.
func (k Keeper) payDrip(ctx sdk.Context, recipient sdk.AccAddress, amount math.Int) error {
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := k.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
		return fmt.Errorf("failed to mint faucet drip of %s: %w", amount, err)
	}
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, recipient, coins); err != nil {
		return fmt.Errorf("failed to send faucet drip of %s to %s: %w", amount, recipient, err)
	}
	return nil
}
