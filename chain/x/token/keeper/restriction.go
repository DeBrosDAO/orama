package keeper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// transferScope marks a context inside MsgTransfer, which has already applied
// every capability check for the move it is about to make.
type transferScope struct{}

// scopedTransfer returns ctx marked as inside MsgTransfer's own bank send.
func scopedTransfer(ctx sdk.Context) sdk.Context {
	return ctx.WithValue(transferScope{}, true)
}

// SendRestriction is the bank send restriction that makes a token's powers hold
// on every path that moves its coins: x/bank MsgSend and MsgMultiSend, a
// contract's BankMsg and attached funds, and any module that sends. Without it
// a frozen holder, a paused or non-transferable token and a transfer fee would
// all be bypassed by a plain bank send.
//
// Coins that are not factory denoms pass. A send with the token module account
// on either side is the module's own mint, burn or fee burn, and passes; so
// does the bank send inside MsgTransfer, which has made these checks already.
// Any other move of a token is refused when the token is paused, non-transferable,
// has a transfer fee or a transfer hook (those run only in MsgTransfer), or when
// either account is frozen.
func (k Keeper) SendRestriction(ctx context.Context, from, to sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
	if scoped, _ := ctx.Value(transferScope{}).(bool); scoped {
		return to, nil
	}
	module := authtypes.NewModuleAddress(types.ModuleName)
	if from.Equals(module) || to.Equals(module) {
		return to, nil
	}
	for _, coin := range amt {
		if !strings.HasPrefix(coin.Denom, types.DenomPrefix+"/") {
			continue
		}
		if err := k.checkPlainSend(ctx, coin.Denom, from, to); err != nil {
			return nil, err
		}
	}
	return to, nil
}

func (k Keeper) checkPlainSend(ctx context.Context, denom string, from, to sdk.AccAddress) error {
	token, err := k.Tokens.Get(ctx, denom)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to load token %s: %w", denom, err)
	}
	if token.Paused {
		return fmt.Errorf("token %s is paused", denom)
	}
	if token.Extensions.NonTransferable {
		return fmt.Errorf("token %s is non-transferable", denom)
	}
	if token.Extensions.TransferFeeBps > 0 || token.Extensions.TransferHook {
		return fmt.Errorf("token %s has a transfer fee or hook and moves only by x/token MsgTransfer", denom)
	}
	for _, account := range []sdk.AccAddress{from, to} {
		frozen, err := k.isFrozen(ctx, denom, account.String())
		if err != nil {
			return err
		}
		if frozen {
			return fmt.Errorf("account %s is frozen for %s", account, denom)
		}
	}
	return nil
}
