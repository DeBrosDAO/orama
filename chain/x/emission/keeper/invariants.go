package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// ReconcileBurns runs once per EndBlock. x/emission has no burn path of its own, but other
// modules do (x/slashing burns a validator's bonded/not-bonded stake on a double-sign or downtime
// slash). Since x/emission's own BeginBlocker has already minted this block's validator share (and
// updated CumulativeMinted to match) by the time EndBlock runs, any further drop in bank supply
// below genesis_supply + cumulative_minted + cumulative_development_minted - cumulative_burned
// during the block must be a burn
// that happened elsewhere, and is attributed to CumulativeBurned here so the supply invariant
// keeps holding without x/emission needing a direct dependency on x/slashing.
func (k Keeper) ReconcileBurns(ctx sdk.Context) error {
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load emission epoch state: %w", err)
	}

	expected := expectedSupply(state)
	actual := k.bankKeeper.GetSupply(ctx, params.BaseDenom).Amount
	if !actual.LT(expected) {
		return nil
	}

	burned := expected.Sub(actual)
	state.CumulativeBurned = state.CumulativeBurned.Add(burned)
	if err := k.EpochState.Set(ctx, state); err != nil {
		return fmt.Errorf("failed to record %s norama burned this block: %w", burned, err)
	}
	k.Logger(ctx).Info("emission observed a burn elsewhere in the chain", "amount", burned.String())
	return nil
}

// CheckSupplyInvariant checks the two invariants described in
// plans/open-network/track-c-chain.md C3:
//
//  1. cumulative minted must equal exactly what the schedule's validator share would mint for the
//     epochs already completed - not merely "no more than", since x/emission's CloseEpoch mints
//     that exact amount unconditionally every time an epoch closes;
//  2. the base-denom bank supply must equal genesis_supply + cumulative_minted
//     + cumulative_development_minted + cumulative_service_minted - cumulative_burned, where genesis_supply is the
//     (normally zero) norama supply observed at this chain incarnation's genesis - see the
//     devnet-only bootstrap-stake exception documented on Keeper.InitGenesis, and
//     cumulative_burned is kept current by ReconcileBurns. cumulative_development_minted is
//     only what MintDevelopmentSpend has minted.
//
// It returns a human-readable detail message and whether either invariant is broken.
func (k Keeper) CheckSupplyInvariant(ctx sdk.Context) (string, bool) {
	detail, mintedExact, supplyMatches := k.checkSupplyInvariantDetailed(ctx)
	return detail, !mintedExact || !supplyMatches
}

// checkSupplyInvariantDetailed is CheckSupplyInvariant's implementation, reporting the two
// component checks separately so the `oramad q emission invariants` query can show each on its
// own (types.QueryInvariantsResponse has one field per check).
func (k Keeper) checkSupplyInvariantDetailed(ctx sdk.Context) (detail string, mintedExact, supplyMatches bool) {
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return fmt.Sprintf("failed to load emission epoch state: %v", err), false, false
	}

	var completedEpochs uint64
	if state.CurrentEpoch > 0 {
		completedEpochs = state.CurrentEpoch - 1
	}
	wantMinted := types.CumulativeValidatorMinted(completedEpochs)
	mintedExact = state.CumulativeMinted.Equal(wantMinted)

	actualSupply := k.bankKeeper.GetSupply(ctx, params.BaseDenom).Amount
	expected := expectedSupply(state)
	supplyMatches = actualSupply.Equal(expected)

	detail = fmt.Sprintf(
		"minted exactly matches schedule: %t (cumulative_minted=%s, want=%s for %d completed epochs)\n"+
			"supply matches minted: %t (bank_supply=%s, expected=%s = genesis_supply(%s)+validator_minted(%s)+development_minted(%s)+service_minted(%s)-burned(%s))\n",
		mintedExact, state.CumulativeMinted, wantMinted, completedEpochs,
		supplyMatches, actualSupply, expected,
		state.GenesisSupply, state.CumulativeMinted, nonNilInt(state.CumulativeDevelopmentMinted),
		nonNilInt(state.CumulativeServiceMinted), state.CumulativeBurned,
	)

	return detail, mintedExact, supplyMatches
}

// expectedSupply is genesis_supply + validator mints + development mints +
// storage and relay service mints - burns.
// cumulative_minted stays the validator share only, so a development mint does
// not disturb the schedule equality check.
func expectedSupply(state types.EpochState) math.Int {
	return nonNilInt(state.GenesisSupply).
		Add(nonNilInt(state.CumulativeMinted)).
		Add(nonNilInt(state.CumulativeDevelopmentMinted)).
		Add(nonNilInt(state.CumulativeServiceMinted)).
		Sub(nonNilInt(state.CumulativeBurned))
}

func nonNilInt(v math.Int) math.Int {
	if v.IsNil() {
		return math.ZeroInt()
	}
	return v
}
