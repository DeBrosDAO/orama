package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// CodeFee is the burned price of storing code: the uncompressed wasm size times deposit_per_byte,
// the same per-byte rate a contract's state deposit is locked at. Unlike the deposit it is never
// refunded: code is permanent.
func (k Keeper) CodeFee(ctx context.Context, wasm []byte) (size uint64, fee math.Int, err error) {
	code, ok := uncompressedWasm(wasm)
	if !ok {
		return 0, math.Int{}, types.ErrCodeFee.Wrapf("the code is not valid gzip or is over %d bytes uncompressed", maxHashedWasmBytes)
	}
	perByte, err := k.DepositPerByte.Get(ctx)
	if err != nil {
		return 0, math.Int{}, fmt.Errorf("failed to load deposit_per_byte: %w", err)
	}
	size = uint64(len(code))
	return size, math.NewIntFromUint64(size).Mul(perByte), nil
}

// ChargeCodeFee burns the code fee of wasm from payer's bank balance and emits an event with the
// bytes and the amount. It refuses, changing nothing, when payer cannot pay. Empty code costs
// nothing and is left for wasmd to reject. Genesis-embedded contracts never reach it: they enter
// through InitGenesis, not a message.
func (k Keeper) ChargeCodeFee(ctx context.Context, payer sdk.AccAddress, wasm []byte) error {
	if payer.Empty() {
		return types.ErrDepositPayer.Wrap("a code upload has no account to pay the code fee")
	}
	size, fee, err := k.CodeFee(ctx, wasm)
	if err != nil {
		return err
	}
	if size == 0 || !fee.IsPositive() {
		return nil
	}
	if err := k.fees.BurnFromBalance(ctx, payer, fee, fmt.Sprintf("the code fee for %d bytes of wasm", size)); err != nil {
		return types.ErrCodeFee.Wrap(err.Error())
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeCodeFee,
		sdk.NewAttribute(types.AttributeCodePayer, payer.String()),
		sdk.NewAttribute(types.AttributeCodeBytes, fmt.Sprint(size)),
		sdk.NewAttribute(types.AttributeCodeFee, fee.String()),
	))
	return nil
}
