package keeper

import (
	"context"
	"fmt"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// EventTypeBundle is emitted for every accepted bundle.
const EventTypeBundle = "shielded_bundle"

type msgServer struct{ Keeper }

// NewMsgServerImpl returns the module's Msg server. The message server is the authority: every
// message verifies its bundle here, whatever the ante handler already did.
func NewMsgServerImpl(k Keeper) types.MsgServer { return msgServer{k} }

func (m msgServer) emit(ctx sdk.Context, kind string, adm *Admitted) {
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeBundle,
		sdk.NewAttribute("kind", kind),
		sdk.NewAttribute("actions", fmt.Sprint(adm.Bundle.Actions)),
		sdk.NewAttribute("amount", adm.Amount.String()),
	))
}

func (m msgServer) ShieldedTransfer(goCtx context.Context, msg *types.MsgShieldedTransfer) (*types.MsgShieldedTransferResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}
	// A signer-less transfer declares exactly its bundle's gas, so its own state accesses run on an
	// unmetered context and the tx meter is charged the fixed schedule instead (Keeper.Verify).
	work := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	adm, err := m.Admit(work, msg.Bundle, nil, KindTransfer, false)
	if err != nil {
		return nil, err
	}
	if err := m.Verify(ctx, msg.Bundle, nil, adm); err != nil {
		return nil, err
	}
	if err := m.ExecuteTransfer(work, adm); err != nil {
		return nil, err
	}
	m.emit(ctx, "transfer", adm)
	return &types.MsgShieldedTransferResponse{}, nil
}

func (m msgServer) Shield(goCtx context.Context, msg *types.MsgShield) (*types.MsgShieldResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	signer, err := m.signed(msg.ValidateBasic(), msg.Signer)
	if err != nil {
		return nil, err
	}
	adm, err := m.Admit(ctx, msg.Bundle, nil, KindShield, true)
	if err != nil {
		return nil, err
	}
	if err := m.ExecuteShield(ctx, signer, adm); err != nil {
		return nil, err
	}
	m.emit(ctx, "shield", adm)
	return &types.MsgShieldResponse{}, nil
}

func (m msgServer) ShieldEarnings(goCtx context.Context, msg *types.MsgShieldEarnings) (*types.MsgShieldEarningsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	signer, err := m.signed(msg.ValidateBasic(), msg.Signer)
	if err != nil {
		return nil, err
	}
	adm, err := m.Admit(ctx, msg.Bundle, nil, KindShieldEarnings, true)
	if err != nil {
		return nil, err
	}
	if err := m.ExecuteShieldEarnings(ctx, signer, adm); err != nil {
		return nil, err
	}
	m.emit(ctx, "shield_earnings", adm)
	return &types.MsgShieldEarningsResponse{}, nil
}

func (m msgServer) Unshield(goCtx context.Context, msg *types.MsgUnshield) (*types.MsgUnshieldResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	signer, err := m.signed(msg.ValidateBasic(), msg.Signer)
	if err != nil {
		return nil, err
	}
	binding, err := msg.Binding()
	if err != nil {
		return nil, err
	}
	adm, err := m.Admit(ctx, msg.Bundle, binding, KindUnshield, true)
	if err != nil {
		return nil, err
	}
	queued, err := m.ExecuteUnshield(ctx, msg, signer, adm)
	if err != nil {
		return nil, err
	}
	m.emit(ctx, "unshield", adm)
	return &types.MsgUnshieldResponse{Queued: queued}, nil
}

// signed returns the signer's address once ValidateBasic has passed.
func (m msgServer) signed(validateErr error, signer string) (sdk.AccAddress, error) {
	if validateErr != nil {
		return nil, validateErr
	}
	return sdk.AccAddressFromBech32(signer)
}
