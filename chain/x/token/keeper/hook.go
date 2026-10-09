package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdkstore "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// runTransferHook calls the token's transfer hook under a gas meter capped at
// TransferHookGasCap. The hook's state writes are returned and must be
// committed only after the bank send succeeds. Out-of-gas and gas overflow
// from that meter become ErrHookGasCap; any other panic is re-raised.
func (k Keeper) runTransferHook(ctx sdk.Context, token types.Token, from, to sdk.AccAddress, amount math.Int) (func(), error) {
	if token.Extensions.TransferHook == "" {
		return nil, nil
	}
	if k.transferHook == nil {
		return nil, fmt.Errorf("token %s has a transfer hook but none is registered", token.Denom)
	}
	contract, err := parseAcc(token.Extensions.TransferHook, "transfer hook contract")
	if err != nil {
		return nil, err
	}

	cacheCtx, write := ctx.CacheContext()
	meter := sdkstore.NewGasMeter(types.TransferHookGasCap)
	hookCtx := cacheCtx.WithGasMeter(meter)

	var hookErr error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			switch recovered.(type) {
			case sdkstore.ErrorOutOfGas, sdkstore.ErrorGasOverflow:
				hookErr = fmt.Errorf("transfer hook for %s: %w", token.Denom, types.ErrHookGasCap)
			default:
				panic(recovered)
			}
		}()
		if err := k.transferHook.OnTransfer(hookCtx, contract, token.Denom, from, to, amount); err != nil {
			hookErr = fmt.Errorf("transfer hook rejected %s: %w", token.Denom, err)
		}
	}()

	ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "x/token transfer hook")
	if hookErr != nil {
		return nil, hookErr
	}
	return write, nil
}
