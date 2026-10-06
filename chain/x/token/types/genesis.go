package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesisState returns x/token's default genesis: default Params and no tokens.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:         DefaultParams(),
		Tokens:         []Token{},
		FrozenAccounts: []FrozenAccount{},
	}
}

// Validate performs genesis-state sanity checks.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(gs.Tokens))
	for _, token := range gs.Tokens {
		if err := token.Validate(); err != nil {
			return err
		}
		if _, ok := seen[token.Denom]; ok {
			return fmt.Errorf("duplicate token %s", token.Denom)
		}
		seen[token.Denom] = struct{}{}
	}

	seenFrozen := make(map[string]struct{}, len(gs.FrozenAccounts))
	for _, frozen := range gs.FrozenAccounts {
		if _, ok := seen[frozen.Denom]; !ok {
			return fmt.Errorf("frozen account references unknown token %s", frozen.Denom)
		}
		if _, err := sdk.AccAddressFromBech32(frozen.Account); err != nil {
			return fmt.Errorf("frozen account on %s has an invalid address %q: %w", frozen.Denom, frozen.Account, err)
		}
		key := frozen.Denom + "\x00" + frozen.Account
		if _, ok := seenFrozen[key]; ok {
			return fmt.Errorf("duplicate freeze of %s on %s", frozen.Account, frozen.Denom)
		}
		seenFrozen[key] = struct{}{}
	}
	return nil
}
