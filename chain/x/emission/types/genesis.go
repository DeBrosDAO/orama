package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// DefaultGenesisState returns x/emission's default genesis: default Params, epoch 1 not yet
// started, and zero cumulative supply. Genesis supply is always zero on a normal genesis
// (plans/open-network.md "Supply and emission").
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params: DefaultParams(),
		EpochState: EpochState{
			CurrentEpoch:                1,
			EpochStartUnixNano:          0,
			BlocksInEpoch:               0,
			CumulativeMinted:            math.ZeroInt(),
			CumulativeBurned:            math.ZeroInt(),
			GenesisSupply:               math.ZeroInt(),
			CumulativeDevelopmentMinted: math.ZeroInt(),
		},
		Ceilings: []CeilingRecord{},
	}
}

// Validate performs genesis-state sanity checks: valid params, a well-formed epoch state,
// non-negative running totals, cumulative minted matching exactly what the schedule would have
// minted for the epochs already completed (not merely "no more than"), and every ceiling record
// matching the schedule's split for its epoch and sitting within the trailing window.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}

	if gs.EpochState.CurrentEpoch == 0 {
		return fmt.Errorf("current_epoch must be >= 1, got 0")
	}
	if gs.EpochState.CurrentEpoch > MaxSafeEpoch {
		return fmt.Errorf("current_epoch must be at most %d, got %d", uint64(MaxSafeEpoch), gs.EpochState.CurrentEpoch)
	}
	if gs.EpochState.EpochStartUnixNano < 0 {
		return fmt.Errorf("epoch_start_unix_nano must not be negative, got %d", gs.EpochState.EpochStartUnixNano)
	}
	if gs.EpochState.CumulativeMinted.IsNil() || gs.EpochState.CumulativeMinted.IsNegative() {
		return fmt.Errorf("cumulative_minted must be a non-negative integer")
	}
	if gs.EpochState.CumulativeBurned.IsNil() || gs.EpochState.CumulativeBurned.IsNegative() {
		return fmt.Errorf("cumulative_burned must be a non-negative integer")
	}
	if gs.EpochState.GenesisSupply.IsNil() || gs.EpochState.GenesisSupply.IsNegative() {
		return fmt.Errorf("genesis_supply must be a non-negative integer")
	}

	completedEpochs := gs.EpochState.CurrentEpoch - 1
	wantMinted := CumulativeValidatorMinted(completedEpochs)
	if !gs.EpochState.CumulativeMinted.Equal(wantMinted) {
		return fmt.Errorf(
			"cumulative_minted is %s, want exactly %s for %d completed epochs",
			gs.EpochState.CumulativeMinted, wantMinted, completedEpochs,
		)
	}

	seen := make(map[uint64]bool, len(gs.Ceilings))
	developmentMintedSum := math.ZeroInt()
	for _, c := range gs.Ceilings {
		if seen[c.Epoch] {
			return fmt.Errorf("duplicate ceiling record for epoch %d", c.Epoch)
		}
		seen[c.Epoch] = true

		if c.Epoch == 0 || c.Epoch >= gs.EpochState.CurrentEpoch {
			return fmt.Errorf("ceiling record epoch %d must be a closed epoch (< current_epoch %d)", c.Epoch, gs.EpochState.CurrentEpoch)
		}
		if completedEpochs-c.Epoch >= CeilingWindow {
			return fmt.Errorf("ceiling record for epoch %d is outside the trailing %d-epoch window (current completed epoch: %d)", c.Epoch, uint64(CeilingWindow), completedEpochs)
		}
		if c.StorageCeiling.IsNil() || c.RelayCeiling.IsNil() || c.DevelopmentCeiling.IsNil() || c.ValidatorMinted.IsNil() {
			return fmt.Errorf("ceiling record for epoch %d has a nil amount", c.Epoch)
		}
		mintedDev := c.DevelopmentMinted
		if mintedDev.IsNil() {
			mintedDev = math.ZeroInt()
		}
		if mintedDev.IsNegative() || mintedDev.GT(c.DevelopmentCeiling) {
			return fmt.Errorf("ceiling record for epoch %d development_minted must be in [0, development_ceiling], got %s", c.Epoch, mintedDev)
		}
		developmentMintedSum = developmentMintedSum.Add(mintedDev)

		want := SplitEpochMint(MaxMintableForEpoch(c.Epoch))
		if !c.StorageCeiling.Equal(want.Storage) || !c.RelayCeiling.Equal(want.Relay) ||
			!c.DevelopmentCeiling.Equal(want.Development) || !c.ValidatorMinted.Equal(want.Validator) {
			return fmt.Errorf(
				"ceiling record for epoch %d does not match the schedule split: got (storage=%s, relay=%s, development=%s, validator=%s), want (storage=%s, relay=%s, development=%s, validator=%s)",
				c.Epoch, c.StorageCeiling, c.RelayCeiling, c.DevelopmentCeiling, c.ValidatorMinted,
				want.Storage, want.Relay, want.Development, want.Validator,
			)
		}
	}

	cumulativeDevelopment := gs.EpochState.CumulativeDevelopmentMinted
	if cumulativeDevelopment.IsNil() {
		cumulativeDevelopment = math.ZeroInt()
	}
	if cumulativeDevelopment.IsNegative() {
		return fmt.Errorf("cumulative_development_minted must be a non-negative integer")
	}
	// Records still inside the window cannot sum to more than the all-time total.
	// The total may be larger: pruning drops the record and keeps the cumulative.
	if cumulativeDevelopment.LT(developmentMintedSum) {
		return fmt.Errorf("cumulative_development_minted %s is less than the %s still recorded on ceiling records", cumulativeDevelopment, developmentMintedSum)
	}

	return nil
}
