package app

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// noopTokenHook is the transfer hook until a contract binding is linked.
// A token that set the hook extension still calls it; the call does nothing
// and stays inside the gas cap the token keeper enforces.
type noopTokenHook struct{}

func (noopTokenHook) OnTransfer(context.Context, string, sdk.AccAddress, sdk.AccAddress, math.Int) error {
	return nil
}
