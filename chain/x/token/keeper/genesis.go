package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// InitGenesis sets x/token's state from a GenesisState. It does not mint
// balances or lock deposits; those must already match the imported records
// (CheckInvariants compares them).
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return fmt.Errorf("invalid token genesis state: %w", err)
	}
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return fmt.Errorf("failed to set token params: %w", err)
	}
	for _, token := range genState.Tokens {
		if err := k.Tokens.Set(ctx, token.Denom, token); err != nil {
			return fmt.Errorf("failed to set token %s: %w", token.Denom, err)
		}
	}
	for _, frozen := range genState.FrozenAccounts {
		account, err := sdk.AccAddressFromBech32(frozen.Account)
		if err != nil {
			return fmt.Errorf("frozen account on %s has an invalid address %q: %w", frozen.Denom, frozen.Account, err)
		}
		if err := k.Frozen.Set(ctx, collections.Join(frozen.Denom, account.String())); err != nil {
			return fmt.Errorf("failed to freeze %s on %s: %w", account, frozen.Denom, err)
		}
	}
	return nil
}

// ExportGenesis reads x/token's current state back into a GenesisState.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get token params: %w", err)
	}

	var tokens []types.Token
	if err := k.Tokens.Walk(ctx, nil, func(_ string, token types.Token) (bool, error) {
		tokens = append(tokens, token)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk tokens: %w", err)
	}

	var frozen []types.FrozenAccount
	if err := k.Frozen.Walk(ctx, nil, func(key collections.Pair[string, string]) (bool, error) {
		frozen = append(frozen, types.FrozenAccount{Denom: key.K1(), Account: key.K2()})
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk frozen accounts: %w", err)
	}

	return &types.GenesisState{
		Params:         p,
		Tokens:         tokens,
		FrozenAccounts: frozen,
	}, nil
}
