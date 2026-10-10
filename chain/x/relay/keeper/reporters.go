package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// allowReporterChangeKey is a context value, not store state. It defaults to
// unset, which reporterChangeAllowed reads as false. x/relay never sets it.
type allowReporterChangeKey struct{}

// WithAllowReporterChange returns a context on which MsgUpdateReporters is
// accepted. x/houses must call this only while executing a passed structural
// proposal that changes the reporter set. No code in x/relay calls this.
func WithAllowReporterChange(ctx context.Context) context.Context {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.WithValue(allowReporterChangeKey{}, true)
}

func reporterChangeAllowed(ctx context.Context) bool {
	allowed, _ := ctx.Value(allowReporterChangeKey{}).(bool)
	return allowed
}

func (k Keeper) updateReporters(ctx sdk.Context, msg *types.MsgUpdateReporters) error {
	if _, err := sdk.AccAddressFromBech32(msg.Signer); err != nil {
		return fmt.Errorf("update reporters: invalid signer: %w", err)
	}
	// signer is not an authority. There is no admin key. The context flag is
	// the only authorization, and this function only reads it.
	if !reporterChangeAllowed(ctx) {
		return fmt.Errorf("update reporters: %w", types.ErrReporterChangeForbidden)
	}
	next, err := types.NormalizeReporters(msg.Reporters, false)
	if err != nil {
		return fmt.Errorf("update reporters: %w", err)
	}
	want := make(map[string]struct{}, len(next))
	for _, addr := range next {
		want[addr] = struct{}{}
	}
	if err := k.requireNoRelayOperator(ctx, want); err != nil {
		return fmt.Errorf("update reporters: %w", err)
	}

	var existing []string
	if err := k.Reporters.Walk(ctx, nil, func(addr string, _ bool) (bool, error) {
		existing = append(existing, addr)
		return false, nil
	}); err != nil {
		return fmt.Errorf("update reporters: failed to walk reporters: %w", err)
	}
	for _, addr := range existing {
		if _, ok := want[addr]; ok {
			continue
		}
		if err := k.Reporters.Remove(ctx, addr); err != nil {
			return fmt.Errorf("update reporters: failed to remove %s: %w", addr, err)
		}
	}
	for _, addr := range next {
		if err := k.Reporters.Set(ctx, addr, true); err != nil {
			return fmt.Errorf("update reporters: failed to set %s: %w", addr, err)
		}
	}
	return nil
}

// requireNoRelayOperator refuses a reporter set that names the operator of a
// registered relay.
func (k Keeper) requireNoRelayOperator(ctx context.Context, reporters map[string]struct{}) error {
	return k.Relays.Walk(ctx, nil, func(_ []byte, relay types.Relay) (bool, error) {
		if _, ok := reporters[relay.Operator]; ok {
			return true, fmt.Errorf("reporter %s operates relay %s: %w", relay.Operator, relay.NodeId, types.ErrReporterOperatesRelay)
		}
		return false, nil
	})
}

func (k Keeper) isReporter(ctx context.Context, addr string) (bool, error) {
	ok, err := k.Reporters.Has(ctx, addr)
	if err != nil {
		return false, fmt.Errorf("failed to check reporter %s: %w", addr, err)
	}
	return ok, nil
}

func (k Keeper) reporterSet(ctx context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if err := k.Reporters.Walk(ctx, nil, func(addr string, _ bool) (bool, error) {
		out[addr] = struct{}{}
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk reporters: %w", err)
	}
	return out, nil
}
