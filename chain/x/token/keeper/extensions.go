package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// SetFrozen freezes or unfreezes an account. The freeze capability must still
// be held by the creator. Renouncing freeze does not clear existing freezes.
func (k Keeper) SetFrozen(ctx sdk.Context, msg *types.MsgSetFrozen) error {
	if msg == nil {
		return fmt.Errorf("nil set frozen message")
	}
	sender, err := parseAcc(msg.Sender, "sender")
	if err != nil {
		return err
	}
	account, err := parseAcc(msg.Account, "account")
	if err != nil {
		return err
	}
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return err
	}
	if !token.Extensions.Freeze || token.Creator != sender.String() {
		return fmt.Errorf("freeze authority for %s is not held by %s", token.Denom, sender)
	}
	key := collections.Join(token.Denom, account.String())
	if msg.Frozen {
		if err := k.Frozen.Set(ctx, key); err != nil {
			return fmt.Errorf("failed to freeze %s on %s: %w", account, token.Denom, err)
		}
		return nil
	}
	if err := k.Frozen.Remove(ctx, key); err != nil {
		return fmt.Errorf("failed to unfreeze %s on %s: %w", account, token.Denom, err)
	}
	return nil
}

// SetPaused pauses or unpauses transfers. Mint and burn are not paused.
// The pause capability must still be held by the creator. Renouncing pause
// does not clear an already-paused flag.
func (k Keeper) SetPaused(ctx sdk.Context, msg *types.MsgSetPaused) error {
	if msg == nil {
		return fmt.Errorf("nil set paused message")
	}
	sender, err := parseAcc(msg.Sender, "sender")
	if err != nil {
		return err
	}
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return err
	}
	if !token.Extensions.Pause || token.Creator != sender.String() {
		return fmt.Errorf("pause authority for %s is not held by %s", token.Denom, sender)
	}
	if token.Paused == msg.Paused {
		return nil
	}
	token.Paused = msg.Paused
	return k.storeToken(ctx, token)
}

// Renounce drops one capability. The creator signs every renounce except
// permanent delegate, which only that delegate can renounce. A dropped
// capability cannot be set again.
func (k Keeper) Renounce(ctx sdk.Context, msg *types.MsgRenounce) error {
	if msg == nil {
		return fmt.Errorf("nil renounce message")
	}
	sender, err := parseAcc(msg.Sender, "sender")
	if err != nil {
		return err
	}
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return err
	}
	if msg.Extension == types.EXTENSION_PERMANENT_DELEGATE {
		if token.Extensions.PermanentDelegate == "" || token.Extensions.PermanentDelegate != sender.String() {
			return fmt.Errorf("%s cannot renounce permanent delegate of %s", sender, token.Denom)
		}
	} else if token.Creator != sender.String() {
		return fmt.Errorf("%s cannot renounce %s on %s", sender, msg.Extension, token.Denom)
	}
	next, err := token.Extensions.Renounce(msg.Extension)
	if err != nil {
		return fmt.Errorf("renounce %s on %s: %w", msg.Extension, token.Denom, err)
	}
	token.Extensions = next
	return k.storeToken(ctx, token)
}

// SetShieldable sets the one-way shieldable flag. It does not move tokens.
// Any signer may call it. It is refused while freeze, permanent delegate, or
// pause is still held.
func (k Keeper) SetShieldable(ctx sdk.Context, msg *types.MsgSetShieldable) error {
	if msg == nil {
		return fmt.Errorf("nil set shieldable message")
	}
	if _, err := parseAcc(msg.Sender, "sender"); err != nil {
		return err
	}
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return err
	}
	if token.Extensions.BlocksShield() {
		return fmt.Errorf("%s: %w", token.Denom, types.ErrShieldPowers)
	}
	if token.Shieldable {
		return nil
	}
	token.Shieldable = true
	return k.storeToken(ctx, token)
}
