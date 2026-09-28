package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesisState returns x/fees's default genesis: default Params, the base fee starting at
// Params.InitialBaseFee, and no earnings accounts or deposits yet.
func DefaultGenesisState() *GenesisState {
	p := DefaultParams()
	return &GenesisState{
		Params:           p,
		BaseFee:          p.InitialBaseFee,
		EarningsAccounts: []EarningsAccount{},
		Deposits:         []Deposit{},
	}
}

// Validate performs genesis-state sanity checks.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if gs.BaseFee.IsNil() || gs.BaseFee.LT(gs.Params.MinBaseFee) {
		return fmt.Errorf("base_fee must be at least min_base_fee (%s), got %s", gs.Params.MinBaseFee, gs.BaseFee)
	}

	seenEarnings := make(map[string]bool, len(gs.EarningsAccounts))
	for _, e := range gs.EarningsAccounts {
		if _, err := sdk.AccAddressFromBech32(e.Address); err != nil {
			return fmt.Errorf("invalid earnings account address %q: %w", e.Address, err)
		}
		if seenEarnings[e.Address] {
			return fmt.Errorf("duplicate earnings account %q", e.Address)
		}
		seenEarnings[e.Address] = true
		if e.Balance.IsNil() || !e.Balance.IsPositive() {
			return fmt.Errorf("earnings account %q balance must be positive, got %s", e.Address, e.Balance)
		}
	}

	seenDeposits := make(map[string]bool, len(gs.Deposits))
	for _, d := range gs.Deposits {
		if d.Id == "" {
			return fmt.Errorf("deposit has an empty id")
		}
		if seenDeposits[d.Id] {
			return fmt.Errorf("duplicate deposit id %q", d.Id)
		}
		seenDeposits[d.Id] = true
		if _, err := sdk.AccAddressFromBech32(d.Owner); err != nil {
			return fmt.Errorf("deposit %q has an invalid owner %q: %w", d.Id, d.Owner, err)
		}
		if d.Amount.IsNil() || !d.Amount.IsPositive() {
			return fmt.Errorf("deposit %q amount must be positive, got %s", d.Id, d.Amount)
		}
	}

	return nil
}
